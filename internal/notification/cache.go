/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

import (
	"context"
	"maps"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"
)

// stateCacheEntry holds cached sink states with metadata for TTL management.
type stateCacheEntry struct {
	states     map[string]string
	lastAccess time.Time
	ttl        time.Duration
}

// isExpired returns true if the entry has exceeded its TTL.
func (e *stateCacheEntry) isExpired(now time.Time) bool {
	return now.Sub(e.lastAccess) > e.ttl
}

// touch updates the last access time to now, extending the TTL.
func (e *stateCacheEntry) touch(now time.Time) {
	e.lastAccess = now
}

// SinkStateCache provides an in-memory cache for sink states with TTL support.
// It ensures that recently accessed states remain in memory while stale entries
// are evicted. This solves the race condition between async status updates
// and immediate state reads for subsequent notifications.
type SinkStateCache struct {
	mu      sync.RWMutex
	entries map[string]*stateCacheEntry
	ttl     time.Duration
	log     logr.Logger
}

// NewSinkStateCache creates a new state cache with the specified TTL.
func NewSinkStateCache(log logr.Logger, ttl time.Duration) *SinkStateCache {
	return &SinkStateCache{
		entries: make(map[string]*stateCacheEntry),
		ttl:     ttl,
		log:     log,
	}
}

// cacheKey creates a unique cache key from notification and sink identifiers.
func cacheKey(notificationRef types.NamespacedName, sinkName, planNamespace, planName, cycleID, operation string) string {
	return notificationRef.String() + "|" + SinkStatusKey(sinkName, planNamespace, planName, cycleID, operation)
}

// Get retrieves states from the cache if present and not expired.
// On cache hit, the TTL is reset (touch-on-read).
// Returns (states, true) on hit, (nil, false) on miss or expired.
func (c *SinkStateCache) Get(notificationRef types.NamespacedName, sinkName, planNamespace, planName, cycleID, operation string) (map[string]string, bool) {
	key := cacheKey(notificationRef, sinkName, planNamespace, planName, cycleID, operation)

	c.mu.RLock()
	entry, exists := c.entries[key]
	c.mu.RUnlock()

	if !exists {
		c.log.V(2).Info("state cache miss", "key", key)
		return nil, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	if entry.isExpired(now) {
		c.log.V(2).Info("state cache expired", "key", key)
		delete(c.entries, key)
		return nil, false
	}

	// Touch-on-read: extend TTL
	entry.touch(now)

	c.log.V(2).Info("state cache hit", "key", key, "state_count", len(entry.states))
	return entry.states, true
}

// Set stores states in the cache with the configured TTL.
// Overwrites any existing entry for the same key.
func (c *SinkStateCache) Set(notificationRef types.NamespacedName, sinkName, planNamespace, planName, cycleID, operation string, states map[string]string) {
	key := cacheKey(notificationRef, sinkName, planNamespace, planName, cycleID, operation)

	// Deep copy states to prevent external mutation
	statesCopy := make(map[string]string, len(states))
	maps.Copy(statesCopy, states)

	c.mu.Lock()
	c.entries[key] = &stateCacheEntry{
		states:     statesCopy,
		lastAccess: time.Now(),
		ttl:        c.ttl,
	}
	c.mu.Unlock()

	c.log.V(2).Info("state cache set", "key", key, "state_count", len(statesCopy))
}

// Delete removes a specific entry from the cache.
func (c *SinkStateCache) Delete(notificationRef types.NamespacedName, sinkName, planNamespace, planName, cycleID, operation string) {
	key := cacheKey(notificationRef, sinkName, planNamespace, planName, cycleID, operation)

	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()

	c.log.V(2).Info("state cache delete", "key", key)
}

// StartEvictionLoop begins a background goroutine that periodically evicts expired entries.
// Call this once after creating the cache. The loop stops when the provided context is cancelled.
func (c *SinkStateCache) StartEvictionLoop(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				c.log.V(1).Info("state cache eviction loop stopped")
				return
			case <-ticker.C:
				c.evictExpired()
			}
		}
	}()
}

// evictExpired removes all expired entries from the cache.
func (c *SinkStateCache) evictExpired() {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	expiredCount := 0
	for key, entry := range c.entries {
		if entry.isExpired(now) {
			delete(c.entries, key)
			expiredCount++
		}
	}

	if expiredCount > 0 {
		c.log.V(1).Info("state cache eviction completed", "expired_count", expiredCount, "remaining", len(c.entries))
	}
}

// Len returns the current number of entries in the cache.
func (c *SinkStateCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// Clear removes all entries from the cache.
func (c *SinkStateCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*stateCacheEntry)
}

// GetOrFetch atomically retrieves states from the cache, or if not present,
// calls the fetch function to retrieve from the source and caches the result.
// This ensures that even when multiple goroutines request the same key simultaneously,
// only one fetch occurs and all callers receive the same result.
// On cache hit, the TTL is reset (touch-on-read).
func (c *SinkStateCache) GetOrFetch(ctx context.Context,
	notificationRef types.NamespacedName,
	sinkName, planNamespace, planName, cycleID, operation string,
	fetchFunc func(context.Context) (map[string]string, error),
) (map[string]string, error) {
	key := cacheKey(notificationRef, sinkName, planNamespace, planName, cycleID, operation)

	// First, try to get from cache with read lock (fast path)
	c.mu.RLock()
	entry, exists := c.entries[key]
	c.mu.RUnlock()

	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	if exists {
		if !entry.isExpired(now) {
			// Cache hit - update access time and return
			entry.touch(now)
			c.log.V(2).Info("state cache hit", "key", key, "state_count", len(entry.states))
			return entry.states, nil
		}
		// Expired entry will be replaced below
		c.log.V(2).Info("state cache expired, refetching", "key", key)
	} else {
		c.log.V(2).Info("state cache miss, fetching", "key", key)
	}

	// Check again under write lock in case another goroutine fetched while we waited
	entry, exists = c.entries[key]
	if exists && !entry.isExpired(now) {
		entry.touch(now)
		c.log.V(2).Info("state cache hit after lock", "key", key, "state_count", len(entry.states))
		return entry.states, nil
	}

	// We are the first to fetch - call the fetch function
	states, err := fetchFunc(ctx)
	if err != nil {
		return nil, err
	}

	if len(states) > 0 {
		// Deep copy states to prevent external mutation
		statesCopy := make(map[string]string, len(states))
		maps.Copy(statesCopy, states)

		c.entries[key] = &stateCacheEntry{
			states:     statesCopy,
			lastAccess: time.Now(),
			ttl:        c.ttl,
		}
		c.log.V(2).Info("state cache set after fetch", "key", key, "state_count", len(statesCopy))
	}

	return states, nil
}
