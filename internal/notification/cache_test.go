/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/types"
)

func TestSinkStateCache_GetAndSet(t *testing.T) {
	log := logr.Discard()
	cache := NewSinkStateCache(log, 100*time.Millisecond)

	notifRef := types.NamespacedName{Namespace: "default", Name: "test-notif"}
	sinkName := "slack"
	planNamespace := "default"
	planName := "test-plan"
	cycleID := "cycle-123"
	operation := "Hibernate"

	states := map[string]string{
		"slack.thread.root_ts":       "1234.5678",
		"slack.thread.last_reaction": "loading",
	}

	// Test cache miss
	result, ok := cache.Get(notifRef, sinkName, planNamespace, planName, cycleID, operation)
	assert.False(t, ok)
	assert.Nil(t, result)

	// Set cache
	cache.Set(notifRef, sinkName, planNamespace, planName, cycleID, operation, states)

	// Test cache hit
	result, ok = cache.Get(notifRef, sinkName, planNamespace, planName, cycleID, operation)
	assert.True(t, ok)
	assert.Equal(t, states, result)
}

func TestSinkStateCache_TouchOnRead(t *testing.T) {
	log := logr.Discard()
	ttl := 100 * time.Millisecond
	cache := NewSinkStateCache(log, ttl)

	notifRef := types.NamespacedName{Namespace: "default", Name: "test-notif"}
	sinkName := "slack"
	planNamespace := "default"
	planName := "test-plan"
	cycleID := "cycle-123"
	operation := "Hibernate"

	states := map[string]string{"key": "value"}

	// Set cache
	cache.Set(notifRef, sinkName, planNamespace, planName, cycleID, operation, states)

	// Read cache multiple times to extend TTL
	for i := 0; i < 5; i++ {
		time.Sleep(50 * time.Millisecond)
		result, ok := cache.Get(notifRef, sinkName, planNamespace, planName, cycleID, operation)
		assert.True(t, ok, "cache should still be valid at iteration %d", i)
		assert.Equal(t, states, result)
	}

	// Wait longer than TTL without reading
	time.Sleep(150 * time.Millisecond)

	// Cache should be expired now
	result, ok := cache.Get(notifRef, sinkName, planNamespace, planName, cycleID, operation)
	assert.False(t, ok)
	assert.Nil(t, result)
}

func TestSinkStateCache_Expiration(t *testing.T) {
	log := logr.Discard()
	ttl := 50 * time.Millisecond
	cache := NewSinkStateCache(log, ttl)

	notifRef := types.NamespacedName{Namespace: "default", Name: "test-notif"}

	// Set multiple entries
	cache.Set(notifRef, "sink1", "ns", "plan", "cycle", "op", map[string]string{"k": "v1"})
	cache.Set(notifRef, "sink2", "ns", "plan", "cycle", "op", map[string]string{"k": "v2"})

	assert.Equal(t, 2, cache.Len())

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Both should be expired
	_, ok1 := cache.Get(notifRef, "sink1", "ns", "plan", "cycle", "op")
	_, ok2 := cache.Get(notifRef, "sink2", "ns", "plan", "cycle", "op")
	assert.False(t, ok1)
	assert.False(t, ok2)
	assert.Equal(t, 0, cache.Len())
}

func TestSinkStateCache_DeepCopy(t *testing.T) {
	log := logr.Discard()
	cache := NewSinkStateCache(log, time.Minute)

	notifRef := types.NamespacedName{Namespace: "default", Name: "test-notif"}
	states := map[string]string{"key": "original"}

	cache.Set(notifRef, "slack", "ns", "plan", "cycle", "op", states)

	// Modify original map
	states["key"] = "modified"

	// Retrieve from cache - should be the original value
	result, ok := cache.Get(notifRef, "slack", "ns", "plan", "cycle", "op")
	assert.True(t, ok)
	assert.Equal(t, "original", result["key"])
}

