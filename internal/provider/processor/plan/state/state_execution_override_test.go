/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/scheduler"
)

// ---------------------------------------------------------------------------
// buildEffectivePlan
// ---------------------------------------------------------------------------

func TestBuildEffectivePlan_NoActiveException_ReturnsNil(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	ep := st.buildEffectivePlan(plan)
	assert.Nil(t, ep)
}

func TestBuildEffectivePlan_SuspendException_ReturnsNil(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds"},
	}

	exc := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "suspend-exc", Namespace: "default"},
		Status:     hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:      hibernatorv1alpha1.ExceptionSuspend,
			ValidFrom: metav1.Time{Time: time.Now().Add(-24 * time.Hour)},
			ValidUntil: metav1.Time{Time: time.Now().Add(24 * time.Hour)},
			Windows: []hibernatorv1alpha1.OffHourWindow{{Start: "00:00", End: "23:59", DaysOfWeek: []string{"MON"}}},
			TargetOverrides: []hibernatorv1alpha1.TargetOverride{
				{TargetName: "db", Disabled: true},
			},
		},
	}

	c := newHandlerFakeClient(plan, exc)
	st := newHandlerState(plan, c)
	st.PlanCtx.Exceptions = []hibernatorv1alpha1.ScheduleException{*exc}

	ep := st.buildEffectivePlan(plan)
	assert.Nil(t, ep, "suspend exceptions should not have effective plan")
}

func TestBuildEffectivePlan_ParameterOverride(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds", Parameters: &hibernatorv1alpha1.Parameters{Raw: []byte(`{"env":"prod"}`)}},
	}

	exc := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "override-exc", Namespace: "default"},
		Status:     hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       hibernatorv1alpha1.ExceptionExtend,
			ValidFrom:  metav1.Time{Time: time.Now().Add(-24 * time.Hour)},
			ValidUntil: metav1.Time{Time: time.Now().Add(24 * time.Hour)},
			Windows: []hibernatorv1alpha1.OffHourWindow{{Start: "00:00", End: "23:59", DaysOfWeek: []string{"MON"}}},
			TargetOverrides: []hibernatorv1alpha1.TargetOverride{
				{TargetName: "db", Parameters: &hibernatorv1alpha1.Parameters{Raw: []byte(`{"env":"event"}`)}},
			},
		},
	}

	c := newHandlerFakeClient(plan, exc)
	st := newHandlerState(plan, c)
	st.PlanCtx.Exceptions = []hibernatorv1alpha1.ScheduleException{*exc}

	ep := st.buildEffectivePlan(plan)
	require.NotNil(t, ep)

	// Effective plan should have the overridden parameter
	require.Len(t, ep.Spec.Targets, 1)
	require.NotNil(t, ep.Spec.Targets[0].Parameters)
	assert.Equal(t, `{"env":"event"}`, string(ep.Spec.Targets[0].Parameters.Raw))

	// Original plan should be unchanged
	require.NotNil(t, plan.Spec.Targets[0].Parameters)
	assert.Equal(t, `{"env":"prod"}`, string(plan.Spec.Targets[0].Parameters.Raw))
}

func TestBuildEffectivePlan_DisabledTarget(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds"},
		{Name: "app", Type: "eks"},
	}

	exc := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "override-exc", Namespace: "default"},
		Status:     hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       hibernatorv1alpha1.ExceptionExtend,
			ValidFrom:  metav1.Time{Time: time.Now().Add(-24 * time.Hour)},
			ValidUntil: metav1.Time{Time: time.Now().Add(24 * time.Hour)},
			Windows: []hibernatorv1alpha1.OffHourWindow{{Start: "00:00", End: "23:59", DaysOfWeek: []string{"MON"}}},
			TargetOverrides: []hibernatorv1alpha1.TargetOverride{
				{TargetName: "db", Disabled: true},
			},
		},
	}

	c := newHandlerFakeClient(plan, exc)
	st := newHandlerState(plan, c)
	st.PlanCtx.Exceptions = []hibernatorv1alpha1.ScheduleException{*exc}

	ep := st.buildEffectivePlan(plan)
	require.NotNil(t, ep)

	// Effective plan should have only the non-disabled target
	require.Len(t, ep.Spec.Targets, 1)
	assert.Equal(t, "app", ep.Spec.Targets[0].Name)

	// Original plan should be unchanged
	require.Len(t, plan.Spec.Targets, 2)
	assert.Equal(t, "db", plan.Spec.Targets[0].Name)
	assert.Equal(t, "app", plan.Spec.Targets[1].Name)
}

