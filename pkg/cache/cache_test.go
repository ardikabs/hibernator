/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package cache

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheBasicOperations(t *testing.T) {
	c, err := New(WithMaxSize[string, int](3))
	require.NoError(t, err)
	defer c.Close()

	c.Add("a", 1)
	c.Add("b", 2)

	v, ok := c.Get("a")
	require.True(t, ok)
	assert.Equal(t, 1, v)

	v, ok = c.Peek("b")
	require.True(t, ok)
	assert.Equal(t, 2, v)

	assert.True(t, c.Contains("a"))
	assert.False(t, c.Contains("z"))

	assert.Equal(t, 2, c.Len())

	removed := c.Remove("a")
	assert.True(t, removed)
	assert.False(t, c.Contains("a"))
	assert.Equal(t, 1, c.Len())
}

func TestCacheLRUEviction(t *testing.T) {
	c, err := New(WithMaxSize[string, int](2))
	require.NoError(t, err)
	defer c.Close()

	c.Add("a", 1)
	c.Add("b", 2)
	c.Add("c", 3) // should evict "a" (least recently used)

	assert.False(t, c.Contains("a"), "oldest entry should be evicted")
	assert.True(t, c.Contains("b"))
	assert.True(t, c.Contains("c"))
	assert.Equal(t, 2, c.Len())
}

func TestCacheLRURecencyUpdate(t *testing.T) {
	c, err := New(WithMaxSize[string, int](2))
	require.NoError(t, err)
	defer c.Close()

	c.Add("a", 1)
	c.Add("b", 2)

	// Access "a" to make it most-recently-used
	c.Get("a")

	// Add "c" — should evict "b" (now least recently used)
	c.Add("c", 3)

	assert.True(t, c.Contains("a"), "accessed entry should survive")
	assert.False(t, c.Contains("b"), "least recently used should be evicted")
	assert.True(t, c.Contains("c"))
}

func TestCacheStandardTTLExpiry(t *testing.T) {
	var evictedKey string
	var evictedValue int

	c, err := New(
		WithMaxSize[string, int](10),
		WithTTL[string, int](100*time.Millisecond),
		WithSweepInterval[string, int](50*time.Millisecond),
		WithOnEvict(func(k string, v int) {
			evictedKey = k
			evictedValue = v
		}),
	)
	require.NoError(t, err)
	defer c.Close()

	c.Add("a", 42)
	require.True(t, c.Contains("a"))

	// Wait for TTL to expire + sweep to run
	time.Sleep(200 * time.Millisecond)

	assert.False(t, c.Contains("a"), "entry should expire after TTL")
	assert.Equal(t, "a", evictedKey)
	assert.Equal(t, 42, evictedValue)
}

func TestCacheStandardTTLGetDoesNotReset(t *testing.T) {
	c, err := New(
		WithMaxSize[string, int](10),
		WithTTL[string, int](100*time.Millisecond),
		WithSweepInterval[string, int](50*time.Millisecond),
	)
	require.NoError(t, err)
	defer c.Close()

	c.Add("a", 42)

	// Access repeatedly within TTL — but standard mode does NOT reset TTL
	time.Sleep(60 * time.Millisecond)
	c.Get("a")
	time.Sleep(60 * time.Millisecond)
	c.Get("a")

	// Total elapsed: ~120ms > TTL 100ms
	time.Sleep(50 * time.Millisecond)
	assert.False(t, c.Contains("a"), "entry should expire even though it was accessed")
}

func TestCacheActiveTTLResetsOnGet(t *testing.T) {
	c, err := New(
		WithMaxSize[string, int](10),
		WithTTL[string, int](100*time.Millisecond),
		WithSweepInterval[string, int](50*time.Millisecond),
		WithActiveMode[string, int](),
	)
	require.NoError(t, err)
	defer c.Close()

	c.Add("a", 42)

	// Access within TTL — active mode resets TTL
	time.Sleep(60 * time.Millisecond)
	c.Get("a")

	// Now TTL is reset; wait another 60ms (total ~120ms from Add, but only 60ms from last Get)
	time.Sleep(60 * time.Millisecond)
	assert.True(t, c.Contains("a"), "active mode should reset TTL on Get")

	// Wait for full TTL after last access
	time.Sleep(150 * time.Millisecond)
	assert.False(t, c.Contains("a"), "entry should expire after idle TTL")
}

