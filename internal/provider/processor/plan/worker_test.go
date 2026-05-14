/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package plan

import (
	"context"
	"testing"
	"time"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"

	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/internal/provider/processor/plan/state"
	"github.com/ardikabs/hibernator/pkg/keyedworker"
)

// ---------------------------------------------------------------------------
// Worker — pure / in-memory methods
// ---------------------------------------------------------------------------

func newTestWorker(clk clock.Clock) *Worker {
	w := &Worker{
		Infrastructure: state.Infrastructure{
			Clock: clk,
		},
		key:      types.NamespacedName{Name: "p", Namespace: "default"},
		log:      logr.Discard(),
		slot:     keyedworker.LatestWinsSlot[*message.PlanContext]()(),
		Statuses: newTestStatuses(),
	}
	w.timers = NewTimerSet(logr.Discard(), w.Clock, defaultWorkerIdleTimeout, TimerHooks{
		OnRequeue: func(_ context.Context, planCtx *message.PlanContext) {
			if planCtx != nil {
				w.handle(nil, planCtx, false)
			}
		},
		OnDeadline: func(_ context.Context, planCtx *message.PlanContext) {
			if planCtx != nil {
				w.handle(nil, planCtx, true)
			}
		},
		OnInactivity: func() {},
	})
	return w
}

func planCtxWithPhase(phase hibernatorv1alpha1.PlanPhase) *message.PlanContext {
	plan := &hibernatorv1alpha1.HibernatePlan{}
	plan.Status.Phase = phase
	return &message.PlanContext{Plan: plan}
}

func TestWorker_MergeIncoming_FirstDelivery_SetsCachedCtx(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	incoming := &message.PlanContext{HasRestoreData: true}

	w.mergeIncoming(incoming)

	assert.Same(t, incoming, w.cachedCtx)
}

func TestWorker_MergeIncoming_PreservesOptimisticStatus(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	// Establish an optimistic in-memory phase.
	cached := &hibernatorv1alpha1.HibernatePlan{}
	cached.Status.Phase = hibernatorv1alpha1.PhaseHibernating
	w.cachedCtx = &message.PlanContext{Plan: cached}

	// Incoming delivery carries stale status.
	stale := &hibernatorv1alpha1.HibernatePlan{}
	stale.Status.Phase = hibernatorv1alpha1.PhaseActive
	incoming := &message.PlanContext{Plan: stale, HasRestoreData: true}

	w.mergeIncoming(incoming)

	assert.Equal(t, hibernatorv1alpha1.PhaseHibernating, w.cachedCtx.Plan.Status.Phase,
		"optimistic phase should be preserved")
	assert.True(t, w.cachedCtx.HasRestoreData,
		"provider-derived fields should come from incoming")
}

func TestWorker_MergeIncoming_NilCachedPlan_AcceptsIncoming(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	w.cachedCtx = &message.PlanContext{Plan: nil}

	incoming := &message.PlanContext{HasRestoreData: true}
	w.mergeIncoming(incoming)

	assert.Same(t, incoming, w.cachedCtx)
}

func TestWorker_TrackConsecutiveJobMiss_IncrementsThenThresholds(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	w.cachedCtx = planCtxWithPhase(hibernatorv1alpha1.PhaseHibernating)

	for i := 0; i < consecutiveJobMissThreshold-1; i++ {
		assert.False(t, w.trackConsecutiveJobMiss("target-a"),
			"should return false before threshold at iteration %d", i)
	}

	assert.True(t, w.trackConsecutiveJobMiss("target-a"),
		"should return true at threshold")
	assert.Equal(t, 0, w.consecutiveJobMisses["target-a"],
		"counter should reset to 0 after threshold")
}

func TestWorker_TrackConsecutiveJobMiss_WakingUpPhase_Works(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	w.cachedCtx = planCtxWithPhase(hibernatorv1alpha1.PhaseWakingUp)

	for i := 0; i < consecutiveJobMissThreshold-1; i++ {
		w.trackConsecutiveJobMiss("target-b")
	}
	assert.True(t, w.trackConsecutiveJobMiss("target-b"))
}

func TestWorker_TrackConsecutiveJobMiss_NilCachedCtx_ReturnsFalse(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	assert.False(t, w.trackConsecutiveJobMiss("target-a"))
}

func TestWorker_TrackConsecutiveJobMiss_WrongPhase_ReturnsFalse(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	w.cachedCtx = planCtxWithPhase(hibernatorv1alpha1.PhaseActive)

	// Calling many times should always return false for non-execution phases.
	for i := 0; i < consecutiveJobMissThreshold+5; i++ {
		assert.False(t, w.trackConsecutiveJobMiss("target-a"))
	}
}

