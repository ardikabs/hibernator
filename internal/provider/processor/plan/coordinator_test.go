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
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"

	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/pkg/keyedworker"
)

// ---------------------------------------------------------------------------
// Coordinator helpers
// ---------------------------------------------------------------------------

func newTestCoordinator() *Coordinator {
	return &Coordinator{
		Log:      logr.Discard(),
		Statuses: newTestStatuses(),
	}
}

// ---------------------------------------------------------------------------
// workerFactory — unit-level tests
// ---------------------------------------------------------------------------

// TestCoordinator_WorkerFactory_ReturnsNonNilRunFn verifies that workerFactory
// returns a non-nil goroutine body for any key/slot combination.
func TestCoordinator_WorkerFactory_ReturnsNonNilRunFn(t *testing.T) {
	c := newTestCoordinator()
	key := types.NamespacedName{Name: "plan-a", Namespace: "default"}
	slot := keyedworker.LatestWinsSlot[*message.PlanContext]()()

	fn := c.workerFactory(key, slot)
	require.NotNil(t, fn, "workerFactory should return a non-nil goroutine body")
}

// TestCoordinator_WorkerFactory_GoroutineExitsOnCtxCancel verifies that the
// goroutine body returned by workerFactory respects context cancellation and
// returns promptly when the context is cancelled (without calling handle, since
// no real deps are wired).
func TestCoordinator_WorkerFactory_GoroutineExitsOnCtxCancel(t *testing.T) {
	c := newTestCoordinator()
	key := types.NamespacedName{Name: "plan-b", Namespace: "default"}
	slot := keyedworker.LatestWinsSlot[*message.PlanContext]()()

	fn := c.workerFactory(key, slot)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		fn(ctx) // Worker.run — blocks until ctx is cancelled or idle timeout
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// OK — goroutine exited on context cancellation.
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine did not exit after context cancellation")
	}
}

// ---------------------------------------------------------------------------
// Coordinator.Start — integration-level tests via pool behaviour
// ---------------------------------------------------------------------------

// TestCoordinator_Start_StartsAndStopsCleanly verifies that the coordinator
// starts successfully, subscribes to the watchable map, and shuts down cleanly
// on context cancellation without requiring fully-wired infrastructure deps.
func TestCoordinator_Start_StartsAndStopsCleanly(t *testing.T) {
	c := newTestCoordinator()
	c.Resources = new(message.ControllerResources)

	ctx, cancel := context.WithCancel(context.Background())

	coordDone := make(chan error, 1)
	go func() { coordDone <- c.Start(ctx) }()

	// Give the coordinator time to enter its HandleSubscription loop.
	time.Sleep(20 * time.Millisecond)

	// Cancel and verify clean shutdown.
	cancel()
	select {
	case err := <-coordDone:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("coordinator did not stop after context cancellation")
	}
}

// TestCoordinator_Start_DeleteUnknownKey_IsNoop verifies that a delete event
// for a key that was never upserted is handled safely (pool.Remove on an
// unknown key must not panic or block).
func TestCoordinator_Start_DeleteUnknownKey_IsNoop(t *testing.T) {
	c := newTestCoordinator()
	c.Resources = new(message.ControllerResources)

	ctx, cancel := context.WithCancel(context.Background())

	coordDone := make(chan error, 1)
	go func() { coordDone <- c.Start(ctx) }()

	// Trigger a delete for a key that was never stored.
	// The watchable map emits a delete-only update; the coordinator must handle it gracefully.
	key := types.NamespacedName{Name: "ghost-plan", Namespace: "default"}
	c.Resources.PlanResources.Delete(key)

	time.Sleep(20 * time.Millisecond)

	cancel()
	select {
	case err := <-coordDone:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("coordinator did not stop after context cancellation")
	}
}

// ---------------------------------------------------------------------------
// Worker — pure / in-memory methods
// ---------------------------------------------------------------------------

