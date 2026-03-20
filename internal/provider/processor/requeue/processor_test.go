/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package requeue

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// captureEnqueuer records every Enqueue call in a buffered channel.
type captureEnqueuer struct {
	mu   sync.Mutex
	keys []types.NamespacedName
}

func (e *captureEnqueuer) Enqueue(key types.NamespacedName) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.keys = append(e.keys, key)
}

func (e *captureEnqueuer) Calls() []types.NamespacedName {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]types.NamespacedName, len(e.keys))
	copy(out, e.keys)
	return out
}

func (e *captureEnqueuer) CountFor(key types.NamespacedName) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	count := 0
	for _, k := range e.keys {
		if k == key {
			count++
		}
	}
	return count
}

// newProcessor builds a PlanRequeueProcessor backed by a fake clock and capture enqueuer.
func newProcessor(clk *clocktesting.FakeClock) (*PlanRequeueProcessor, *message.ControllerResources, *captureEnqueuer) {
	resources := new(message.ControllerResources)
	enqueuer := &captureEnqueuer{}
	p := &PlanRequeueProcessor{
		Clock:     clk,
		Log:       logr.Discard(),
		Resources: resources,
		Enqueuer:  enqueuer,
	}
	return p, resources, enqueuer
}

// planCtxWithSchedule returns a PlanContext with a Schedule.NextEvent set.
func planCtxWithSchedule(nextEvent time.Time) *message.PlanContext {
	return &message.PlanContext{
		Plan: &hibernatorv1alpha1.HibernatePlan{},
		Schedule: &message.ScheduleEvaluation{
			NextEvent: nextEvent,
		},
	}
}

// planCtxWithException returns a PlanContext carrying a single ScheduleException.
func planCtxWithException(validFrom, validUntil time.Time) *message.PlanContext {
	exc := hibernatorv1alpha1.ScheduleException{}
	if !validFrom.IsZero() {
		exc.Spec.ValidFrom = metav1.Time{Time: validFrom}
	}
	if !validUntil.IsZero() {
		exc.Spec.ValidUntil = metav1.Time{Time: validUntil}
	}
	return &message.PlanContext{
		Plan:       &hibernatorv1alpha1.HibernatePlan{},
		Exceptions: []hibernatorv1alpha1.ScheduleException{exc},
	}
}

// startProcessor runs the processor in the background and returns a cancel func.
func startProcessor(t *testing.T, p *PlanRequeueProcessor) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		if err := p.Start(ctx); err != nil {
			t.Logf("processor exited with error: %v", err)
		}
	}()
	// Small sleep to ensure the processor's subscription goroutine is running
	// before callers trigger map mutations.
	time.Sleep(10 * time.Millisecond)
	return cancel
}