func TestCacheActiveTTLPeekDoesNotReset(t *testing.T) {
	c, err := New(
		WithMaxSize[string, int](10),
		WithTTL[string, int](100*time.Millisecond),
		WithSweepInterval[string, int](50*time.Millisecond),
		WithActiveMode[string, int](),
	)
	require.NoError(t, err)
	defer c.Close()

	c.Add("a", 42)

	// Peek does not reset TTL even in active mode
	time.Sleep(60 * time.Millisecond)
	c.Peek("a")
	time.Sleep(60 * time.Millisecond)
	c.Peek("a")

	// Total elapsed: ~120ms > TTL 100ms
	time.Sleep(50 * time.Millisecond)
	assert.False(t, c.Contains("a"), "Peek should not reset TTL in active mode")
}

func TestCacheLRUEvictionDoesNotFireOnEvict(t *testing.T) {
	var evictCount atomic.Int32

	c, err := New(
		WithMaxSize[string, int](2),
		WithTTL[string, int](1*time.Hour), // long TTL so expiry won't happen
		WithSweepInterval[string, int](1*time.Hour),
		WithOnEvict(func(k string, v int) {
			evictCount.Add(1)
		}),
	)
	require.NoError(t, err)
	defer c.Close()

	c.Add("a", 1)
	c.Add("b", 2)
	c.Add("c", 3) // evicts "a" due to max size

	// Give sweep a chance to run (it shouldn't, interval is 1h, but just in case)
	time.Sleep(10 * time.Millisecond)

	assert.Equal(t, int32(0), evictCount.Load(), "OnEvict should not fire on LRU eviction")
}

func TestCacheSweepCleansUpDeadKeys(t *testing.T) {
	c, err := New(
		WithMaxSize[string, int](2),
		WithTTL[string, int](1*time.Hour),
		WithSweepInterval[string, int](50*time.Millisecond),
	)
	require.NoError(t, err)
	defer c.Close()

	c.Add("a", 1)
	c.Add("b", 2)
	c.Add("c", 3) // evicts "a" via LRU

	// Sweep should clean up the dead tracking entry for "a"
	time.Sleep(100 * time.Millisecond)

	// If the dead key wasn't cleaned up, adding more entries might behave oddly.
	// The main assertion is that Len() is correct and no panic occurs.
	assert.Equal(t, 2, c.Len())
}

func TestCacheCloseIsSafe(t *testing.T) {
	c, err := New(
		WithMaxSize[string, int](10),
		WithTTL[string, int](100*time.Millisecond),
	)
	require.NoError(t, err)

	c.Close()
	c.Close() // should not panic

	// Operations after close should still work (LRU is still valid)
	c.Add("a", 1)
	assert.True(t, c.Contains("a"))
}

func TestCacheDefaultMaxSize(t *testing.T) {
	c, err := New[int, int]()
	require.NoError(t, err)
	defer c.Close()

	// Default max size is 1000; adding 1001 should evict the oldest
	for i := 0; i < 1001; i++ {
		c.Add(i, i)
	}
	assert.Equal(t, 1000, c.Len())
	assert.False(t, c.Contains(0), "oldest entry should be evicted")
}

