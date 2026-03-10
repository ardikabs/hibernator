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

// newForceActionState wires a *state with the supplied annotation value, ScheduleResult, and hasRestoreData.
func newForceActionState(
	plan *hibernatorv1alpha1.HibernatePlan,
	sr *message.ScheduleEvaluation,
	hasRestoreData bool,
) *state {
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)
	st.PlanCtx.ScheduleResult = sr
	st.PlanCtx.HasRestoreData = hasRestoreData
	return st
}

// ---------------------------------------------------------------------------
// selectHandler dispatch — force-action gate
// ---------------------------------------------------------------------------

func TestNew_ForceActionAnnotation_PhaseActive_ReturnsForceActionState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Annotations = map[string]string{
		wellknown.AnnotationForceAction: wellknown.ForceActionHibernate,
	}
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*forceActionState)
	assert.True(t, ok, "expected *forceActionState for PhaseActive + force-action annotation")
}

func TestNew_ForceActionAnnotation_PhaseHibernated_ReturnsForceActionState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
	plan.Annotations = map[string]string{
		wellknown.AnnotationForceAction: wellknown.ForceActionWakeup,
	}
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*forceActionState)
	assert.True(t, ok, "expected *forceActionState for PhaseHibernated + force-action annotation")
}

// Annotation present but phase=Hibernating — falls through the gate to hibernatingState.
// This ensures controller restarts during execution are safe.
func TestNew_ForceActionAnnotation_PhaseHibernating_FallsThroughToHibernatingState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernating)
	plan.Annotations = map[string]string{
		wellknown.AnnotationForceAction: wellknown.ForceActionHibernate,
	}
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*hibernatingState)
	assert.True(t, ok, "expected *hibernatingState for PhaseHibernating (annotation ignored during execution)")
}

// Annotation present but phase=WakingUp — falls through to wakingUpState.
func TestNew_ForceActionAnnotation_PhaseWakingUp_FallsThroughToWakingUpState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseWakingUp)
	plan.Annotations = map[string]string{
		wellknown.AnnotationForceAction: wellknown.ForceActionWakeup,
	}
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*wakingUpState)
	assert.True(t, ok, "expected *wakingUpState for PhaseWakingUp (annotation ignored during execution)")
}

// Annotation present but phase=Error — falls through to recoveryState.
// Error recovery must not be hijacked by the force-action annotation.
func TestNew_ForceActionAnnotation_PhaseError_FallsThroughToRecoveryState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseError)
	plan.Annotations = map[string]string{
		wellknown.AnnotationForceAction: wellknown.ForceActionHibernate,
	}
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*recoveryState)
	assert.True(t, ok, "expected *recoveryState for PhaseError (annotation ignored during error recovery)")
}

// Spec.Suspend=true takes priority over force-action (Priority 2 > Priority 3).
func TestNew_ForceActionAnnotation_SuspendRequested_SuspendTakesPriority(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Spec.Suspend = true
	plan.Annotations = map[string]string{
		wellknown.AnnotationForceAction: wellknown.ForceActionHibernate,
	}
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(HandlerFunc)
	assert.True(t, ok, "expected HandlerFunc(TransitionToSuspended): Suspend priority > force-action")
}

// Phase=Suspended + Spec.Suspend=false → suspendedState (not forceActionState, as Suspended ∉ {Active,Hibernated}).
func TestNew_ForceActionAnnotation_PhaseSuspended_ReturnsSuspendedState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseSuspended)
	plan.Spec.Suspend = false
	plan.Annotations = map[string]string{
		wellknown.AnnotationForceAction: wellknown.ForceActionHibernate,
	}
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*suspendedState)
	assert.True(t, ok, "expected *suspendedState for PhaseSuspended (annotation ignored until resume)")
}

// ---------------------------------------------------------------------------
// forceActionState.Handle() — force-action=hibernate
// ---------------------------------------------------------------------------

// Plan is Active during an active window (schedule says no hibernate).
// force-action=hibernate must override the schedule and initiate hibernation.
func TestForceActionState_Hibernate_FromActive_DuringActiveWindow_TransitionsToHibernating(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Annotations = map[string]string{wellknown.AnnotationForceAction: wellknown.ForceActionHibernate}
	sr := &message.ScheduleEvaluation{ShouldHibernate: false, RequeueAfter: 5 * time.Minute}
	st := newForceActionState(plan, sr, false)
	h := &forceActionState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.True(t, result.Requeue, "should requeue immediately to drive Hibernating phase")
	assert.Equal(t, hibernatorv1alpha1.PhaseHibernating, plan.Status.Phase,
		"plan must transition to Hibernating regardless of active-window schedule signal")
	assert.GreaterOrEqual(t, planStatuses(st).Len(), 1, "at least one status update must be queued")
}

