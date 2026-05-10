/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package cache provides a unified cache implementation backed by an LRU.
// It supports two modes:
//
//   - Standard: entries expire after a fixed TTL from their last Add time.
//     Access via Get does NOT reset the TTL.
//   - Active: entries expire after a fixed TTL from their last access time.
//     Every Get resets the TTL, keeping frequently accessed entries alive.
//
// Both modes enforce a maximum size via LRU eviction. TTL-based expiry fires
// an optional OnEvict callback; LRU eviction is silent cleanup.
package cache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

// ErrDontCache is a sentinel error that can be returned from a GetOrFetch
// fetch function to indicate the result should be returned to the caller
// but not stored in the cache.
var ErrDontCache = errors.New("cache: do not cache this result")

// Config holds cache configuration.
type Config[K comparable, V any] struct {
	MaxSize       int
	TTL           time.Duration // 0 = no TTL
	Active        bool          // true = TTL resets on Get
	SweepInterval time.Duration
	OnEvict       func(key K, value V)
}

// Option configures a Cache.
type Option[K comparable, V any] func(*Config[K, V])

// WithMaxSize sets the maximum number of entries. When exceeded, the
// least-recently-used entry is evicted.
func WithMaxSize[K comparable, V any](size int) Option[K, V] {
	return func(c *Config[K, V]) {
		c.MaxSize = size
	}
}

// WithTTL sets the time-to-live for entries. When TTL expires, the entry
// is removed and the optional OnEvict callback is invoked.
func WithTTL[K comparable, V any](ttl time.Duration) Option[K, V] {
	return func(c *Config[K, V]) {
		c.TTL = ttl
	}
}

// WithActiveMode enables active caching: every Get resets the entry's TTL,
// keeping it alive as long as it is being accessed.
func WithActiveMode[K comparable, V any]() Option[K, V] {
	return func(c *Config[K, V]) {
		c.Active = true
	}
}

// WithSweepInterval sets the interval between background TTL sweeps.
// Default is 1 minute. Only used when TTL > 0.
func WithSweepInterval[K comparable, V any](d time.Duration) Option[K, V] {
	return func(c *Config[K, V]) {
		c.SweepInterval = d
	}
}

// WithOnEvict registers a callback invoked when an entry expires due to TTL.
// It is NOT invoked on LRU eviction (max-size overflow).
func WithOnEvict[K comparable, V any](fn func(K, V)) Option[K, V] {
	return func(c *Config[K, V]) {
		c.OnEvict = fn
	}
}

func defaults[K comparable, V any]() Config[K, V] {
	return Config[K, V]{
		MaxSize:       1000,
		SweepInterval: time.Minute,
	}
}

// call tracks an in-flight fetch for singleflight deduplication.
type call[V any] struct {
	wg  sync.WaitGroup
	val V
	err error
}

// flightGroup deduplicates concurrent fetches for the same key.
type flightGroup[K comparable, V any] struct {
	mu sync.Mutex
	m  map[K]*call[V]
}

func (g *flightGroup[K, V]) Do(key K, fn func() (V, error)) (v V, err error) {
	g.mu.Lock()
	if g.m == nil {
		g.m = make(map[K]*call[V])
	}
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}
	c := &call[V]{}
	c.wg.Add(1)
	g.m[key] = c
	g.mu.Unlock()

	// Ensure cleanup (wg.Done + map delete) happens even if fn panics.
	// Panics are converted to errors so waiters receive the failure
	// instead of blocking forever.
	defer func() {
		if r := recover(); r != nil {
			c.err = fmt.Errorf("panic in fetch: %v", r)
			v, err = c.val, c.err
		}
		c.wg.Done()
		g.mu.Lock()
		delete(g.m, key)
		g.mu.Unlock()
	}()

	c.val, c.err = fn()
	return c.val, c.err
}

// Cache is a size-bounded cache with optional TTL-based expiry.
type Cache[K comparable, V any] struct {
	cfg Config[K, V]

	// lru is the underlying LRU cache. Always present.
	// It is thread-safe.
	lru *lru.Cache[K, V]

	// lastAccessed tracks the last time each key was added or accessed.
	// Only non-nil when TTL > 0.
	lastAccessed map[K]time.Time
	mu           sync.RWMutex

	sweepTicker *time.Ticker
	stopCh      chan struct{}
	closeOnce   sync.Once
	wg          sync.WaitGroup
	flight      flightGroup[K, V]
}

// New creates a new Cache.
func New[K comparable, V any](opts ...Option[K, V]) (*Cache[K, V], error) {
	cfg := defaults[K, V]()
	for _, opt := range opts {
		opt(&cfg)
	}

	if cfg.MaxSize <= 0 {
		cfg.MaxSize = 1000
	}
	if cfg.SweepInterval <= 0 {
		cfg.SweepInterval = time.Minute
	}

	c := &Cache[K, V]{
		cfg: cfg,
	}

	var err error
	c.lru, err = lru.New[K, V](cfg.MaxSize)
	if err != nil {
		return nil, err
	}

	if cfg.TTL > 0 {
		c.lastAccessed = make(map[K]time.Time)
		c.stopCh = make(chan struct{})
		c.sweepTicker = time.NewTicker(cfg.SweepInterval)
		c.wg.Add(1)
		go c.sweepLoop()
	}

	return c, nil
}

