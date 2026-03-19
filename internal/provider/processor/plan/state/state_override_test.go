/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// newOverrideActionState wires a *state with the supplied annotations, ScheduleResult, and hasRestoreData.
func newOverrideActionState(
	plan *hibernatorv1alpha1.HibernatePlan,
	sr *message.ScheduleEvaluation,
	hasRestoreData bool,
) *state {
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)
	st.PlanCtx.Schedule = sr
	st.PlanCtx.HasRestoreData = hasRestoreData
	return st
}

// ---------------------------------------------------------------------------
// selectHandler dispatch — override-action gate (Priority 3)
// ---------------------------------------------------------------------------

func TestNew_OverrideActionAnnotation_PhaseActive_ReturnsOverrideActionState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction:      "true",
		wellknown.AnnotationOverridePhaseTarget: wellknown.OverridePhaseTargetHibernate,
	}
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*overrideActionState)
	assert.True(t, ok, "expected *overrideActionState for PhaseActive + override-action annotation")
}

func TestNew_OverrideActionAnnotation_PhaseHibernated_ReturnsOverrideActionState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction:      "true",
		wellknown.AnnotationOverridePhaseTarget: wellknown.OverridePhaseTargetWakeup,
	}
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*overrideActionState)
	assert.True(t, ok, "expected *overrideActionState for PhaseHibernated + override-action annotation")
}

// Annotation present but phase=Hibernating — falls through the gate to hibernatingState.
// This ensures controller restarts during execution are safe.
func TestNew_OverrideActionAnnotation_PhaseHibernating_FallsThroughToHibernatingState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernating)
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction:      "true",
		wellknown.AnnotationOverridePhaseTarget: wellknown.OverridePhaseTargetHibernate,
	}
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*hibernatingState)
	assert.True(t, ok, "expected *hibernatingState for PhaseHibernating (annotation ignored during execution)")
}

// Annotation present but phase=WakingUp — falls through to wakingUpState.
func TestNew_OverrideActionAnnotation_PhaseWakingUp_FallsThroughToWakingUpState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseWakingUp)
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction:      "true",
		wellknown.AnnotationOverridePhaseTarget: wellknown.OverridePhaseTargetWakeup,
	}
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*wakingUpState)
	assert.True(t, ok, "expected *wakingUpState for PhaseWakingUp (annotation ignored during execution)")
}

// Annotation present but phase=Error — falls through to recoveryState.
// Error recovery must not be hijacked by the override-action annotation.
func TestNew_OverrideActionAnnotation_PhaseError_FallsThroughToRecoveryState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseError)
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction:      "true",
		wellknown.AnnotationOverridePhaseTarget: wellknown.OverridePhaseTargetHibernate,
	}
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*recoveryState)
	assert.True(t, ok, "expected *recoveryState for PhaseError (annotation ignored during error recovery)")
}

// Spec.Suspend=true takes priority over override-action (Priority 2 > Priority 3).
func TestNew_OverrideActionAnnotation_SuspendRequested_SuspendTakesPriority(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Spec.Suspend = true
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction:      "true",
		wellknown.AnnotationOverridePhaseTarget: wellknown.OverridePhaseTargetHibernate,
	}
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*preSuspensionState)
	assert.True(t, ok, "expected *preSuspensionState: Suspend priority > override-action")
}

// Phase=Suspended + Spec.Suspend=false → suspendedState (Suspended ∉ {Active,Hibernated}).
func TestNew_OverrideActionAnnotation_PhaseSuspended_ReturnsSuspendedState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseSuspended)
	plan.Spec.Suspend = false
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction:      "true",
		wellknown.AnnotationOverridePhaseTarget: wellknown.OverridePhaseTargetHibernate,
	}
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*suspendedState)
	assert.True(t, ok, "expected *suspendedState for PhaseSuspended (annotation ignored until resume)")
}