// Even if schedule also says hibernate (unusual: Active + ShouldHibernate=true),
// force-action=hibernate must behave identically — transition to Hibernating.
func TestForceActionState_Hibernate_FromActive_ScheduleAgreesHibernate_TransitionsToHibernating(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Annotations = map[string]string{wellknown.AnnotationForceAction: wellknown.ForceActionHibernate}
	sr := &message.ScheduleEvaluation{ShouldHibernate: true, RequeueAfter: 5 * time.Minute}
	st := newForceActionState(plan, sr, false)
	h := &forceActionState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.True(t, result.Requeue)
	assert.Equal(t, hibernatorv1alpha1.PhaseHibernating, plan.Status.Phase)
}

// Plan is Hibernated, schedule says wakeup (active window opened).
// force-action=hibernate suppresses the wakeup signal — plan stays Hibernated.
func TestForceActionState_Hibernate_FromHibernated_SuppressesScheduleWakeup(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
	plan.Annotations = map[string]string{wellknown.AnnotationForceAction: wellknown.ForceActionHibernate}
	sr := &message.ScheduleEvaluation{ShouldHibernate: false, RequeueAfter: 5 * time.Minute} // wakeup signal
	st := newForceActionState(plan, sr, true /* has restore data */)
	h := &forceActionState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	// No-op: target already reached; schedule wakeup signal is silently suppressed.
	assert.False(t, result.Requeue)
	assert.Equal(t, hibernatorv1alpha1.PhaseHibernated, plan.Status.Phase,
		"plan must stay Hibernated: force-action=hibernate suppresses schedule wakeup")
	assert.Zero(t, planStatuses(st).Len(), "no status update should be queued for a no-op")
}

// Plan is Hibernated, schedule also says hibernate (within the off-hours window).
// force-action=hibernate is a no-op — already at the target phase.
func TestForceActionState_Hibernate_FromHibernated_DuringHibernatedWindow_NoopAlways(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
	plan.Annotations = map[string]string{wellknown.AnnotationForceAction: wellknown.ForceActionHibernate}
	sr := &message.ScheduleEvaluation{ShouldHibernate: true, RequeueAfter: 5 * time.Minute}
	st := newForceActionState(plan, sr, false)
	h := &forceActionState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.False(t, result.Requeue)
	assert.Equal(t, hibernatorv1alpha1.PhaseHibernated, plan.Status.Phase)
	assert.Zero(t, planStatuses(st).Len())
}

// ---------------------------------------------------------------------------
// forceActionState.Handle() — force-action=wakeup
// ---------------------------------------------------------------------------

// Plan is Hibernated with restore data and the annotation requests wakeup.
// Schedule is in hibernated window (ShouldHibernate=true) — wakeup must be forced.
func TestForceActionState_Wakeup_FromHibernated_WithRestoreData_TransitionsToWakingUp(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
	plan.Annotations = map[string]string{wellknown.AnnotationForceAction: wellknown.ForceActionWakeup}
	sr := &message.ScheduleEvaluation{ShouldHibernate: true, RequeueAfter: 5 * time.Minute}
	st := newForceActionState(plan, sr, true /* has restore data */)
	h := &forceActionState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.True(t, result.Requeue, "should requeue immediately to drive WakingUp phase")
	assert.Equal(t, hibernatorv1alpha1.PhaseWakingUp, plan.Status.Phase,
		"plan must transition to WakingUp regardless of hibernated-window schedule signal")
	assert.GreaterOrEqual(t, planStatuses(st).Len(), 1)
}

// Plan is Hibernated but has no restore data — wakeup cannot proceed.
// The annotation stays acknowledged but no transition is made.
func TestForceActionState_Wakeup_FromHibernated_NoRestoreData_NoopWithWarning(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
	plan.Annotations = map[string]string{wellknown.AnnotationForceAction: wellknown.ForceActionWakeup}
	sr := &message.ScheduleEvaluation{ShouldHibernate: true, RequeueAfter: 5 * time.Minute}
	st := newForceActionState(plan, sr, false /* no restore data */)
	h := &forceActionState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.False(t, result.Requeue)
	assert.Equal(t, hibernatorv1alpha1.PhaseHibernated, plan.Status.Phase)
	assert.Zero(t, planStatuses(st).Len())
}

