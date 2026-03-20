/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package scheduleexception

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/go-logr/logr"
	"github.com/telepresenceio/watchable"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
	statusprocessor "github.com/ardikabs/hibernator/internal/provider/processor/status"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// LifecycleProcessor manages the ScheduleException lifecycle state machine:
// Pending → Active → Expired.
//
// It subscribes to PlanResources (not ExceptionResources) and processes all
// exceptions carried in PlanContext.Exceptions. This ensures that exception
// lifecycle transitions are driven by the same delivery pipeline as the plan
// itself, fixing the time-based state transition bug where HandleSubscription
// would suppress re-delivery because the exception object itself hadn't changed.
//
// Handles:
//   - Finalizer management
//   - Plan label management for efficient querying
//   - State transitions based on ValidFrom/ValidUntil
//   - Deletion cleanup (removing exception reference from plan status)
type LifecycleProcessor struct {
	client.Client
	Clock clock.Clock
	Log   logr.Logger

	Resources *message.ControllerResources
	Statuses  *statusprocessor.ControllerStatuses
}

// NeedLeaderElection returns true since this processor modifies resources.
func (p *LifecycleProcessor) NeedLeaderElection() bool {
	return true
}

// Start implements manager.Runnable. It subscribes to PlanResources and processes
// exception state transitions from PlanContext.Exceptions.
func (p *LifecycleProcessor) Start(ctx context.Context) error {
	log := p.Log.WithName("lifecycle")
	log.Info("starting exception lifecycle processor")

	message.HandleSubscription(ctx, log, message.Metadata{
		Runner:  "exception-lifecycle",
		Message: "plan-resources",
	}, p.Resources.PlanResources.Subscribe(ctx),
		func(update watchable.Update[types.NamespacedName, *message.PlanContext], errChan chan error) {
			if update.Delete {
				log.V(1).Info("received plan delete event", "plan", update.Key)
				p.handlePlanDelete(ctx, log, update.Key, errChan)
				return
			}

			p.handlePlanUpdate(ctx, log, update.Key, update.Value, errChan)
		},
	)

	log.Info("exception lifecycle processor stopped")
	return nil
}

// handlePlanUpdate processes all exceptions in the PlanContext.
func (p *LifecycleProcessor) handlePlanUpdate(ctx context.Context, log logr.Logger, planKey types.NamespacedName, planCtx *message.PlanContext, errChan chan error) {
	if planCtx == nil {
		return
	}

	for i := range planCtx.Exceptions {
		exc := &planCtx.Exceptions[i]
		excKey := types.NamespacedName{Name: exc.Name, Namespace: exc.Namespace}

		if !exc.DeletionTimestamp.IsZero() {
			p.handleExceptionDelete(ctx, log, excKey, errChan)
			continue
		}

		p.handleExceptionUpdate(ctx, log, excKey, exc, errChan)
	}

	// Sync exception references into plan status
	p.updateExceptionReferences(log, planKey, planCtx.Plan, planCtx.Exceptions)
}

