/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

func TestWakingUpState_Handle_WrongOperation_IsNoop(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseWakingUp)
	// CurrentOperation is not "wakeup" → handler should be a no-op.
	plan.Status.CurrentOperation = "shutdown"
	plan.Spec.Execution.Strategy.Type = hibernatorv1alpha1.StrategySequential
	c := newHandlerFakeClient(plan)
	tt := &timerTracker{}
	state := newHandlerState(plan, c, tt)

	h := &wakingUpState{State: state}
	h.Handle(context.Background())

	// No status queued; poll timer reset as the phase is still WakingUp.
	assert.Zero(t, state.Statuses.PlanStatuses.Len())
	assert.True(t, tt.requeueCalled, "requeue timer should be reset while phase is still WakingUp")
}