func newTestWorker() *Worker {
	return &Worker{
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
	w := newTestWorker()
	incoming := &message.PlanContext{HasRestoreData: true}

	w.mergeIncoming(incoming)

	assert.Same(t, incoming, w.cachedCtx)
}

func TestWorker_MergeIncoming_PreservesOptimisticStatus(t *testing.T) {
	w := newTestWorker()

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
	w := newTestWorker()
	w.cachedCtx = &message.PlanContext{Plan: nil}

	incoming := &message.PlanContext{HasRestoreData: true}
	w.mergeIncoming(incoming)

	assert.Same(t, incoming, w.cachedCtx)
}

func TestWorker_TrackConsecutiveJobMiss_IncrementsThenThresholds(t *testing.T) {
	w := newTestWorker()
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
	w := newTestWorker()
	w.cachedCtx = planCtxWithPhase(hibernatorv1alpha1.PhaseWakingUp)

	for i := 0; i < consecutiveJobMissThreshold-1; i++ {
		w.trackConsecutiveJobMiss("target-b")
	}
	assert.True(t, w.trackConsecutiveJobMiss("target-b"))
}

func TestWorker_TrackConsecutiveJobMiss_NilCachedCtx_ReturnsFalse(t *testing.T) {
	w := newTestWorker()
	assert.False(t, w.trackConsecutiveJobMiss("target-a"))
}

func TestWorker_TrackConsecutiveJobMiss_WrongPhase_ReturnsFalse(t *testing.T) {
	w := newTestWorker()
	w.cachedCtx = planCtxWithPhase(hibernatorv1alpha1.PhaseActive)

	// Calling many times should always return false for non-execution phases.
	for i := 0; i < consecutiveJobMissThreshold+5; i++ {
		assert.False(t, w.trackConsecutiveJobMiss("target-a"))
	}
}

func TestWorker_ResetConsecutiveJobMiss_ClearsCounter(t *testing.T) {
	w := newTestWorker()
	w.consecutiveJobMisses = map[string]int{"target-a": 2}

	w.resetConsecutiveJobMiss("target-a")

	_, exists := w.consecutiveJobMisses["target-a"]
	assert.False(t, exists)
}

func TestWorker_ResetConsecutiveJobMiss_NilMap_IsNoop(t *testing.T) {
	w := newTestWorker()
	// Must not panic.
	w.resetConsecutiveJobMiss("target-a")
}

func TestTimerChan_NilTimer_ReturnsNil(t *testing.T) {
	assert.Nil(t, timerChan(nil))
}

func TestTimerChan_ActiveTimer_ReturnsChannel(t *testing.T) {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	assert.NotNil(t, timerChan(timer))
}

// ---------------------------------------------------------------------------
// Worker — timer helpers
// ---------------------------------------------------------------------------

func TestWorker_StopRequeue_NilTimer_IsNoop(t *testing.T) {
	w := newTestWorker()
	// Must not panic when pollTimer is nil.
	w.stopRequeueTimer()
	assert.Nil(t, w.requeueTimer)
}

func TestWorker_StopRequeueTimer_ActiveTimer_StopsAndNils(t *testing.T) {
	w := newTestWorker()
	w.setRequeueTimer(time.Hour)
	w.stopRequeueTimer()
	assert.Nil(t, w.requeueTimer)
}

func TestWorker_SetRequeueTimer_SetsNewTimer(t *testing.T) {
	w := newTestWorker()
	w.setRequeueTimer(time.Hour)

	require.NotNil(t, w.requeueTimer)
	w.requeueTimer.Stop() // cleanup
}

func TestWorker_SetRequeueTimer_ReplacesExistingTimer(t *testing.T) {
	w := newTestWorker()
	w.requeueTimer = time.NewTimer(time.Hour)
	prevC := w.requeueTimer.C

	w.setRequeueTimer(2 * time.Hour)

	require.NotNil(t, w.requeueTimer)
	assert.NotEqual(t, prevC, w.requeueTimer.C, "should have created a new timer")
	w.requeueTimer.Stop()
}

func TestWorker_SetDeadlineTimer_SetsNewTimer(t *testing.T) {
	w := newTestWorker()
	w.setDeadlineTimer(time.Hour)

	require.NotNil(t, w.deadlineTimer)
	w.deadlineTimer.Stop()
}

func TestWorker_SetDeadlineTimer_PreserveExistingTimer(t *testing.T) {
	w := newTestWorker()
	w.deadlineTimer = time.NewTimer(time.Hour)
	prevC := w.deadlineTimer.C

	w.setDeadlineTimer(30 * time.Minute)
	w.setDeadlineTimer(15 * time.Minute)
	w.setDeadlineTimer(16 * time.Minute)
	w.setDeadlineTimer(17 * time.Minute)

	require.NotNil(t, w.deadlineTimer)
	assert.Equal(t, prevC, w.deadlineTimer.C, "should have preserved the existing timer")
	w.deadlineTimer.Stop()
}

func TestWorker_StopDeadlineTimer_NilTimer_IsNoop(t *testing.T) {
	w := newTestWorker()
	w.stopDeadlineTimer()
	assert.Nil(t, w.deadlineTimer)
}

func TestWorker_Cleanup_ClearsAllTimers(t *testing.T) {
	w := newTestWorker()
	w.requeueTimer = time.NewTimer(time.Hour)
	w.deadlineTimer = time.NewTimer(time.Hour)

	w.cleanup()

	assert.Nil(t, w.requeueTimer)
	assert.Nil(t, w.deadlineTimer)
}

// ---------------------------------------------------------------------------
// Coordinator.NeedLeaderElection
// ---------------------------------------------------------------------------

func TestCoordinator_NeedLeaderElection_ReturnsTrue(t *testing.T) {
	c := newTestCoordinator()
	assert.True(t, c.NeedLeaderElection())
}