// updateExceptionReferences builds a sorted ExceptionReference list from the
// PlanContext's exceptions and queues a plan status update. This keeps the plan's
// ExceptionReferences in sync without requiring a separate observer or extra API calls,
// since all exceptions are already available in the PlanContext.
func (p *LifecycleProcessor) updateExceptionReferences(log logr.Logger, key types.NamespacedName, plan *hibernatorv1alpha1.HibernatePlan, exceptions []hibernatorv1alpha1.ScheduleException) {
	// Build ExceptionReferences from all exceptions (regardless of state)
	exceptionRefs := make([]hibernatorv1alpha1.ExceptionReference, 0, len(exceptions))
	for i := range exceptions {
		exc := &exceptions[i]
		// Skip exceptions being deleted — they are handled by removeFromPlanStatus
		if !exc.DeletionTimestamp.IsZero() {
			continue
		}
		exceptionRefs = append(exceptionRefs, hibernatorv1alpha1.ExceptionReference{
			Name:       exc.Name,
			Type:       exc.Spec.Type,
			ValidFrom:  exc.Spec.ValidFrom,
			ValidUntil: exc.Spec.ValidUntil,
			State:      exc.Status.State,
			AppliedAt:  exc.Status.AppliedAt,
		})
	}

	// Sort: Active > Pending > Expired, then by ValidFrom descending (most recent first)
	stateOrder := map[hibernatorv1alpha1.ExceptionState]int{
		hibernatorv1alpha1.ExceptionStateActive:  0,
		hibernatorv1alpha1.ExceptionStatePending: 1,
		hibernatorv1alpha1.ExceptionStateExpired: 2,
	}

	sort.Slice(exceptionRefs, func(i, j int) bool {
		stateA := stateOrder[exceptionRefs[i].State]
		stateB := stateOrder[exceptionRefs[j].State]
		if stateA != stateB {
			return stateA < stateB
		}
		return exceptionRefs[i].ValidFrom.After(exceptionRefs[j].ValidFrom.Time)
	})

	// Cap at 10 most recent
	if len(exceptionRefs) > 10 {
		exceptionRefs = exceptionRefs[:10]
	}

	// Skip update if nothing changed
	if hibernatorv1alpha1.ExceptionReferencesEqual(plan.Status.ExceptionReferences, exceptionRefs) {
		log.V(1).Info("exception references unchanged, skipping plan status update")
		return
	}

	p.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: key,
		Resource:       plan,
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.ExceptionReferences = exceptionRefs
		}),
	})

	log.V(1).Info("queued exception references update for plan", "plan", key, "count", len(exceptionRefs))
}

// handlePlanDelete handles all exceptions associated with a deleted plan.
// Exceptions with an OwnerReference to the plan are skipped — Kubernetes garbage
// collection will cascade-delete them. All other exceptions are transitioned to
// the Detached state to signal that their referenced plan no longer exists.
func (p *LifecycleProcessor) handlePlanDelete(ctx context.Context, log logr.Logger, planKey types.NamespacedName, errChan chan error) {
	// Use the cached client (not APIReader) because field indexes only work with the cache.
	var exceptionList hibernatorv1alpha1.ScheduleExceptionList
	if err := p.List(ctx, &exceptionList,
		client.InNamespace(planKey.Namespace),
		client.MatchingFields{wellknown.FieldIndexExceptionPlanRef: planKey.Name},
	); err != nil {
		errChan <- fmt.Errorf("plan %s: failed to list exceptions for cleanup: %w", planKey, err)
		return
	}

	for i := range exceptionList.Items {
		exc := &exceptionList.Items[i]
		excKey := types.NamespacedName{Name: exc.Name, Namespace: exc.Namespace}

		// If the exception has an OwnerReference to the plan, Kubernetes GC
		// will cascade-delete it — nothing to do here.
		if hasOwnerReferenceToPlan(exc, planKey) {
			log.V(1).Info("exception has owner reference to plan, skipping (GC will handle)",
				"exception", excKey)
			continue
		}

		// Transition to Detached state — the plan is gone but the exception lives on.
		p.transitionToDetached(ctx, log, excKey, exc, planKey.Name)
	}
}

