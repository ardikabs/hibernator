/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// idleState handles the Active and Hibernated phases by evaluating the pre-computed
// schedule result and driving Active→Hibernating and Hibernated→WakingUp transitions.
type idleState struct {
	*State
}

func (state *idleState) Handle(ctx context.Context) {
	planCtx := state.PlanCtx
	plan := planCtx.Plan
	log := state.Log.
		WithName("idle").
		WithValues(
			"plan", state.Key.String(),
			"phase", plan.Status.Phase)

	if plan.DeletionTimestamp != nil && !plan.DeletionTimestamp.IsZero() {
		log.V(1).Info("plan has deletion timestamp, skipping schedule evaluation")
		return
	}

	if planCtx.ScheduleResult == nil {
		log.V(1).Info("no schedule result available, skipping")
		return
	}

	shouldHibernate := planCtx.ScheduleResult.ShouldHibernate

	switch plan.Status.Phase {
	case hibernatorv1alpha1.PhaseActive:
		if shouldHibernate {
			log.Info("schedule indicates hibernation, transitioning to Hibernating")

			state.transitionToHibernating(ctx, log)
			return
		}

		log.V(1).Info("schedule indicates active period, no transition needed")

	case hibernatorv1alpha1.PhaseHibernated:
		if !shouldHibernate {
			if planCtx.HasRestoreData {
				log.Info("schedule indicates wake-up, transitioning to WakingUp")

				state.transitionToWakingUp(ctx, log)
				return
			}
			log.Info("schedule indicates wake-up but no restore data found, skipping")
		} else {
			log.V(1).Info("schedule indicates hibernation period, staying Hibernated")
		}
	}
}

// transitionToHibernating initialises the shutdown operation, queues a status update,
// and immediately dispatches to the Hibernating phase handler.
func (state *idleState) transitionToHibernating(ctx context.Context, log logr.Logger) {
	plan := state.plan()
	cycleID := uuid.New().String()[:8]
	now := state.Clock.Now()

	executions := make([]hibernatorv1alpha1.ExecutionStatus, len(plan.Spec.Targets))
	for i, t := range plan.Spec.Targets {
		executions[i] = hibernatorv1alpha1.ExecutionStatus{
			Target:   t.Name,
			Executor: t.Type,
			State:    hibernatorv1alpha1.StatePending,
		}
	}

	mutate := func(st *hibernatorv1alpha1.HibernatePlanStatus) {
		st.Phase = hibernatorv1alpha1.PhaseHibernating
		st.CurrentCycleID = cycleID
		st.CurrentStageIndex = 0
		st.CurrentOperation = "shutdown"
		st.Executions = executions
		st.LastTransitionTime = ptr.To(metav1.NewTime(now))
	}

	mutate(&plan.Status)
	state.Statuses.PlanStatuses.Send(&message.PlanStatusUpdate{
		NamespacedName: state.Key,
		Mutate:         mutate,
	})

	log.V(1).Info("queued transition to Hibernating", "cycleID", cycleID)
	state.dispatch(ctx)
}

// transitionToWakingUp initialises the wakeup operation, queues a status update,
// and immediately dispatches to the WakingUp phase handler.
func (state *idleState) transitionToWakingUp(ctx context.Context, log logr.Logger) {
	plan := state.plan()
	now := state.Clock.Now()

	executions := make([]hibernatorv1alpha1.ExecutionStatus, len(plan.Spec.Targets))
	for i, t := range plan.Spec.Targets {
		executions[i] = hibernatorv1alpha1.ExecutionStatus{
			Target:   t.Name,
			Executor: t.Type,
			State:    hibernatorv1alpha1.StatePending,
		}
	}

	mutate := func(st *hibernatorv1alpha1.HibernatePlanStatus) {
		st.Phase = hibernatorv1alpha1.PhaseWakingUp
		st.CurrentStageIndex = 0
		st.CurrentOperation = "wakeup"
		st.Executions = executions
		st.LastTransitionTime = ptr.To(metav1.NewTime(now))
	}

	mutate(&plan.Status)
	state.Statuses.PlanStatuses.Send(&message.PlanStatusUpdate{
		NamespacedName: state.Key,
		Mutate:         mutate,
	})

	log.V(1).Info("queued transition to WakingUp")
	state.dispatch(ctx)
}