func TestBuildEffectivePlan_StrategyOverride(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Spec.Execution.Strategy = hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategyParallel}

	exc := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "override-exc", Namespace: "default"},
		Status:     hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       hibernatorv1alpha1.ExceptionReplace,
			ValidFrom:  metav1.Time{Time: time.Now().Add(-24 * time.Hour)},
			ValidUntil: metav1.Time{Time: time.Now().Add(24 * time.Hour)},
			Windows: []hibernatorv1alpha1.OffHourWindow{{Start: "00:00", End: "23:59", DaysOfWeek: []string{"MON"}}},
			ExecutionOverride: &hibernatorv1alpha1.ExecutionOverride{
				Strategy: &hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
			},
		},
	}

	c := newHandlerFakeClient(plan, exc)
	st := newHandlerState(plan, c)
	st.PlanCtx.Exceptions = []hibernatorv1alpha1.ScheduleException{*exc}

	ep := st.buildEffectivePlan(plan)
	require.NotNil(t, ep)

	// Effective plan should have the overridden strategy
	assert.Equal(t, hibernatorv1alpha1.StrategySequential, ep.Spec.Execution.Strategy.Type)

	// Original plan should be unchanged
	assert.Equal(t, hibernatorv1alpha1.StrategyParallel, plan.Spec.Execution.Strategy.Type)
}

func TestBuildEffectivePlan_BehaviorOverride(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Spec.Behavior = hibernatorv1alpha1.Behavior{Mode: hibernatorv1alpha1.BehaviorStrict}

	exc := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "override-exc", Namespace: "default"},
		Status:     hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       hibernatorv1alpha1.ExceptionReplace,
			ValidFrom:  metav1.Time{Time: time.Now().Add(-24 * time.Hour)},
			ValidUntil: metav1.Time{Time: time.Now().Add(24 * time.Hour)},
			Windows: []hibernatorv1alpha1.OffHourWindow{{Start: "00:00", End: "23:59", DaysOfWeek: []string{"MON"}}},
			ExecutionOverride: &hibernatorv1alpha1.ExecutionOverride{
				Behavior: &hibernatorv1alpha1.Behavior{Mode: hibernatorv1alpha1.BehaviorBestEffort},
			},
		},
	}

	c := newHandlerFakeClient(plan, exc)
	st := newHandlerState(plan, c)
	st.PlanCtx.Exceptions = []hibernatorv1alpha1.ScheduleException{*exc}

	ep := st.buildEffectivePlan(plan)
	require.NotNil(t, ep)

	// Effective plan should have the overridden behavior
	assert.Equal(t, hibernatorv1alpha1.BehaviorBestEffort, ep.Spec.Behavior.Mode)

	// Original plan should be unchanged
	assert.Equal(t, hibernatorv1alpha1.BehaviorStrict, plan.Spec.Behavior.Mode)
}

func TestBuildEffectivePlan_StatusPreserved(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Status.CurrentCycleID = "cycle-001"
	plan.Status.CurrentStageIndex = 2

	exc := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "override-exc", Namespace: "default"},
		Status:     hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       hibernatorv1alpha1.ExceptionExtend,
			ValidFrom:  metav1.Time{Time: time.Now().Add(-24 * time.Hour)},
			ValidUntil: metav1.Time{Time: time.Now().Add(24 * time.Hour)},
			Windows: []hibernatorv1alpha1.OffHourWindow{{Start: "00:00", End: "23:59", DaysOfWeek: []string{"MON"}}},
			ExecutionOverride: &hibernatorv1alpha1.ExecutionOverride{
				Strategy: &hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
			},
		},
	}

	c := newHandlerFakeClient(plan, exc)
	st := newHandlerState(plan, c)
	st.PlanCtx.Exceptions = []hibernatorv1alpha1.ScheduleException{*exc}

	ep := st.buildEffectivePlan(plan)
	require.NotNil(t, ep)

	// Effective plan should preserve the status
	assert.Equal(t, "cycle-001", ep.Status.CurrentCycleID)
	assert.Equal(t, 2, ep.Status.CurrentStageIndex)
}

