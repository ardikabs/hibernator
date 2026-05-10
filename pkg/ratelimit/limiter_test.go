/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigWithDefaults(t *testing.T) {
	tests := []struct {
		name     string
		input    Config
		expected Config
	}{
		{
			name:     "zero config uses defaults",
			input:    Config{},
			expected: DefaultConfig(),
		},
		{
			name: "partial config fills in defaults",
			input: Config{
				Rate: 2.0,
			},
			expected: Config{
				Rate:  2.0,
				Unit:  time.Second,
				Burst: 10, // default
			},
		},
		{
			name: "full config no defaults needed",
			input: Config{
				Rate:  10.0,
				Unit:  time.Second,
				Burst: 20,
			},
			expected: Config{
				Rate:  10.0,
				Unit:  time.Second,
				Burst: 20,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.input.withDefaults()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name:    "valid config",
			config:  Config{Rate: 1.0, Unit: time.Second, Burst: 5},
			wantErr: false,
		},
		{
			name:    "zero values are valid (will use defaults)",
			config:  Config{},
			wantErr: false,
		},
		{
			name:    "negative rate is invalid",
			config:  Config{Rate: -1.0, Unit: time.Second, Burst: 5},
			wantErr: true,
		},
		{
			name:    "negative burst is invalid",
			config:  Config{Rate: 1.0, Unit: time.Second, Burst: -1},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestLimiterAllow(t *testing.T) {
	cfg := Config{
		Rate:  100.0, // High rate for testing
		Unit:  time.Second,
		Burst: 10,
	}
	limiter := New(cfg)

	// Should allow burst
	for i := 0; i < 10; i++ {
		assert.True(t, limiter.Allow(), "iteration %d", i)
	}

	// Should deny after burst is exhausted
	assert.False(t, limiter.Allow())
}

func TestLimiterWait(t *testing.T) {
	cfg := Config{
		Rate:  1000.0, // Very high rate for fast test
		Unit:  time.Second,
		Burst: 1,
	}
	limiter := New(cfg)

	// Exhaust the burst
	require.True(t, limiter.Allow())

	// Next Wait should block briefly then succeed
	ctx := context.Background()
	start := time.Now()
	err := limiter.Wait(ctx)
	duration := time.Since(start)

	assert.NoError(t, err)
	assert.Less(t, duration, 50*time.Millisecond, "Should not wait long with high rate")
}

func TestLimiterWaitContextCancelled(t *testing.T) {
	cfg := Config{
		Rate:  0.1, // Very low rate: 1 request per 10 seconds
		Unit:  time.Second,
		Burst: 1, // Minimal burst
	}
	limiter := New(cfg)

	// Exhaust the one token
	require.True(t, limiter.Allow())

	// Context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := limiter.Wait(ctx)
	duration := time.Since(start)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context")
	assert.Less(t, duration, 200*time.Millisecond, "Should return quickly after context cancellation")
}

func TestRegistryGet(t *testing.T) {
	registry := NewRegistry()

	// Get limiter for key1
	limiter1 := registry.Get("key1")
	assert.NotNil(t, limiter1)

	// Get again should return same instance (same pointer)
	limiter1Again := registry.Get("key1")
	assert.True(t, limiter1 == limiter1Again, "Should return same limiter instance for same key")

	// Get for different key should return different instance
	limiter2 := registry.Get("key2")
	assert.NotNil(t, limiter2)
	assert.False(t, limiter1 == limiter2, "Should return different limiter instance for different key")

	// Check count
	assert.Equal(t, 2, registry.Len())
}

func TestRegistryWait(t *testing.T) {
	registry := NewRegistry()

	ctx := context.Background()
	err := registry.Wait(ctx, "test-key")
	assert.NoError(t, err)

	// Should have created the limiter
	assert.Equal(t, 1, registry.Len())
}

func TestLimiterSingleBucket(t *testing.T) {
	// Single bucket: 10 RPS, burst 5.
	cfg := Config{
		Rate:  10.0,
		Unit:  time.Second,
		Burst: 5,
	}
	limiter := New(cfg)

	ctx := context.Background()

	// Exhaust burst (5 requests fast)
	for i := 0; i < 5; i++ {
		start := time.Now()
		err := limiter.Wait(ctx)
		duration := time.Since(start)
		assert.NoError(t, err)
		assert.Less(t, duration, 50*time.Millisecond, "Request %d should be fast (within burst)", i+1)
	}

	// Next request should wait ~100ms due to rate limiting
	start := time.Now()
	err := limiter.Wait(ctx)
	duration := time.Since(start)
	assert.NoError(t, err)
	assert.Greater(t, duration, 50*time.Millisecond, "Should be rate limited by bucket")
}

func TestLimiterSetOperation(t *testing.T) {
	// Global: 100 RPS, burst 10 (fast)
	// Operation: 1 RPS, burst 1 (slow)
	limiter := New(Config{Rate: 100.0, Unit: time.Second, Burst: 10})
	limiter.SetOperation("slow", Config{Rate: 1.0, Unit: time.Second, Burst: 1})

	ctx := context.Background()

	// First operation call is within burst
	start := time.Now()
	err := limiter.WaitOperation(ctx, "slow")
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 20*time.Millisecond)

	// Second operation call should wait ~1s because operation is bottleneck
	start = time.Now()
	err = limiter.WaitOperation(ctx, "slow")
	require.NoError(t, err)
	assert.Greater(t, time.Since(start), 500*time.Millisecond, "operation should be the bottleneck")
}

func TestLimiterSetOperationUpdateInPlace(t *testing.T) {
	limiter := New(Config{Rate: 100.0, Unit: time.Second, Burst: 10})
	limiter.SetOperation("op", Config{Rate: 1.0, Unit: time.Second, Burst: 1})

	// Exhaust operation burst
	ctx := context.Background()
	require.NoError(t, limiter.WaitOperation(ctx, "op"))

	// Update operation to be very fast
	limiter.SetOperation("op", Config{Rate: 1000.0, Unit: time.Second, Burst: 10})

	// Next call should be fast again
	start := time.Now()
	err := limiter.WaitOperation(ctx, "op")
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 50*time.Millisecond, "updated operation should be fast")
}

func TestLimiterWaitOperationUnknownOp(t *testing.T) {
	// Global: 100 RPS, burst 10
	limiter := New(Config{Rate: 100.0, Unit: time.Second, Burst: 10})

	ctx := context.Background()
	// Calling unknown operation should only wait global
	start := time.Now()
	err := limiter.WaitOperation(ctx, "unknown")
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 20*time.Millisecond)
}

func TestLimiterSetConfigInPlace(t *testing.T) {
	cfg1 := Config{Rate: 1.0, Unit: time.Second, Burst: 1}
	limiter := New(cfg1)

	// Exhaust burst
	ctx := context.Background()
	require.NoError(t, limiter.Wait(ctx))

	// Update to fast config in-place
	cfg2 := Config{Rate: 1000.0, Unit: time.Second, Burst: 10}
	limiter.SetConfig(cfg2)

	// Next call should be fast
	start := time.Now()
	err := limiter.Wait(ctx)
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 50*time.Millisecond, "updated config should be fast")
}

