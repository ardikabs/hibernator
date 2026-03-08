/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	statusprocessor "github.com/ardikabs/hibernator/internal/provider/processor/status"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/go-logr/logr"
)

// suspendedState manages the Suspended phase:
//   - If suspend-until annotation is present and deadline is future → schedule deadlineTimer.
//   - If suspend-until has expired → patch Spec.Suspend=false + process resume.
//   - If Spec.Suspend is still true (no deadline or deadline future) → stay suspended.
//   - If Spec.Suspend is false → resume (cancel deadlineTimer + resume()).
type suspendedState struct {
	*state
}

func (state *suspendedState) Handle(ctx context.Context) (StateResult, error) {
	plan := state.plan()
	log := state.Log.
		WithName("suspended").
		WithValues(
			"plan", state.Key.String(),
			"phase", plan.Status.Phase)

	log.V(1).Info("processing suspended plan",
		"suspend", plan.Spec.Suspend,
		"hasSuspendUntil", plan.Annotations[wellknown.AnnotationSuspendUntil] != "")

	// --- suspend-until deadline handling ---
	if suspendUntilStr, ok := plan.Annotations[wellknown.AnnotationSuspendUntil]; ok {
		deadline, err := time.Parse(time.RFC3339, suspendUntilStr)
		if err != nil {
			log.Error(err, "invalid suspend-until annotation format, ignoring", "got", suspendUntilStr)
			return StateResult{}, err
		} else {
			now := state.Clock.Now()
			if !now.After(deadline) {
				// Deadline pending — schedule an internal timer.
				remaining := deadline.Sub(now)
				log.V(1).Info("suspend-until deadline pending, scheduling deadline timer",
					"deadline", deadline.Format(time.RFC3339),
					"remaining", remaining.Round(time.Second).String())
				return StateResult{DeadlineAfter: remaining}, nil
			}

			// Deadline expired — patch Spec.Suspend=false.
			log.Info("suspension deadline reached, revoking suspension", "deadline", deadline.Format(time.RFC3339))
			orig := plan.DeepCopy()
			plan.Spec.Suspend = false
			delete(plan.Annotations, wellknown.AnnotationSuspendUntil)
			if err := state.patchPreservingStatus(ctx, plan, client.MergeFrom(orig)); err != nil {
				log.Error(err, "failed to auto-resume from suspension deadline")
				return StateResult{}, err
			}
			// Fall through to resume logic below.
		}
	}

	if plan.Spec.Suspend {
		log.V(1).Info("plan still suspended, waiting for resume")
		return StateResult{}, nil
	}

	// Spec.Suspend is false — resume (timer cancellation implicit via Requeue result).
	return state.resume(ctx, log)
}

// OnDeadline is called when the worker's deadlineTimer fires. It patches
// Spec.Suspend=false and immediately processes the resume to avoid a full provider roundtrip.
func (state *suspendedState) OnDeadline(ctx context.Context) (StateResult, error) {
	plan := state.plan()

	log := state.Log.
		WithName("suspended").
		WithValues("plan", state.Key.String())

	log.Info("suspension deadline fired, revoking suspension")

	orig := plan.DeepCopy()
	plan.Spec.Suspend = false
	delete(plan.Annotations, wellknown.AnnotationSuspendUntil)
	if err := state.patchPreservingStatus(ctx, plan, client.MergeFrom(orig)); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "deadline: failed to patch Spec.Suspend=false")
		}
		return StateResult{}, err
	}

	return state.resume(ctx, log)
}

// resume transitions the plan from Suspended back to the appropriate phase.
// Called by suspendedState after Spec.Suspend is cleared.
//
// Priority order:
//  1. Suspended-at-Error → resumeFromError() (operation-aware idle phase + idleState re-evaluates)
//  2. Suspended-mid-execution → resumeFromExecution() (continue or route to idle baseline)
//  3. Force-wakeup conditions met → forceWakeUpOnResume()
//  4. Default → PhaseActive
func (state *suspendedState) resume(ctx context.Context, log logr.Logger) (StateResult, error) {
	plan := state.plan()

	log.Info("resuming plan from suspended state")

	if result, handled, err := state.resumeFromError(ctx, log); handled {
		return result, err
	}

	if result, handled, err := state.resumeFromExecution(ctx, log); handled {
		return result, err
	}

	if state.shouldForceWakeUpOnResume() {
		log.V(1).Info("force wakeup conditions met, transitioning to WakingUp")
		return state.forceWakeUpOnResume(ctx, log)
	}

	log.V(1).Info("normal resume, transitioning to Active")

	state.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: state.Key,
		Resource:       plan,
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.Phase = hibernatorv1alpha1.PhaseActive
			p.Status.LastTransitionTime = ptr.To(metav1.NewTime(state.Clock.Now()))
		}),
	})

	state.cleanupSuspensionAnnotations(ctx, log, plan)
	return StateResult{Requeue: true}, nil
}