// override-action annotation present but value is not "true" — treated as absent, falls through.
func TestNew_OverrideActionAnnotation_WrongValue_FallsThroughToIdleState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction:      "yes", // not "true"
		wellknown.AnnotationOverridePhaseTarget: wellknown.OverridePhaseTargetHibernate,
	}
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, isOverride := h.(*overrideActionState)
	_, isIdle := h.(*idleState)
	assert.False(t, isOverride, "must NOT be overrideActionState when override-action value is not 'true'")
	assert.True(t, isIdle)
}

// ---------------------------------------------------------------------------
// selectHandler dispatch — standalone restart gate (Priority 4)
// ---------------------------------------------------------------------------

// restart=true at PhaseActive without override-action → restartState selected.
func TestNew_RestartAnnotation_PhaseActive_NoOverride_ReturnsRestartState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Annotations = map[string]string{wellknown.AnnotationRestart: "true"}
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*restartState)
	assert.True(t, ok, "expected *restartState for PhaseActive + standalone restart annotation")
}

// restart=true at PhaseHibernated without override-action → restartState selected.
func TestNew_RestartAnnotation_PhaseHibernated_NoOverride_ReturnsRestartState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
	plan.Annotations = map[string]string{wellknown.AnnotationRestart: "true"}
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*restartState)
	assert.True(t, ok, "expected *restartState for PhaseHibernated + standalone restart annotation")
}

// restart=true at PhaseHibernating — falls through (execution in progress; restart ignored).
func TestNew_RestartAnnotation_PhaseHibernating_FallsThroughToHibernatingState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernating)
	plan.Annotations = map[string]string{wellknown.AnnotationRestart: "true"}
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*hibernatingState)
	assert.True(t, ok, "expected *hibernatingState for PhaseHibernating (restart ignored during execution)")
}

// Both override-action and restart set at Active → overrideActionState wins (Priority 3 fires first).
// restart is consumed inside overrideActionState's no-op branch, not by restartState.
func TestNew_BothOverrideAndRestart_OverrideTakesPriority(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction:      "true",
		wellknown.AnnotationOverridePhaseTarget: wellknown.OverridePhaseTargetWakeup,
		wellknown.AnnotationRestart:             "true",
	}
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*overrideActionState)
	assert.True(t, ok, "overrideActionState must take priority over standalone restartState when both present")
}

// ---------------------------------------------------------------------------
// overrideActionState.Handle() — override-phase-target=hibernate
// ---------------------------------------------------------------------------

// Plan is Active during an active window (schedule says no hibernate).
// override-action + override-phase-target=hibernate must override the schedule.
func TestOverrideActionState_Hibernate_FromActive_DuringActiveWindow_TransitionsToHibernating(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction:      "true",
		wellknown.AnnotationOverridePhaseTarget: wellknown.OverridePhaseTargetHibernate,
	}
	sr := &message.ScheduleEvaluation{ShouldHibernate: false, RequeueAfter: 5 * time.Minute}
	st := newOverrideActionState(plan, sr, false)
	h := &overrideActionState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.True(t, result.Requeue, "should requeue immediately to drive Hibernating phase")
	assert.Equal(t, hibernatorv1alpha1.PhaseHibernating, plan.Status.Phase,
		"plan must transition to Hibernating regardless of active-window schedule signal")
	assert.GreaterOrEqual(t, planStatuses(st).Len(), 1, "at least one status update must be queued")
}

// Even if schedule also says hibernate, override must behave identically.
func TestOverrideActionState_Hibernate_FromActive_ScheduleAgreesHibernate_TransitionsToHibernating(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction:      "true",
		wellknown.AnnotationOverridePhaseTarget: wellknown.OverridePhaseTargetHibernate,
	}
	sr := &message.ScheduleEvaluation{ShouldHibernate: true, RequeueAfter: 5 * time.Minute}
	st := newOverrideActionState(plan, sr, false)
	h := &overrideActionState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.True(t, result.Requeue)
	assert.Equal(t, hibernatorv1alpha1.PhaseHibernating, plan.Status.Phase)
}