func TestSinkStateCache_DifferentKeys(t *testing.T) {
	log := logr.Discard()
	cache := NewSinkStateCache(log, time.Minute)

	notifRef := types.NamespacedName{Namespace: "default", Name: "test-notif"}

	// Different plans should have separate cache entries
	cache.Set(notifRef, "slack", "ns", "plan1", "cycle1", "op", map[string]string{"k": "v1"})
	cache.Set(notifRef, "slack", "ns", "plan2", "cycle1", "op", map[string]string{"k": "v2"})
	cache.Set(notifRef, "slack", "ns", "plan1", "cycle2", "op", map[string]string{"k": "v3"})
	cache.Set(notifRef, "telegram", "ns", "plan1", "cycle1", "op", map[string]string{"k": "v4"})

	assert.Equal(t, 4, cache.Len())

	// Verify each key returns correct value
	result1, _ := cache.Get(notifRef, "slack", "ns", "plan1", "cycle1", "op")
	assert.Equal(t, "v1", result1["k"])

	result2, _ := cache.Get(notifRef, "slack", "ns", "plan2", "cycle1", "op")
	assert.Equal(t, "v2", result2["k"])

	result3, _ := cache.Get(notifRef, "slack", "ns", "plan1", "cycle2", "op")
	assert.Equal(t, "v3", result3["k"])

	result4, _ := cache.Get(notifRef, "telegram", "ns", "plan1", "cycle1", "op")
	assert.Equal(t, "v4", result4["k"])
}

func TestSinkStateCache_Delete(t *testing.T) {
	log := logr.Discard()
	cache := NewSinkStateCache(log, time.Minute)

	notifRef := types.NamespacedName{Namespace: "default", Name: "test-notif"}

	cache.Set(notifRef, "slack", "ns", "plan", "cycle", "op", map[string]string{"k": "v"})
	assert.Equal(t, 1, cache.Len())

	cache.Delete(notifRef, "slack", "ns", "plan", "cycle", "op")
	assert.Equal(t, 0, cache.Len())

	_, ok := cache.Get(notifRef, "slack", "ns", "plan", "cycle", "op")
	assert.False(t, ok)
}

func TestSinkStateCache_Clear(t *testing.T) {
	log := logr.Discard()
	cache := NewSinkStateCache(log, time.Minute)

	notifRef := types.NamespacedName{Namespace: "default", Name: "test-notif"}

	cache.Set(notifRef, "sink1", "ns", "plan", "cycle", "op", map[string]string{"k": "v1"})
	cache.Set(notifRef, "sink2", "ns", "plan", "cycle", "op", map[string]string{"k": "v2"})
	cache.Set(notifRef, "sink3", "ns", "plan", "cycle", "op", map[string]string{"k": "v3"})

	assert.Equal(t, 3, cache.Len())

	cache.Clear()
	assert.Equal(t, 0, cache.Len())
}

func TestStateCacheEntry_IsExpired(t *testing.T) {
	now := time.Now()

	entry := &stateCacheEntry{
		states:     map[string]string{"k": "v"},
		lastAccess: now,
		ttl:        100 * time.Millisecond,
	}

	assert.False(t, entry.isExpired(now))
	assert.False(t, entry.isExpired(now.Add(50*time.Millisecond)))
	assert.True(t, entry.isExpired(now.Add(150*time.Millisecond)))
}

func TestStateCacheEntry_Touch(t *testing.T) {
	entry := &stateCacheEntry{
		states:     map[string]string{"k": "v"},
		lastAccess: time.Now(),
		ttl:        time.Minute,
	}

	oldAccess := entry.lastAccess
	time.Sleep(10 * time.Millisecond)

	newNow := time.Now()
	entry.touch(newNow)

	assert.True(t, entry.lastAccess.After(oldAccess))
	assert.Equal(t, newNow, entry.lastAccess)
}

