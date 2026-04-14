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
			return state.transitionToHibernating(log)
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
func (state *idleState) transitionToHibernating(log logr.Logger) (StateResult, error) {
	plan := state.plan()
	cycleID := uuid.New().String()[:8]
	now := state.Clock.Now()

	executions := make([]hibernatorv1alpha1.ExecutionStatus, len(plan.Spec.Targets))
	for i, t := range plan.Spec.Targets {
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
			p.Status.LastTransitionTime = ptr.To(metav1.NewTime(now))
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
	now := state.Clock.Now()

	executions := make([]hibernatorv1alpha1.ExecutionStatus, len(plan.Spec.Targets))
	for i, t := range plan.Spec.Targets {
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
			p.Status.LastTransitionTime = ptr.To(metav1.NewTime(now))
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