// Plan is Hibernated, schedule says wakeup (active window opened).
// override-phase-target=hibernate suppresses the wakeup signal.
func TestOverrideActionState_Hibernate_FromHibernated_SuppressesScheduleWakeup(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction:      "true",
		wellknown.AnnotationOverridePhaseTarget: wellknown.OverridePhaseTargetHibernate,
	}
	sr := &message.ScheduleEvaluation{ShouldHibernate: false, RequeueAfter: 5 * time.Minute}
	st := newOverrideActionState(plan, sr, true)
	h := &overrideActionState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.False(t, result.Requeue)
	assert.Equal(t, hibernatorv1alpha1.PhaseHibernated, plan.Status.Phase,
		"plan must stay Hibernated: override suppresses schedule wakeup")
	assert.Zero(t, planStatuses(st).Len())
}

// Plan is Hibernated in the off-hours window — no-op, already at target.
func TestOverrideActionState_Hibernate_FromHibernated_DuringHibernatedWindow_NoopAlways(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction:      "true",
		wellknown.AnnotationOverridePhaseTarget: wellknown.OverridePhaseTargetHibernate,
	}
	sr := &message.ScheduleEvaluation{ShouldHibernate: true, RequeueAfter: 5 * time.Minute}
	st := newOverrideActionState(plan, sr, false)
	h := &overrideActionState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.False(t, result.Requeue)
	assert.Equal(t, hibernatorv1alpha1.PhaseHibernated, plan.Status.Phase)
	assert.Zero(t, planStatuses(st).Len())
}

// ---------------------------------------------------------------------------
// overrideActionState.Handle() — override-phase-target=wakeup
// ---------------------------------------------------------------------------

// Plan is Hibernated with restore data — wakeup must be forced.
func TestOverrideActionState_Wakeup_FromHibernated_WithRestoreData_TransitionsToWakingUp(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction:      "true",
		wellknown.AnnotationOverridePhaseTarget: wellknown.OverridePhaseTargetWakeup,
	}
	sr := &message.ScheduleEvaluation{ShouldHibernate: true, RequeueAfter: 5 * time.Minute}
	st := newOverrideActionState(plan, sr, true)
	h := &overrideActionState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.True(t, result.Requeue)
	assert.Equal(t, hibernatorv1alpha1.PhaseWakingUp, plan.Status.Phase)
	assert.GreaterOrEqual(t, planStatuses(st).Len(), 1)
}

// Hibernated with no restore data — wakeup cannot proceed.
func TestOverrideActionState_Wakeup_FromHibernated_NoRestoreData_NoopWithWarning(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction:      "true",
		wellknown.AnnotationOverridePhaseTarget: wellknown.OverridePhaseTargetWakeup,
	}
	sr := &message.ScheduleEvaluation{ShouldHibernate: true, RequeueAfter: 5 * time.Minute}
	st := newOverrideActionState(plan, sr, false)
	h := &overrideActionState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.False(t, result.Requeue)
	assert.Equal(t, hibernatorv1alpha1.PhaseHibernated, plan.Status.Phase)
	assert.Zero(t, planStatuses(st).Len())
}

// override-phase-target=wakeup from PhaseActive is ALWAYS a no-op (loop prevention) —
// unless restart=true is set.
func TestOverrideActionState_Wakeup_FromActive_AlwaysNoopRegardlessOfRestoreData(t *testing.T) {
	for _, hasRestoreData := range []bool{false, true} {
		t.Run("hasRestoreData="+map[bool]string{false: "false", true: "true"}[hasRestoreData], func(t *testing.T) {
			plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
			plan.Annotations = map[string]string{
				wellknown.AnnotationOverrideAction:      "true",
				wellknown.AnnotationOverridePhaseTarget: wellknown.OverridePhaseTargetWakeup,
			}
			sr := &message.ScheduleEvaluation{ShouldHibernate: false}
			st := newOverrideActionState(plan, sr, hasRestoreData)
			h := &overrideActionState{idleState: &idleState{state: st}}

			result, err := h.Handle(context.Background())
			require.NoError(t, err)

			assert.False(t, result.Requeue,
				"override-phase-target=wakeup from Active must be a no-op (loop prevention)")
			assert.Equal(t, hibernatorv1alpha1.PhaseActive, plan.Status.Phase)
			assert.Zero(t, planStatuses(st).Len())
		})
	}
}

