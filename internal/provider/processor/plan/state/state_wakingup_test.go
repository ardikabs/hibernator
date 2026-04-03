/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

func TestWakingUpState_Handle_WrongOperation_IsNoop(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseWakingUp)
	// CurrentOperation is not "wakeup" → handler should return a PlanError to
	// surface the mismatch and break any potential infinite loop.
	plan.Status.CurrentOperation = hibernatorv1alpha1.OperationHibernate
	plan.Spec.Execution.Strategy.Type = hibernatorv1alpha1.StrategySequential
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := &wakingUpState{state: st}
	_, err := h.Handle(context.Background())
	require.Error(t, err)
	var pe *PlanError
	assert.True(t, errors.As(err, &pe), "expected a PlanError for operation mismatch, got: %v", err)
}

func TestWakingUpState_OnError_WritesWakeupHistory(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseWakingUp)
	plan.Status.CurrentCycleID = "cycle-001"
	plan.Status.CurrentOperation = hibernatorv1alpha1.OperationWakeUp
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "target-a", State: hibernatorv1alpha1.StateFailed},
		{Target: "target-b", State: hibernatorv1alpha1.StateAborted},
	}

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)
	h := &wakingUpState{state: st}

	planErr := AsPlanError(assert.AnError)
	result := h.OnError(context.Background(), planErr)

	assert.True(t, result.Requeue, "OnError with PlanError should requeue")
	assert.Equal(t, hibernatorv1alpha1.PhaseError, plan.Status.Phase,
		"plan must transition to PhaseError")

	// Verify execution history was written
	require.Len(t, plan.Status.ExecutionHistory, 1, "expected one cycle in history")
	cycle := plan.Status.ExecutionHistory[0]
	assert.Equal(t, "cycle-001", cycle.CycleID)
	require.NotNil(t, cycle.WakeupExecution, "wakeup execution should be recorded")
	assert.Equal(t, hibernatorv1alpha1.OperationWakeUp, cycle.WakeupExecution.Operation)
	assert.False(t, cycle.WakeupExecution.Success, "wakeup should report failure")
	assert.Nil(t, cycle.ShutdownExecution, "shutdown execution should remain nil")
}

func TestWakingUpState_OnError_SkipsHistoryWhenAllPending(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseWakingUp)
	plan.Status.CurrentCycleID = "cycle-002"
	plan.Status.CurrentOperation = hibernatorv1alpha1.OperationWakeUp
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "target-a", State: hibernatorv1alpha1.StatePending},
		{Target: "target-b", State: hibernatorv1alpha1.StatePending},
	}

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)
	h := &wakingUpState{state: st}

	planErr := AsPlanError(assert.AnError)
	result := h.OnError(context.Background(), planErr)

	assert.True(t, result.Requeue, "OnError with PlanError should still requeue")
	assert.Equal(t, hibernatorv1alpha1.PhaseError, plan.Status.Phase,
		"plan must still transition to PhaseError")

	// Verify no execution history was written (guardrail: no progress)
	assert.Empty(t, plan.Status.ExecutionHistory,
		"execution history should remain empty when all executions are pending")
}

func TestWakingUpState_OnError_NonPlanError_NoHistory(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseWakingUp)
	plan.Status.CurrentCycleID = "cycle-003"
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "target-a", State: hibernatorv1alpha1.StateFailed},
	}

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)
	h := &wakingUpState{state: st}

	regularErr := errors.New("transient network error")
	_ = h.OnError(context.Background(), regularErr)

	// Non-PlanError should not write execution history
	assert.Empty(t, plan.Status.ExecutionHistory,
		"non-PlanError should not trigger history write")
}
