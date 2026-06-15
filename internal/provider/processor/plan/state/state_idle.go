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
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
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
		if shouldHibernate {
			log.Info("schedule indicates hibernation, transitioning to Hibernating")
			return state.transitionToHibernating(ctx, log)
		}

		log.V(1).Info("schedule indicates active period, no transition needed")

	case hibernatorv1alpha1.PhaseHibernated:
		if !shouldHibernate {
			if planCtx.HasRestoreData {
				log.Info("schedule indicates wake-up, transitioning to WakingUp")
				return state.transitionToWakingUp(log)
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
func (state *idleState) transitionToHibernating(ctx context.Context, log logr.Logger) (StateResult, error) {
	plan := state.plan()

	// For idempotent restart: reuse cycle ID from existing live restore data if available.
	// This ensures that if the runner restarts mid-operation, the same cycle ID is used
	// and the ManagedByCycleIDs markers remain valid for preserving already-processed state.
	cycleID := state.getExistingCycleIDForHibernation(ctx, log, plan)
	if cycleID == "" {
		cycleID = uuid.New().String()[:8]
		log.V(1).Info("generated new cycle ID for hibernation", "cycleID", cycleID)
	} else {
		log.V(1).Info("reusing existing cycle ID from live restore data", "cycleID", cycleID)
	}

	// Build effective plan with execution overrides applied at the start of the new cycle.
	// The effective plan is a deep copy; the original plan is never modified.
	var effectivePlan = plan
	appliedExceptionName := ""
	if ep := state.buildEffectivePlan(plan); ep != nil {
		effectivePlan = ep
		if exc := state.findActiveExceptionOverride(); exc != nil {
			appliedExceptionName = exc.Name
		}
	}

	now := state.Clock.Now()

	executions := make([]hibernatorv1alpha1.ExecutionStatus, len(effectivePlan.Spec.Targets))
	for i, t := range effectivePlan.Spec.Targets {
		executions[i] = hibernatorv1alpha1.ExecutionStatus{
			Target:   t.Name,
			Executor: t.Type,
			State:    hibernatorv1alpha1.StatePending,
			Message:  "Target pending hibernation",
		}
	}

	previousPhase := plan.Status.Phase
	state.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: state.Key,
		Resource:       plan,
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.Phase = hibernatorv1alpha1.PhaseHibernating
			p.Status.CurrentCycleID = cycleID
			p.Status.CurrentStageIndex = 0
			p.Status.CurrentOperation = hibernatorv1alpha1.OperationHibernate
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
func (state *idleState) transitionToWakingUp(log logr.Logger) (StateResult, error) {
	plan := state.plan()

	// Build effective plan with execution overrides applied at the start of the new cycle.
	// The effective plan is a deep copy; the original plan is never modified.
	var effectivePlan = plan
	appliedExceptionName := ""
	if ep := state.buildEffectivePlan(plan); ep != nil {
		effectivePlan = ep
		if exc := state.findActiveExceptionOverride(); exc != nil {
			appliedExceptionName = exc.Name
		}
	}

	now := state.Clock.Now()

	executions := make([]hibernatorv1alpha1.ExecutionStatus, len(effectivePlan.Spec.Targets))
	for i, t := range effectivePlan.Spec.Targets {
		executions[i] = hibernatorv1alpha1.ExecutionStatus{
			Target:   t.Name,
			Executor: t.Type,
			State:    hibernatorv1alpha1.StatePending,
			Message:  "Target pending wakeup",
		}
	}

	previousPhase := plan.Status.Phase
	state.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: state.Key,
		Resource:       plan,
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.Phase = hibernatorv1alpha1.PhaseWakingUp
			p.Status.CurrentStageIndex = 0
			p.Status.CurrentOperation = hibernatorv1alpha1.OperationWakeUp
			p.Status.Executions = executions
			p.Status.AppliedExceptionOverride = appliedExceptionName
			p.Status.LastTransitionTime = ptr.To(metav1.NewTime(now))
			if appliedExceptionName != "" {
				p.Status.PlanSnapshot = &hibernatorv1alpha1.PlanSnapshot{
					CycleID:       plan.Status.CurrentCycleID,
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
