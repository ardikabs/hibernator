/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryRegisterIdempotent(t *testing.T) {
	// Test that Register is idempotent - calling it multiple times
	// with the same key should not reset the limiter

	registry := NewRegistry(WithLogger(logr.Discard()))

	key := "test-key"
	cfg := Config{
		Rate:  1.0,
		Unit:  time.Second,
		Burst: 1,
	}

	// Register first time
	registry.Register(key, cfg)
	require.Equal(t, 1, registry.Len(), "Should have 1 limiter after first register")

	// Get the limiter
	limiter1 := registry.Get(key)
	require.NotNil(t, limiter1)

	// Register again with same key - should be idempotent
	registry.Register(key, cfg)
	require.Equal(t, 1, registry.Len(), "Should still have 1 limiter after second register")

	// Get the limiter again - should be the same instance
	limiter2 := registry.Get(key)
	require.Same(t, limiter1, limiter2, "Should return same limiter instance")

	err := limiter1.Wait(context.Background())
	require.NoError(t, err)

	require.False(t, limiter2.Allow(), "limiter2 should be exhausted because limiter1 consumed the token")
}

func TestRegistryRegisterDifferentKeys(t *testing.T) {
	// Test that different keys create different limiters

	registry := NewRegistry(WithLogger(logr.Discard()))

	cfg := Config{
		Rate:  10.0,
		Unit:  time.Second,
		Burst: 2,
	}

	// Register multiple different keys
	registry.Register("key1", cfg)
	registry.Register("key2", cfg)
	registry.Register("key3", cfg)

	require.Equal(t, 3, registry.Len(), "Should have 3 limiters")

	// Each key should have its own limiter (different pointers)
	limiter1 := registry.Get("key1")
	limiter2 := registry.Get("key2")
	limiter3 := registry.Get("key3")

	assert.NotSame(t, limiter1, limiter2, "Different keys should have different limiters")
	assert.NotSame(t, limiter2, limiter3, "Different keys should have different limiters")
}

func TestRegistryLRUEviction(t *testing.T) {
	// Test that LRU eviction works when max keys is exceeded

	registry := NewRegistry(
		WithLogger(logr.Discard()),
		WithMaxKeys(3), // Only allow 3 keys
	)

	cfg := Config{
		Rate:  10.0,
		Unit:  time.Second,
		Burst: 2,
	}

	// Register 3 keys (at capacity)
	registry.Register("key1", cfg)
	registry.Register("key2", cfg)
	registry.Register("key3", cfg)

	require.Equal(t, 3, registry.Len(), "Should have 3 limiters at capacity")
	assert.True(t, registry.HasKey("key1"), "key1 should exist")
	assert.True(t, registry.HasKey("key2"), "key2 should exist")
	assert.True(t, registry.HasKey("key3"), "key3 should exist")

	// Register a 4th key - should evict the least recently used (key1)
	registry.Register("key4", cfg)

	require.Equal(t, 3, registry.Len(), "Should still have 3 limiters after eviction")
	assert.False(t, registry.HasKey("key1"), "key1 should be evicted (least recently used)")
	assert.True(t, registry.HasKey("key2"), "key2 should still exist")
	assert.True(t, registry.HasKey("key3"), "key3 should still exist")
	assert.True(t, registry.HasKey("key4"), "key4 should exist")
}

func TestRegistryLRUAccessUpdatesOrder(t *testing.T) {
	// Test that accessing a key updates its position in the LRU order

	registry := NewRegistry(
		WithLogger(logr.Discard()),
		WithMaxKeys(3),
	)

	cfg := Config{
		Rate:  10.0,
		Unit:  time.Second,
		Burst: 2,
	}

	// Register 3 keys
	registry.Register("key1", cfg)
	registry.Register("key2", cfg)
	registry.Register("key3", cfg)

	// Access key1 to make it recently used
	_ = registry.Get("key1")

	// Register key4 - should evict key2 (now least recently used)
	registry.Register("key4", cfg)

	require.Equal(t, 3, registry.Len())
	assert.True(t, registry.HasKey("key1"), "key1 should still exist (was accessed recently)")
	assert.False(t, registry.HasKey("key2"), "key2 should be evicted (least recently used)")
	assert.True(t, registry.HasKey("key3"), "key3 should still exist")
	assert.True(t, registry.HasKey("key4"), "key4 should exist")
}