// resumeFromError handles resume when the plan was suspended while in PhaseError.
// Rather than routing back through the error/retry machinery, it resolves to the
// correct idle phase based on which operation was in-flight when the error occurred:
//
//   - CurrentOperation == OperationShutdown → the resource was never shut down → PhaseActive
//   - CurrentOperation == OperationWakeUp   → the resource was never woken up  → PhaseHibernated
//
// The status is persisted (ErrorMessage, RetryCount cleared, Phase set to baseline) to
// acknowledge the operator's manual intervention. A guaranteed PlanStatuses.Send is issued
// before dispatch so that K8s reflects the baseline phase even if idleState takes no action
// (e.g. Active + ShouldHibernate=false is a no-op for idleState). idleState then
// re-evaluates the schedule naturally from the correct baseline — no special retry routing
// needed.
func (state *suspendedState) resumeFromError(ctx context.Context, log logr.Logger) (StateResult, bool, error) {
	plan := state.plan()
	suspendedAtPhase := plan.Annotations[wellknown.AnnotationSuspendedAtPhase]

	if suspendedAtPhase != string(hibernatorv1alpha1.PhaseError) {
		return StateResult{}, false, nil
	}

	log.Info("resuming from error-suspended state, routing via operation-aware idle phase")

	// Determine baseline phase from the failed operation.
	// Default to PhaseActive (safe: treats resource as never shut down).
	targetPhase := hibernatorv1alpha1.PhaseActive
	if plan.Status.CurrentOperation == hibernatorv1alpha1.OperationWakeUp {
		// Wakeup never completed → resource is still hibernated.
		targetPhase = hibernatorv1alpha1.PhaseHibernated
	}

	log.Info("resuming from error suspension to idle phase",
		"currentOperation", plan.Status.CurrentOperation,
		"targetPhase", targetPhase)

	// Apply the baseline-phase mutation in-memory AND queue a guaranteed status write
	// to K8s. This is necessary because idleState may take no action when it is
	// dispatched next (e.g. PhaseActive + ShouldHibernate=false → no-op branch), which
	// means no PlanStatuses.Send would ever be called and K8s would remain stuck at
	// PhaseSuspended. Sending here ensures the write reaches the StatusWriter regardless
	// of what the subsequent dispatch decides to do.
	// If idleState also queues a transition (e.g. PhaseActive → PhaseHibernating), the
	// KeyedWorkerPool processes updates FIFO, so the baseline write lands first and is
	// immediately followed by the correct next-phase write — both are visible in order.
	now := state.Clock.Now()
	state.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: state.Key,
		Resource:       plan,
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.Phase = targetPhase
			p.Status.RetryCount = 0
			p.Status.ErrorMessage = ""
			p.Status.LastRetryTime = nil
			p.Status.LastTransitionTime = ptr.To(metav1.NewTime(now))
		}),
	})

	state.cleanupSuspensionAnnotations(ctx, log, plan)
	return StateResult{Requeue: true}, true, nil
}