// ---------------------------------------------------------------------------
// findActiveExceptionOverride
// ---------------------------------------------------------------------------

func TestFindActiveExceptionOverride_MostRecentWins(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)

	olderExc := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "older-exc",
			Namespace:         "default",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-1 * time.Hour)),
		},
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       hibernatorv1alpha1.ExceptionExtend,
			ValidFrom:  metav1.Time{Time: time.Now().Add(-24 * time.Hour)},
			ValidUntil: metav1.Time{Time: time.Now().Add(24 * time.Hour)},
			Windows: []hibernatorv1alpha1.OffHourWindow{{Start: "00:00", End: "23:59", DaysOfWeek: []string{"MON"}}},
			ExecutionOverride: &hibernatorv1alpha1.ExecutionOverride{
				Strategy: &hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
			},
		},
	}

	newerExc := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "newer-exc",
			Namespace:         "default",
			CreationTimestamp: metav1.NewTime(time.Now()),
		},
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       hibernatorv1alpha1.ExceptionExtend,
			ValidFrom:  metav1.Time{Time: time.Now().Add(-24 * time.Hour)},
			ValidUntil: metav1.Time{Time: time.Now().Add(24 * time.Hour)},
			Windows: []hibernatorv1alpha1.OffHourWindow{{Start: "00:00", End: "23:59", DaysOfWeek: []string{"MON"}}},
			ExecutionOverride: &hibernatorv1alpha1.ExecutionOverride{
				Strategy: &hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategyDAG},
			},
		},
	}

	c := newHandlerFakeClient(plan, olderExc, newerExc)
	st := newHandlerState(plan, c)
	st.PlanCtx.Exceptions = []hibernatorv1alpha1.ScheduleException{*olderExc, *newerExc}

	result := st.findActiveExceptionOverride()
	require.NotNil(t, result)
	assert.Equal(t, "newer-exc", result.Name, "most recent exception should be selected")
}

// ---------------------------------------------------------------------------
// validateRuntimeOverrides
// ---------------------------------------------------------------------------

func TestValidateRuntimeOverrides_Valid(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds"},
	}
	plan.Status.CurrentStageIndex = 0

	exc := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "override-exc", Namespace: "default"},
		Status:     hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       hibernatorv1alpha1.ExceptionExtend,
			ValidFrom:  metav1.Time{Time: time.Now().Add(-24 * time.Hour)},
			ValidUntil: metav1.Time{Time: time.Now().Add(24 * time.Hour)},
			Windows: []hibernatorv1alpha1.OffHourWindow{{Start: "00:00", End: "23:59", DaysOfWeek: []string{"MON"}}},
			TargetOverrides: []hibernatorv1alpha1.TargetOverride{
				{TargetName: "db", Parameters: &hibernatorv1alpha1.Parameters{Raw: []byte(`{"selector":{"instanceIds":["my-db"]}}`)}},
			},
		},
	}

	c := newHandlerFakeClient(plan, exc)
	st := newHandlerState(plan, c)
	st.PlanCtx.Exceptions = []hibernatorv1alpha1.ScheduleException{*exc}

	// Build effective plan for validation
	ep := st.buildEffectivePlan(plan)
	require.NotNil(t, ep)

	err := st.validateRuntimeOverrides(nil, st.Log, ep)
	assert.NoError(t, err)
}

