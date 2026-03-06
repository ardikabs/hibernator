/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// hibernatingState drives stage-based Job execution for the shutdown operation.
type hibernatingState struct {
	*State
}

func (state *hibernatingState) Handle(ctx context.Context) {
	plan := state.plan()
	log := state.Log.
		WithName("hibernating").
		WithValues(
			"plan", state.Key.String(),
			"phase", plan.Status.Phase,
			"cycleID", plan.Status.CurrentCycleID,
			"stage", plan.Status.CurrentStageIndex)

	defer func() {
		if plan.Status.Phase == hibernatorv1alpha1.PhaseHibernating {
			state.RequeueAfter(wellknown.RequeueIntervalDuringStage)
		} else {
			state.CancelRequeue()
		}
	}()

	if plan.Status.CurrentOperation != "shutdown" {
		log.V(1).Info("Hibernating but currentOperation != shutdown, skipping",
			"currentOperation", plan.Status.CurrentOperation)
		return
	}

	state.execute(ctx, log, "shutdown", false,
		func(ctx context.Context, err error) { state.setError(ctx, err) },
		func(nextIdx int) { state.nextStage(nextIdx) },
		func(ctx context.Context, ep scheduler.ExecutionPlan) { state.finalize(ctx, log, ep) },
	)
}

func (state *hibernatingState) finalize(ctx context.Context, log logr.Logger, _ scheduler.ExecutionPlan) {
	plan := state.plan()

	if !IsOperationComplete(plan) {
		log.V(1).Info("targets still in progress, not completing shutdown yet")
		return
	}

	log.Info("all stages completed, finalizing shutdown operation")

	summary := BuildOperationSummary(state.Clock, plan, "shutdown")
	currentCycleID := plan.Status.CurrentCycleID

	mutate := func(st *hibernatorv1alpha1.HibernatePlanStatus) {
		st.Phase = hibernatorv1alpha1.PhaseHibernated
		st.LastTransitionTime = ptr.To(metav1.NewTime(state.Clock.Now()))

		cycleIdx := findOrAppendCycle(st, currentCycleID)
		if st.ExecutionHistory[cycleIdx].ShutdownExecution == nil {
			st.ExecutionHistory[cycleIdx].ShutdownExecution = summary
		}
		pruneCycleHistory(st)

		st.RetryCount = 0
		st.LastRetryTime = nil
		st.ErrorMessage = ""
	}

	mutate(&plan.Status)
	state.Statuses.PlanStatuses.Send(&message.PlanStatusUpdate{
		NamespacedName: state.Key,
		Mutate:         mutate,
	})

	state.dispatch(ctx)
}
