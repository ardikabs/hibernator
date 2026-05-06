/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package ratelimit

import (
	"container/list"
	"context"
	"sync"

	"github.com/go-logr/logr"
)

// entry is an LRU cache entry
type entry struct {
	key     string
	config  Config
	limiter *Limiter
}

// Registry manages rate limiters per key.
// It creates limiters on-demand and caches them for reuse.
// Each key (typically a credential, token, webhook URL, or any unique identifier)
// gets its own rate limiter with its own configuration.
//
// The registry implements LRU eviction to prevent unbounded memory growth.
// When the maximum number of keys is reached, the least-recently-used limiter
// is evicted to make room for new ones.
type Registry struct {
	mu       sync.RWMutex
	limiters map[string]*list.Element // map key -> LRU list element
	lru      *list.List               // doubly-linked list for LRU tracking
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
		limiters: make(map[string]*list.Element),
		lru:      list.New(),
		defaults: DefaultConfig(),
		maxKeys:  1000, // Default max keys
		log:      logr.Discard(),
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Register creates or updates a rate limiter for the given key with the specified config.
// This is typically called by callers to register their key-specific
// rate limit configuration from user-provided secrets.
//
// Behavior:
//   - If key doesn't exist: creates new limiter with the config
//   - If key exists and config is unchanged: no-op (idempotent)
//   - If key exists and config changed: updates limiter with new config
//
// This allows configuration to be updated dynamically when users change settings.
// If the registry is at capacity, the least-recently-used limiter is evicted.
func (r *Registry) Register(key string, cfg Config) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Apply defaults for any zero values
	cfg = cfg.withDefaults()

	// Check if limiter already exists for this key
	if elem, exists := r.limiters[key]; exists {
		ent := elem.Value.(*entry)

		// If config hasn't changed, just update LRU position (idempotent)
		// Compare individual fields since the caller creates a new struct each time
		if configsEqual(ent.config, cfg) {
			r.lru.MoveToFront(elem)
			return
		}

		// Config changed - update the limiter
		r.log.V(1).Info("updating rate limiter config", "key", redactKey(key),
			"old_rps", ent.config.RequestsPerSecond,
			"new_rps", cfg.RequestsPerSecond,
			"old_burst", ent.config.Burst,
			"new_burst", cfg.Burst,
			"old_rpm", ent.config.RequestsPerMinute,
			"new_rpm", cfg.RequestsPerMinute,
		)

		ent.config = cfg
		ent.limiter = NewLimiter(cfg)
		r.lru.MoveToFront(elem)
		return
	}

	// Create new limiter and add to LRU list
	ent := &entry{
		key:     key,
		config:  cfg,
		limiter: NewLimiter(cfg),
	}
	elem := r.lru.PushFront(ent)
	r.limiters[key] = elem

	r.log.V(1).Info("registered rate limiter", "key", redactKey(key), "rps", cfg.RequestsPerSecond, "burst", cfg.Burst, "rpm", cfg.RequestsPerMinute, "total_keys", r.lru.Len())

	// Evict oldest entry if over capacity
	if r.lru.Len() > r.maxKeys {
		r.evictOldest()
	}
}

// evictOldest removes the least-recently-used limiter from the registry.
// Must be called with lock held.
func (r *Registry) evictOldest() {
	elem := r.lru.Back()
	if elem == nil {
		return
	}

	ent := elem.Value.(*entry)
	delete(r.limiters, ent.key)
	r.lru.Remove(elem)

	r.log.V(1).Info("evicted rate limiter due to max_keys reached", "key", redactKey(ent.key), "max_keys", r.maxKeys, "remaining_keys", r.lru.Len())
}

// Get returns the rate limiter for the given key.
// If no limiter exists, creates one with default configuration.
// Updates LRU order to mark the key as recently used.
func (r *Registry) Get(key string) *Limiter {
	r.mu.RLock()
	elem, exists := r.limiters[key]
	r.mu.RUnlock()

	if exists {
		// Move to front (mark as recently used)
		r.mu.Lock()
		r.lru.MoveToFront(elem)
		r.mu.Unlock()
		return elem.Value.(*entry).limiter
	}

	// Create new limiter with defaults
	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock
	if elem, exists := r.limiters[key]; exists {
		r.lru.MoveToFront(elem)
		return elem.Value.(*entry).limiter
	}

	r.log.V(1).Info("creating default rate limiter", "key", redactKey(key), "rps", r.defaults.RequestsPerSecond, "burst", r.defaults.Burst)
	ent := &entry{
		key:     key,
		config:  r.defaults,
		limiter: NewLimiter(r.defaults),
	}
	elem = r.lru.PushFront(ent)
	r.limiters[key] = elem

	// Evict oldest entry if over capacity
	if r.lru.Len() > r.maxKeys {
		r.evictOldest()
	}

	return ent.limiter
}

// Wait waits for a token from the rate limiter for the given key.
// This is a convenience method that gets or creates the limiter and waits.
// Typically called by the HTTP transport before making requests.
func (r *Registry) Wait(ctx context.Context, key string) error {
	limiter := r.Get(key)
	return limiter.WaitWithMetrics(ctx, key)
}

// Len returns the number of limiters in the registry.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lru.Len()
}

// HasKey checks if a rate limiter exists for the given key.
func (r *Registry) HasKey(key string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.limiters[key]
	return exists
}

// MaxKeys returns the maximum number of keys the registry can hold.
func (r *Registry) MaxKeys() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.maxKeys
}

// configsEqual compares two Config structs for equality.
// Used since callers create new Config instances on each call.
func configsEqual(a, b Config) bool {
	return a.RequestsPerSecond == b.RequestsPerSecond &&
		a.Burst == b.Burst &&
		a.RequestsPerMinute == b.RequestsPerMinute
}