func TestSinkStateCache_GetOrFetch_Atomic(t *testing.T) {
	log := logr.Discard()
	cache := NewSinkStateCache(log, time.Minute)

	notifRef := types.NamespacedName{Namespace: "default", Name: "test-notif"}
	sinkName := "slack"
	planNamespace := "default"
	planName := "test-plan"
	cycleID := "cycle-123"
	operation := "Hibernate"

	expectedStates := map[string]string{
		"slack.thread.root_ts":       "1234.5678",
		"slack.thread.last_reaction": "loading",
	}

	// Simulate a slow API call
	fetchCount := 0
	fetchFunc := func(context.Context) (map[string]string, error) {
		fetchCount++
		time.Sleep(50 * time.Millisecond) // Simulate network delay
		return expectedStates, nil
	}

	// First call should fetch from API
	result1, err1 := cache.GetOrFetch(context.Background(), notifRef, sinkName, planNamespace, planName, cycleID, operation, fetchFunc)
	assert.NoError(t, err1)
	assert.Equal(t, expectedStates, result1)
	assert.Equal(t, 1, fetchCount, "First call should trigger fetch")

	// Subsequent calls should use cache (no additional fetches)
	for i := 0; i < 5; i++ {
		result, err := cache.GetOrFetch(context.Background(), notifRef, sinkName, planNamespace, planName, cycleID, operation, fetchFunc)
		assert.NoError(t, err)
		assert.Equal(t, expectedStates, result)
	}
	assert.Equal(t, 1, fetchCount, "Subsequent calls should not trigger additional fetches")
}

func TestSinkStateCache_GetOrFetch_Concurrent(t *testing.T) {
	log := logr.Discard()
	cache := NewSinkStateCache(log, time.Minute)

	notifRef := types.NamespacedName{Namespace: "default", Name: "test-notif"}
	sinkName := "slack"
	planNamespace := "default"
	planName := "test-plan"
	cycleID := "cycle-123"
	operation := "Hibernate"

	expectedStates := map[string]string{
		"slack.thread.root_ts": "1234.5678",
	}

	var fetchCount int64
	fetchFunc := func(context.Context) (map[string]string, error) {
		atomic.AddInt64(&fetchCount, 1)
		time.Sleep(100 * time.Millisecond) // Simulate slow API call
		return expectedStates, nil
	}

	// Launch multiple concurrent goroutines trying to fetch the same key
	var wg sync.WaitGroup
	results := make([]map[string]string, 10)
	errors := make([]error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			result, err := cache.GetOrFetch(context.Background(), notifRef, sinkName, planNamespace, planName, cycleID, operation, fetchFunc)
			results[idx] = result
			errors[idx] = err
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Verify only one fetch occurred (atomic behavior)
	assert.Equal(t, int64(1), atomic.LoadInt64(&fetchCount), "Only one fetch should occur even with 10 concurrent requests")

	// All goroutines should get the same result
	for i := 0; i < 10; i++ {
		assert.NoError(t, errors[i])
		assert.Equal(t, expectedStates, results[i])
	}
}

func TestSinkStateCache_GetOrFetch_Error(t *testing.T) {
	log := logr.Discard()
	cache := NewSinkStateCache(log, time.Minute)

	notifRef := types.NamespacedName{Namespace: "default", Name: "test-notif"}
	fetchFunc := func(context.Context) (map[string]string, error) {
		return nil, fmt.Errorf("API error")
	}

	result, err := cache.GetOrFetch(context.Background(), notifRef, "slack", "ns", "plan", "cycle", "op", fetchFunc)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestSinkStateCache_GetOrFetch_EmptyResult(t *testing.T) {
	log := logr.Discard()
	cache := NewSinkStateCache(log, time.Minute)

	notifRef := types.NamespacedName{Namespace: "default", Name: "test-notif"}

	// First call returns empty
	fetchCount := 0
	fetchFunc := func(context.Context) (map[string]string, error) {
		fetchCount++
		return nil, nil
	}

	result1, err1 := cache.GetOrFetch(context.Background(), notifRef, "slack", "ns", "plan", "cycle", "op", fetchFunc)
	assert.NoError(t, err1)
	assert.Nil(t, result1)
	assert.Equal(t, 1, fetchCount)

	// Second call should still fetch (empty result is not cached)
	result2, err2 := cache.GetOrFetch(context.Background(), notifRef, "slack", "ns", "plan", "cycle", "op", fetchFunc)
	assert.NoError(t, err2)
	assert.Nil(t, result2)
	assert.Equal(t, 2, fetchCount, "Empty result should not be cached, so second call should fetch again")
}