// handleExceptionUpdate drives the ScheduleException state machine for a single exception.
func (p *LifecycleProcessor) handleExceptionUpdate(ctx context.Context, log logr.Logger, key types.NamespacedName, exception *hibernatorv1alpha1.ScheduleException, errChan chan error) {
	if exception == nil {
		return
	}

	log = log.WithValues(
		"exception", exception.Name,
		"namespace", exception.Namespace,
		"type", exception.Spec.Type,
		"plan", exception.Spec.PlanRef.Name,
	)

	log.V(1).Info("processing exception",
		"currentState", exception.Status.State,
		"hasDeletionTimestamp", !exception.DeletionTimestamp.IsZero(),
		"hasFinalizer", controllerutil.ContainsFinalizer(exception, wellknown.ExceptionFinalizerName))

	// Handle deletion (DeletionTimestamp set)
	if !exception.DeletionTimestamp.IsZero() {
		log.V(1).Info("exception has deletion timestamp, handling deletion")
		p.handleExceptionDelete(ctx, log, key, errChan)
		return
	}

	// Ensure finalizer
	if !controllerutil.ContainsFinalizer(exception, wellknown.ExceptionFinalizerName) {
		orig := exception.DeepCopy()
		controllerutil.AddFinalizer(exception, wellknown.ExceptionFinalizerName)
		if err := p.Patch(ctx, exception, client.MergeFrom(orig)); err != nil {
			errChan <- fmt.Errorf("exception %s/%s: failed to add finalizer: %w", exception.Namespace, exception.Name, err)
			return
		}
		log.V(1).Info("added finalizer")
	}

	// Ensure plan label for efficient querying
	if err := p.ensurePlanLabel(ctx, log, exception); err != nil {
		errChan <- fmt.Errorf("exception %s/%s: failed to ensure plan label: %w", exception.Namespace, exception.Name, err)
	}

	// Determine desired state
	now := p.Clock.Now()
	desiredState := p.computeDesiredState(now, exception)

	log.V(1).Info("computed desired exception state",
		"desiredState", desiredState,
		"currentState", exception.Status.State,
		"validFrom", exception.Spec.ValidFrom.Time,
		"validUntil", exception.Spec.ValidUntil.Time)

	// Transition state if needed
	if exception.Status.State != desiredState {
		p.transitionState(ctx, log, key, exception, desiredState, now)
		return
	}

	log.V(1).Info("exception state is current, checking message update")

	// Update informational message
	p.updateMessage(ctx, log, key, exception, now)
}

// computeDesiredState determines what state the exception should be in based on current time.
func (p *LifecycleProcessor) computeDesiredState(now time.Time, exception *hibernatorv1alpha1.ScheduleException) hibernatorv1alpha1.ExceptionState {
	if now.Before(exception.Spec.ValidFrom.Time) {
		return hibernatorv1alpha1.ExceptionStatePending
	}
	if !exception.Spec.ValidUntil.IsZero() && now.After(exception.Spec.ValidUntil.Time) {
		return hibernatorv1alpha1.ExceptionStateExpired
	}
	return hibernatorv1alpha1.ExceptionStateActive
}

// transitionState moves the exception to a new state.
func (p *LifecycleProcessor) transitionState(_ context.Context, log logr.Logger, key types.NamespacedName, exception *hibernatorv1alpha1.ScheduleException, desiredState hibernatorv1alpha1.ExceptionState, now time.Time) {
	oldState := exception.Status.State
	if oldState == "" {
		oldState = "<unset>"
	}

	// Queue status update via status processor
	p.Statuses.ExceptionStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.ScheduleException]{
		NamespacedName: key,
		Resource:       exception,
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.ScheduleException](func(e *hibernatorv1alpha1.ScheduleException) {
			e.Status.State = desiredState

			switch desiredState {
			case hibernatorv1alpha1.ExceptionStatePending:
				e.Status.AppliedAt = nil
				e.Status.ExpiredAt = nil
				e.Status.DetachedAt = nil
				e.Status.Message = "Exception pending"
			case hibernatorv1alpha1.ExceptionStateActive:
				nowTime := now
				e.Status.AppliedAt = &metav1.Time{Time: nowTime}
				e.Status.ExpiredAt = nil
				e.Status.DetachedAt = nil
				e.Status.Message = "Exception activated"
			case hibernatorv1alpha1.ExceptionStateExpired:
				nowTime := now
				e.Status.ExpiredAt = &metav1.Time{Time: nowTime}
				e.Status.AppliedAt = nil
				e.Status.DetachedAt = nil
				e.Status.Message = "Exception expired"
			}
		}),
	})

	log.Info("queued exception state transition", "from", string(oldState), "to", string(desiredState))
}