// ---------------------------------------------------------------------------
// overrideActionState.Handle() — missing or unrecognised override-phase-target
// ---------------------------------------------------------------------------

func TestOverrideActionState_MissingPhaseTarget_NoopWithWarning(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction: "true",
		// AnnotationOverridePhaseTarget intentionally absent
	}
	st := newOverrideActionState(plan, nil, false)
	h := &overrideActionState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.False(t, result.Requeue)
	assert.Equal(t, hibernatorv1alpha1.PhaseActive, plan.Status.Phase,
		"missing override-phase-target must not trigger any transition")
	assert.Zero(t, planStatuses(st).Len())
}

func TestOverrideActionState_InvalidPhaseTarget_NoopWithWarning(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction:      "true",
		wellknown.AnnotationOverridePhaseTarget: "totally-invalid",
	}
	st := newOverrideActionState(plan, nil, false)
	h := &overrideActionState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.False(t, result.Requeue)
	assert.Equal(t, hibernatorv1alpha1.PhaseActive, plan.Status.Phase)
	assert.Zero(t, planStatuses(st).Len())
}

// ---------------------------------------------------------------------------
// overrideActionState.Handle() — restart=true companion annotation
// ---------------------------------------------------------------------------

// override-phase-target=hibernate + plan is Hibernated (normally a no-op),
// but restart=true is also set → re-run hibernation executor.
// The restart annotation must be consumed (deleted) before the transition.
func TestOverrideActionState_Hibernate_FromHibernated_WithRestart_ReTriggersHibernation(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction:      "true",
		wellknown.AnnotationOverridePhaseTarget: wellknown.OverridePhaseTargetHibernate,
		wellknown.AnnotationRestart:             "true",
	}
	sr := &message.ScheduleEvaluation{ShouldHibernate: true}
	st := newOverrideActionState(plan, sr, false)
	h := &overrideActionState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.True(t, result.Requeue, "restart must re-trigger Hibernating even when plan is already Hibernated")
	assert.Equal(t, hibernatorv1alpha1.PhaseHibernating, plan.Status.Phase)
	assert.NotContains(t, plan.Annotations, wellknown.AnnotationRestart,
		"restart must be consumed (deleted) after triggering")
	assert.Contains(t, plan.Annotations, wellknown.AnnotationOverrideAction,
		"override-action must NOT be removed — it remains as the mode-switch annotation")
}

// override-phase-target=wakeup + plan is Active (normally a no-op for loop prevention),
// but restart=true is set AND restore data is available → re-run wakeup executor.
func TestOverrideActionState_Wakeup_FromActive_WithRestart_AndRestoreData_ReTriggersWakeup(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction:      "true",
		wellknown.AnnotationOverridePhaseTarget: wellknown.OverridePhaseTargetWakeup,
		wellknown.AnnotationRestart:             "true",
	}
	sr := &message.ScheduleEvaluation{ShouldHibernate: false}
	st := newOverrideActionState(plan, sr, true)
	h := &overrideActionState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.True(t, result.Requeue, "restart must re-trigger WakingUp even when plan is already Active")
	assert.Equal(t, hibernatorv1alpha1.PhaseWakingUp, plan.Status.Phase)
	assert.NotContains(t, plan.Annotations, wellknown.AnnotationRestart,
		"restart must be consumed (deleted) after triggering")
	assert.Contains(t, plan.Annotations, wellknown.AnnotationOverrideAction,
		"override-action must NOT be removed")
}

