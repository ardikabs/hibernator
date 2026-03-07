/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package message

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/telepresenceio/watchable"
)

// --- coalesceUpdates tests ---

func TestCoalesceUpdates_Empty(t *testing.T) {
	result := coalesceUpdates[string, string](nil)
	assert.Nil(t, result)
}

func TestCoalesceUpdates_Single(t *testing.T) {
	updates := []watchable.Update[string, string]{
		{Key: "a", Value: "v1"},
	}
	result := coalesceUpdates(updates)
	assert.Len(t, result, 1)
	assert.Equal(t, "a", result[0].Key)
	assert.Equal(t, "v1", result[0].Value)
}

func TestCoalesceUpdates_DuplicatesKeepLast(t *testing.T) {
	updates := []watchable.Update[string, string]{
		{Key: "a", Value: "v1"},
		{Key: "a", Value: "v2"},
		{Key: "a", Value: "v3"},
	}
	result := coalesceUpdates(updates)
	assert.Len(t, result, 1)
	assert.Equal(t, "a", result[0].Key)
	assert.Equal(t, "v3", result[0].Value, "should keep the last update for key 'a'")
}

func TestCoalesceUpdates_MultipleKeys(t *testing.T) {
	updates := []watchable.Update[string, string]{
		{Key: "a", Value: "a1"},
		{Key: "b", Value: "b1"},
		{Key: "a", Value: "a2"},
		{Key: "c", Value: "c1"},
		{Key: "b", Value: "b2"},
	}
	result := coalesceUpdates(updates)
	require.Len(t, result, 3)

	// Algorithm: iterate backwards finding last update per key, then reverse.
	// Last updates: a→a2, b→b2, c→c1
	// Backwards pass order: b2, c1, a2
	// After reverse: a2, c1, b2
	assert.Equal(t, "a", result[0].Key)
	assert.Equal(t, "a2", result[0].Value)
	assert.Equal(t, "c", result[1].Key)
	assert.Equal(t, "c1", result[1].Value)
	assert.Equal(t, "b", result[2].Key)
	assert.Equal(t, "b2", result[2].Value)
}

func TestCoalesceUpdates_DeleteEvent(t *testing.T) {
	updates := []watchable.Update[string, string]{
		{Key: "a", Value: "v1"},
		{Key: "a", Delete: true},
	}
	result := coalesceUpdates(updates)
	assert.Len(t, result, 1)
	assert.Equal(t, "a", result[0].Key)
	assert.True(t, result[0].Delete, "should keep the last event which is a delete")
}

// --- HandleSubscription tests ---

func TestHandleSubscription_BootstrapProcessing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var m watchable.Map[string, string]
	m.Store("key1", "value1")
	m.Store("key2", "value2")

	sub := m.Subscribe(ctx)

	var mu sync.Mutex
	seen := make(map[string]string)

	go HandleSubscription(ctx, logr.Discard(), Metadata{
		Runner:  "test-runner",
		Message: "test-msg",
	}, sub, func(update watchable.Update[string, string], errChan chan error) {
		mu.Lock()
		defer mu.Unlock()
		seen[update.Key] = update.Value
	})

	// Wait for bootstrap to process
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) == 2
	}, time.Second, 10*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "value1", seen["key1"])
	assert.Equal(t, "value2", seen["key2"])
}

func TestHandleSubscription_SubsequentUpdates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var m watchable.Map[string, string]
	sub := m.Subscribe(ctx)

	var mu sync.Mutex
	seen := make(map[string]string)

	go HandleSubscription(ctx, logr.Discard(), Metadata{
		Runner:  "test-runner",
		Message: "test-msg",
	}, sub, func(update watchable.Update[string, string], errChan chan error) {
		mu.Lock()
		defer mu.Unlock()
		seen[update.Key] = update.Value
	})

	// Store after subscribe
	m.Store("keyA", "valA")

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return seen["keyA"] == "valA"
	}, time.Second, 10*time.Millisecond)
}

func TestHandleSubscription_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var m watchable.Map[string, string]
	sub := m.Subscribe(ctx)

	done := make(chan struct{})
	go func() {
		HandleSubscription(ctx, logr.Discard(), Metadata{
			Runner:  "test-runner",
			Message: "test-msg",
		}, sub, func(update watchable.Update[string, string], errChan chan error) {})
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// HandleSubscription returned — success
	case <-time.After(2 * time.Second):
		t.Fatal("HandleSubscription did not return after context cancellation")
	}
}

func TestHandleSubscription_PanicRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var m watchable.Map[string, string]
	sub := m.Subscribe(ctx)

	callCount := 0
	var mu sync.Mutex

	go HandleSubscription(ctx, logr.Discard(), Metadata{
		Runner:  "test-runner",
		Message: "test-msg",
	}, sub, func(update watchable.Update[string, string], errChan chan error) {
		mu.Lock()
		callCount++
		count := callCount
		mu.Unlock()
		if count == 1 {
			panic("intentional panic for testing")
		}
	})

	// First store triggers handler → panic → recover
	m.Store("key1", "v1")
	time.Sleep(100 * time.Millisecond)

	// Second store should still be processed (subscription continues after panic)
	m.Store("key2", "v2")

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return callCount >= 2
	}, time.Second, 10*time.Millisecond, "handler should be called again after panic recovery")
}

func TestHandleSubscription_ErrChanRouting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var m watchable.Map[string, string]
	sub := m.Subscribe(ctx)

	go HandleSubscription(ctx, logr.Discard(), Metadata{
		Runner:  "test-runner",
		Message: "test-msg",
	}, sub, func(update watchable.Update[string, string], errChan chan error) {
		errChan <- assert.AnError
	})

	// Trigger handler; the error is consumed by the internal goroutine in HandleSubscription.
	// We verify no deadlock or panic by ensuring the test completes.
	m.Store("err-key", "err-val")
	time.Sleep(200 * time.Millisecond)
}

func TestHandleSubscription_DeleteEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var m watchable.Map[string, string]
	m.Store("toDelete", "val")
	sub := m.Subscribe(ctx)

	var mu sync.Mutex
	var deleteSeen bool

	go HandleSubscription(ctx, logr.Discard(), Metadata{
		Runner:  "test-runner",
		Message: "test-msg",
	}, sub, func(update watchable.Update[string, string], errChan chan error) {
		mu.Lock()
		defer mu.Unlock()
		if update.Delete && update.Key == "toDelete" {
			deleteSeen = true
		}
	})

	// Bootstrap processes the existing key first; then delete after subscribe
	time.Sleep(50 * time.Millisecond)
	m.Delete("toDelete")

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return deleteSeen
	}, time.Second, 10*time.Millisecond)
}
