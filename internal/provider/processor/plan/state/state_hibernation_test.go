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

func TestHibernatingState_Handle_WrongOperation_IsNoop(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernating)
	// CurrentOperation is not "shutdown" → handler should be a no-op (no status changes).
	plan.Status.CurrentOperation = "wakeup"
	plan.Spec.Execution.Strategy.Type = hibernatorv1alpha1.StrategySequential
	c := newHandlerFakeClient(plan)
	tt := &timerTracker{}
	state := newHandlerState(plan, c, tt)

	h := &hibernatingState{State: state}
	h.Handle(context.Background())

	// No status queued; poll timer reset as the phase is still Hibernating.
	assert.Zero(t, state.Statuses.PlanStatuses.Len())
	assert.True(t, tt.requeueCalled, "poll timer should be reset while phase is still Hibernating")
}