// (PhaseHibernating or PhaseWakingUp). It uses the current schedule result to determine
// whether the resume falls inside the same operation window or a different one:
//
//   - PhaseHibernating + ShouldHibernate=true  → still in off-hours → resume to PhaseHibernating;
//     existing CycleID/StageIndex/Executions are preserved so execute() re-observes in-flight
//     Job results and continues from the exact stage bookmark.
//   - PhaseHibernating + ShouldHibernate=false → now on-hours → route to PhaseActive;
//     execution bookmarks cleared; shutdown never completed, resource treated as running.
//   - PhaseWakingUp   + ShouldHibernate=false → still in on-hours → resume to PhaseWakingUp;
//     same bookmark-preservation semantics.
//   - PhaseWakingUp   + ShouldHibernate=true  → now off-hours → route to PhaseHibernated;
//     wakeup never completed, resource treated as still hibernated.
func (state *suspendedState) resumeFromExecution(ctx context.Context, log logr.Logger) (StateResult, bool, error) {
	plan := state.plan()
	suspendedAtPhase := plan.Annotations[wellknown.AnnotationSuspendedAtPhase]

	if suspendedAtPhase != string(hibernatorv1alpha1.PhaseHibernating) &&
		suspendedAtPhase != string(hibernatorv1alpha1.PhaseWakingUp) {
		return StateResult{}, false, nil
	}

	planCtx := state.PlanCtx
	if planCtx.ScheduleResult == nil {
		return StateResult{}, false, nil
	}

	shouldHibernate := planCtx.ScheduleResult.ShouldHibernate

	var targetPhase hibernatorv1alpha1.PlanPhase

	switch hibernatorv1alpha1.PlanPhase(suspendedAtPhase) {
	case hibernatorv1alpha1.PhaseHibernating:
		if shouldHibernate {
			// Still in the same off-hours window — continue the in-progress shutdown.
			targetPhase = hibernatorv1alpha1.PhaseHibernating
		} else {
			// Resumed during on-hours — shutdown was interrupted; resource never shut down.
			targetPhase = hibernatorv1alpha1.PhaseActive
		}
	case hibernatorv1alpha1.PhaseWakingUp:
		if !shouldHibernate {
			// Still in on-hours — continue the in-progress wakeup.
			targetPhase = hibernatorv1alpha1.PhaseWakingUp
		} else {
			// Resumed during off-hours — wakeup was interrupted; resource still hibernated.
			targetPhase = hibernatorv1alpha1.PhaseHibernated
		}
	}

	log.Info("resuming from mid-execution suspension",
		"suspendedAtPhase", suspendedAtPhase,
		"targetPhase", targetPhase,
		"shouldHibernate", shouldHibernate)

	now := state.Clock.Now()
	state.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: state.Key,
		Resource:       plan,
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.Phase = targetPhase
			p.Status.LastTransitionTime = ptr.To(metav1.NewTime(now))
		}),
	})

	state.cleanupSuspensionAnnotations(ctx, log, plan)
	return StateResult{Requeue: true}, true, nil
}

func (state *suspendedState) shouldForceWakeUpOnResume() bool {
	planCtx := state.PlanCtx
	plan := planCtx.Plan
	suspendedAtPhase := plan.Annotations[wellknown.AnnotationSuspendedAtPhase]

	if suspendedAtPhase == "" ||
		suspendedAtPhase == string(hibernatorv1alpha1.PhaseActive) ||
		suspendedAtPhase == string(hibernatorv1alpha1.PhaseError) {
		return false
	}
	if !planCtx.HasRestoreData {
		return false
	}
	if planCtx.ScheduleResult == nil {
		return false
	}
	return !planCtx.ScheduleResult.ShouldHibernate
}

func (state *suspendedState) forceWakeUpOnResume(ctx context.Context, log logr.Logger) (StateResult, error) {
	plan := state.plan()
	suspendedAtPhase := plan.Annotations[wellknown.AnnotationSuspendedAtPhase]
	log.Info("forcing wake-up after resume from suspension",
		"suspendedAtPhase", suspendedAtPhase,
		"reason", "restore data exists and schedule indicates active period")

	// Transition to Hibernated rather than directly to WakingUp. This lets
	// the worker re-evaluate via idleState, which sees !ShouldHibernate + HasRestoreData
	// and calls transitionToWakingUp() — the canonical path that correctly
	// initialises Executions, CycleID, StageIndex, and CurrentOperation.
	// Going directly to WakingUp would inherit stale execution state from
	// the pre-suspension cycle.
	plan.Status.Phase = hibernatorv1alpha1.PhaseHibernated

	log.Info("plan resumed to Hibernated phase, returning Requeue to trigger wake-up flow", "targetPhase", plan.Status.Phase)
	state.cleanupSuspensionAnnotations(ctx, log, plan)
	return StateResult{Requeue: true}, nil
}

func (state *suspendedState) cleanupSuspensionAnnotations(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) {
	_, hasSuspendedAt := plan.Annotations[wellknown.AnnotationSuspendedAtPhase]
	_, hasSuspendReason := plan.Annotations[wellknown.AnnotationSuspendReason]
	if !hasSuspendedAt && !hasSuspendReason {
		return
	}
	orig := plan.DeepCopy()
	delete(plan.Annotations, wellknown.AnnotationSuspendedAtPhase)
	delete(plan.Annotations, wellknown.AnnotationSuspendReason)
	if err := state.patchPreservingStatus(ctx, plan, client.MergeFrom(orig)); err != nil {
		log.Error(err, "failed to clean up suspension annotations (non-fatal)")
	}
}
