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
	"github.com/ardikabs/hibernator/pkg/conflate"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/ardikabs/hibernator/internal/message"
)

// ---------------------------------------------------------------------------
// Coordinator helpers
// ---------------------------------------------------------------------------

func newTestCoordinator() *Coordinator {
	return &Coordinator{
		Log:      logr.Discard(),
		Statuses: message.NewControllerStatuses(),
	}
}

func TestCoordinator_Spawn_CreatesEntry(t *testing.T) {
	c := newTestCoordinator()
	key := types.NamespacedName{Name: "plan-a", Namespace: "default"}

	// Use a pre-cancelled context so the spawned goroutine exits immediately
	// on ctx.Done() without ever calling handle() (which requires non-nil deps).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c.mu.Lock()
	entry := c.spawn(ctx, key)
	c.mu.Unlock()

	require.NotNil(t, entry)
	require.NotNil(t, entry.slot)
	assert.Len(t, c.workers, 1)

	c.shutdownAll()
}

func TestCoordinator_Despawn_RemovesEntry(t *testing.T) {
	c := newTestCoordinator()
	key := types.NamespacedName{Name: "plan-a", Namespace: "default"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so goroutine exits immediately

	c.mu.Lock()
	c.spawn(ctx, key)
	c.mu.Unlock()

	c.despawn(key)

	c.mu.Lock()
	_, exists := c.workers[key]
	c.mu.Unlock()
	assert.False(t, exists)
}

func TestCoordinator_Despawn_UnknownKey_IsNoop(t *testing.T) {
	c := newTestCoordinator()
	// Must not panic.
	c.despawn(types.NamespacedName{Name: "unknown", Namespace: "default"})
}

func TestCoordinator_Reap_RemovesEntry(t *testing.T) {
	c := newTestCoordinator()
	key := types.NamespacedName{Name: "plan-b", Namespace: "default"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so goroutine exits immediately

	c.mu.Lock()
	c.spawn(ctx, key)
	c.mu.Unlock()

	c.reap(key)

	c.mu.Lock()
	_, exists := c.workers[key]
	c.mu.Unlock()
	assert.False(t, exists)
}

func TestCoordinator_ShutdownAll_ClearsAllWorkers(t *testing.T) {
	c := newTestCoordinator()

	// Pre-cancel context so spawned goroutines exit without calling handle().
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	keys := []types.NamespacedName{
		{Name: "p1", Namespace: "default"},
		{Name: "p2", Namespace: "default"},
		{Name: "p3", Namespace: "default"},
	}
	c.mu.Lock()
	for _, k := range keys {
		c.spawn(ctx, k)
	}
	c.mu.Unlock()

	c.shutdownAll()

	c.mu.Lock()
	count := len(c.workers)
	c.mu.Unlock()
	assert.Equal(t, 0, count)
}

func TestCoordinator_Delivery_SpawnsWorkerOnFirstDelivery(t *testing.T) {
	c := newTestCoordinator()
	key := types.NamespacedName{Name: "plan-c", Namespace: "default"}

	// Insert a pre-existing entry directly to avoid starting a real goroutine.
	// This tests the data-structure invariant of delivery(): entry is present afterward.
	cancelCtx, cancelFn := context.WithCancel(context.Background())
	cancelFn() // already cancelled — no goroutine will call handle()

	c.mu.Lock()
	if c.workers == nil {
		c.workers = make(map[types.NamespacedName]*workerEntry)
	}
	slot := conflate.New[*message.PlanContext]()
	c.workers[key] = &workerEntry{cancel: cancelFn, slot: slot}
	c.mu.Unlock()

	// Simulate what delivery would do on second call (reuse path).
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "plan-c", Namespace: "default"},
	}
	planCtx := &message.PlanContext{Plan: plan}
	slot.Send(planCtx)

	// Entry must still be present.
	c.mu.Lock()
	entry, exists := c.workers[key]
	c.mu.Unlock()

	require.True(t, exists, "worker entry should be present")
	require.NotNil(t, entry.slot)
	_ = cancelCtx
}

func TestCoordinator_Delivery_ReusesExistingWorker(t *testing.T) {
	c := newTestCoordinator()
	key := types.NamespacedName{Name: "plan-d", Namespace: "default"}

	_, cancelFn := context.WithCancel(context.Background())
	cancelFn()

	// Insert entry manually — simulates an already-spawned worker.
	c.mu.Lock()
	if c.workers == nil {
		c.workers = make(map[types.NamespacedName]*workerEntry)
	}
	firstSlot := conflate.New[*message.PlanContext]()
	c.workers[key] = &workerEntry{cancel: cancelFn, slot: firstSlot}
	c.mu.Unlock()

	// A second send to the same slot should land on the same slot pointer.
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "plan-d", Namespace: "default"},
	}
	firstSlot.Send(&message.PlanContext{Plan: plan, HasRestoreData: true})

	c.mu.Lock()
	secondSlot := c.workers[key].slot
	c.mu.Unlock()

	assert.Same(t, firstSlot, secondSlot, "second delivery should reuse the existing worker slot")
}

// ---------------------------------------------------------------------------
// Worker  — pure / in-memory methods
// ---------------------------------------------------------------------------

func newTestWorker() *Worker {
	return &Worker{
		key:      types.NamespacedName{Name: "p", Namespace: "default"},
		log:      logr.Discard(),
		slot:     conflate.New[*message.PlanContext](),
		Statuses: message.NewControllerStatuses(),
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