// override-phase-target=wakeup + Active + restart=true, but NO restore data →
// restart annotation is consumed but no transition occurs (no restore data to apply).
func TestOverrideActionState_Wakeup_FromActive_WithRestart_NoRestoreData_ConsumesThenNoTransition(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction:      "true",
		wellknown.AnnotationOverridePhaseTarget: wellknown.OverridePhaseTargetWakeup,
		wellknown.AnnotationRestart:             "true",
	}
	sr := &message.ScheduleEvaluation{ShouldHibernate: false}
	st := newOverrideActionState(plan, sr, false)
	h := &overrideActionState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.False(t, result.Requeue, "no transition without restore data")
	assert.Equal(t, hibernatorv1alpha1.PhaseActive, plan.Status.Phase)
	assert.NotContains(t, plan.Annotations, wellknown.AnnotationRestart,
		"restart annotation is still consumed even when transition cannot proceed")
	assert.Zero(t, planStatuses(st).Len())
}

// restart with a non-"true" value is ignored (treated as absent).
func TestOverrideActionState_Hibernate_FromHibernated_RestartWrongValue_StillNoop(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction:      "true",
		wellknown.AnnotationOverridePhaseTarget: wellknown.OverridePhaseTargetHibernate,
		wellknown.AnnotationRestart:             "yes", // not "true"
	}
	sr := &message.ScheduleEvaluation{ShouldHibernate: true}
	st := newOverrideActionState(plan, sr, false)
	h := &overrideActionState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.False(t, result.Requeue, "invalid restart value must be treated as absent → no-op")
	assert.Equal(t, hibernatorv1alpha1.PhaseHibernated, plan.Status.Phase)
	assert.Contains(t, plan.Annotations, wellknown.AnnotationRestart,
		"annotation with wrong value must not be consumed")
	assert.Zero(t, planStatuses(st).Len())
}

// ---------------------------------------------------------------------------
// Regression: annotation absent should fall back to idleState behaviour
// ---------------------------------------------------------------------------

// Without override-action annotation, PhaseActive must go through idleState.
func TestNew_NoOverrideActionAnnotation_PhaseActive_ReturnsIdleState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, isOverride := h.(*overrideActionState)
	_, isIdle := h.(*idleState)
	assert.False(t, isOverride, "must NOT be overrideActionState when annotation absent")
	assert.True(t, isIdle, "must be idleState when annotation absent")
}

// Without override-action annotation, PhaseHibernated follows idleState (schedule-driven wakeup).
func TestNew_NoOverrideActionAnnotation_PhaseHibernated_ReturnsIdleState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, isOverride := h.(*overrideActionState)
	_, isIdle := h.(*idleState)
	assert.False(t, isOverride)
	assert.True(t, isIdle)
}

// ---------------------------------------------------------------------------
// overrideActionState.OnDeadline / OnError — inherited from embedded idleState/state
// ---------------------------------------------------------------------------

func TestOverrideActionState_OnDeadline_ReturnsZeroResult(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction:      "true",
		wellknown.AnnotationOverridePhaseTarget: wellknown.OverridePhaseTargetHibernate,
	}
	st := newOverrideActionState(plan, nil, false)
	h := &overrideActionState{idleState: &idleState{state: st}}

	result, err := h.OnDeadline(context.Background())
	require.NoError(t, err)
	assert.Equal(t, StateResult{}, result, "OnDeadline should be a no-op (override state has no deadline)")
}

func TestOverrideActionState_OnError_PlanError_SetsError(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction:      "true",
		wellknown.AnnotationOverridePhaseTarget: wellknown.OverridePhaseTargetHibernate,
	}
	now := metav1.NewTime(plan.CreationTimestamp.Time)
	_ = now

	st := newOverrideActionState(plan, nil, false)
	h := &overrideActionState{idleState: &idleState{state: st}}

	planErr := AsPlanError(assert.AnError)
	_ = h.OnError(context.Background(), planErr)

	assert.Equal(t, hibernatorv1alpha1.PhaseError, plan.Status.Phase,
		"OnError with PlanError must transition plan to PhaseError")
}
