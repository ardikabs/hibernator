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
	"sync"
	"time"

	"github.com/ardikabs/hibernator/internal/metrics"
	"golang.org/x/time/rate"
)

// Config holds rate limiting configuration.
// Rate is expressed as requests per Unit (e.g. Rate: 5.0, Unit: time.Second
// means 5 requests per second). This is unambiguous: there is only one rate.
type Config struct {
	// Rate is the sustained rate limit (e.g., 5.0 for 5 req/unit).
	// Zero means use default.
	Rate float64 `json:"rate,omitempty"`

	// Unit is the time unit for Rate (e.g. time.Second, time.Minute).
	// Zero means use default (time.Second).
	Unit time.Duration `json:"-"`

	// Burst is the maximum burst size allowed.
	// Zero means use default.
	Burst int `json:"burst,omitempty"`
}

// minWaitMetricsThreshold is the minimum duration a Wait must take before
// it is considered "delayed" and recorded in metrics. This filters out
// sub-millisecond waits caused by scheduler jitter rather than actual rate
// limiting.
const minWaitMetricsThreshold = 1 * time.Millisecond

// DefaultConfig returns the default rate limiting configuration.
func DefaultConfig() Config {
	return Config{
		Rate:  5.0,        // 5 requests per unit
		Unit:  time.Second, // default unit is per-second
		Burst: 10,         // Allow bursts up to 10
	}
}

// Limiter wraps a token bucket rate limiter with optional per-operation
// sub-limiters. The global bucket enforces the aggregate rate; each
// operation registered via SetOperation gets its own bucket that is
// consulted after the global one.
type Limiter struct {
	limiter *rate.Limiter
	config  Config
	ops     map[string]*rate.Limiter
	opsMu   sync.RWMutex
}

// New creates a new rate limiter with the given configuration.
// If config is zero, uses DefaultConfig().
func New(cfg Config) *Limiter {
	cfg = cfg.withDefaults()
	return &Limiter{
		limiter: newRateLimiter(cfg),
		config:  cfg,
		ops:     make(map[string]*rate.Limiter),
	}
}

// newRateLimiter builds a *rate.Limiter from Config.
// The token interval is Unit / Rate.
func newRateLimiter(cfg Config) *rate.Limiter {
	interval := time.Duration(float64(cfg.Unit) / cfg.Rate)
	return rate.NewLimiter(rate.Every(interval), cfg.Burst)
}

// Wait blocks until a token is available from the global bucket or the
// context is cancelled.
func (l *Limiter) Wait(ctx context.Context) error {
	return l.limiter.Wait(ctx)
}

// WaitWithMetrics blocks until a token is available from the global bucket
// and records metrics. The key is used as a label for metrics.
func (l *Limiter) WaitWithMetrics(ctx context.Context, key string) error {
	start := time.Now()
	if err := l.limiter.Wait(ctx); err != nil {
		return err
	}
	if waitDuration := time.Since(start); waitDuration > minWaitMetricsThreshold {
		metrics.NotificationRateLimitWaitDuration.WithLabelValues(key).Observe(waitDuration.Seconds())
		metrics.NotificationRateLimitDelayTotal.WithLabelValues(key).Inc()
	}
	return nil
}

// WaitOperation blocks until a token is available from both the global
// bucket and the named operation bucket, or the context is cancelled.
// If the operation has not been registered, only the global bucket is waited.
func (l *Limiter) WaitOperation(ctx context.Context, op string) error {
	if err := l.limiter.Wait(ctx); err != nil {
		return err
	}
	l.opsMu.RLock()
	opLim, ok := l.ops[op]
	l.opsMu.RUnlock()
	if ok {
		if err := opLim.Wait(ctx); err != nil {
			return err
		}
	}
	return nil
}

// WaitOperationWithMetrics is like WaitOperation but records metrics on
// the provided key.
func (l *Limiter) WaitOperationWithMetrics(ctx context.Context, key, op string) error {
	start := time.Now()
	if err := l.limiter.Wait(ctx); err != nil {
		return err
	}
	l.opsMu.RLock()
	opLim, ok := l.ops[op]
	l.opsMu.RUnlock()
	if ok {
		if err := opLim.Wait(ctx); err != nil {
			return err
		}
	}
	if waitDuration := time.Since(start); waitDuration > minWaitMetricsThreshold {
		metrics.NotificationRateLimitWaitDuration.WithLabelValues(key).Observe(waitDuration.Seconds())
		metrics.NotificationRateLimitDelayTotal.WithLabelValues(key).Inc()
	}
	return nil
}

// Allow reports whether a token is available immediately from the global
// bucket. This is non-blocking.
func (l *Limiter) Allow() bool {
	return l.limiter.Allow()
}

// Config returns the current configuration.
func (l *Limiter) Config() Config {
	return l.config
}

// SetConfig updates the global limiter in-place with a new configuration.
// Existing operation limiters are untouched.
func (l *Limiter) SetConfig(cfg Config) {
	cfg = cfg.withDefaults()
	interval := time.Duration(float64(cfg.Unit) / cfg.Rate)
	l.limiter.SetLimit(rate.Every(interval))
	l.limiter.SetBurst(cfg.Burst)
	l.config = cfg
}

// SetOperation creates or updates a per-operation rate limiter.
// The operation limiter is consulted after the global limiter on every
// WaitOperation call. If the operation already exists, its rate and burst
// are updated in-place.
func (l *Limiter) SetOperation(op string, cfg Config) {
	cfg = cfg.withDefaults()
	interval := time.Duration(float64(cfg.Unit) / cfg.Rate)
	burst := cfg.Burst

	l.opsMu.Lock()
	defer l.opsMu.Unlock()

	if existing, ok := l.ops[op]; ok {
		existing.SetLimit(rate.Every(interval))
		existing.SetBurst(burst)
	} else {
		l.ops[op] = rate.NewLimiter(rate.Every(interval), burst)
	}
}

// withDefaults applies default values to zero fields.
func (c Config) withDefaults() Config {
	defaultCfg := DefaultConfig()

	if c.Rate <= 0 {
		c.Rate = defaultCfg.Rate
	}
	if c.Unit <= 0 {
		c.Unit = defaultCfg.Unit
	}
	if c.Burst <= 0 {
		c.Burst = defaultCfg.Burst
	}

	return c
}

// Validate checks if the configuration is valid.
func (c Config) Validate() error {
	if c.Rate < 0 {
		return fmt.Errorf("rate must be non-negative")
	}
	if c.Burst < 0 {
		return fmt.Errorf("burst must be non-negative")
	}
	if c.Burst == 0 && c.Rate > 0 {
		return fmt.Errorf("burst must be positive when rate is set")
	}
	return nil
}
