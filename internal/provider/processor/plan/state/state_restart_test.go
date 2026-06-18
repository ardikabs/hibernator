/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"testing"
	"time"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// Standalone restart from PhaseActive with CurrentOperation=wakeup + fresh + restore data →
// fresh is ignored; wakeup re-runs with existing cycle intent.
func TestRestartState_FromActive_OperationWakeup_Fresh_Ignored(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Status.CurrentOperation = hibernatorv1alpha1.OperationWakeUp
	plan.Status.CurrentCycleID = "cycle-001"
	plan.Status.AppliedExceptionOverride = "old-exc"
	plan.Status.PlanSnapshot = &hibernatorv1alpha1.PlanSnapshot{
		CycleID:       "cycle-001",
		ExceptionName: "old-exc",
		Targets:       []hibernatorv1alpha1.Target{{Name: "db", Type: "rds"}},
	}
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "app", Type: "eks"},
		{Name: "db", Type: "rds"},
	}

	exc := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "new-exc", Namespace: "default"},
		Status:     hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       hibernatorv1alpha1.ExceptionExtend,
			ValidFrom:  metav1.Time{Time: time.Now().Add(-24 * time.Hour)},
			ValidUntil: metav1.Time{Time: time.Now().Add(24 * time.Hour)},
			Windows:    []hibernatorv1alpha1.OffHourWindow{{Start: "00:00", End: "23:59", DaysOfWeek: []string{"MON"}}},
			TargetOverrides: []hibernatorv1alpha1.TargetOverride{
				{TargetName: "app", Disabled: true},
			},
		},
	}

	plan.Annotations = map[string]string{
		wellknown.AnnotationRestart: "true",
		wellknown.AnnotationFresh:   "true",
	}

	st := newOverrideActionState(plan, nil, true)
	st.PlanCtx.Exceptions = []hibernatorv1alpha1.ScheduleException{*exc}
	h := &restartState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.True(t, result.Requeue)
	assert.Equal(t, hibernatorv1alpha1.PhaseWakingUp, plan.Status.Phase)
	assert.NotContains(t, plan.Annotations, wellknown.AnnotationRestart)
	assert.NotContains(t, plan.Annotations, wellknown.AnnotationFresh)

	upd := <-planStatuses(st).C()
	require.NotNil(t, upd.Mutator)
	testPlan := plan.DeepCopy()
	upd.Mutator.Mutate(testPlan)

	// Fresh is ignored for wakeup: existing cycle and snapshot are preserved.
	assert.Equal(t, "cycle-001", testPlan.Status.CurrentCycleID)
	assert.Equal(t, "old-exc", testPlan.Status.AppliedExceptionOverride)
	require.NotNil(t, testPlan.Status.PlanSnapshot)
	assert.Equal(t, "old-exc", testPlan.Status.PlanSnapshot.ExceptionName)
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