// force-action=wakeup from PhaseActive is ALWAYS a no-op — the plan is already awake.
// This prevents an infinite loop: after a forced wakeup completes (Hibernated→Active),
// HasRestoreData remains true (ConfigMap data survives UnlockRestoreData), so without
// this no-op the handler would immediately re-trigger WakingUp on the next tick.
func TestForceActionState_Wakeup_FromActive_AlwaysNoopRegardlessOfRestoreData(t *testing.T) {
	for _, hasRestoreData := range []bool{false, true} {
		t.Run("hasRestoreData="+map[bool]string{false: "false", true: "true"}[hasRestoreData], func(t *testing.T) {
			plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
			plan.Annotations = map[string]string{wellknown.AnnotationForceAction: wellknown.ForceActionWakeup}
			sr := &message.ScheduleEvaluation{ShouldHibernate: false}
			st := newForceActionState(plan, sr, hasRestoreData)
			h := &forceActionState{idleState: &idleState{state: st}}

			result, err := h.Handle(context.Background())
			require.NoError(t, err)

			assert.False(t, result.Requeue,
				"force-action=wakeup from Active must be a no-op (loop prevention)")
			assert.Equal(t, hibernatorv1alpha1.PhaseActive, plan.Status.Phase)
			assert.Zero(t, planStatuses(st).Len())
		})
	}
}

// ---------------------------------------------------------------------------
// forceActionState.Handle() — invalid / unrecognised value
// ---------------------------------------------------------------------------

func TestForceActionState_InvalidAction_NoopWithWarning(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Annotations = map[string]string{wellknown.AnnotationForceAction: "totally-invalid"}
	st := newForceActionState(plan, nil, false)
	h := &forceActionState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.False(t, result.Requeue)
	assert.Equal(t, hibernatorv1alpha1.PhaseActive, plan.Status.Phase,
		"unrecognised value must not trigger any transition")
	assert.Zero(t, planStatuses(st).Len())
}

// Empty annotation value also treated as unrecognised.
func TestForceActionState_EmptyAction_NoopWithWarning(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Annotations = map[string]string{wellknown.AnnotationForceAction: ""}
	st := newForceActionState(plan, nil, false)
	h := &forceActionState{idleState: &idleState{state: st}}

	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.False(t, result.Requeue)
	assert.Equal(t, hibernatorv1alpha1.PhaseActive, plan.Status.Phase)
	assert.Zero(t, planStatuses(st).Len())
}

// ---------------------------------------------------------------------------
// Regression: annotation absent should fall back to idleState behaviour
// ---------------------------------------------------------------------------

// Without force-action annotation, PhaseActive + ShouldHibernate=true must go through
// idleState, not forceActionState (New() returns *idleState).
func TestNew_NoForceActionAnnotation_PhaseActive_ReturnsIdleStateNotForceActionState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	// No annotation.
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, isForce := h.(*forceActionState)
	_, isIdle := h.(*idleState)
	assert.False(t, isForce, "must NOT be forceActionState when annotation is absent")
	assert.True(t, isIdle, "must be idleState when annotation is absent")
}

// Without force-action annotation, PhaseHibernated + ShouldHibernate=false follows
// idleState (schedule-driven wakeup).
func TestNew_NoForceActionAnnotation_PhaseHibernated_ReturnsIdleStateNotForceActionState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, isForce := h.(*forceActionState)
	_, isIdle := h.(*idleState)
	assert.False(t, isForce)
	assert.True(t, isIdle)
}

// ---------------------------------------------------------------------------
// forceActionState.OnDeadline / OnError — inherited from embedded idleState/state
// ---------------------------------------------------------------------------

func TestForceActionState_OnDeadline_ReturnsZeroResult(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Annotations = map[string]string{wellknown.AnnotationForceAction: wellknown.ForceActionHibernate}
	st := newForceActionState(plan, nil, false)
	h := &forceActionState{idleState: &idleState{state: st}}

	result, err := h.OnDeadline(context.Background())
	require.NoError(t, err)
	assert.Equal(t, StateResult{}, result, "OnDeadline should be a no-op (force state has no deadline)")
}

func TestForceActionState_OnError_PlanError_SetsError(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Annotations = map[string]string{wellknown.AnnotationForceAction: wellknown.ForceActionHibernate}
	now := metav1.NewTime(plan.CreationTimestamp.Time)
	plan.DeletionTimestamp = nil
	_ = now

	st := newForceActionState(plan, nil, false)
	h := &forceActionState{idleState: &idleState{state: st}}

	planErr := AsPlanError(assert.AnError)
	_ = h.OnError(context.Background(), planErr)

	// Should have queued a status update with PhaseError
	assert.Equal(t, hibernatorv1alpha1.PhaseError, plan.Status.Phase,
		"OnError with PlanError must transition plan to PhaseError")
}
