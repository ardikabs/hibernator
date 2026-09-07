/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/notification"
	statusprocessor "github.com/ardikabs/hibernator/internal/provider/processor/status"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// idleState handles the Active and Hibernated phases by evaluating the pre-computed
// schedule result and driving Active→Hibernating and Hibernated→WakingUp transitions.
type idleState struct {
	*state
}

func (state *idleState) Handle(ctx context.Context) (StateResult, error) {
	planCtx := state.PlanCtx
	plan := planCtx.Plan
	log := state.Log.
		WithName("idle").
		WithValues(
			"plan", state.Key.String(),
			"phase", plan.Status.Phase)

	if planCtx.Schedule == nil {
		log.V(1).Info("no schedule result available, skipping")
		return StateResult{}, nil
	}

	shouldHibernate := planCtx.Schedule.ShouldHibernate

	switch plan.Status.Phase {
	case hibernatorv1alpha1.PhaseActive:
		// Handle revert annotation consumption after successful revert wakeup
		if plan.Annotations[wellknown.AnnotationRevert] == "true" {
			// Ensure OperationTrigger is cleared when consuming the revert annotation.
			// This handles edge cases where the plan reached Active without going
			// through the normal wakeup finalize path (e.g., manual status patch).
			if plan.Status.OperationTrigger == hibernatorv1alpha1.TriggerRevert {
				log.V(1).Info("clearing OperationTrigger while consuming revert annotation")
				state.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
					NamespacedName: state.Key,
					Resource:       plan,
					Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
						p.Status.OperationTrigger = ""
					}),
				})
			}

			if shouldHibernate {
				// During hibernation window: suspend the plan after revert
				log.Info("revert complete during hibernation window, suspending plan")
				orig := plan.DeepCopy()
				plan.Spec.Suspend = true
				if plan.Annotations == nil {
					plan.Annotations = make(map[string]string)
				}
				plan.Annotations[wellknown.AnnotationSuspendReason] = "revert"
				delete(plan.Annotations, wellknown.AnnotationRevert)
				if err := state.patchAndPreserveStatus(ctx, plan, client.MergeFrom(orig)); err != nil {
					return StateResult{}, err
				}
				return StateResult{Requeue: true}, nil
			}
			// During active window: just remove the revert annotation
			log.Info("revert complete during active window, removing revert annotation")
			orig := plan.DeepCopy()
			delete(plan.Annotations, wellknown.AnnotationRevert)
			if err := state.patchAndPreserveStatus(ctx, plan, client.MergeFrom(orig)); err != nil {
				return StateResult{}, err
			}
			return StateResult{Requeue: true}, nil
		}

		if shouldHibernate {
			log.Info("schedule indicates hibernation, transitioning to Hibernating")
			return state.transitionToHibernating(ctx, log, false, hibernatorv1alpha1.TriggerSchedule)
		}

		log.V(1).Info("schedule indicates active period, no transition needed")

	case hibernatorv1alpha1.PhaseHibernated:
		if !shouldHibernate {
			if planCtx.HasRestoreData {
				log.Info("schedule indicates wake-up, transitioning to WakingUp")
				return state.transitionToWakingUp(log, hibernatorv1alpha1.TriggerSchedule)
			}
			log.Info("schedule indicates wake-up but no restore data found, skipping")
		} else {
			log.V(1).Info("schedule indicates hibernation period, staying Hibernated")
		}
	}
	return StateResult{}, nil
}

// transitionToHibernating initialises the shutdown operation, queues a status update,
// and returns Requeue so the worker immediately drives the Hibernating phase handler.
//
// When fresh is true, a new cycle ID is always generated and the PlanSnapshot is rebuilt
// from the live ScheduleException state. This is used when the operator explicitly requests
// a fresh cycle via the hibernator.ardikabs.com/fresh annotation.
func (state *idleState) transitionToHibernating(ctx context.Context, log logr.Logger, fresh bool, trigger hibernatorv1alpha1.OperationTrigger) (StateResult, error) {
	plan := state.plan()

	var cycleID string
	if fresh {
		// Fresh cycle: ignore any existing live restore data cycle ID and start anew.
		cycleID = uuid.New().String()[:8]
		log.V(1).Info("generated new cycle ID for fresh hibernation", "cycleID", cycleID)
	} else {
		// For idempotent restart: reuse cycle ID from existing live restore data if available.
		// This ensures that if the runner restarts mid-operation, the same cycle ID is used
		// and the ManagedByCycleIDs markers remain valid for preserving already-processed state.
		cycleID = state.getExistingCycleIDForHibernation(ctx, log, plan)
		if cycleID == "" {
			cycleID = uuid.New().String()[:8]
			log.V(1).Info("generated new cycle ID for hibernation", "cycleID", cycleID)
		} else {
			log.V(1).Info("reusing existing cycle ID from live restore data", "cycleID", cycleID)
		}
	}

	// Build effective plan with execution overrides applied at the start of the new cycle.
	// The effective plan is a deep copy; the original plan is never modified.
	// Disabled targets stay listed; they are seeded instantly-completed below (D1).
	var effectivePlan = plan
	appliedExceptionName := ""
	var hibernateOverrides []hibernatorv1alpha1.TargetOverride
	if ep := state.buildEffectivePlan(plan); ep != nil {
		effectivePlan = ep
		if exc := state.findActiveExceptionOverride(); exc != nil {
			appliedExceptionName = exc.Name
			hibernateOverrides = exc.Spec.TargetOverrides
		}
	}

	now := state.Clock.Now()

	executions := buildExecutionsWithSeededSkips(effectivePlan.Spec.Targets, hibernateOverrides, appliedExceptionName, "Target pending hibernation")

	previousPhase := plan.Status.Phase
	state.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: state.Key,
		Resource:       plan,
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.Phase = hibernatorv1alpha1.PhaseHibernating
			p.Status.CurrentCycleID = cycleID
			p.Status.CurrentStageIndex = 0
			p.Status.CurrentOperation = hibernatorv1alpha1.OperationHibernate
			p.Status.OperationTrigger = trigger
			p.Status.Executions = executions
			p.Status.AppliedExceptionOverride = appliedExceptionName
			p.Status.LastTransitionTime = ptr.To(metav1.NewTime(now))
			if appliedExceptionName != "" {
				p.Status.PlanSnapshot = &hibernatorv1alpha1.PlanSnapshot{
					CycleID:       cycleID,
					ExceptionName: appliedExceptionName,
					Targets:       effectivePlan.Spec.Targets,
					Execution:     effectivePlan.Spec.Execution,
					Behavior:      effectivePlan.Spec.Behavior,
				}
			}
		}),
		PostHook: chainHooks(
			state.notifyHook(hibernatorv1alpha1.EventStart, func(p *hibernatorv1alpha1.HibernatePlan) notification.Payload {
				return buildPayload(p, hibernatorv1alpha1.EventStart, state.Clock.Now)
			}),
			state.phaseChangePostHook(previousPhase),
		),
	})

	log.V(1).Info("queued transition to Hibernating", "cycleID", cycleID)
	return StateResult{Requeue: true}, nil
}

