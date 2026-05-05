/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package ratelimit provides rate limiting utilities for controlling
// burst traffic to external APIs.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/ardikabs/hibernator/internal/metrics"
	"golang.org/x/time/rate"
)

// Config holds rate limiting configuration for a sink.
type Config struct {
	// RequestsPerSecond is the sustained rate limit (e.g., 1.0 for 1 req/sec).
	// Zero means use default.
	RequestsPerSecond float64 `json:"requests_per_second,omitempty"`

	// Burst is the maximum burst size allowed.
	// Zero means use default.
	Burst int `json:"burst,omitempty"`
}

// DefaultConfig returns the default rate limiting configuration.
func DefaultConfig() Config {
	return Config{
		RequestsPerSecond: 5.0, // 5 request per second
		Burst:             10,  // Allow bursts up to 10
	}
}

// Limiter wraps a token bucket rate limiter with additional functionality.
type Limiter struct {
	limiter *rate.Limiter
	config  Config
}

// NewLimiter creates a new rate limiter with the given configuration.
// If config is zero, uses DefaultConfig().
func NewLimiter(cfg Config) *Limiter {
	cfg = cfg.withDefaults()

	// Convert requests per second to interval
	interval := time.Duration(float64(time.Second) / cfg.RequestsPerSecond)

	return &Limiter{
		limiter: rate.NewLimiter(rate.Every(interval), cfg.Burst),
		config:  cfg,
	}
}

// Wait blocks until a token is available or the context is cancelled.
// Returns an error if the context is cancelled before a token is available.
func (l *Limiter) Wait(ctx context.Context) error {
	return l.limiter.Wait(ctx)
}

// WaitWithMetrics blocks until a token is available and records metrics.
// The sinkName is used as a label for metrics.
func (l *Limiter) WaitWithMetrics(ctx context.Context, sinkName string) error {
	start := time.Now()

	// Check if we'll need to wait (token not immediately available)
	needsWait := !l.limiter.Allow()

	err := l.limiter.Wait(ctx)

	if needsWait {
		waitDuration := time.Since(start)
		metrics.NotificationRateLimitWaitDuration.WithLabelValues(sinkName).Observe(waitDuration.Seconds())
		metrics.NotificationRateLimitDelayTotal.WithLabelValues(sinkName).Inc()
	}

	return err
}

// Allow reports whether a token is available immediately.
// This is non-blocking - use Wait() for blocking behavior.
func (l *Limiter) Allow() bool {
	return l.limiter.Allow()
}

// Config returns the current configuration.
func (l *Limiter) Config() Config {
	return l.config
}

// withDefaults applies default values to zero fields.
func (c Config) withDefaults() Config {
	defaultCfg := DefaultConfig()

	if c.RequestsPerSecond <= 0 {
		c.RequestsPerSecond = defaultCfg.RequestsPerSecond
	}
	if c.Burst <= 0 {
		c.Burst = defaultCfg.Burst
	}

	return c
}

// Validate checks if the configuration is valid.
func (c Config) Validate() error {
	if c.RequestsPerSecond < 0 {
		return fmt.Errorf("requests_per_second must be non-negative")
	}
	if c.Burst < 0 {
		return fmt.Errorf("burst must be non-negative")
	}
	if c.Burst == 0 && c.RequestsPerSecond > 0 {
		return fmt.Errorf("burst must be positive when requests_per_second is set")
	}
	return nil
}