func TestCacheGetOrFetch(t *testing.T) {
	c, err := New(WithMaxSize[string, int](10))
	require.NoError(t, err)
	defer c.Close()

	fetchCount := 0
	val, err := c.GetOrFetch(context.Background(), "a", func(ctx context.Context) (int, error) {
		fetchCount++
		return 42, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 42, val)
	assert.Equal(t, 1, fetchCount)

	// Second call should hit cache
	val, err = c.GetOrFetch(context.Background(), "a", func(ctx context.Context) (int, error) {
		fetchCount++
		return 99, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 42, val) // still 42, not 99
	assert.Equal(t, 1, fetchCount)
}

func TestCacheGetOrFetch_Concurrent(t *testing.T) {
	c, err := New(WithMaxSize[string, int](10))
	require.NoError(t, err)
	defer c.Close()

	var fetchCount atomic.Int32

	var wg sync.WaitGroup
	results := make([]int, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			v, err := c.GetOrFetch(context.Background(), "a", func(ctx context.Context) (int, error) {
				fetchCount.Add(1)
				time.Sleep(50 * time.Millisecond)
				return 42, nil
			})
			require.NoError(t, err)
			results[idx] = v
		}(i)
	}
	wg.Wait()

	assert.Equal(t, int32(1), fetchCount.Load(), "only one fetch should occur")
	for i := 0; i < 10; i++ {
		assert.Equal(t, 42, results[i])
	}
}

func TestCacheGetOrFetch_ErrDontCache(t *testing.T) {
	c, err := New(WithMaxSize[string, int](10))
	require.NoError(t, err)
	defer c.Close()

	fetchCount := 0
	val, err := c.GetOrFetch(context.Background(), "a", func(ctx context.Context) (int, error) {
		fetchCount++
		return 42, ErrDontCache
	})
	require.NoError(t, err)
	assert.Equal(t, 42, val)
	assert.Equal(t, 1, fetchCount)

	// Should refetch because it wasn't cached
	val, err = c.GetOrFetch(context.Background(), "a", func(ctx context.Context) (int, error) {
		fetchCount++
		return 99, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 99, val)
	assert.Equal(t, 2, fetchCount)
}

func TestCacheGetOrFetch_PropagatesContext(t *testing.T) {
	c, err := New(WithMaxSize[string, int](10))
	require.NoError(t, err)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = c.GetOrFetch(ctx, "a", func(ctx context.Context) (int, error) {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
			return 42, nil
		}
	})
	require.ErrorIs(t, err, context.Canceled)
}

func TestCacheGetOrFetch_PanicRecovery(t *testing.T) {
	c, err := New(WithMaxSize[string, int](10))
	require.NoError(t, err)
	defer c.Close()

	// Direct caller gets an error, not a propagated panic.
	_, err = c.GetOrFetch(context.Background(), "a", func(ctx context.Context) (int, error) {
		panic("fetch explosion")
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "panic in fetch")
	assert.Contains(t, err.Error(), "fetch explosion")

	// The entry must not have been cached.
	assert.False(t, c.Contains("a"))

	// A subsequent call should start a fresh fetch (flightGroup map cleaned up).
	fetchCount := 0
	val, err := c.GetOrFetch(context.Background(), "a", func(ctx context.Context) (int, error) {
		fetchCount++
		return 42, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 42, val)
	assert.Equal(t, 1, fetchCount)
}

func TestCacheGetOrFetch_PanicWaiters(t *testing.T) {
	c, err := New(WithMaxSize[string, int](10))
	require.NoError(t, err)
	defer c.Close()

	started := make(chan struct{})
	var wg sync.WaitGroup

	// Leader goroutine starts a fetch that will panic.
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := c.GetOrFetch(context.Background(), "a", func(ctx context.Context) (int, error) {
			close(started)
			time.Sleep(50 * time.Millisecond)
			panic("boom")
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "panic in fetch")
	}()

	// Wait until the leader has registered the in-flight call.
	<-started

	// Waiter goroutine should join the same flight and also receive the error.
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := c.GetOrFetch(context.Background(), "a", func(ctx context.Context) (int, error) {
			// This fetch must NOT run because the flight is already in progress.
			t.Error("waiter should not trigger a second fetch")
			return 0, nil
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "panic in fetch")
	}()

	wg.Wait()

	// After cleanup, a new fetch should succeed.
	val, err := c.GetOrFetch(context.Background(), "a", func(ctx context.Context) (int, error) {
		return 42, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 42, val)
}

func TestCacheGetExpiresOnRead(t *testing.T) {
	c, err := New(
		WithMaxSize[string, int](10),
		WithTTL[string, int](100*time.Millisecond),
	)
	require.NoError(t, err)
	defer c.Close()

	c.Add("a", 42)
	require.True(t, c.Contains("a"))

	time.Sleep(150 * time.Millisecond)

	_, ok := c.Get("a")
	assert.False(t, ok, "Get should return false for expired entries")
	assert.False(t, c.Contains("a"), "Contains should return false for expired entries")
	assert.Equal(t, 0, c.Len(), "expired entry should be removed")
}
