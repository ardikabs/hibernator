/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package ratelimit

import (
	"context"
	"strings"

	"github.com/ardikabs/hibernator/pkg/cache"
	"github.com/go-logr/logr"
)

// Registry manages rate limiters per key.
// It creates limiters on-demand and caches them for reuse.
// Each key (typically a credential, token, webhook URL, or any unique identifier)
// gets its own rate limiter with its own configuration.
//
// The registry implements LRU eviction to prevent unbounded memory growth.
// When the maximum number of keys is reached, the least-recently-used limiter
// is evicted to make room for new ones.
//
// Keys containing "#" are treated as operation-scoped: the portion before
// "#" is the parent key and the portion after is the operation name.
// Register stores the operation config on the parent limiter via SetOperation.
// Get/GetLimiter/Wait resolve the parent limiter and apply the operation.
type Registry struct {
	cache    *cache.Cache[string, *Limiter]
	defaults Config
	maxKeys  int
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

// WithMaxKeys sets the maximum number of keys to store in the registry.
// When exceeded, the least-recently-used limiter is evicted.
// Default is 1000.
func WithMaxKeys(max int) RegistryOption {
	return func(r *Registry) {
		r.maxKeys = max
	}
}

// NewRegistry creates a new rate limiter registry.
func NewRegistry(opts ...RegistryOption) *Registry {
	r := &Registry{
		defaults: DefaultConfig(),
		maxKeys:  1000,
		log:      logr.Discard(),
	}

	for _, opt := range opts {
		opt(r)
	}

	c, err := cache.New(
		cache.WithMaxSize[string, *Limiter](r.maxKeys),
	)
	if err != nil {
		// Should never happen with positive maxKeys
		panic(err)
	}
	r.cache = c

	return r
}

// isChildKey reports whether key contains the operation delimiter.
func isChildKey(key string) bool {
	return strings.Contains(key, "#")
}

// splitChildKey returns the parent key and operation name for a child key.
// If key is not a child key, returns ("", "").
func splitChildKey(key string) (parent, op string) {
	idx := strings.LastIndex(key, "#")
	if idx < 0 {
		return "", ""
	}
	return key[:idx], key[idx+1:]
}

// Register creates or updates a rate limiter for the given key with the specified config.
// This is typically called by callers to register their key-specific
// rate limit configuration from user-provided secrets.
//
// Behavior:
//   - If key doesn't exist: creates new limiter with the config
//   - If key exists and config is unchanged: no-op (idempotent)
//   - If key exists and config changed: updates limiter in-place with new config
//
// Register does NOT accept operation-scoped keys (keys containing "#").
// Use registerEntry for coordinated parent+operation registration.
func (r *Registry) Register(key string, cfg Config) {
	// Apply defaults for any zero values
	cfg = cfg.withDefaults()

	// Check if limiter already exists
	lim, ok := r.cache.Get(key)
	if ok {
		// If config hasn't changed, just update LRU position (idempotent)
		if configsEqual(lim.Config(), cfg) {
			return
		}

		// Config changed - update the limiter in-place
		r.log.V(1).Info("updating rate limiter config", "key", redactKey(key),
			"old_rate", lim.Config().Rate,
			"new_rate", cfg.Rate,
			"old_unit", lim.Config().Unit,
			"new_unit", cfg.Unit,
			"old_burst", lim.Config().Burst,
			"new_burst", cfg.Burst,
		)

		lim.SetConfig(cfg)
		return
	}

	// Create new limiter
	r.cache.Add(key, New(cfg))

	r.log.V(1).Info("registered rate limiter", "key", redactKey(key), "rate", cfg.Rate, "unit", cfg.Unit, "burst", cfg.Burst, "total_keys", r.cache.Len())
}

// getOrCreate returns the limiter for key, creating it with defaults if it
// does not exist.
func (r *Registry) getOrCreate(key string) *Limiter {
	lim, ok := r.cache.Get(key)
	if ok {
		return lim
	}

	r.log.V(1).Info("creating default rate limiter", "key", redactKey(key), "rate", r.defaults.Rate, "unit", r.defaults.Unit, "burst", r.defaults.Burst)
	lim = New(r.defaults)
	r.cache.Add(key, lim)
	return lim
}

// registerEntry registers a parent limiter and optionally an operation
// in a single coordinated step. This avoids split registration where the
// parent might be created with defaults before its real config is applied.
func (r *Registry) registerEntry(entry *rateLimitEntry) {
	parentCfg := entry.cfg
	parentCfg = parentCfg.withDefaults()

	lim, ok := r.cache.Get(entry.key)
	if ok {
		if !configsEqual(lim.Config(), parentCfg) {
			r.log.V(1).Info("updating rate limiter config", "key", redactKey(entry.key),
				"old_rate", lim.Config().Rate,
				"new_rate", parentCfg.Rate,
				"old_unit", lim.Config().Unit,
				"new_unit", parentCfg.Unit,
				"old_burst", lim.Config().Burst,
				"new_burst", parentCfg.Burst,
			)
			lim.SetConfig(parentCfg)
		}
	} else {
		lim = New(parentCfg)
		r.cache.Add(entry.key, lim)
		r.log.V(1).Info("registered rate limiter", "key", redactKey(entry.key), "rate", parentCfg.Rate, "unit", parentCfg.Unit, "burst", parentCfg.Burst, "total_keys", r.cache.Len())
	}

	if entry.opName != "" {
		lim.SetOperation(entry.opName, entry.opCfg)
	}
}

// Get returns the limiter for the given key.
// For operation-scoped keys (containing "#"), returns the parent limiter.
// If no limiter exists, creates one with default configuration.
// Updates LRU order to mark the key as recently used.
func (r *Registry) Get(key string) *Limiter {
	if isChildKey(key) {
		parentKey, _ := splitChildKey(key)
		return r.Get(parentKey)
	}
	return r.getOrCreate(key)
}

// GetLimiter returns the limiter for the given key.
// For operation-scoped keys (containing "#"), returns the parent limiter.
// This is the method the transport should use for rate limiting.
func (r *Registry) GetLimiter(key string) *Limiter {
	return r.Get(key)
}

// Wait waits for a token from the rate limiter for the given key.
// This is a convenience method that gets or creates the limiter and waits.
// For operation-scoped keys (containing "#"), it waits the global bucket
// then the operation bucket. Metrics are recorded on the full key.
func (r *Registry) Wait(ctx context.Context, key string) error {
	if isChildKey(key) {
		parentKey, op := splitChildKey(key)
		limiter := r.Get(parentKey)
		return limiter.WaitOperationWithMetrics(ctx, key, op)
	}
	limiter := r.Get(key)
	return limiter.WaitWithMetrics(ctx, key)
}

// Len returns the number of limiters in the registry.
func (r *Registry) Len() int {
	return r.cache.Len()
}

// HasKey checks if a rate limiter exists for the given key.
func (r *Registry) HasKey(key string) bool {
	if isChildKey(key) {
		parentKey, _ := splitChildKey(key)
		return r.HasKey(parentKey)
	}
	return r.cache.Contains(key)
}

// MaxKeys returns the maximum number of keys the registry can hold.
func (r *Registry) MaxKeys() int {
	return r.maxKeys
}

// Close stops the background sweep goroutine of the underlying cache.
// It is safe to call multiple times.
func (r *Registry) Close() {
	r.cache.Close()
}

// configsEqual compares two Config structs for equality.
func configsEqual(a, b Config) bool {
	return a.Rate == b.Rate &&
		a.Unit == b.Unit &&
		a.Burst == b.Burst
}