func TestValidateRuntimeOverrides_InvalidParameters(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds"},
	}
	plan.Status.CurrentStageIndex = 0

	exc := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "override-exc", Namespace: "default"},
		Status:     hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       hibernatorv1alpha1.ExceptionExtend,
			ValidFrom:  metav1.Time{Time: time.Now().Add(-24 * time.Hour)},
			ValidUntil: metav1.Time{Time: time.Now().Add(24 * time.Hour)},
			Windows: []hibernatorv1alpha1.OffHourWindow{{Start: "00:00", End: "23:59", DaysOfWeek: []string{"MON"}}},
			TargetOverrides: []hibernatorv1alpha1.TargetOverride{
				{TargetName: "db", Parameters: &hibernatorv1alpha1.Parameters{Raw: []byte(`{"invalid":"params"}`)}},
			},
		},
	}

	c := newHandlerFakeClient(plan, exc)
	st := newHandlerState(plan, c)
	st.PlanCtx.Exceptions = []hibernatorv1alpha1.ScheduleException{*exc}

	ep := st.buildEffectivePlan(plan)
	require.NotNil(t, ep)

	err := st.validateRuntimeOverrides(nil, st.Log, ep)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "db")
}

func TestValidateRuntimeOverrides_NotAtStageZero(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds"},
	}
	plan.Status.CurrentStageIndex = 1

	exc := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "override-exc", Namespace: "default"},
		Status:     hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       hibernatorv1alpha1.ExceptionExtend,
			ValidFrom:  metav1.Time{Time: time.Now().Add(-24 * time.Hour)},
			ValidUntil: metav1.Time{Time: time.Now().Add(24 * time.Hour)},
			Windows: []hibernatorv1alpha1.OffHourWindow{{Start: "00:00", End: "23:59", DaysOfWeek: []string{"MON"}}},
			TargetOverrides: []hibernatorv1alpha1.TargetOverride{
				{TargetName: "db", Parameters: &hibernatorv1alpha1.Parameters{Raw: []byte(`{"invalid":"params"}`)}},
			},
		},
	}

	c := newHandlerFakeClient(plan, exc)
	st := newHandlerState(plan, c)
	st.PlanCtx.Exceptions = []hibernatorv1alpha1.ScheduleException{*exc}

	// Build effective plan
	ep := st.buildEffectivePlan(plan)
	require.NotNil(t, ep)

	// validateRuntimeOverrides validates regardless of stage index.
	// The stage-0 guard is in execute(), not in validateRuntimeOverrides.
	err := st.validateRuntimeOverrides(nil, st.Log, ep)
	assert.Error(t, err, "runtime validation should always validate parameters")
}

// ---------------------------------------------------------------------------
// Fresh cycle semantics
// ---------------------------------------------------------------------------

func TestTransitionToHibernating_NoOverride_AppliedExceptionOverrideEmpty(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds"},
	}

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := &idleState{state: st}
	_, err := h.transitionToHibernating(nil, st.Log)
	require.NoError(t, err)

	upd := <-planStatuses(st).C()
	require.NotNil(t, upd.Mutator)

	testPlan := plan.DeepCopy()
	upd.Mutator.Mutate(testPlan)

	// AppliedExceptionOverride should be empty when no override is applied
	assert.Empty(t, testPlan.Status.AppliedExceptionOverride)
	assert.Equal(t, hibernatorv1alpha1.PhaseHibernating, testPlan.Status.Phase)
}

func TestTransitionToHibernating_UsesEffectivePlanTargets(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds"},
		{Name: "app", Type: "eks"},
	}

	exc := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "override-exc", Namespace: "default"},
		Status:     hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       hibernatorv1alpha1.ExceptionExtend,
			ValidFrom:  metav1.Time{Time: time.Now().Add(-24 * time.Hour)},
			ValidUntil: metav1.Time{Time: time.Now().Add(24 * time.Hour)},
			Windows: []hibernatorv1alpha1.OffHourWindow{{Start: "00:00", End: "23:59", DaysOfWeek: []string{"MON"}}},
			TargetOverrides: []hibernatorv1alpha1.TargetOverride{
				{TargetName: "db", Disabled: true},
			},
		},
	}

	c := newHandlerFakeClient(plan, exc)
	st := newHandlerState(plan, c)
	st.PlanCtx.Exceptions = []hibernatorv1alpha1.ScheduleException{*exc}

	h := &idleState{state: st}
	_, err := h.transitionToHibernating(nil, st.Log)
	require.NoError(t, err)

	upd := <-planStatuses(st).C()
	require.NotNil(t, upd.Mutator)

	testPlan := plan.DeepCopy()
	upd.Mutator.Mutate(testPlan)

	// Executions should be built from effective plan (only "app", not "db")
	require.Len(t, testPlan.Status.Executions, 1)
	assert.Equal(t, "app", testPlan.Status.Executions[0].Target)

	// AppliedExceptionOverride should be recorded
	assert.Equal(t, "override-exc", testPlan.Status.AppliedExceptionOverride)

	// Original plan should not be mutated
	require.Len(t, plan.Spec.Targets, 2)
	assert.Equal(t, "db", plan.Spec.Targets[0].Name)
	assert.Equal(t, "app", plan.Spec.Targets[1].Name)
}