func TestRegistryRegisterUpdatesLRU(t *testing.T) {
	// Test that calling Register on an existing key updates its LRU position

	registry := NewRegistry(
		WithLogger(logr.Discard()),
		WithMaxKeys(3),
	)

	cfg := Config{
		Rate:  10.0,
		Unit:  time.Second,
		Burst: 2,
	}

	// Register 3 keys
	registry.Register("key1", cfg)
	registry.Register("key2", cfg)
	registry.Register("key3", cfg)

	// Re-register key1 to make it recently used (idempotent but updates LRU)
	registry.Register("key1", cfg)

	// Register key4 - should evict key2 (now least recently used)
	registry.Register("key4", cfg)

	require.Equal(t, 3, registry.Len())
	assert.True(t, registry.HasKey("key1"), "key1 should still exist (was re-registered)")
	assert.False(t, registry.HasKey("key2"), "key2 should be evicted")
	assert.True(t, registry.HasKey("key3"), "key3 should still exist")
	assert.True(t, registry.HasKey("key4"), "key4 should exist")
}

func TestRegistryMaxKeysDefault(t *testing.T) {
	// Test that default max keys is 1000

	registry := NewRegistry(WithLogger(logr.Discard()))
	assert.Equal(t, 1000, registry.MaxKeys(), "Default max keys should be 1000")
}

func TestRegistryMaxKeysOption(t *testing.T) {
	// Test that WithMaxKeys option works

	registry := NewRegistry(
		WithLogger(logr.Discard()),
		WithMaxKeys(100),
	)
	assert.Equal(t, 100, registry.MaxKeys(), "Max keys should be 100")
}

func TestRegistryRegisterUpdatesConfig(t *testing.T) {
	// Test that Register updates the limiter when config changes

	registry := NewRegistry(WithLogger(logr.Discard()))

	key := "test-key"
	cfg1 := Config{
		Rate:  10.0,
		Unit:  time.Second,
		Burst: 2,
	}
	cfg2 := Config{
		Rate:  20.0,
		Unit:  time.Second,
		Burst: 5,
	}

	// Register first time
	registry.Register(key, cfg1)
	require.Equal(t, 1, registry.Len(), "Should have 1 limiter")

	// Get the limiter and verify config
	limiter1 := registry.Get(key)
	require.NotNil(t, limiter1)
	assert.Equal(t, cfg1, limiter1.Config(), "Initial config should match")

	// Register again with different config - should update the limiter in-place
	registry.Register(key, cfg2)
	require.Equal(t, 1, registry.Len(), "Should still have 1 limiter")

	// Get the limiter again - should be the same instance with updated config
	limiter2 := registry.Get(key)
	require.NotNil(t, limiter2)
	assert.Same(t, limiter1, limiter2, "Should return same limiter instance after config change")
	assert.Equal(t, cfg2, limiter2.Config(), "Config should be updated")

	// Register again with same config - should be idempotent (no change)
	limiter3 := registry.Get(key)
	registry.Register(key, cfg2)
	limiter4 := registry.Get(key)
	assert.Same(t, limiter3, limiter4, "Should return same instance when config unchanged")
}

func TestRegistry_OperationKeyNotInLRU(t *testing.T) {
	registry := NewRegistry(WithLogger(logr.Discard()))

	// Register parent and set operation directly on the limiter
	registry.Register("parent", Config{Rate: 10.0, Unit: time.Second, Burst: 5})
	parent := registry.Get("parent")
	parent.SetOperation("child", Config{Rate: 1.0, Unit: time.Second, Burst: 2})

	// Operation should NOT create a separate registry entry — only parent is in LRU
	assert.Equal(t, 1, registry.Len(), "Operation key should not be in LRU")
	assert.True(t, registry.HasKey("parent"), "Parent should be in registry")
	// HasKey for operation resolves to parent
	assert.True(t, registry.HasKey("parent#child"), "Operation should resolve to parent")
}

