/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// ---------------------------------------------------------------------------
// suspendedState.shouldForceWakeUpOnResume()
// ---------------------------------------------------------------------------

func TestSuspendedState_ShouldForceWakeUp_NoPriorPhase_ReturnsFalse(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseSuspended)
	// No suspended-at-phase annotation → no force wakeup.
	c := newHandlerFakeClient(plan)
	state := newHandlerState(plan, c, &timerTracker{})

	st := &suspendedState{State: state}
	assert.False(t, st.shouldForceWakeUpOnResume())
}

func TestSuspendedState_ShouldForceWakeUp_SuspendedAtActive_ReturnsFalse(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseSuspended)
	plan.Annotations = map[string]string{
		wellknown.AnnotationSuspendedAtPhase: string(hibernatorv1alpha1.PhaseActive),
	}
	c := newHandlerFakeClient(plan)
	state := newHandlerState(plan, c, &timerTracker{})

	st := &suspendedState{State: state}
	assert.False(t, st.shouldForceWakeUpOnResume(), "suspended from Active → no forced wakeup")
}

func TestSuspendedState_ShouldForceWakeUp_NoRestoreData_ReturnsFalse(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseSuspended)
	plan.Annotations = map[string]string{
		wellknown.AnnotationSuspendedAtPhase: string(hibernatorv1alpha1.PhaseHibernated),
	}
	c := newHandlerFakeClient(plan)
	state := newHandlerState(plan, c, &timerTracker{})
	// HasRestoreData defaults to false.

	st := &suspendedState{State: state}
	assert.False(t, st.shouldForceWakeUpOnResume(), "no restore data → no forced wakeup")
}

func TestSuspendedState_ShouldForceWakeUp_AllConditionsMet_ReturnsTrue(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseSuspended)
	plan.Annotations = map[string]string{
		wellknown.AnnotationSuspendedAtPhase: string(hibernatorv1alpha1.PhaseHibernated),
	}
	c := newHandlerFakeClient(plan)
	state := newHandlerState(plan, c, &timerTracker{})
	state.PlanCtx.HasRestoreData = true
	state.PlanCtx.ScheduleResult = &message.ScheduleEvaluation{ShouldHibernate: false}

	st := &suspendedState{State: state}
	assert.True(t, st.shouldForceWakeUpOnResume())
}

// ---------------------------------------------------------------------------
// suspendedState.Handle()
// ---------------------------------------------------------------------------

func TestSuspendedState_Handle_SuspendUntilFuture_SchedulesDeadline(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseSuspended)
	plan.Spec.Suspend = true

	// Set suspend-until to a future time.
	future := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	plan.Annotations = map[string]string{
		wellknown.AnnotationSuspendUntil: future,
	}

	c := newHandlerFakeClient(plan)
	tt := &timerTracker{}
	state := newHandlerState(plan, c, tt)

	h := &suspendedState{State: state}
	h.Handle(context.Background())

	assert.True(t, tt.deadlineDuration > 0, "deadline timer should be scheduled")
}

func TestSuspendedState_Handle_StillSuspended_NoOp(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseSuspended)
	plan.Spec.Suspend = true
	// No suspend-until → just stay suspended.
	c := newHandlerFakeClient(plan)
	tt := &timerTracker{}
	state := newHandlerState(plan, c, tt)

	h := &suspendedState{State: state}
	h.Handle(context.Background())

	// No timer, no status queue changes.
	assert.Zero(t, tt.deadlineDuration)
	assert.Zero(t, state.Statuses.PlanStatuses.Len())
}

