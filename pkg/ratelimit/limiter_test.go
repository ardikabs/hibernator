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
				RequestsPerSecond: 2.0,
			},
			expected: Config{
				RequestsPerSecond: 2.0,
				Burst:             10, // default
			},
		},
		{
			name: "full config no defaults needed",
			input: Config{
				RequestsPerSecond: 10.0,
				Burst:             20,
			},
			expected: Config{
				RequestsPerSecond: 10.0,
				Burst:             20,
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
			config:  Config{RequestsPerSecond: 1.0, Burst: 5},
			wantErr: false,
		},
		{
			name:    "zero values are valid (will use defaults)",
			config:  Config{},
			wantErr: false,
		},
		{
			name:    "negative rps is invalid",
			config:  Config{RequestsPerSecond: -1.0, Burst: 5},
			wantErr: true,
		},
		{
			name:    "negative burst is invalid",
			config:  Config{RequestsPerSecond: 1.0, Burst: -1},
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
		RequestsPerSecond: 100.0, // High rate for testing
		Burst:             10,
	}
	limiter := NewLimiter(cfg)

	// Should allow burst
	for i := 0; i < 10; i++ {
		assert.True(t, limiter.Allow(), "iteration %d", i)
	}

	// Should deny after burst is exhausted
	assert.False(t, limiter.Allow())
}

func TestLimiterWait(t *testing.T) {
	cfg := Config{
		RequestsPerSecond: 1000.0, // Very high rate for fast test
		Burst:             1,
	}
	limiter := NewLimiter(cfg)

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
		RequestsPerSecond: 0.1, // Very low rate: 1 request per 10 seconds
		Burst:             1,   // Minimal burst
	}
	limiter := NewLimiter(cfg)

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

	// Get limiter for sink1
	limiter1 := registry.Get("sink1")
	assert.NotNil(t, limiter1)

	// Get again should return same instance (same pointer)
	limiter1Again := registry.Get("sink1")
	assert.True(t, limiter1 == limiter1Again, "Should return same limiter instance for same sink")

	// Get for different sink should return different instance
	limiter2 := registry.Get("sink2")
	assert.NotNil(t, limiter2)
	assert.False(t, limiter1 == limiter2, "Should return different limiter instance for different sink")

	// Check count
	assert.Equal(t, 2, registry.Len())
}

func TestRegistryGetWithConfig(t *testing.T) {
	registry := NewRegistry()

	customCfg := Config{
		RequestsPerSecond: 5.0,
		Burst:             10,
	}

	// Get with custom config
	limiter1 := registry.GetWithConfig("sink1", customCfg)
	assert.NotNil(t, limiter1)
	assert.Equal(t, customCfg, limiter1.Config())

	// Get again with same config should return same instance
	limiter1Again := registry.GetWithConfig("sink1", customCfg)
	assert.Equal(t, limiter1, limiter1Again)

	// Get with different config should still return same instance (cached)
	// Note: Registry caches by sink name, not by config
	differentCfg := Config{
		RequestsPerSecond: 10.0,
		Burst:             20,
	}
	limiter1Different := registry.GetWithConfig("sink1", differentCfg)
	assert.Equal(t, limiter1, limiter1Different)
	// Config remains the first one used
	assert.Equal(t, customCfg, limiter1Different.Config())
}

func TestRegistryWait(t *testing.T) {
	registry := NewRegistry()

	ctx := context.Background()
	err := registry.Wait(ctx, "test-sink")
	assert.NoError(t, err)

	// Should have created the limiter
	assert.Equal(t, 1, registry.Len())
}