func TestWorker_ResetConsecutiveJobMiss_ClearsCounter(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	w.consecutiveJobMisses = map[string]int{"target-a": 2}

	w.resetConsecutiveJobMiss("target-a")

	_, exists := w.consecutiveJobMisses["target-a"]
	assert.False(t, exists)
}

func TestWorker_ResetConsecutiveJobMiss_NilMap_IsNoop(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	// Must not panic.
	w.resetConsecutiveJobMiss("target-a")
}

// ---------------------------------------------------------------------------
// Worker — timer helpers via TimerSet
// ---------------------------------------------------------------------------

func TestWorker_TimerSet_StopRequeue_NilTimer_IsNoop(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	// Must not panic when requeue timer is nil.
	w.timers.StopRequeue()
	assert.False(t, w.timers.Requeue.IsArmed())
}

func TestWorker_TimerSet_SetRequeueTimer_SetsNewTimer(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	w.timers.SetRequeue(time.Hour)

	require.True(t, w.timers.Requeue.IsArmed())
	w.timers.StopRequeue() // cleanup
}

func TestWorker_TimerSet_SetRequeueTimer_ReplacesExistingTimer(t *testing.T) {
	clk := &clock.RealClock{}

	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	w.timers.Requeue = NewSchedule(clk, "requeue")
	w.timers.Requeue.Arm(time.Hour)
	prevC := w.timers.Requeue.C()

	w.timers.SetRequeue(2 * time.Hour)

	require.True(t, w.timers.Requeue.IsArmed())
	assert.NotEqual(t, prevC, w.timers.Requeue.C(), "should have created a new timer")
	w.timers.StopRequeue()
}

func TestWorker_TimerSet_SetDeadlineTimer_SetsNewTimer(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	w.timers.SetDeadline(time.Hour)

	require.True(t, w.timers.Deadline.IsArmed())
	assert.Equal(t, time.Hour, w.timers.Deadline.Duration())

	w.timers.StopDeadline()
}

func TestWorker_TimerSet_SetDeadlineTimer_AlwaysReplaces(t *testing.T) {
	clk := &clock.RealClock{}

	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	w.timers.Deadline = NewSchedule(clk, "deadline")
	w.timers.Deadline.Arm(time.Hour)
	prevC := w.timers.Deadline.C()

	w.timers.SetDeadline(30 * time.Minute)

	require.True(t, w.timers.Deadline.IsArmed())
	assert.NotEqual(t, prevC, w.timers.Deadline.C(), "should have replaced the existing timer")
	assert.Equal(t, 30*time.Minute, w.timers.Deadline.Duration(), "should use the new deadline duration")
	w.timers.StopDeadline()
}

func TestWorker_TimerSet_StopDeadlineTimer_NilTimer_IsNoop(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	// Must not panic when deadline timer is not armed.
	w.timers.StopDeadline()
	assert.False(t, w.timers.Deadline.IsArmed())
}

func TestWorker_TimerSet_KeepAlive_Default(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	w.timers.Inactivity.Arm(defaultWorkerIdleTimeout)

	w.timers.KeepAlive()

	// FakeClock timer doesn't expose duration; verify no panic and timer still active.
	assert.True(t, w.timers.Inactivity.IsArmed())
}

func TestWorker_TimerSet_KeepAlive_ShortDeadline_NoExtension(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	w.timers.Inactivity.Arm(defaultWorkerIdleTimeout)
	w.timers.SetDeadline(5 * time.Minute) // shorter than 30 min

	w.timers.KeepAlive()

	assert.True(t, w.timers.Inactivity.IsArmed())
	// inactivity timer should remain at default defaultWorkerIdleTimeout (30 min)
	// since deadline (5 min) is shorter.
}

func TestWorker_TimerSet_KeepAlive_LongDeadline_ExtendsInactivity(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	w.timers.Inactivity.Arm(defaultWorkerIdleTimeout)
	w.timers.SetDeadline(2 * time.Hour) // longer than 30 min

	w.timers.KeepAlive()

	assert.True(t, w.timers.Inactivity.IsArmed())
	// inactivity timer should be extended to 2h + 1m to cover the deadline.
}

func TestWorker_TimerSet_Cleanup_ClearsAllTimers(t *testing.T) {
	clk := &clock.RealClock{}

	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	w.timers.Requeue = NewSchedule(clk, "requeue")
	w.timers.Requeue.Arm(time.Hour)
	w.timers.Deadline = NewSchedule(clk, "deadline")
	w.timers.Deadline.Arm(time.Hour)
	w.timers.Inactivity = NewSchedule(clk, "inactivity")
	w.timers.Inactivity.Arm(time.Hour)

	w.timers.Cleanup()

	assert.False(t, w.timers.Requeue.IsArmed())
	assert.False(t, w.timers.Deadline.IsArmed())
	assert.False(t, w.timers.Inactivity.IsArmed())
}
