/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"testing"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// restartState.Handle() — standalone restart based on CurrentOperation
// ---------------------------------------------------------------------------

// Standalone restart from PhaseHibernated with CurrentOperation=hibernate → re-trigger.
func TestRestartState_FromHibernated_OperationHibernate_ReTriggersHibernation(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
	plan.Status.CurrentOperation = hibernatorv1alpha1.OperationHibernate
	plan.Annotations = map[string]string{wellknown.AnnotationRestart: "true"}
	st := newOverrideActionState(plan, nil, false)
	h := &restartState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.True(t, result.Requeue, "restart must re-trigger Hibernating based on CurrentOperation")
	assert.Equal(t, hibernatorv1alpha1.PhaseHibernating, plan.Status.Phase)
	assert.NotContains(t, plan.Annotations, wellknown.AnnotationRestart,
		"restart annotation must be consumed (one-shot)")
}

// Standalone restart from PhaseActive with CurrentOperation=wakeup + restore data → re-trigger.
func TestRestartState_FromActive_OperationWakeup_WithRestoreData_ReTriggersWakeup(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Status.CurrentOperation = hibernatorv1alpha1.OperationWakeUp
	plan.Annotations = map[string]string{wellknown.AnnotationRestart: "true"}
	st := newOverrideActionState(plan, nil, true)
	h := &restartState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.True(t, result.Requeue)
	assert.Equal(t, hibernatorv1alpha1.PhaseWakingUp, plan.Status.Phase)
	assert.NotContains(t, plan.Annotations, wellknown.AnnotationRestart)
}

// Standalone restart: CurrentOperation=wakeup from Active but no restore data → consume + no-op.
func TestRestartState_FromActive_OperationWakeup_NoRestoreData_ConsumesThenNoop(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Status.CurrentOperation = hibernatorv1alpha1.OperationWakeUp
	plan.Annotations = map[string]string{wellknown.AnnotationRestart: "true"}
	st := newOverrideActionState(plan, nil, false)
	h := &restartState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.False(t, result.Requeue)
	assert.Equal(t, hibernatorv1alpha1.PhaseActive, plan.Status.Phase)
	assert.NotContains(t, plan.Annotations, wellknown.AnnotationRestart, "annotation must still be consumed")
}

// Standalone restart: CurrentOperation=hibernate but plan is Active (phase/op mismatch → no-op).
func TestRestartState_FromActive_OperationHibernate_PhaseMismatch_Noop(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Status.CurrentOperation = hibernatorv1alpha1.OperationHibernate
	plan.Annotations = map[string]string{wellknown.AnnotationRestart: "true"}
	st := newOverrideActionState(plan, nil, false)
	h := &restartState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.False(t, result.Requeue, "phase/op mismatch must be a no-op")
	assert.Equal(t, hibernatorv1alpha1.PhaseActive, plan.Status.Phase)
	assert.NotContains(t, plan.Annotations, wellknown.AnnotationRestart, "annotation still consumed on mismatch")
}

// Standalone restart: empty CurrentOperation → consume + no-op.
func TestRestartState_EmptyCurrentOperation_ConsumesThenNoop(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Status.CurrentOperation = "" // no prior cycle
	plan.Annotations = map[string]string{wellknown.AnnotationRestart: "true"}
	st := newOverrideActionState(plan, nil, false)
	h := &restartState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.False(t, result.Requeue)
	assert.NotContains(t, plan.Annotations, wellknown.AnnotationRestart)
	assert.Zero(t, planStatuses(st).Len())
}