func TestTransitionToWakingUp_UsesEffectivePlanTargets(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds"},
		{Name: "app", Type: "eks"},
	}
	plan.Status.CurrentCycleID = "cycle-001"

	exc := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "override-exc", Namespace: "default"},
		Status:     hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       hibernatorv1alpha1.ExceptionExtend,
			ValidFrom:  metav1.Time{Time: time.Now().Add(-24 * time.Hour)},
			ValidUntil: metav1.Time{Time: time.Now().Add(24 * time.Hour)},
			Windows: []hibernatorv1alpha1.OffHourWindow{{Start: "00:00", End: "23:59", DaysOfWeek: []string{"MON"}}},
			TargetOverrides: []hibernatorv1alpha1.TargetOverride{
				{TargetName: "db", Disabled: true},
			},
		},
	}

	c := newHandlerFakeClient(plan, exc)
	st := newHandlerState(plan, c)
	st.PlanCtx.Exceptions = []hibernatorv1alpha1.ScheduleException{*exc}

	h := &idleState{state: st}
	_, err := h.transitionToWakingUp(st.Log)
	require.NoError(t, err)

	upd := <-planStatuses(st).C()
	require.NotNil(t, upd.Mutator)

	testPlan := plan.DeepCopy()
	upd.Mutator.Mutate(testPlan)

	// Executions should be built from effective plan (only "app", not "db")
	require.Len(t, testPlan.Status.Executions, 1)
	assert.Equal(t, "app", testPlan.Status.Executions[0].Target)

	// AppliedExceptionOverride should be recorded
	assert.Equal(t, "override-exc", testPlan.Status.AppliedExceptionOverride)
}

// ---------------------------------------------------------------------------
// Full override: targetOverrides + executionOverride
// ---------------------------------------------------------------------------
// Webhook override validation (via executorparams)
// ---------------------------------------------------------------------------

func TestBuildEffectivePlan_UnknownTarget_Skipped(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds"},
	}

	exc := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "override-exc", Namespace: "default"},
		Status:     hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       hibernatorv1alpha1.ExceptionExtend,
			ValidFrom:  metav1.Time{Time: time.Now().Add(-24 * time.Hour)},
			ValidUntil: metav1.Time{Time: time.Now().Add(24 * time.Hour)},
			Windows: []hibernatorv1alpha1.OffHourWindow{{Start: "00:00", End: "23:59", DaysOfWeek: []string{"MON"}}},
			TargetOverrides: []hibernatorv1alpha1.TargetOverride{
				{TargetName: "nonexistent", Disabled: true},
			},
		},
	}

	c := newHandlerFakeClient(plan, exc)
	st := newHandlerState(plan, c)
	st.PlanCtx.Exceptions = []hibernatorv1alpha1.ScheduleException{*exc}

	ep := st.buildEffectivePlan(plan)
	require.NotNil(t, ep)

	// Non-existent target override should be skipped
	require.Len(t, ep.Spec.Targets, 1)
	assert.Equal(t, "db", ep.Spec.Targets[0].Name)
}

// ---------------------------------------------------------------------------
// Full override: targetOverrides + executionOverride
// ---------------------------------------------------------------------------