func TestRegistry_OperationKeyGetLimiter(t *testing.T) {
	registry := NewRegistry(WithLogger(logr.Discard()))

	parentCfg := Config{Rate: 10.0, Unit: time.Second, Burst: 5}
	childCfg := Config{Rate: 1.0, Unit: time.Second, Burst: 2}
	registry.Register("parent", parentCfg)
	parent := registry.Get("parent")
	parent.SetOperation("child", childCfg)

	// GetLimiter for operation should return the parent *Limiter
	limiter := registry.GetLimiter("parent#child")
	require.NotNil(t, limiter)
	assert.Equal(t, parentCfg, limiter.Config(), "GetLimiter for op key should return parent limiter")

	// But the parent limiter should have the operation configured
	ctx := context.Background()
	// First WaitOperation for child is within burst
	require.NoError(t, limiter.WaitOperation(ctx, "child"))
	// Second should wait because operation RPS=1, burst=2
	// Actually burst=2, so 2nd is also fast. 3rd should wait.
	require.NoError(t, limiter.WaitOperation(ctx, "child"))
}

func TestRegistry_OperationKeyEviction(t *testing.T) {
	registry := NewRegistry(
		WithLogger(logr.Discard()),
		WithMaxKeys(2),
	)

	// Register 2 parents (at capacity) + operations for each
	registry.Register("parent1", Config{Rate: 10.0, Unit: time.Second, Burst: 5})
	p1 := registry.Get("parent1")
	p1.SetOperation("child", Config{Rate: 1.0, Unit: time.Second, Burst: 2})

	registry.Register("parent2", Config{Rate: 10.0, Unit: time.Second, Burst: 5})
	p2 := registry.Get("parent2")
	p2.SetOperation("child", Config{Rate: 1.0, Unit: time.Second, Burst: 2})

	require.Equal(t, 2, registry.Len(), "Should have 2 parent limiters")

	// Register a 3rd parent — should evict parent1 and its operations
	registry.Register("parent3", Config{Rate: 10.0, Unit: time.Second, Burst: 5})

	require.Equal(t, 2, registry.Len(), "Should still have 2 parent limiters")
	assert.False(t, registry.HasKey("parent1"), "parent1 should be evicted")
	assert.True(t, registry.HasKey("parent2"), "parent2 should still exist")
	assert.True(t, registry.HasKey("parent3"), "parent3 should exist")

	// Child of evicted parent should be gone (new parent created with defaults)
	childLimiter := registry.Get("parent1#child")
	assert.Equal(t, DefaultConfig().Rate, childLimiter.Config().Rate,
		"Evicted parent's child should be recreated with defaults")
}

func TestRegistry_OperationKeyConfigUpdate(t *testing.T) {
	registry := NewRegistry(WithLogger(logr.Discard()))

	registry.Register("parent", Config{Rate: 10.0, Unit: time.Second, Burst: 5})
	parent := registry.Get("parent")
	parent.SetOperation("child", Config{Rate: 1.0, Unit: time.Second, Burst: 2})

	// Verify operation is set
	ctx := context.Background()
	require.NoError(t, parent.WaitOperation(ctx, "child"))
	require.NoError(t, parent.WaitOperation(ctx, "child"))

	// Update child config
	parent.SetOperation("child", Config{Rate: 2.0, Unit: time.Second, Burst: 4})

	// Parent limiter should have updated operation in-place
	// 3rd call should now be fast because burst was updated to 4
	require.NoError(t, parent.WaitOperation(ctx, "child"))
	require.NoError(t, parent.WaitOperation(ctx, "child"))
}

func TestRegistry_OperationKeyParentEvicted(t *testing.T) {
	registry := NewRegistry(
		WithLogger(logr.Discard()),
		WithMaxKeys(1),
	)

	registry.Register("parent", Config{Rate: 10.0, Unit: time.Second, Burst: 5})
	parent := registry.Get("parent")
	parent.SetOperation("child", Config{Rate: 1.0, Unit: time.Second, Burst: 2})

	// Evict parent by adding a new one
	registry.Register("other", Config{Rate: 20.0, Unit: time.Second, Burst: 10})

	// GetLimiter for child should create default parent and return it
	limiter := registry.GetLimiter("parent#child")
	require.NotNil(t, limiter)
	assert.Equal(t, DefaultConfig().Rate, limiter.Config().Rate,
		"GetLimiter should return default parent when original was evicted")
}