func TestLimiterParentSharedAcrossOperations(t *testing.T) {
	// Global: 2 RPS, burst 2
	// Op A: 10 RPS, burst 10
	// Op B: 10 RPS, burst 10
	// Expectation: global caps aggregate throughput.
	limiter := New(Config{Rate: 2.0, Unit: time.Second, Burst: 2})
	limiter.SetOperation("A", Config{Rate: 10.0, Unit: time.Second, Burst: 10})
	limiter.SetOperation("B", Config{Rate: 10.0, Unit: time.Second, Burst: 10})

	ctx := context.Background()

	// Exhaust global burst of 2
	require.NoError(t, limiter.WaitOperation(ctx, "A"))
	require.NoError(t, limiter.WaitOperation(ctx, "B"))

	// Third request (from A) should wait because global is exhausted
	start := time.Now()
	err := limiter.WaitOperation(ctx, "A")
	duration := time.Since(start)

	require.NoError(t, err)
	assert.Greater(t, duration, 200*time.Millisecond, "global should be shared and exhausted")
}

func TestLimiterOperationMetrics(t *testing.T) {
	// Global: 1 RPS, burst 1 — guaranteed wait after first call
	limiter := New(Config{Rate: 1.0, Unit: time.Second, Burst: 1})
	limiter.SetOperation("op", Config{Rate: 100.0, Unit: time.Second, Burst: 10})

	ctx := context.Background()

	// First call fast (within burst)
	require.NoError(t, limiter.WaitOperationWithMetrics(ctx, "test#op", "op"))

	// Second call should wait and record metrics
	start := time.Now()
	err := limiter.WaitOperationWithMetrics(ctx, "test#op", "op")
	duration := time.Since(start)

	require.NoError(t, err)
	assert.Greater(t, duration, 100*time.Millisecond, "should have waited and recorded metrics")
}