// eventually retries the assertion f up to timeout, polling every 5 ms.
func eventually(t *testing.T, timeout time.Duration, f func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if f() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// ---------------------------------------------------------------------------
// computeBoundary
// ---------------------------------------------------------------------------

func TestComputeBoundary_NilContext_ReturnsFalse(t *testing.T) {
	_, ok := computeBoundary(time.Now(), nil)
	assert.False(t, ok)
}

func TestComputeBoundary_NoScheduleNoExceptions_ReturnsFalse(t *testing.T) {
	now := time.Now()
	_, ok := computeBoundary(now, &message.PlanContext{
		Plan: &hibernatorv1alpha1.HibernatePlan{},
	})
	assert.False(t, ok)
}

func TestComputeBoundary_ScheduleNextEvent_ReturnsDirectly(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	nextEvent := now.Add(30 * time.Minute)
	ctx := planCtxWithSchedule(nextEvent)

	boundary, ok := computeBoundary(now, ctx)
	require.True(t, ok)
	assert.Equal(t, nextEvent, boundary)
}

func TestComputeBoundary_ExceptionValidFrom_InFuture_IsIncluded(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	validFrom := now.Add(2 * time.Hour)
	ctx := planCtxWithException(validFrom, time.Time{})

	boundary, ok := computeBoundary(now, ctx)
	require.True(t, ok)
	assert.Equal(t, validFrom, boundary)
}

func TestComputeBoundary_ExceptionValidFrom_InPast_IsIgnored(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	validFrom := now.Add(-1 * time.Hour) // past
	ctx := planCtxWithException(validFrom, time.Time{})

	_, ok := computeBoundary(now, ctx)
	assert.False(t, ok, "past ValidFrom should not produce a boundary")
}

func TestComputeBoundary_ExceptionValidUntil_InFuture_IsIncluded(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	validUntil := now.Add(3 * time.Hour)
	ctx := planCtxWithException(time.Time{}, validUntil)

	boundary, ok := computeBoundary(now, ctx)
	require.True(t, ok)
	assert.Equal(t, validUntil, boundary)
}

func TestComputeBoundary_PicksEarliestAcrossScheduleAndExceptions(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// Schedule fires in 1 hour; exception ValidFrom in 30 minutes.
	// ValidFrom (30 min) should win.
	ctx := &message.PlanContext{
		Plan: &hibernatorv1alpha1.HibernatePlan{},
		Schedule: &message.ScheduleEvaluation{
			NextEvent: now.Add(1 * time.Hour),
		},
		Exceptions: []hibernatorv1alpha1.ScheduleException{
			{Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
				ValidFrom:  metav1.Time{Time: now.Add(30 * time.Minute)},
				ValidUntil: metav1.Time{Time: now.Add(5 * time.Hour)},
			}},
		},
	}

	boundary, ok := computeBoundary(now, ctx)
	require.True(t, ok)
	assert.Equal(t, now.Add(30*time.Minute), boundary, "should pick the earliest boundary (exception ValidFrom)")
}

func TestComputeBoundary_MultipleExceptions_PicksEarliest(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ctx := &message.PlanContext{
		Plan: &hibernatorv1alpha1.HibernatePlan{},
		Exceptions: []hibernatorv1alpha1.ScheduleException{
			{Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
				ValidFrom: metav1.Time{Time: now.Add(4 * time.Hour)},
			}},
			{Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
				ValidFrom: metav1.Time{Time: now.Add(1 * time.Hour)},
			}},
			{Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
				ValidUntil: metav1.Time{Time: now.Add(2 * time.Hour)},
			}},
		},
	}

	boundary, ok := computeBoundary(now, ctx)
	require.True(t, ok)
	assert.Equal(t, now.Add(1*time.Hour), boundary, "should pick the earliest ValidFrom across all exceptions")
}

// ---------------------------------------------------------------------------
// PlanRequeueProcessor — timer fires and enqueues
// ---------------------------------------------------------------------------

func TestProcessor_TimerFires_EnqueuesPlan(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clk := clocktesting.NewFakeClock(now)
	p, resources, enqueuer := newProcessor(clk)
	cancel := startProcessor(t, p)
	defer cancel()

	key := types.NamespacedName{Name: "my-plan", Namespace: "default"}
	resources.PlanResources.Store(key, planCtxWithSchedule(now.Add(5*time.Minute)))

	// Give HandleSubscription time to process the store.
	time.Sleep(20 * time.Millisecond)

	// Advance the clock past the boundary — timer should fire.
	clk.Step(6 * time.Minute)

	ok := eventually(t, 200*time.Millisecond, func() bool {
		return enqueuer.CountFor(key) >= 1
	})
	assert.True(t, ok, "plan should be enqueued after timer fires")
}

// ---------------------------------------------------------------------------
// PlanRequeueProcessor — all exception timestamps in the past → no boundary
// ---------------------------------------------------------------------------

