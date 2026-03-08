/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package scheduleexception

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/telepresenceio/watchable"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
// It subscribes to ALL exceptions (no phase filter) and handles:
//   - Finalizer management
//   - Plan label management for efficient querying
//   - State transitions based on ValidFrom/ValidUntil
//   - Deletion cleanup (removing exception reference from plan status)
type LifecycleProcessor struct {
	client.Client
	// APIReader must be the uncached API reader (mgr.GetAPIReader()) so that Get calls
	// inside RetryOnConflict always fetch the true server state rather than a potentially
	// stale informer-cache snapshot. Using the cache here would cause retries to serialize
	// against the same stale ResourceVersion, exhaust the budget, and leave orphaned
	// ExceptionReferences in plan.Status.ActiveExceptions with no recovery path.
	APIReader client.Reader
	Clock     clock.Clock
	Log       logr.Logger
	Scheme    *runtime.Scheme

	Resources *message.ControllerResources
	Statuses  *statusprocessor.ControllerStatuses
}

// NeedLeaderElection returns true since this processor modifies resources.
func (p *LifecycleProcessor) NeedLeaderElection() bool {
	return true
}

// Start implements manager.Runnable. It subscribes to all exception updates
// and processes state transitions.
func (p *LifecycleProcessor) Start(ctx context.Context) error {
	log := p.Log.WithName("lifecycle")
	log.Info("starting exception lifecycle processor")

	message.HandleSubscription(ctx, log, message.Metadata{
		Runner:  "exception-lifecycle",
		Message: "exception-resources",
	}, p.Resources.ExceptionResources.Subscribe(ctx),
		func(update watchable.Update[types.NamespacedName, *hibernatorv1alpha1.ScheduleException], errChan chan error) {
			if update.Delete {
				log.V(1).Info("received delete event", "exception", update.Key)
				p.handleDelete(ctx, log, update.Key, update.Value, errChan)
				return
			}

			p.handleUpdate(ctx, log, update.Key, update.Value, errChan)
		},
	)

	log.Info("exception lifecycle processor stopped")
	return nil
}

// handleUpdate drives the ScheduleException state machine.
func (p *LifecycleProcessor) handleUpdate(ctx context.Context, log logr.Logger, key types.NamespacedName, exception *hibernatorv1alpha1.ScheduleException, errChan chan error) {
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
		p.Resources.ExceptionResources.Delete(key)
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
func (p *LifecycleProcessor) transitionState(ctx context.Context, log logr.Logger, key types.NamespacedName, exception *hibernatorv1alpha1.ScheduleException, desiredState hibernatorv1alpha1.ExceptionState, now time.Time) {
	oldState := exception.Status.State
	if oldState == "" {
		oldState = "<unset>"
	}

	nn := types.NamespacedName{Name: exception.Name, Namespace: exception.Namespace}

	// Queue status update via status processor
	p.Statuses.ExceptionStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.ScheduleException]{
		NamespacedName: nn,
		Resource:       exception,
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.ScheduleException](func(e *hibernatorv1alpha1.ScheduleException) {
			e.Status.State = desiredState

			switch desiredState {
			case hibernatorv1alpha1.ExceptionStatePending:
				e.Status.AppliedAt = nil
				e.Status.ExpiredAt = nil
				e.Status.Message = "Exception pending"
			case hibernatorv1alpha1.ExceptionStateActive:
				nowTime := now
				e.Status.AppliedAt = &metav1.Time{Time: nowTime}
				e.Status.ExpiredAt = nil
				e.Status.Message = "Exception activated"
			case hibernatorv1alpha1.ExceptionStateExpired:
				nowTime := now
				e.Status.ExpiredAt = &metav1.Time{Time: nowTime}
				e.Status.Message = "Exception expired"
			}
		}),
	})

	log.Info("queued exception state transition", "from", string(oldState), "to", string(desiredState))
}

// updateMessage updates the exception's status message with time-based information.
func (p *LifecycleProcessor) updateMessage(ctx context.Context, log logr.Logger, key types.NamespacedName, exception *hibernatorv1alpha1.ScheduleException, now time.Time) {
	var newMessage string

	switch exception.Status.State {
	case hibernatorv1alpha1.ExceptionStatePending:
		newMessage = formatPendingMessage(now, exception)
	case hibernatorv1alpha1.ExceptionStateActive:
		newMessage = formatActiveMessage(now, exception)
	case hibernatorv1alpha1.ExceptionStateExpired:
		newMessage = "Exception expired"
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

	nn := types.NamespacedName{Name: exception.Name, Namespace: exception.Namespace}
	p.Statuses.ExceptionStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.ScheduleException]{
		NamespacedName: nn,
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

// handleDelete handles an exception that has DeletionTimestamp set.
// The watchable delete event carries a stale snapshot, so we re-fetch the exception
// from the API server to get the current ResourceVersion before patching the finalizer.
func (p *LifecycleProcessor) handleDelete(ctx context.Context, log logr.Logger, key types.NamespacedName, _ *hibernatorv1alpha1.ScheduleException, errChan chan error) {
	// Re-fetch fresh copy from the API server to avoid 409 Conflict on stale ResourceVersion.
	exception := &hibernatorv1alpha1.ScheduleException{}
	if err := p.APIReader.Get(ctx, key, exception); err != nil {
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
	log.V(1).Info("handling exception deletion",
		"hasFinalizer", controllerutil.ContainsFinalizer(exception, wellknown.ExceptionFinalizerName))

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
			for _, ref := range p.Status.ActiveExceptions {
				if ref.Name != exceptionName {
					updated = append(updated, ref)
				}
			}
			p.Status.ActiveExceptions = updated
		}),
	})

	log.V(1).Info("queued removal of exception from plan status", "plan", planKey.Name)
	return nil
}
