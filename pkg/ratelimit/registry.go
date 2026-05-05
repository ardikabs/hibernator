/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package ratelimit

import (
	"context"
	"sync"

	"github.com/go-logr/logr"
)

// Registry manages rate limiters per sink name.
// It creates limiters on-demand and caches them for reuse.
type Registry struct {
	mu       sync.RWMutex
	limiters map[string]*Limiter
	defaults Config
	log      logr.Logger
}

// RegistryOption configures a Registry.
type RegistryOption func(*Registry)

// WithLogger sets the logger for the registry.
func WithLogger(log logr.Logger) RegistryOption {
	return func(r *Registry) {
		r.log = log
	}
}

// WithDefaultConfig sets the default configuration for new limiters.
func WithDefaultConfig(cfg Config) RegistryOption {
	return func(r *Registry) {
		r.defaults = cfg
	}
}

// NewRegistry creates a new rate limiter registry.
func NewRegistry(opts ...RegistryOption) *Registry {
	r := &Registry{
		limiters: make(map[string]*Limiter),
		defaults: DefaultConfig(),
		log:      logr.Discard(),
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Get returns the rate limiter for the given sink name.
// If no limiter exists, creates one with default configuration.
func (r *Registry) Get(sinkName string) *Limiter {
	r.mu.RLock()
	limiter, exists := r.limiters[sinkName]
	r.mu.RUnlock()

	if exists {
		return limiter
	}

	// Create new limiter
	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock
	if limiter, exists := r.limiters[sinkName]; exists {
		return limiter
	}

	r.log.V(1).Info("creating new rate limiter", "sink_name", sinkName, "rps", r.defaults.RequestsPerSecond, "burst", r.defaults.Burst)
	limiter = NewLimiter(r.defaults)
	r.limiters[sinkName] = limiter
	return limiter
}

// GetWithConfig returns the rate limiter for the given sink name,
// creating one with the specified configuration if it doesn't exist.
func (r *Registry) GetWithConfig(sinkName string, cfg Config) *Limiter {
	r.mu.RLock()
	limiter, exists := r.limiters[sinkName]
	r.mu.RUnlock()

	if exists {
		return limiter
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock
	if limiter, exists := r.limiters[sinkName]; exists {
		return limiter
	}

	// Use provided config, falling back to defaults for zero values
	cfg = cfg.withDefaults()
	r.log.V(1).Info("creating new rate limiter with config", "sink_name", sinkName, "rps", cfg.RequestsPerSecond, "burst", cfg.Burst)
	limiter = NewLimiter(cfg)
	r.limiters[sinkName] = limiter
	return limiter
}

// Wait waits for a token from the rate limiter for the given sink name.
// This is a convenience method that gets or creates the limiter and waits.
func (r *Registry) Wait(ctx context.Context, sinkName string) error {
	limiter := r.Get(sinkName)
	return limiter.WaitWithMetrics(ctx, sinkName)
}

// WaitWithConfig waits for a token using the specified configuration.
// Creates a new limiter if one doesn't exist for this sink name.
func (r *Registry) WaitWithConfig(ctx context.Context, sinkName string, cfg Config) error {
	limiter := r.GetWithConfig(sinkName, cfg)
	return limiter.WaitWithMetrics(ctx, sinkName)
}

// Len returns the number of limiters in the registry.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.limiters)
}