// TestProcessor_AllExceptionTimestampsPast_NoBoundary verifies that when all
// exception ValidFrom/ValidUntil timestamps are already in the past,
// computeBoundary returns false and no timer/enqueue is created. This is
// distinct from the "no schedule, no exceptions" case: exceptions exist but
// none contribute a future boundary.
func TestProcessor_AllExceptionTimestampsPast_NoBoundary(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clk := clocktesting.NewFakeClock(now)
	p, resources, enqueuer := newProcessor(clk)
	cancel := startProcessor(t, p)
	defer cancel()

	key := types.NamespacedName{Name: "past-exc-plan", Namespace: "default"}
	// Both ValidFrom and ValidUntil are in the past → computeBoundary returns false.
	resources.PlanResources.Store(key, planCtxWithException(
		now.Add(-2*time.Hour), // past ValidFrom
		now.Add(-1*time.Hour), // past ValidUntil
	))
	time.Sleep(20 * time.Millisecond)

	// Advance the clock well past all timestamps — still no enqueue.
	clk.Step(1 * time.Hour)
	time.Sleep(50 * time.Millisecond)

	assert.Equal(t, 0, enqueuer.CountFor(key),
		"plan whose exception timestamps are all in the past should never be enqueued")
}

// ---------------------------------------------------------------------------
// PlanRequeueProcessor — plan delete cancels timer
// ---------------------------------------------------------------------------

func TestProcessor_PlanDeleted_TimerCancelledNoEnqueue(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clk := clocktesting.NewFakeClock(now)
	p, resources, enqueuer := newProcessor(clk)
	cancel := startProcessor(t, p)
	defer cancel()

	key := types.NamespacedName{Name: "delete-plan", Namespace: "default"}
	resources.PlanResources.Store(key, planCtxWithSchedule(now.Add(10*time.Minute)))
	time.Sleep(20 * time.Millisecond)

	// Delete the plan — timer entry should be removed.
	resources.PlanResources.Delete(key)
	time.Sleep(20 * time.Millisecond)

	// Advance past the original boundary.
	clk.Step(15 * time.Minute)
	time.Sleep(50 * time.Millisecond)

	assert.Equal(t, 0, enqueuer.CountFor(key), "deleted plan must not be enqueued after timer would have fired")
}

// ---------------------------------------------------------------------------
// PlanRequeueProcessor — timer replacement cancels previous goroutine
// ---------------------------------------------------------------------------

// TestProcessor_TimerReplaced_OnlyLatestBoundaryFires stores a plan first with a
// +10m boundary, then replaces it with a +5m boundary. It verifies:
//  1. The replacement timer (+5m) fires and enqueues the plan exactly once when
//     the clock advances past +5m but before +10m.
//  2. The cancelled original timer (+10m) does NOT fire a second time.
//
// Synchronization: clk.HasWaiters() is used to confirm the first timer is
// registered in the fake clock before issuing the replacement Store. A 100ms
// sleep after the replacement gives the handler ample time to cancel the +10m
// timer and arm the +5m timer, preventing the race where clk.Step advances
// the clock before the new timer is registered (which would cause the timer to
// be created at now+7m+5m=now+12m instead of now+5m).
func TestProcessor_TimerReplaced_OnlyLatestBoundaryFires(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clk := clocktesting.NewFakeClock(now)
	p, resources, enqueuer := newProcessor(clk)
	cancel := startProcessor(t, p)
	defer cancel()

	key := types.NamespacedName{Name: "replace-plan", Namespace: "default"}

	// First delivery: timer at +10m.
	resources.PlanResources.Store(key, planCtxWithSchedule(now.Add(10*time.Minute)))

	// Wait until the +10m timer is actually registered in the fake clock.
	// HasWaiters() becomes true once the handler calls Clock.NewTimer(). This
	// guarantees the handler has run before we issue the replacement Store.
	waitForWaiters := eventually(t, 500*time.Millisecond, func() bool {
		return clk.HasWaiters()
	})
	require.True(t, waitForWaiters, "first timer (+10m) must be registered before replacement")

	// Second delivery: replacement timer at +5m.
	// The handler will:
	// (a) cancel the +10m goroutine via timerCtx.Done(),
	// (b) stop the +10m fake-clock timer,
	// (c) sets a new +5m timer.
	resources.PlanResources.Store(key, planCtxWithSchedule(now.Add(5*time.Minute)))

	// Give the replacement handler enough time to complete the stop-and-rearm
	// sequence. Without this, clk.Step could run while the handler is mid-way
	// (old timer stopped but new timer not yet armed), causing the timer to be
	// created relative to the already-advanced fake clock time.
	time.Sleep(100 * time.Millisecond)

	// Advance to +7m — between the +5m and +10m marks.
	// The replacement +5m timer should fire; the stopped +10m timer must not.
	clk.Step(7 * time.Minute)
	ok := eventually(t, 500*time.Millisecond, func() bool {
		return enqueuer.CountFor(key) >= 1
	})
	require.True(t, ok, "replacement timer at +5m should fire and enqueue the plan")

	// Advance past +10m — the original timer was stopped so it must not fire again.
	clk.Step(5 * time.Minute) // total: +12m
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, enqueuer.CountFor(key), "plan should be enqueued exactly once; cancelled +10m timer must not fire")
}