// Add inserts a key-value pair into the cache.
// If the key already exists, its value is updated and it becomes most-recently-used.
func (c *Cache[K, V]) Add(key K, value V) {
	c.lru.Add(key, value)

	if c.lastAccessed != nil {
		c.mu.Lock()
		c.lastAccessed[key] = time.Now()
		c.mu.Unlock()
	}
}

// Get retrieves a value from the cache and marks it as most-recently-used.
// In active mode, it also resets the entry's TTL.
// If the entry has expired, it is removed and the zero value is returned.
func (c *Cache[K, V]) Get(key K) (V, bool) {
	v, ok := c.lru.Get(key)
	if !ok {
		var zero V
		return zero, false
	}

	if c.lastAccessed == nil {
		return v, true
	}

	c.mu.Lock()
	t, tracked := c.lastAccessed[key]
	if !tracked {
		c.mu.Unlock()
		var zero V
		return zero, false
	}

	if time.Since(t) > c.cfg.TTL {
		c.lru.Remove(key)
		delete(c.lastAccessed, key)
		if c.cfg.OnEvict != nil {
			c.cfg.OnEvict(key, v)
		}
		c.mu.Unlock()
		var zero V
		return zero, false
	}

	if c.cfg.Active {
		c.lastAccessed[key] = time.Now()
	}
	c.mu.Unlock()
	return v, true
}

// GetOrFetch atomically retrieves a value from the cache, or if not present,
// calls fetchFunc to retrieve it and stores the result. The context is passed
// to fetchFunc so it can respect cancellation and deadlines.
//
// Concurrent calls for the same key are deduplicated: only one fetchFunc
// invocation occurs and all waiters receive the same result.
//
// If fetchFunc returns ErrDontCache, the result is returned but not cached.
func (c *Cache[K, V]) GetOrFetch(ctx context.Context, key K, fetchFunc func(context.Context) (V, error)) (V, error) {
	if v, ok := c.Get(key); ok {
		return v, nil
	}

	return c.flight.Do(key, func() (V, error) {
		if v, ok := c.Get(key); ok {
			return v, nil
		}
		fetched, err := fetchFunc(ctx)
		if err == ErrDontCache {
			return fetched, nil
		}
		if err != nil {
			var zero V
			return zero, err
		}
		c.Add(key, fetched)
		return fetched, nil
	})
}

// Peek retrieves a value without updating its recency or TTL.
func (c *Cache[K, V]) Peek(key K) (V, bool) {
	return c.lru.Peek(key)
}

// Remove deletes a key from the cache.
func (c *Cache[K, V]) Remove(key K) bool {
	removed := c.lru.Remove(key)
	if removed && c.lastAccessed != nil {
		c.mu.Lock()
		delete(c.lastAccessed, key)
		c.mu.Unlock()
	}
	return removed
}

// Contains reports whether the cache contains the given key without
// affecting recency or TTL. Expired entries are reported as not present.
func (c *Cache[K, V]) Contains(key K) bool {
	if c.lastAccessed == nil {
		return c.lru.Contains(key)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.lru.Contains(key) {
		return false
	}

	t, tracked := c.lastAccessed[key]
	if !tracked {
		return false
	}

	if time.Since(t) > c.cfg.TTL {
		if v, ok := c.lru.Peek(key); ok {
			c.lru.Remove(key)
			delete(c.lastAccessed, key)
			if c.cfg.OnEvict != nil {
				c.cfg.OnEvict(key, v)
			}
		}
		return false
	}

	return true
}

// Len returns the number of entries in the cache.
func (c *Cache[K, V]) Len() int {
	return c.lru.Len()
}

// Close stops the background sweep goroutine.
// It is safe to call multiple times.
func (c *Cache[K, V]) Close() {
	if c.stopCh == nil {
		return
	}
	c.closeOnce.Do(func() {
		close(c.stopCh)
		c.wg.Wait()
		c.sweepTicker.Stop()
	})
}

func (c *Cache[K, V]) sweepLoop() {
	defer c.wg.Done()
	for {
		select {
		case <-c.sweepTicker.C:
			c.sweep()
		case <-c.stopCh:
			return
		}
	}
}

func (c *Cache[K, V]) sweep() {
	cutoff := time.Now().Add(-c.cfg.TTL)

	c.mu.Lock()
	defer c.mu.Unlock()

	for k, t := range c.lastAccessed {
		if !c.lru.Contains(k) {
			// Already evicted by LRU; clean up tracking.
			delete(c.lastAccessed, k)
			continue
		}
		if t.Before(cutoff) {
			if v, ok := c.lru.Peek(k); ok {
				c.lru.Remove(k)
				delete(c.lastAccessed, k)
				if c.cfg.OnEvict != nil {
					c.cfg.OnEvict(k, v)
				}
			}
		}
	}
}