// transitionToWakingUp initialises the wakeup operation, queues a status update,
// and returns Requeue so the worker immediately drives the WakingUp phase handler.
//
// The existing PlanSnapshot is reused when its CycleID matches the plan's CurrentCycleID,
// ensuring cycle intent locking. If no snapshot exists, the live plan spec targets are used
// as a backward-compatible fallback.
func (state *idleState) transitionToWakingUp(log logr.Logger, trigger hibernatorv1alpha1.OperationTrigger) (StateResult, error) {
	plan := state.plan()

	now := state.Clock.Now()

	// Resolve the wakeup target list: snapshot for cycle intent locking,
	// live base as fallback. Disabled targets stay listed (D1); a suspend
	// override active now only seeds their executions as skipped below.
	// Snapshot/AppliedExceptionOverride are preserved for Monday revert.
	var targetList []hibernatorv1alpha1.Target
	if snap := plan.Status.PlanSnapshot; snap != nil && snap.CycleID == plan.Status.CurrentCycleID {
		targetList = snap.Targets
		log.V(1).Info("reusing plan snapshot targets for wakeup", "cycleID", snap.CycleID, "exception", snap.ExceptionName)
	} else if snap != nil {
		targetList = plan.Spec.Targets
		log.V(1).Info("plan snapshot cycleID mismatch, using live plan targets",
			"snapshotCycleID", snap.CycleID, "currentCycleID", plan.Status.CurrentCycleID)
	} else {
		targetList = plan.Spec.Targets
		log.V(1).Info("no plan snapshot for current cycle, using live plan targets")
	}

	var wakeOverrides []hibernatorv1alpha1.TargetOverride
	wakeExcName := ""
	if sus := state.findActiveSuspendOverride(); sus != nil {
		wakeOverrides = sus.Spec.TargetOverrides
		wakeExcName = sus.Name
		log.V(1).Info("seeding skipped executions for suspend wakeup", "exception", sus.Name)
	}
	executions := buildExecutionsWithSeededSkips(targetList, wakeOverrides, wakeExcName, "Target pending wakeup")

	previousPhase := plan.Status.Phase
	state.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: state.Key,
		Resource:       plan,
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.Phase = hibernatorv1alpha1.PhaseWakingUp
			p.Status.CurrentStageIndex = 0
			p.Status.CurrentOperation = hibernatorv1alpha1.OperationWakeUp
			p.Status.OperationTrigger = trigger
			p.Status.Executions = executions
			p.Status.LastTransitionTime = ptr.To(metav1.NewTime(now))
			// CurrentCycleID, AppliedExceptionOverride, and PlanSnapshot are preserved
			// from hibernation to maintain cycle intent locking.
		}),
		PostHook: chainHooks(
			state.notifyHook(hibernatorv1alpha1.EventStart, func(p *hibernatorv1alpha1.HibernatePlan) notification.Payload {
				return buildPayload(p, hibernatorv1alpha1.EventStart, state.Clock.Now)
			}),
			state.phaseChangePostHook(previousPhase),
		),
	})

	log.V(1).Info("queued transition to WakingUp", "cycleID", plan.Status.CurrentCycleID)
	return StateResult{Requeue: true}, nil
}

// getExistingCycleIDForHibernation checks if there's existing live restore data for any target
// in the plan and returns the cycle ID from that data. This enables idempotent restarts by
// reusing the same cycle ID when the runner restarts mid-hibernation, or when a suspended
// plan resumes and the schedule re-triggers hibernation.
//
// Restore data is the authoritative source of truth for cycle liveness. As long as
// data.IsLive is true, the cycle is considered active regardless of plan status changes
// (suspension, resume, worker restart, etc.).
// Returns empty string if no live restore data exists.
func (state *idleState) getExistingCycleIDForHibernation(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) string {
	if state.RestoreManager == nil {
		return ""
	}

	for _, target := range plan.Spec.Targets {
		data, err := state.RestoreManager.Load(ctx, plan.Namespace, plan.Name, target.Name)
		if err != nil {
			log.V(1).Error(err, "failed to load restore data for cycle ID check",
				"target", target.Name)
			continue
		}
		// Found live data with active cycle ID - reuse it for idempotent restart
		if data != nil && data.IsLive && data.CycleID != "" {
			return data.CycleID
		}
	}

	return ""
}