// updateMessage updates the exception's status message with time-based information.
func (p *LifecycleProcessor) updateMessage(_ context.Context, log logr.Logger, key types.NamespacedName, exception *hibernatorv1alpha1.ScheduleException, now time.Time) {
	var newMessage string

	switch exception.Status.State {
	case hibernatorv1alpha1.ExceptionStatePending:
		newMessage = formatPendingMessage(now, exception)
	case hibernatorv1alpha1.ExceptionStateActive:
		newMessage = formatActiveMessage(now, exception)
	case hibernatorv1alpha1.ExceptionStateExpired:
		newMessage = "Exception expired"
	case hibernatorv1alpha1.ExceptionStateDetached:
		newMessage = fmt.Sprintf("Referenced plan %q no longer exists; exception is detached", exception.Spec.PlanRef.Name)
	default:
		newMessage = fmt.Sprintf("Exception state: %s", exception.Status.State)
	}

	// Only queue update if message changed
	if exception.Status.Message == newMessage {
		log.V(1).Info("exception message unchanged, skipping update")
		return
	}

	log.V(1).Info("updating exception message",
		"oldMessage", exception.Status.Message,
		"newMessage", newMessage)

	p.Statuses.ExceptionStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.ScheduleException]{
		NamespacedName: key,
		Resource:       exception,
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.ScheduleException](func(e *hibernatorv1alpha1.ScheduleException) {
			e.Status.Message = newMessage
		}),
	})
}

// formatPendingMessage creates a human-readable message for pending exceptions.
func formatPendingMessage(now time.Time, exception *hibernatorv1alpha1.ScheduleException) string {
	if exception.Spec.ValidFrom.IsZero() {
		return "Exception pending"
	}

	remaining := exception.Spec.ValidFrom.Sub(now)
	days := int(remaining.Hours() / 24)
	if days > 0 {
		return fmt.Sprintf("Exception pending, activates in %d days", days)
	}

	hours := int(remaining.Hours())
	if hours > 0 {
		return fmt.Sprintf("Exception pending, activates in %d hours", hours)
	}

	return "Exception pending, activates soon"
}

// formatActiveMessage creates a human-readable message for active exceptions.
func formatActiveMessage(now time.Time, exception *hibernatorv1alpha1.ScheduleException) string {
	if exception.Spec.ValidUntil.IsZero() {
		return "Exception active"
	}

	remaining := exception.Spec.ValidUntil.Sub(now)
	days := int(remaining.Hours() / 24)
	if days > 0 {
		return fmt.Sprintf("Exception active, expires in %d days", days)
	}

	hours := int(remaining.Hours())
	if hours > 0 {
		return fmt.Sprintf("Exception active, expires in %d hours", hours)
	}

	return "Exception active, expires soon"
}

// ensurePlanLabel adds the plan label to the exception for efficient querying.
func (p *LifecycleProcessor) ensurePlanLabel(ctx context.Context, log logr.Logger, exception *hibernatorv1alpha1.ScheduleException) error {
	planName := exception.Spec.PlanRef.Name
	if exception.Labels != nil && exception.Labels[wellknown.LabelPlan] == planName {
		return nil
	}

	orig := exception.DeepCopy()
	if exception.Labels == nil {
		exception.Labels = make(map[string]string)
	}
	exception.Labels[wellknown.LabelPlan] = planName
	if err := p.Patch(ctx, exception, client.MergeFrom(orig)); err != nil {
		return fmt.Errorf("update exception plan label: %w", err)
	}

	log.V(1).Info("added plan label to exception")
	return nil
}

// handleExceptionDelete handles an exception that has DeletionTimestamp set.
// It re-fetches the exception from the API server to get the current ResourceVersion
// before patching the finalizer.
func (p *LifecycleProcessor) handleExceptionDelete(ctx context.Context, log logr.Logger, key types.NamespacedName, errChan chan error) {
	// Re-fetch fresh copy from the API server to avoid 409 Conflict on stale ResourceVersion.
	exception := new(hibernatorv1alpha1.ScheduleException)
	if err := p.Get(ctx, key, exception); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.V(1).Info("exception already gone, nothing to clean up")
			return
		}
		errChan <- fmt.Errorf("exception %s: failed to re-fetch for deletion: %w", key, err)
		return
	}

	log = log.WithValues(
		"exception", exception.Name,
		"namespace", exception.Namespace,
	)
	log.V(1).Info("handling exception deletion")

	// Remove exception from plan status
	if err := p.removeFromPlanStatus(ctx, log, exception); err != nil {
		errChan <- fmt.Errorf("exception %s/%s: failed to remove from plan status: %w", exception.Namespace, exception.Name, err)
	}

	// Remove finalizer with retry to handle concurrent updates.
	if controllerutil.ContainsFinalizer(exception, wellknown.ExceptionFinalizerName) {
		orig := exception.DeepCopy()
		controllerutil.RemoveFinalizer(exception, wellknown.ExceptionFinalizerName)
		if err := p.Patch(ctx, exception, client.MergeFrom(orig)); err != nil {
			errChan <- fmt.Errorf("exception %s/%s: failed to remove finalizer: %w", exception.Namespace, exception.Name, err)
			return
		}
		log.V(1).Info("removed finalizer")
	}

	log.Info("exception deleted successfully")
}

