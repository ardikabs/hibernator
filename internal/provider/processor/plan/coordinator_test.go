/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package plan

import (
	"context"
	"testing"
	"time"

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
// Coordinator helpers
// ---------------------------------------------------------------------------

func newTestCoordinator(clk clock.Clock) *Coordinator {
	return &Coordinator{
		Log:      logr.Discard(),
		Clock:    clk,
		Statuses: newTestStatuses(),
	}
}

// ---------------------------------------------------------------------------
// workerFactory — unit-level tests
// ---------------------------------------------------------------------------

// TestCoordinator_WorkerFactory_ReturnsNonNilRunFn verifies that workerFactory
// returns a non-nil goroutine body for any key/slot combination.
func TestCoordinator_WorkerFactory_ReturnsNonNilRunFn(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Now())
	c := newTestCoordinator(fakeClock)
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
	fakeClock := clocktesting.NewFakeClock(time.Now())
	c := newTestCoordinator(fakeClock)
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
	fakeClock := clocktesting.NewFakeClock(time.Now())
	c := newTestCoordinator(fakeClock)
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
	fakeClock := clocktesting.NewFakeClock(time.Now())
	c := newTestCoordinator(fakeClock)
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
// Coordinator.NeedLeaderElection
// ---------------------------------------------------------------------------

func TestCoordinator_NeedLeaderElection_ReturnsTrue(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Now())
	c := newTestCoordinator(fakeClock)
	assert.True(t, c.NeedLeaderElection())
}
