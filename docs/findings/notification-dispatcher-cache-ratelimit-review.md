# Notification Subsystem Review: Dispatcher, Cache, and Slack Rate Limiting

**Date:** 2026-05-07  
**Scope:** `internal/notification/` — dispatcher, cache implementation, and Slack sink rate limit handling  
**Reviewers:** AI-assisted code review

---

## 1. Dispatcher Architecture

### What's Working Well

- **Per-stream FIFO ordering via `keyedworker.Pool`** eliminates the classic race between `ExecutionProgress` and `Success` events for the same plan/cycle. The test suite verifies this deterministically across 10 iterations.
- **Fire-and-forget `Submit`** is truly non-blocking; overflow requests are dropped with metrics rather than blocking the reconciler.
- **Graceful shutdown** drains in-flight + buffered items per-stream with a 30s deadline, backed by atomic worker counting.
- **Worker idle TTL (30m)** prevents goroutine leaks while keeping hot streams responsive.

### Areas of Concern

#### A. State fetch blocks under write lock

`SinkStateCache.GetOrFetch` acquires `c.mu.Lock()` for the entire duration of the API server fetch:

```go
c.mu.Lock()
defer c.mu.Unlock()
// ... fetchFunc(ctx) runs here while holding the lock ...
```

**Impact:** If the API server is slow (~100ms), all cache operations for *all* streams are serialized. In a busy cluster, this creates a head-of-line blocking problem.

**Recommendation:** Consider a single-flight pattern (e.g., `sync.Map` of `sync.Once` or a `future` pattern) where the fetch happens outside the lock and only the result insertion is guarded.

---

#### B. Empty states are not cached

`GetOrFetch` skips caching when `len(states) == 0`:

```go
if len(states) > 0 {
    // cache it
}
```

**Impact:** Every notification for a new plan/cycle triggers an API server GET, even though "no state" is a valid and stable initial condition. This creates unnecessary load on the API server.

**Recommendation:** Cache empty results with a sentinel value or a boolean flag to indicate "fetched but empty."

---

#### C. `dispatchTimeout` (90s) is a hard ceiling for *everything*

The timeout wraps `resolveSecret`, `resolveSinkState` (potentially with API fetch), template resolution, and the actual HTTP dispatch. A slow state fetch can leave only a few seconds for the actual Slack API call.

**Recommendation:** Add a dedicated, shorter sub-timeout for `resolveSinkState` (e.g., 5s) so that cache misses don't starve the actual delivery.

---

#### D. `reportDelivery` calls the callback synchronously

The `deliveryCallback` (which persists status back to the API server) runs inline in the worker goroutine:

```go
func (d *Dispatcher) reportDelivery(...) {
    d.stateCache.Set(...)          // fast (memory)
    d.deliveryCallback(...)        // slow (API server write)
}
```

**Impact:** Slow status updates block the per-stream worker, delaying subsequent notifications in the same stream.

**Recommendation:** Queue delivery results to a small buffered channel and process them asynchronously, or document explicitly that callbacks must be non-blocking.

---

## 2. Cache Implementation (Idempotency & Reliability)

### What's Working Well

- **Touch-on-read TTL** keeps hot entries alive without explicit renewal.
- **Deep copy** on `Set` and `GetOrFetch` prevents external mutation of cached maps.
- **Background eviction** prevents memory leaks from abandoned entries.
- **Atomic fetch semantics** in `GetOrFetch` guarantee exactly one API call even under concurrent load (verified in `TestSinkStateCache_GetOrFetch_Concurrent`).

### Areas of Concern

#### A. Unbounded memory growth

`SinkStateCache` has no maximum size limit. With a 10-minute TTL and high plan churn, memory can grow significantly before eviction catches up.

**Recommendation:** Add a `maxEntries` limit with LRU eviction (or simple random eviction) as a safety valve. The current `evictExpired` only removes stale entries, not entries when the cache is "full."

---

#### B. `Get` does lazy deletion but `GetOrFetch` does not

`Get` deletes expired entries:

```go
if entry.isExpired(now) {
    delete(c.entries, key)
    return nil, false
}
```

But `GetOrFetch` leaves expired entries in place if another goroutine wins the race:

```go
entry, exists = c.entries[key]
if exists && !entry.isExpired(now) {
    entry.touch(now)
    return entry.states, nil
}
// If expired and another goroutine fetches first, we don't delete the stale entry
```

**Impact:** Minor — the stale entry will be replaced by the fetcher, but if the fetch fails, the expired entry remains.

---

#### C. No cache metrics

