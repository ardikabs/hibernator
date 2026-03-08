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

func TestHibernatingState_Handle_WrongOperation_IsNoop(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernating)
	// CurrentOperation is not "shutdown" → handler should return a PlanError to
	// surface the mismatch and break any potential infinite loop.
	plan.Status.CurrentOperation = hibernatorv1alpha1.OperationWakeUp
	plan.Spec.Execution.Strategy.Type = hibernatorv1alpha1.StrategySequential
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := &hibernatingState{state: st}
	_, err := h.Handle(context.Background())
	require.Error(t, err)
	var pe *PlanError
	assert.True(t, errors.As(err, &pe), "expected a PlanError for operation mismatch, got: %v", err)
}