// removeFromPlanStatus queues a status update that removes the exception from the
// HibernatePlan's ActiveExceptions list. The actual write is performed by the
// status writer processor, preserving the architectural invariant that only the
// status writer writes status sub-resources.
func (p *LifecycleProcessor) removeFromPlanStatus(_ context.Context, log logr.Logger, exception *hibernatorv1alpha1.ScheduleException) error {
	planKey := types.NamespacedName{
		Name:      exception.Spec.PlanRef.Name,
		Namespace: exception.Namespace,
	}

	exceptionName := exception.Name
	p.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: planKey,
		Resource:       new(hibernatorv1alpha1.HibernatePlan),
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			var updated []hibernatorv1alpha1.ExceptionReference
			for _, ref := range p.Status.ExceptionReferences {
				if ref.Name != exceptionName {
					updated = append(updated, ref)
				}
			}
			p.Status.ExceptionReferences = updated
		}),
	})

	log.V(1).Info("queued removal of exception from plan status", "plan", planKey.Name)
	return nil
}

// transitionToDetached queues a status update that moves the exception to the Detached state
// and removes the finalizer. This is used when the referenced plan is deleted but the exception
// has no OwnerReference, meaning it should not be cascade-deleted.
//
// Removing the finalizer allows:
// - Users to delete the orphaned exception if desired
// - The exception to be re-linked if a plan with the same name is recreated
func (p *LifecycleProcessor) transitionToDetached(ctx context.Context, log logr.Logger, key types.NamespacedName, exception *hibernatorv1alpha1.ScheduleException, planName string) {
	if exception.Status.State == hibernatorv1alpha1.ExceptionStateDetached {
		log.V(1).Info("exception already detached, skipping", "exception", key)
		return
	}

	oldState := exception.Status.State
	if oldState == "" {
		oldState = "<unset>"
	}

	msg := fmt.Sprintf("Referenced plan %q no longer exists; exception is detached", planName)
	now := p.Clock.Now()

	// Queue status update
	p.Statuses.ExceptionStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.ScheduleException]{
		NamespacedName: key,
		Resource:       exception,
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.ScheduleException](func(e *hibernatorv1alpha1.ScheduleException) {
			e.Status.State = hibernatorv1alpha1.ExceptionStateDetached
			e.Status.DetachedAt = &metav1.Time{Time: now}
			e.Status.Message = msg
		}),
	})

	// Remove finalizer so the exception can be deleted by the user or relinked with a recreated plan
	if controllerutil.ContainsFinalizer(exception, wellknown.ExceptionFinalizerName) {
		orig := exception.DeepCopy()
		controllerutil.RemoveFinalizer(exception, wellknown.ExceptionFinalizerName)
		if err := p.Patch(ctx, exception, client.MergeFrom(orig)); err != nil {
			log.Error(err, "failed to remove finalizer when transitioning to Detached",
				"exception", key,
				"plan", planName)
			return
		}
		log.V(1).Info("removed finalizer from detached exception", "exception", key)
	}

	log.Info("queued exception transition to Detached",
		"exception", key,
		"from", string(oldState),
		"plan", planName)
}

// hasOwnerReferenceToPlan checks whether the exception has an OwnerReference pointing
// to the given HibernatePlan. If it does, Kubernetes garbage collection will
// cascade-delete the exception when the plan is removed.
func hasOwnerReferenceToPlan(exc *hibernatorv1alpha1.ScheduleException, planKey types.NamespacedName) bool {
	for _, ref := range exc.OwnerReferences {
		if ref.Kind == "HibernatePlan" &&
			ref.Name == planKey.Name &&
			ref.APIVersion == hibernatorv1alpha1.GroupVersion.String() {
			return true
		}
	}
	return false
}