func TestSuspendedState_Handle_OnSuspendUntilPeriod(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Date(2026, 5, 4, 20, 1, 0, 0, time.UTC))

	plan := basePlanForState("p", hibernatorv1alpha1.PhaseSuspended)
	plan.Spec.Suspend = false // Already cleared by operator.
	plan.Annotations = map[string]string{
		wellknown.AnnotationSuspendedAtPhase: string(hibernatorv1alpha1.PhaseActive),
		wellknown.AnnotationSuspendUntil:     clk.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339),
	}

	c := newHandlerFakeClient(plan)
	tt := &timerTracker{}
	state := newHandlerState(plan, c, tt)
	state.Clock = clk

	h := &suspendedState{State: state}
	h.Handle(context.Background())

	assert.NotZero(t, tt.deadlineDuration)
	assert.False(t, tt.cancelDeadlineCalled)

	clk.SetTime(time.Date(2026, 5, 4, 22, 0, 0, 0, time.UTC))
	h.Handle(context.Background())

	// resume() queues the Active transition but does not mutate plan.Status in-memory.
	assert.True(t, tt.cancelDeadlineCalled)
	assert.GreaterOrEqual(t, state.Statuses.PlanStatuses.Len(), 1)
	assert.Equal(t, hibernatorv1alpha1.PhaseActive, plan.Status.Phase)
	assert.Empty(t, plan.Annotations[wellknown.AnnotationSuspendUntil])
	assert.Empty(t, plan.Annotations[wellknown.AnnotationSuspendedAtPhase])
}

func TestSuspendedState_Handle_Resume(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseSuspended)
	plan.Spec.Suspend = false // Already cleared by operator.
	plan.Annotations = map[string]string{
		wellknown.AnnotationSuspendedAtPhase: string(hibernatorv1alpha1.PhaseActive),
	}

	c := newHandlerFakeClient(plan)
	tt := &timerTracker{}
	state := newHandlerState(plan, c, tt)

	h := &suspendedState{State: state}
	h.Handle(context.Background())

	// resume() queues the Active transition but does not mutate plan.Status in-memory.
	assert.True(t, tt.cancelDeadlineCalled)
	assert.GreaterOrEqual(t, state.Statuses.PlanStatuses.Len(), 1)
}

func TestSuspendedState_Handle_SuspendUntilExpired_PatchesPlanAndResumes(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseSuspended)
	plan.Spec.Suspend = true
	plan.Annotations = map[string]string{
		// Past time → deadline already expired.
		wellknown.AnnotationSuspendUntil:     time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339),
		wellknown.AnnotationSuspendedAtPhase: string(hibernatorv1alpha1.PhaseActive),
	}

	c := newHandlerFakeClient(plan)
	tt := &timerTracker{}
	state := newHandlerState(plan, c, tt)

	h := &suspendedState{State: state}
	h.Handle(context.Background())

	// Spec.Suspend should have been patched to false, then resume ran.
	// resume() queues the Active transition but does not mutate plan.Status in-memory.
	assert.False(t, plan.Spec.Suspend)
	assert.GreaterOrEqual(t, state.Statuses.PlanStatuses.Len(), 1, "status update should be queued after resume")
}

// ---------------------------------------------------------------------------
// suspendedState.OnDeadline()
// ---------------------------------------------------------------------------

func TestSuspendedState_OnDeadline_PatchesPlanAndResumes(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseSuspended)
	plan.Spec.Suspend = true
	plan.Annotations = map[string]string{
		wellknown.AnnotationSuspendUntil:     metav1.Now().UTC().Format(time.RFC3339),
		wellknown.AnnotationSuspendedAtPhase: string(hibernatorv1alpha1.PhaseActive),
	}

	c := newHandlerFakeClient(plan)
	tt := &timerTracker{}
	state := newHandlerState(plan, c, tt)

	h := &suspendedState{State: state}
	err := c.Update(context.Background(), plan) // ensure object exists in fake store
	require.NoError(t, err)

	h.OnDeadline(context.Background())

	assert.False(t, plan.Spec.Suspend, "Spec.Suspend should be cleared by OnDeadline")
}

// ---------------------------------------------------------------------------
// suspendedState.resumeFromExecution()
// ---------------------------------------------------------------------------

func TestResumeFromExecution_NotExecutionPhase_ReturnsFalse(t *testing.T) {
	// suspendedAtPhase = Hibernated (not an execution phase) → should not be handled here.
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseSuspended)
	plan.Annotations = map[string]string{
		wellknown.AnnotationSuspendedAtPhase: string(hibernatorv1alpha1.PhaseHibernated),
	}
	c := newHandlerFakeClient(plan)
	state := newHandlerState(plan, c, &timerTracker{})

	st := &suspendedState{State: state}
	assert.False(t, st.resumeFromExecution(context.Background(), logr.Discard()))
}