There are no Prometheus metrics for cache hits, misses, or fetches. This makes it hard to tune TTL or detect that the cache is ineffective.

**Recommendation:** Add counters for `state_cache_hit_total`, `state_cache_miss_total`, and `state_cache_fetch_total`.

---

#### D. Eviction goroutine lifecycle

`StartEvictionLoop` launches a goroutine with no way to wait for its exit. In unit tests, this can leak goroutines.

**Recommendation:** Return a `stopCh` or use `sync.WaitGroup` so tests can verify clean shutdown.

---

## 3. Slack Rate Limit Handling

### What's Working Well

- **Two-layer defense:** Client-side token-bucket rate limiting (`ratelimit.Transport`) proactively spaces requests, while `go-retryablehttp` handles reactive 429 retries with `Retry-After` support.
- **Per-key isolation:** Different webhook URLs and bot tokens get independent limiters. Verified in tests (`TestSendRateLimiting_DifferentKeys`).
- **Context-aware waits:** Rate limit waits respect context cancellation (verified in `TestSlackSink_RateLimiting_ContextCancellation`).
- **Thread-mode coverage:** All Web API calls (post, update, reactions) go through the same rate-limited HTTP transport.

### Areas of Concern

#### A. Double token consumption on retry

When Slack returns 429, `go-retryablehttp` retries after the backoff. However, the rate limiter already consumed a token for the initial request, and will consume another for the retry. A single "logical" notification can thus consume 2+ tokens.

**Impact:** Under pressure near the rate limit, this causes more aggressive throttling than mathematically necessary.

**Recommendation:** This is a hard problem to solve perfectly without coupling the rate limiter to the retry layer. A pragmatic mitigation is to ensure burst is sized generously (currently default 10) to absorb retries. Document this behavior explicitly.

---

#### B. Rate limit config re-registered on every `Send`

```go
if s.rateLimitRegistry != nil {
    key := s.extractRateLimitKey(cfg)
    s.useRateLimitConfig(key, cfg.RateLimit)
    ctx = ratelimit.WithContext(ctx, key)
}
```

The `Register` call is idempotent but still acquires a mutex and does struct comparison on every notification. For high-throughput scenarios, this is unnecessary overhead.

**Recommendation:** Cache the registered key/config in the `Sink` struct and only re-register when the config changes.

---

#### C. Default rate limits may be too aggressive for Web API

Defaults are 2 RPS / 50 RPM / burst 10. Slack's Web API tiered rate limits vary by method:

- `chat.postMessage`: ~1/sec per channel, ~50/min per app
- `chat.update`: same
- `reactions.add`: ~100+/min

With thread mode firing 3–4 API calls per notification, 2 RPS could be conservative for a single channel but too aggressive for a shared workspace token across many channels.

**Recommendation:** Consider separate rate limit defaults for channel vs. thread mode, or document that users should tune `rate_limit` based on their Slack app tier.

---

#### D. Missing warning when rate limit key is empty

If `extractRateLimitKey` returns `""` (e.g., unknown delivery mode), rate limiting is silently skipped:

```go
key := s.extractRateLimitKey(cfg)
// key could be ""
s.useRateLimitConfig(key, cfg.RateLimit)
ctx = ratelimit.WithContext(ctx, key)
```

The transport silently bypasses rate limiting for empty keys.

**Recommendation:** Add a warning log when `key == ""` but a registry is configured.

---

#### E. Reaction operations share the same limiter

Thread mode uses `bot_token` as the rate limit key. Reactions, root updates, and replies all share one token bucket. Slack applies different rate limits to different methods, but the current implementation conservatively throttles all equally.

**Impact:** Safe but potentially slower than necessary. A burst of 10 is usually sufficient for a single notification's lifecycle.

---

## Summary & Priority Recommendations

| Priority | Area | Action |
|---|---|---|
| **High** | Cache lock contention | Move fetch outside write lock in `GetOrFetch` |
| **High** | Cache empty results | Cache "no state" to avoid repeated API calls |
| **Medium** | Cache bounds | Add `maxEntries` limit with eviction |
| **Medium** | Delivery callback | Document as non-blocking, or make async |
| **Medium** | Rate limit overhead | Cache registered keys to avoid re-registration |
| **Low** | Metrics | Add cache hit/miss metrics |
| **Low** | Double token consumption | Document burst sizing guidance |

## Overall Assessment

The notification architecture is solid and the test coverage is excellent. The main risks are operational: cache lock contention under load and API server load from uncached empty states. The two-layer rate limit defense (proactive token bucket + reactive retry) is well-designed for Slack's API behavior.
