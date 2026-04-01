/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"fmt"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/notification"
	statusprocessor "github.com/ardikabs/hibernator/internal/provider/processor/status"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// hibernatingState drives stage-based Job execution for the shutdown operation.
type hibernatingState struct {
	*state
}

func (state *hibernatingState) Handle(ctx context.Context) (StateResult, error) {
	plan := state.plan()
	log := state.Log.
		WithName("hibernating").
		WithValues(
			"plan", state.Key.String(),
			"phase", plan.Status.Phase,
			"cycleID", plan.Status.CurrentCycleID,
			"stage", plan.Status.CurrentStageIndex)

	if plan.Status.CurrentOperation != hibernatorv1alpha1.OperationHibernate {
		log.V(1).Info("Hibernating but currentOperation != shutdown, skipping",
			"currentOperation", plan.Status.CurrentOperation)
		return StateResult{}, AsPlanError(fmt.Errorf("mismatch between phase and operation: phase=%s operation=%s", plan.Status.Phase, plan.Status.CurrentOperation))
	}

	return state.execute(ctx, log, hibernatorv1alpha1.OperationHibernate, false,
		func(nextIdx int) { state.nextStage(nextIdx) },
		func(ctx context.Context, ep scheduler.ExecutionPlan) { state.finalize(ctx, log, ep) },
	)
}

func (state *hibernatingState) finalize(_ context.Context, log logr.Logger, _ scheduler.ExecutionPlan) {
	plan := state.plan()

	if !IsOperationComplete(plan) {
		log.V(1).Info("targets still in progress, not completing shutdown yet")
		return
	}

	log.Info("all stages completed, finalizing shutdown operation")

	summary := BuildOperationSummary(state.Clock, plan, hibernatorv1alpha1.OperationHibernate)
	currentCycleID := plan.Status.CurrentCycleID

	previousPhase := plan.Status.Phase
	state.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: state.Key,
		Resource:       plan,
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.Phase = hibernatorv1alpha1.PhaseHibernated
			p.Status.LastTransitionTime = ptr.To(metav1.NewTime(state.Clock.Now()))

			cycleIdx := findOrAppendCycle(&p.Status, currentCycleID)
			if p.Status.ExecutionHistory[cycleIdx].ShutdownExecution == nil {
				p.Status.ExecutionHistory[cycleIdx].ShutdownExecution = summary
			}
			pruneCycleHistory(&p.Status)

			p.Status.RetryCount = 0
			p.Status.LastRetryTime = nil
			p.Status.ErrorMessage = ""
		}),
		PostHook: chainHooks(
			state.notifyHook(hibernatorv1alpha1.EventSuccess, func(p *hibernatorv1alpha1.HibernatePlan) notification.Payload {
				return buildPayload(p, hibernatorv1alpha1.EventSuccess, state.Clock.Now)
			}),
			state.phaseChangePostHook(previousPhase),
		),
	})
}