func TestResumeFromExecution_NoScheduleResult_ReturnsFalse(t *testing.T) {
	// No ScheduleResult available → cannot determine same-window; bail out.
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseSuspended)
	plan.Annotations = map[string]string{
		wellknown.AnnotationSuspendedAtPhase: string(hibernatorv1alpha1.PhaseHibernating),
	}
	c := newHandlerFakeClient(plan)
	state := newHandlerState(plan, c, &timerTracker{})
	// ScheduleResult is nil by default.

	st := &suspendedState{State: state}
	assert.False(t, st.resumeFromExecution(context.Background(), logr.Discard()))
}

func TestResumeFromExecution_Hibernating_SameWindow_ResumesToHibernating(t *testing.T) {
	// Suspended mid-shutdown; resume during the same off-hours window
	// → Phase transitions back to PhaseHibernating; execution bookmarks preserved.
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseSuspended)
	plan.Annotations = map[string]string{
		wellknown.AnnotationSuspendedAtPhase: string(hibernatorv1alpha1.PhaseHibernating),
	}
	plan.Status.CurrentCycleID = "abc123"
	plan.Status.CurrentOperation = "shutdown"
	plan.Status.CurrentStageIndex = 1
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "db", State: hibernatorv1alpha1.StateCompleted},
	}

	c := newHandlerFakeClient(plan)
	state := newHandlerState(plan, c, &timerTracker{})
	state.PlanCtx.ScheduleResult = &message.ScheduleEvaluation{ShouldHibernate: true}

	st := &suspendedState{State: state}
	result := st.resumeFromExecution(context.Background(), logr.Discard())

	assert.True(t, result, "should be handled")

	// Verify the status update that resumeFromExecution queued (the "K8s intent").
	// We drain the first queue entry because dispatch() may chain further updates
	// depending on the test plan's execution setup.
	require.GreaterOrEqual(t, state.Statuses.PlanStatuses.Len(), 1, "status update must be queued")
	firstUpdate := <-state.Statuses.PlanStatuses.C()
	var committed hibernatorv1alpha1.HibernatePlanStatus
	firstUpdate.Mutate(&committed)
	assert.Equal(t, hibernatorv1alpha1.PhaseHibernating, committed.Phase, "queued mutation must set PhaseHibernating")

	// Execution bookmarks must be preserved in the in-memory plan (resumeFromExecution does not clear them).
	assert.Equal(t, "abc123", plan.Status.CurrentCycleID)
	assert.Equal(t, "shutdown", plan.Status.CurrentOperation)
	assert.Equal(t, 1, plan.Status.CurrentStageIndex)
	assert.Len(t, plan.Status.Executions, 1)
}

func TestResumeFromExecution_Hibernating_DifferentWindow_ResumesToActive(t *testing.T) {
	// Suspended mid-shutdown; clock is now on-hours → shutdown window passed,
	// resource was never shut down → route to PhaseActive with preserved bookmarks.
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseSuspended)
	plan.Annotations = map[string]string{
		wellknown.AnnotationSuspendedAtPhase: string(hibernatorv1alpha1.PhaseHibernating),
	}
	plan.Status.CurrentCycleID = "abc123"
	plan.Status.CurrentOperation = "shutdown"
	plan.Status.CurrentStageIndex = 1

	c := newHandlerFakeClient(plan)
	state := newHandlerState(plan, c, &timerTracker{})
	state.PlanCtx.ScheduleResult = &message.ScheduleEvaluation{ShouldHibernate: false}

	st := &suspendedState{State: state}
	result := st.resumeFromExecution(context.Background(), logr.Discard())

	assert.True(t, result)

	// Verify the status update that resumeFromExecution queued (the "K8s intent").
	require.GreaterOrEqual(t, state.Statuses.PlanStatuses.Len(), 1)
	firstUpdate := <-state.Statuses.PlanStatuses.C()
	var committed hibernatorv1alpha1.HibernatePlanStatus
	firstUpdate.Mutate(&committed)
	assert.Equal(t, hibernatorv1alpha1.PhaseActive, committed.Phase, "queued mutation must set PhaseActive")

	// Execution bookmarks are preserved in the in-memory plan (resumeFromExecution does not clear them).
	assert.Equal(t, "abc123", plan.Status.CurrentCycleID)
	assert.Equal(t, "shutdown", plan.Status.CurrentOperation)
	assert.Equal(t, 1, plan.Status.CurrentStageIndex)
}

