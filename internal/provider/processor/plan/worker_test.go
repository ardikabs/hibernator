/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package plan

import (
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
	"github.com/ardikabs/hibernator/pkg/keyedworker"
)

// ---------------------------------------------------------------------------
// Worker — pure / in-memory methods
// ---------------------------------------------------------------------------

func newTestWorker(clk clock.Clock) *Worker {
	return &Worker{
		Clock:    clk,
		key:      types.NamespacedName{Name: "p", Namespace: "default"},
		log:      logr.Discard(),
		slot:     keyedworker.LatestWinsSlot[*message.PlanContext]()(),
		Statuses: newTestStatuses(),
	}
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

func TestTimerChan_NilTimer_ReturnsNil(t *testing.T) {
	assert.Nil(t, timerChan(nil))
}

func TestTimerChan_ActiveTimer_ReturnsChannel(t *testing.T) {
	clk := &clock.RealClock{}
	timer := clk.NewTimer(time.Hour)
	defer timer.Stop()
	assert.NotNil(t, timerChan(timer))
}

// ---------------------------------------------------------------------------
// Worker — timer helpers
// ---------------------------------------------------------------------------

func TestWorker_StopRequeue_NilTimer_IsNoop(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	// Must not panic when pollTimer is nil.
	w.stopRequeueTimer()
	assert.Nil(t, w.requeueTimer)
}

func TestWorker_StopRequeueTimer_ActiveTimer_StopsAndNils(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	w.setRequeueTimer(time.Hour)
	w.stopRequeueTimer()
	assert.Nil(t, w.requeueTimer)
}

func TestWorker_SetRequeueTimer_SetsNewTimer(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	w.setRequeueTimer(time.Hour)

	require.NotNil(t, w.requeueTimer)
	w.requeueTimer.Stop() // cleanup
}

func TestWorker_SetRequeueTimer_ReplacesExistingTimer(t *testing.T) {
	clk := &clock.RealClock{}

	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	w.requeueTimer = clk.NewTimer(time.Hour)
	prevC := w.requeueTimer.C()

	w.setRequeueTimer(2 * time.Hour)

	require.NotNil(t, w.requeueTimer)
	assert.NotEqual(t, prevC, w.requeueTimer.C(), "should have created a new timer")
	w.requeueTimer.Stop()
}

func TestWorker_SetDeadlineTimer_SetsNewTimer(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	w.setDeadlineTimer(time.Hour)

	require.NotNil(t, w.deadlineTimer)
	w.deadlineTimer.Stop()
}

func TestWorker_SetDeadlineTimer_PreserveExistingTimer(t *testing.T) {
	clk := &clock.RealClock{}

	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	w.deadlineTimer = clk.NewTimer(time.Hour)
	prevC := w.deadlineTimer.C()

	w.setDeadlineTimer(30 * time.Minute)
	w.setDeadlineTimer(15 * time.Minute)
	w.setDeadlineTimer(16 * time.Minute)
	w.setDeadlineTimer(17 * time.Minute)

	require.NotNil(t, w.deadlineTimer)
	assert.Equal(t, prevC, w.deadlineTimer.C(), "should have preserved the existing timer")
	w.deadlineTimer.Stop()
}

func TestWorker_StopDeadlineTimer_NilTimer_IsNoop(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	w.stopDeadlineTimer()
	assert.Nil(t, w.deadlineTimer)
}

func TestWorker_Cleanup_ClearsAllTimers(t *testing.T) {
	clk := &clock.RealClock{}

	fakeClock := clocktesting.NewFakeClock(time.Now())
	w := newTestWorker(fakeClock)
	w.requeueTimer = clk.NewTimer(time.Hour)
	w.deadlineTimer = clk.NewTimer(time.Hour)

	w.cleanup()

	assert.Nil(t, w.requeueTimer)
	assert.Nil(t, w.deadlineTimer)
}
