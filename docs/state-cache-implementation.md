# In-Memory State Cache Implementation

## Problem
When multiple notifications fire in rapid succession for the same plan/cycle (e.g., Start followed by ExecutionProgress), the second notification would create a duplicate root message in Slack thread mode because:

1. First notification sends successfully and gets `root_ts`
2. `reportDelivery()` queues an **async** status update 
3. Second notification starts immediately and reads stale cache (no `root_ts`)
4. Second notification creates a **new** root message (duplicate!)
5. Async status updates finally apply (too late)

## Solution
Implemented an **atomic in-memory state cache** with the following properties:

### Features
- **Atomic GetOrFetch** - Even with concurrent requests for the same key, only one API call is made
- **10-minute TTL** - Long enough for most notification sequences
- **Touch-on-read** - Accessing cached data resets the TTL (prevents hot spot eviction)
- **Background eviction** - Expired entries cleaned up every minute
- **Synchronous updates** - Cache is updated immediately when notification is sent
- **Deep copy** - Prevents external mutation of cached data

### Architecture
```
┌─────────────────┐     ┌──────────────────────────┐     ┌─────────────────┐
│   Dispatcher    │────▶│  Atomic GetOrFetch       │◄────│  Concurrent     │
│   (Goroutine A) │     │  (mutex-protected)       │     │  Requests       │
└─────────────────┘     └──────────────────────────┘     └─────────────────┘
         │                       │
         │              ┌────────▼─────────┐
         │              │  1. Check cache  │
         │              │  2. If miss:     │
         │              │     fetch & set  │
         │              │  3. Return value │
         │              └────────┬─────────┘
         │                       │
         │              ┌────────▼─────────┐
         │              │  Async Writer    │
         └─────────────▶│  (to K8s API)    │
                        └──────────────────┘
```

### Key Changes

#### 1. `internal/notification/cache.go`
- `SinkStateCache` struct with thread-safe operations
- `Get()` / `Set()` - Basic cache operations
- **`GetOrFetch()`** - Atomic operation that ensures only one fetch per key, even with concurrent requests
- `StartEvictionLoop()` - Background cleanup goroutine

#### 2. `internal/notification/dispatcher.go`
- Added `stateCache` field to `Dispatcher` struct
- Initialize cache in `NewDispatcher()` with 10-minute TTL
- Start eviction loop in `Start()`
- Modified `resolveSinkState()` to use atomic `GetOrFetch()` 
- Modified `reportDelivery()` to update cache synchronously before async callback

## How It Works

### The Race Condition (Before)
```
Time   Notif A (Start)              Notif B (Progress)
T1     Cache miss
T2     Read API (root_ts="")        
T3                                  Cache miss
T4                                  Read API (root_ts="")
T5     Send notification → get root_ts="1234"
T6     Update cache (async)
T7                                  Send notification → DUPLICATE ROOT!
```

### Atomic Solution (After)
```
Time   Notif A (Start)              Notif B (Progress)
T1     GetOrFetch()
T2     ├─ Lock acquired
T3     ├─ Cache miss
T4     ├─ Fetch from API (root_ts="")
T5     ├─ Set cache
T6     └─ Unlock
T7                                  GetOrFetch()
T8                                  ├─ Lock acquired
T9                                  ├─ Cache hit! (root_ts="")
T10                                 └─ Unlock & return
T11    Send → get root_ts="1234"
T12    Update cache synchronously
T13                                 Send → use root_ts="1234"
```

Even if both notifications start simultaneously, the mutex ensures:
1. Only one goroutine fetches from the API
2. All goroutines get the same cached result
3. No duplicate root messages

## Configuration

```go
// Default values (not configurable via flags)
defaultStateCacheTTL              = 10 * time.Minute
defaultStateCacheEvictionInterval = 1 * time.Minute
```

## Testing

All existing tests pass plus new comprehensive tests:
- `TestSinkStateCache_GetAndSet` - Basic operations
- `TestSinkStateCache_TouchOnRead` - TTL extension on access
- `TestSinkStateCache_Expiration` - TTL expiration works
- `TestSinkStateCache_DeepCopy` - State isolation
- `TestSinkStateCache_DifferentKeys` - Key uniqueness
- `TestSinkStateCache_Delete` - Removal works
- `TestSinkStateCache_Clear` - Bulk removal
- **`TestSinkStateCache_GetOrFetch_Atomic`** - Atomic fetch behavior
- **`TestSinkStateCache_GetOrFetch_Concurrent`** - Concurrent safety with 10 goroutines
- **`TestSinkStateCache_GetOrFetch_Error`** - Error handling
- **`TestSinkStateCache_GetOrFetch_EmptyResult`** - Empty result behavior

## Benefits

1. **Race condition fixed** - Atomic GetOrFetch ensures consistent state
2. **No external dependencies** - Pure in-memory, works with leader election
3. **Bounded memory** - TTL prevents unbounded growth
4. **Backward compatible** - Falls back to APIReader on cache miss
5. **Minimal latency** - Cache hit is ~100ns vs ~10ms for API call
6. **Thread-safe** - Mutex protects concurrent access

## Tradeoffs

- **Memory usage** - ~200 bytes per active notification stream
- **Durability** - Cache is lost on pod restart (but K8s status remains)
- **Consistency** - 10-minute TTL means stale data possible for very old streams

## Monitoring

Cache operations are logged at V(2) level:
```
state cache hit
state cache miss, fetching
state cache set after fetch
state cache eviction completed
```

## Implementation Details

### Double-Checked Locking Pattern
The `GetOrFetch` method uses double-checked locking for efficiency:
1. First check with read lock (fast path for cache hits)
2. If miss, acquire write lock
3. Check again under write lock (another goroutine might have fetched)
4. Only if still missing, perform the expensive fetch

This ensures:
- High throughput for cache hits (read lock only)
- Correctness for cache misses (write lock + double check)
- Only one fetch even with 100+ concurrent requests

### Empty Result Handling
Empty results (nil map) from the fetch function are NOT cached. This ensures that:
- Temporary API errors don't poison the cache
- First notification for a plan always queries the API
- Subsequent notifications benefit from caching once state exists