// ---------------------------------------------------------------------------
// PlanRequeueProcessor — no boundary → no timer, no enqueue
// ---------------------------------------------------------------------------

func TestProcessor_NoBoundary_NeverEnqueues(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clk := clocktesting.NewFakeClock(now)
	p, resources, enqueuer := newProcessor(clk)
	cancel := startProcessor(t, p)
	defer cancel()

	key := types.NamespacedName{Name: "no-boundary", Namespace: "default"}
	// PlanContext with no schedule and no exceptions — no boundary computable.
	resources.PlanResources.Store(key, &message.PlanContext{
		Plan: &hibernatorv1alpha1.HibernatePlan{},
	})
	time.Sleep(20 * time.Millisecond)

	clk.Step(1 * time.Hour)
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 0, enqueuer.CountFor(key), "plan with no computable boundary should never be enqueued")
}

// ---------------------------------------------------------------------------
// PlanRequeueProcessor — exception ValidFrom boundary
// ---------------------------------------------------------------------------

func TestProcessor_ExceptionValidFrom_FiresWhenReached(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clk := clocktesting.NewFakeClock(now)
	p, resources, enqueuer := newProcessor(clk)
	cancel := startProcessor(t, p)
	defer cancel()

	key := types.NamespacedName{Name: "exc-from", Namespace: "default"}
	resources.PlanResources.Store(key, planCtxWithException(
		now.Add(3*time.Minute), // ValidFrom: +3m
		now.Add(1*time.Hour),   // ValidUntil: +1h (farther)
	))
	time.Sleep(20 * time.Millisecond)

	// Before ValidFrom — no enqueue.
	clk.Step(2 * time.Minute)
	time.Sleep(30 * time.Millisecond)
	assert.Equal(t, 0, enqueuer.CountFor(key), "should not enqueue before ValidFrom")

	// Advance past ValidFrom — timer should fire.
	clk.Step(2 * time.Minute)
	ok := eventually(t, 200*time.Millisecond, func() bool {
		return enqueuer.CountFor(key) >= 1
	})
	assert.True(t, ok, "plan should be enqueued when ValidFrom boundary is reached")
}

// ---------------------------------------------------------------------------
// PlanRequeueProcessor — shutdown cancels all goroutines
// ---------------------------------------------------------------------------

func TestProcessor_Shutdown_CancelsAllTimers(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clk := clocktesting.NewFakeClock(now)
	p, resources, enqueuer := newProcessor(clk)
	cancel := startProcessor(t, p)

	keys := []types.NamespacedName{
		{Name: "plan-a", Namespace: "default"},
		{Name: "plan-b", Namespace: "default"},
		{Name: "plan-c", Namespace: "default"},
	}
	for _, k := range keys {
		resources.PlanResources.Store(k, planCtxWithSchedule(now.Add(10*time.Minute)))
	}
	time.Sleep(20 * time.Millisecond)

	// Shut down the processor before timers fire.
	cancel()
	time.Sleep(30 * time.Millisecond)

	// Advance the clock — no timers should fire after shutdown.
	clk.Step(15 * time.Minute)
	time.Sleep(50 * time.Millisecond)

	for _, k := range keys {
		assert.Equal(t, 0, enqueuer.CountFor(k), "no plan should be enqueued after shutdown")
	}
}