func TestBuildEffectivePlan_FullOverride(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds", Parameters: &hibernatorv1alpha1.Parameters{Raw: []byte(`{"env":"prod"}`)}},
		{Name: "app", Type: "eks", Parameters: &hibernatorv1alpha1.Parameters{Raw: []byte(`{"cluster":"prod"}`)}},
	}
	plan.Spec.Execution.Strategy = hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategyParallel}
	plan.Spec.Behavior = hibernatorv1alpha1.Behavior{Mode: hibernatorv1alpha1.BehaviorStrict}

	exc := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "override-exc", Namespace: "default"},
		Status:     hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       hibernatorv1alpha1.ExceptionExtend,
			ValidFrom:  metav1.Time{Time: time.Now().Add(-24 * time.Hour)},
			ValidUntil: metav1.Time{Time: time.Now().Add(24 * time.Hour)},
			Windows: []hibernatorv1alpha1.OffHourWindow{{Start: "00:00", End: "23:59", DaysOfWeek: []string{"MON"}}},
			TargetOverrides: []hibernatorv1alpha1.TargetOverride{
				{TargetName: "db", Disabled: true},
				{TargetName: "app", Parameters: &hibernatorv1alpha1.Parameters{Raw: []byte(`{"cluster":"event"}`)}},
			},
			ExecutionOverride: &hibernatorv1alpha1.ExecutionOverride{
				Strategy: &hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
				Behavior: &hibernatorv1alpha1.Behavior{Mode: hibernatorv1alpha1.BehaviorBestEffort},
			},
		},
	}

	c := newHandlerFakeClient(plan, exc)
	st := newHandlerState(plan, c)
	st.PlanCtx.Exceptions = []hibernatorv1alpha1.ScheduleException{*exc}

	ep := st.buildEffectivePlan(plan)
	require.NotNil(t, ep)

	// Effective plan should have:
	// - Only "app" target ("db" disabled)
	// - "app" parameters overridden
	// - Strategy changed to Sequential
	// - Behavior changed to BestEffort
	require.Len(t, ep.Spec.Targets, 1)
	assert.Equal(t, "app", ep.Spec.Targets[0].Name)
	require.NotNil(t, ep.Spec.Targets[0].Parameters)
	assert.Equal(t, `{"cluster":"event"}`, string(ep.Spec.Targets[0].Parameters.Raw))
	assert.Equal(t, hibernatorv1alpha1.StrategySequential, ep.Spec.Execution.Strategy.Type)
	assert.Equal(t, hibernatorv1alpha1.BehaviorBestEffort, ep.Spec.Behavior.Mode)

	// Original plan should be unchanged
	require.Len(t, plan.Spec.Targets, 2)
	assert.Equal(t, "db", plan.Spec.Targets[0].Name)
	assert.Equal(t, "app", plan.Spec.Targets[1].Name)
	assert.Equal(t, hibernatorv1alpha1.StrategyParallel, plan.Spec.Execution.Strategy.Type)
	assert.Equal(t, hibernatorv1alpha1.BehaviorStrict, plan.Spec.Behavior.Mode)
}

// ---------------------------------------------------------------------------
// PlanSnapshot
// ---------------------------------------------------------------------------

func TestEffectivePlan_UsesSnapshot_WhenCycleIDMatches(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernating)
	plan.Status.CurrentCycleID = "cycle-001"
	plan.Status.PlanSnapshot = &hibernatorv1alpha1.PlanSnapshot{
		CycleID:       "cycle-001",
		ExceptionName: "override-exc",
		Targets: []hibernatorv1alpha1.Target{
			{Name: "snap-target", Type: "rds"},
		},
		Execution: hibernatorv1alpha1.Execution{
			Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
		},
		Behavior: hibernatorv1alpha1.Behavior{Mode: hibernatorv1alpha1.BehaviorBestEffort},
	}

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	effective := st.effectivePlan(plan)
	require.NotNil(t, effective)
	assert.Equal(t, "snap-target", effective.Spec.Targets[0].Name)
	assert.Equal(t, hibernatorv1alpha1.StrategySequential, effective.Spec.Execution.Strategy.Type)
	assert.Equal(t, hibernatorv1alpha1.BehaviorBestEffort, effective.Spec.Behavior.Mode)
}

