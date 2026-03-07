/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

func TestHibernatingState_Handle_WrongOperation_IsNoop(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernating)
	// CurrentOperation is not "shutdown" → handler should be a no-op (no status changes).
	plan.Status.CurrentOperation = "wakeup"
	plan.Spec.Execution.Strategy.Type = hibernatorv1alpha1.StrategySequential
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := &hibernatingState{state: st}
	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	// No status queued; poll timer reset as the phase is still Hibernating.
	assert.Zero(t, st.Statuses.PlanStatuses.Len())
	assert.True(t, result.RequeueAfter > 0, "poll timer should be reset while phase is still Hibernating")
}