func TestResumeFromExecution_WakingUp_SameWindow_ResumesToWakingUp(t *testing.T) {
	// Suspended mid-wakeup; resume still in on-hours → continue wakeup with preserved state.
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseSuspended)
	plan.Annotations = map[string]string{
		wellknown.AnnotationSuspendedAtPhase: string(hibernatorv1alpha1.PhaseWakingUp),
	}
	plan.Status.CurrentCycleID = "def456"
	plan.Status.CurrentOperation = "wakeup"
	plan.Status.CurrentStageIndex = 0
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "app", State: hibernatorv1alpha1.StateRunning},
	}

	c := newHandlerFakeClient(plan)
	state := newHandlerState(plan, c, &timerTracker{})
	state.PlanCtx.ScheduleResult = &message.ScheduleEvaluation{ShouldHibernate: false}

	st := &suspendedState{State: state}
	result := st.resumeFromExecution(context.Background(), logr.Discard())

	assert.True(t, result)

	// Verify the status update that resumeFromExecution queued (the "K8s intent").
	require.GreaterOrEqual(t, state.Statuses.PlanStatuses.Len(), 1)
	firstUpdate := <-state.Statuses.PlanStatuses.C()
	var committed hibernatorv1alpha1.HibernatePlanStatus
	firstUpdate.Mutate(&committed)
	assert.Equal(t, hibernatorv1alpha1.PhaseWakingUp, committed.Phase, "queued mutation must set PhaseWakingUp")

	// Execution bookmarks must be preserved in the in-memory plan.
	assert.Equal(t, "def456", plan.Status.CurrentCycleID)
	assert.Equal(t, "wakeup", plan.Status.CurrentOperation)
	assert.Zero(t, plan.Status.CurrentStageIndex)
	assert.Len(t, plan.Status.Executions, 1)
}

func TestResumeFromExecution_WakingUp_DifferentWindow_ResumesToHibernated(t *testing.T) {
	// Suspended mid-wakeup; clock is now off-hours → wakeup window passed,
	// wakeup never completed → route to PhaseHibernated with preserved bookmarks.
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseSuspended)
	plan.Annotations = map[string]string{
		wellknown.AnnotationSuspendedAtPhase: string(hibernatorv1alpha1.PhaseWakingUp),
	}
	plan.Status.CurrentCycleID = "def456"
	plan.Status.CurrentOperation = "wakeup"
	plan.Status.CurrentStageIndex = 0

	c := newHandlerFakeClient(plan)
	state := newHandlerState(plan, c, &timerTracker{})
	state.PlanCtx.ScheduleResult = &message.ScheduleEvaluation{ShouldHibernate: true}

	st := &suspendedState{State: state}
	result := st.resumeFromExecution(context.Background(), logr.Discard())

	assert.True(t, result)

	// Verify the status update that resumeFromExecution queued (the "K8s intent").
	require.GreaterOrEqual(t, state.Statuses.PlanStatuses.Len(), 1)
	firstUpdate := <-state.Statuses.PlanStatuses.C()
	var committed hibernatorv1alpha1.HibernatePlanStatus
	firstUpdate.Mutate(&committed)
	assert.Equal(t, hibernatorv1alpha1.PhaseHibernated, committed.Phase, "queued mutation must set PhaseHibernated")

	// Execution bookmarks are preserved in the in-memory plan (resumeFromExecution does not clear them).
	assert.Equal(t, "def456", plan.Status.CurrentCycleID)
	assert.Equal(t, "wakeup", plan.Status.CurrentOperation)
	assert.Zero(t, plan.Status.CurrentStageIndex)
}