func TestEffectivePlan_FallsBackToBuildEffectivePlan_WhenNoSnapshot(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernating)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds"},
	}
	plan.Status.CurrentCycleID = "cycle-001"

	exc := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "override-exc", Namespace: "default"},
		Status:     hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       hibernatorv1alpha1.ExceptionExtend,
			ValidFrom:  metav1.Time{Time: time.Now().Add(-24 * time.Hour)},
			ValidUntil: metav1.Time{Time: time.Now().Add(24 * time.Hour)},
			Windows: []hibernatorv1alpha1.OffHourWindow{{Start: "00:00", End: "23:59", DaysOfWeek: []string{"MON"}}},
			TargetOverrides: []hibernatorv1alpha1.TargetOverride{
				{TargetName: "db", Disabled: true},
			},
		},
	}

	c := newHandlerFakeClient(plan, exc)
	st := newHandlerState(plan, c)
	st.PlanCtx.Exceptions = []hibernatorv1alpha1.ScheduleException{*exc}

	effective := st.effectivePlan(plan)
	require.NotNil(t, effective)
	// Should have no targets because the exception disables "db"
	assert.Empty(t, effective.Spec.Targets)
}

func TestFindActiveExceptionOverride_SkipsDeletionTimestamp(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	now := time.Now()

	exc := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "override-exc",
			Namespace:         "default",
			DeletionTimestamp: &metav1.Time{Time: now},
			Finalizers:        []string{"hibernator.ardikabs.com/exception-finalizer"},
		},
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       hibernatorv1alpha1.ExceptionExtend,
			ValidFrom:  metav1.Time{Time: now.Add(-24 * time.Hour)},
			ValidUntil: metav1.Time{Time: now.Add(24 * time.Hour)},
			Windows:    []hibernatorv1alpha1.OffHourWindow{{Start: "00:00", End: "23:59", DaysOfWeek: []string{"MON"}}},
			ExecutionOverride: &hibernatorv1alpha1.ExecutionOverride{
				Strategy: &hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
			},
		},
	}

	c := newHandlerFakeClient(plan, exc)
	st := newHandlerState(plan, c)
	st.PlanCtx.Exceptions = []hibernatorv1alpha1.ScheduleException{*exc}

	result := st.findActiveExceptionOverride()
	assert.Nil(t, result, "exception with DeletionTimestamp should be skipped")
}

func TestTransitionToHibernating_CapturesPlanSnapshot(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds"},
		{Name: "app", Type: "eks"},
	}

	exc := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "override-exc", Namespace: "default"},
		Status:     hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       hibernatorv1alpha1.ExceptionExtend,
			ValidFrom:  metav1.Time{Time: time.Now().Add(-24 * time.Hour)},
			ValidUntil: metav1.Time{Time: time.Now().Add(24 * time.Hour)},
			Windows:    []hibernatorv1alpha1.OffHourWindow{{Start: "00:00", End: "23:59", DaysOfWeek: []string{"MON"}}},
			TargetOverrides: []hibernatorv1alpha1.TargetOverride{
				{TargetName: "db", Disabled: true},
			},
			ExecutionOverride: &hibernatorv1alpha1.ExecutionOverride{
				Strategy: &hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
			},
		},
	}

	c := newHandlerFakeClient(plan, exc)
	st := newHandlerState(plan, c)
	st.PlanCtx.Exceptions = []hibernatorv1alpha1.ScheduleException{*exc}

	h := &idleState{state: st}
	_, err := h.transitionToHibernating(nil, st.Log)
	require.NoError(t, err)

	upd := <-planStatuses(st).C()
	require.NotNil(t, upd.Mutator)

	testPlan := plan.DeepCopy()
	upd.Mutator.Mutate(testPlan)

	require.NotNil(t, testPlan.Status.PlanSnapshot)
	assert.Equal(t, "override-exc", testPlan.Status.PlanSnapshot.ExceptionName)
	assert.Equal(t, hibernatorv1alpha1.StrategySequential, testPlan.Status.PlanSnapshot.Execution.Strategy.Type)
	require.Len(t, testPlan.Status.PlanSnapshot.Targets, 1)
	assert.Equal(t, "app", testPlan.Status.PlanSnapshot.Targets[0].Name)
}

