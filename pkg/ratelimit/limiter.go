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
	"math"
	"time"

	"github.com/ardikabs/hibernator/internal/metrics"
	"golang.org/x/time/rate"
)

// Config holds rate limiting configuration.
type Config struct {
	// RequestsPerSecond is the sustained rate limit (e.g., 1.0 for 1 req/sec).
	// Zero means use default.
	RequestsPerSecond float64 `json:"requests_per_second,omitempty"`

	// Burst is the maximum burst size allowed.
	// Zero means use default.
	Burst int `json:"burst,omitempty"`

	// RequestsPerMinute is the per-minute rate limit (e.g., 100 for 100 req/min).
	// Optional - if zero or not set, defaults to RequestsPerSecond * 60.
	// Set to -1 to disable per-minute limiting entirely (only use per-second).
	RequestsPerMinute int `json:"requests_per_minute,omitempty"`
}

// DefaultConfig returns the default rate limiting configuration.
func DefaultConfig() Config {
	return Config{
		RequestsPerSecond: 5.0, // 5 request per second
		Burst:             10,  // Allow bursts up to 10
	}
}

// Limiter wraps token bucket rate limiters with additional functionality.
// Supports both per-second and optional per-minute rate limiting.
type Limiter struct {
	perSecond *rate.Limiter
	perMinute *rate.Limiter // nil if per-minute limiting is disabled
	config    Config
}

// NewLimiter creates a new rate limiter with the given configuration.
// If config is zero, uses DefaultConfig().
// Automatically configures per-minute limiting based on RequestsPerMinute setting.
func NewLimiter(cfg Config) *Limiter {
	cfg = cfg.withDefaults()

	// Per-second limiter
	perSecondInterval := time.Duration(float64(time.Second) / cfg.RequestsPerSecond)
	perSecondLimiter := rate.NewLimiter(rate.Every(perSecondInterval), cfg.Burst)

	// Per-minute limiter (optional)
	var perMinuteLimiter *rate.Limiter
	if cfg.RequestsPerMinute >= 0 {
		rpm := cfg.RequestsPerMinute
		if rpm == 0 {
			// Calculated based on RPS in minute
			rpm = int(math.Ceil(cfg.RequestsPerSecond * 60))
		}
		perMinuteInterval := time.Duration(float64(time.Minute) / float64(rpm))
		perMinuteLimiter = rate.NewLimiter(rate.Every(perMinuteInterval), rpm)
	}

	return &Limiter{
		perSecond: perSecondLimiter,
		perMinute: perMinuteLimiter,
		config:    cfg,
	}
}

// Wait blocks until a token is available or the context is cancelled.
// Returns an error if the context is cancelled before a token is available.
// Waits for both per-second and per-minute limiters (if configured).
func (l *Limiter) Wait(ctx context.Context) error {
	// Wait for per-second limiter first
	if err := l.perSecond.Wait(ctx); err != nil {
		return err
	}

	// Wait for per-minute limiter if configured
	if l.perMinute != nil {
		if err := l.perMinute.Wait(ctx); err != nil {
			return err
		}
	}

	return nil
}

// WaitWithMetrics blocks until a token is available and records metrics.
// The key is used as a label for metrics.
// Waits for both per-second and per-minute limiters (if configured).
func (l *Limiter) WaitWithMetrics(ctx context.Context, key string) error {
	start := time.Now()

	// Check if we'll need to wait on either limiter
	needsWait := !l.perSecond.Allow()
	if l.perMinute != nil && !needsWait {
		needsWait = !l.perMinute.Allow()
	}

	// Wait for per-second limiter
	if err := l.perSecond.Wait(ctx); err != nil {
		return err
	}

	// Wait for per-minute limiter if configured
	if l.perMinute != nil {
		if err := l.perMinute.Wait(ctx); err != nil {
			return err
		}
	}

	if needsWait {
		waitDuration := time.Since(start)
		metrics.NotificationRateLimitWaitDuration.WithLabelValues(key).Observe(waitDuration.Seconds())
		metrics.NotificationRateLimitDelayTotal.WithLabelValues(key).Inc()
	}

	return nil
}

// Allow reports whether a token is available immediately from both limiters.
// This is non-blocking - use Wait() for blocking behavior.
// Returns true only if both per-second and per-minute (if configured) have tokens available.
func (l *Limiter) Allow() bool {
	if !l.perSecond.Allow() {
		return false
	}
	if l.perMinute != nil && !l.perMinute.Allow() {
		fmt.Println("KNTL", l.perMinute.Burst())
		return false
	}
	return true
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
	if c.RequestsPerMinute < -1 {
		return fmt.Errorf("requests_per_minute must be -1 (disabled), 0 (auto), or positive")
	}
	if c.RequestsPerMinute > 0 && c.RequestsPerMinute < c.Burst {
		// Warn if per-minute limit is less than burst (can cause unexpected blocking)
		return fmt.Errorf("requests_per_minute (%d) should be >= burst (%d)", c.RequestsPerMinute, c.Burst)
	}
	return nil
}
