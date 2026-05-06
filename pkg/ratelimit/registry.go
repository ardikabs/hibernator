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

// Registry manages rate limiters per key.
// It creates limiters on-demand and caches them for reuse.
// Each key (typically a credential, token, webhook URL, or any unique identifier)
// gets its own rate limiter with its own configuration.
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

// Register creates or updates a rate limiter for the given key with the specified config.
// This is typically called by sinks when they initialize to register their key-specific
// rate limit configuration from user-provided secrets.
func (r *Registry) Register(key string, cfg Config) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Apply defaults for any zero values
	cfg = cfg.withDefaults()

	r.log.V(1).Info("registering rate limiter", "key", key, "rps", cfg.RequestsPerSecond, "burst", cfg.Burst, "rpm", cfg.RequestsPerMinute)
	r.limiters[key] = NewLimiter(cfg)
}

// Get returns the rate limiter for the given key.
// If no limiter exists, creates one with default configuration.
func (r *Registry) Get(key string) *Limiter {
	r.mu.RLock()
	limiter, exists := r.limiters[key]
	r.mu.RUnlock()

	if exists {
		return limiter
	}

	// Create new limiter with defaults
	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock
	if limiter, exists := r.limiters[key]; exists {
		return limiter
	}

	r.log.V(1).Info("creating default rate limiter", "key", key, "rps", r.defaults.RequestsPerSecond, "burst", r.defaults.Burst)
	limiter = NewLimiter(r.defaults)
	r.limiters[key] = limiter
	return limiter
}

// GetWithConfig returns the rate limiter for the given key,
// creating one with the specified configuration if it doesn't exist.
// Deprecated: Use Register() for explicit key registration.
func (r *Registry) GetWithConfig(key string, cfg Config) *Limiter {
	r.mu.RLock()
	limiter, exists := r.limiters[key]
	r.mu.RUnlock()

	if exists {
		return limiter
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock
	if limiter, exists := r.limiters[key]; exists {
		return limiter
	}

	// Use provided config, falling back to defaults for zero values
	cfg = cfg.withDefaults()
	r.log.V(1).Info("creating rate limiter with config", "key", key, "rps", cfg.RequestsPerSecond, "burst", cfg.Burst)
	limiter = NewLimiter(cfg)
	r.limiters[key] = limiter
	return limiter
}

// Wait waits for a token from the rate limiter for the given key.
// This is a convenience method that gets or creates the limiter and waits.
// Typically called by the HTTP transport before making requests.
func (r *Registry) Wait(ctx context.Context, key string) error {
	limiter := r.Get(key)
	return limiter.WaitWithMetrics(ctx, key)
}

// WaitWithConfig waits for a token using the specified configuration.
// Creates a new limiter if one doesn't exist for this key.
// Deprecated: Use Register() followed by Wait() for explicit key registration.
func (r *Registry) WaitWithConfig(ctx context.Context, key string, cfg Config) error {
	limiter := r.GetWithConfig(key, cfg)
	return limiter.WaitWithMetrics(ctx, key)
}

// Len returns the number of limiters in the registry.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.limiters)
}

// HasKey checks if a rate limiter exists for the given key.
func (r *Registry) HasKey(key string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.limiters[key]
	return exists
}