func TestHibernatingState_Finalize_ClearsPlanSnapshot(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernating)
	plan.Status.CurrentCycleID = "cycle-001"
	plan.Status.PlanSnapshot = &hibernatorv1alpha1.PlanSnapshot{
		CycleID:       "cycle-001",
		ExceptionName: "override-exc",
	}
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "db", State: hibernatorv1alpha1.StateCompleted},
	}

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)
	h := &hibernatingState{state: st}

	h.finalize(nil, st.Log, scheduler.ExecutionPlan{})

	upd := <-planStatuses(st).C()
	require.NotNil(t, upd.Mutator)

	testPlan := plan.DeepCopy()
	upd.Mutator.Mutate(testPlan)

	assert.Nil(t, testPlan.Status.PlanSnapshot)
	assert.Empty(t, testPlan.Status.AppliedExceptionOverride)
}

func TestWakingUpState_Finalize_ClearsPlanSnapshot(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseWakingUp)
	plan.Status.CurrentCycleID = "cycle-001"
	plan.Status.PlanSnapshot = &hibernatorv1alpha1.PlanSnapshot{
		CycleID:       "cycle-001",
		ExceptionName: "override-exc",
	}
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "db", State: hibernatorv1alpha1.StateCompleted},
	}

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)
	h := &wakingUpState{state: st}

	h.finalize(nil, st.Log, scheduler.ExecutionPlan{})

	upd := <-planStatuses(st).C()
	require.NotNil(t, upd.Mutator)

	testPlan := plan.DeepCopy()
	upd.Mutator.Mutate(testPlan)

	assert.Nil(t, testPlan.Status.PlanSnapshot)
	assert.Empty(t, testPlan.Status.AppliedExceptionOverride)
}

func TestSetError_KeepsPlanSnapshot(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernating)
	plan.Status.CurrentCycleID = "cycle-001"
	plan.Status.PlanSnapshot = &hibernatorv1alpha1.PlanSnapshot{
		CycleID:       "cycle-001",
		ExceptionName: "override-exc",
	}

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	st.setError(nil, fmt.Errorf("test error"))

	upd := <-planStatuses(st).C()
	require.NotNil(t, upd.Mutator)

	testPlan := plan.DeepCopy()
	upd.Mutator.Mutate(testPlan)

	assert.Equal(t, hibernatorv1alpha1.PhaseError, testPlan.Status.Phase)
	require.NotNil(t, testPlan.Status.PlanSnapshot)
	assert.Equal(t, "override-exc", testPlan.Status.PlanSnapshot.ExceptionName)
	assert.Equal(t, "cycle-001", testPlan.Status.PlanSnapshot.CycleID)
}

func TestHandleRetry_UsesPlanSnapshot(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseError)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds"},
	}
	plan.Spec.Execution.Strategy = hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategyParallel}
	plan.Status.CurrentCycleID = "cycle-001"
	plan.Status.CurrentOperation = hibernatorv1alpha1.OperationHibernate
	plan.Status.PlanSnapshot = &hibernatorv1alpha1.PlanSnapshot{
		CycleID:       "cycle-001",
		ExceptionName: "override-exc",
		Targets: []hibernatorv1alpha1.Target{
			{Name: "snap-target", Type: "rds"},
		},
		Execution: hibernatorv1alpha1.Execution{
			Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
		},
	}

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)
	h := &recoveryState{state: st}

	_, err := h.handleRetry(nil, st.Log, fmt.Errorf("test error"))
	require.NoError(t, err)

	upd := <-planStatuses(st).C()
	require.NotNil(t, upd.Mutator)

	testPlan := plan.DeepCopy()
	upd.Mutator.Mutate(testPlan)

	assert.Equal(t, hibernatorv1alpha1.PhaseHibernating, testPlan.Status.Phase)
	// PlanSnapshot should still be present after retry transition
	require.NotNil(t, testPlan.Status.PlanSnapshot)
	assert.Equal(t, "override-exc", testPlan.Status.PlanSnapshot.ExceptionName)
}


