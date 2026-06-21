# RDS Executor: Fix Missing Callback and Stale Message in Await Completion

**Date**: 2026-06-21  
**Status**: Approved  
**Issue**: hib-9tp  
**Related**: hib-bop (future message builder pattern)

## Problem Statement

Two bugs in RDS executor's `handleShutdownAwaitCompletion` and `handleWakeupAwaitCompletion` functions:

### Bug 1: Missing ReportStateCallback (Critical)

**Location**: `internal/executor/rds/rds.go:484`

When `handleShutdownAwaitCompletion` calls `Stop()` on pending resources (resources that needed to wait for state transition before stopping), it passes `nil` for the callback parameter:

```go
stopState, err := s.Stop(deadlineCtx, log, client, p.id, p.snapshotBefore, params, nil)
```

**Impact**: Restore data is never persisted for resources that were in transitional states (e.g., 'creating', 'backing-up'). On wakeup, these resources won't be restored because their state was never saved.

**Root Cause Analysis**: 
- The callback is thread-safe (accumulator uses `sync.Mutex` at `cmd/runner/state/accumulator.go:24,67-68`)
- The same callback is correctly passed in `processResources()` at line 348
- This was likely an oversight rather than intentional

### Bug 2: Stale Message (User Experience)

**Location**: `internal/executor/rds/rds.go:248-251`

The shutdown message is formatted BEFORE `handleShutdownAwaitCompletion` runs:

```go
msg := formatShutdownMessage(stats)
if params.AwaitCompletion.Enabled {
    msg = e.handleShutdownAwaitCompletion(ctx, log, client, params, msg, stats)
}
```

When pending resources successfully transition and stop during await, the message shows stale counts.

**Example Scenario**:
1. Initial discovery: 3 available resources + 1 creating resource
2. Initial stats: applied=3, pending=1
3. Message formatted: "stopped 3 RDS resource(s), pending 1 resource awaiting state transition(s)"
4. Await handler: creating → available → stop successfully
5. Stats updated: applied=4, pending=0
6. **But message still shows**: "pending 1 resource..." (stale!)

**Impact**: Confusing/inconsistent user feedback. Users see "pending 1 resource" but actually all resources stopped properly.

## Design Decision

Implement minimal surgical fixes for immediate issue resolution:

1. **Pass callback to Stop() calls in await handlers**
2. **Move message formatting to after await completion**

Future enhancement (hib-bop) will implement a message builder pattern for cleaner architecture.

## Solution Architecture

### Fix 1: Pass ReportStateCallback to Pending Resource Stop Calls

**Changes in `handleShutdownAwaitCompletion` (line 484)**:

```go
// Before
stopState, err := s.Stop(deadlineCtx, log, client, p.id, p.snapshotBefore, params, nil)

// After
stopState, err := s.Stop(deadlineCtx, log, client, p.id, p.snapshotBefore, params, spec.ReportStateCallback)
```

**Why this is safe**:
- The callback points to accumulator's `add()` method which uses `sync.Mutex`
- Each goroutine calls with unique resource keys (no key collisions)
- Same pattern already used successfully in `processResources()` line 348

**Parallel change in `handleWakeupAwaitCompletion`** (line 596):

```go
// Before
startState, err := s.Start(deadlineCtx, log, client, p.id, params)

// After - No callback needed for Start()
// (Start() doesn't take callback parameter - state already persisted from Shutdown)
```

Note: Start operations don't need callbacks because restore data was already captured during Shutdown.

### Fix 2: Move Message Formatting After Await Completion

**Changes in `Shutdown()` method (lines 248-251)**:

```go
// Before
msg := formatShutdownMessage(stats)
if params.AwaitCompletion.Enabled {
    msg = e.handleShutdownAwaitCompletion(ctx, log, client, params, msg, stats)
}

// After
var msg string
if params.AwaitCompletion.Enabled {
    msg = e.handleShutdownAwaitCompletion(ctx, log, client, params, stats)
} else {
    msg = formatShutdownMessage(stats)
}
```

**Signature change for `handleShutdownAwaitCompletion`**:

```go
// Before
func (e *Executor) handleShutdownAwaitCompletion(
    ctx context.Context, 
    log logr.Logger, 
    client RDSClient, 
    params Parameters, 
    msg string,  // Remove this
    stats *operationStats,
) string

// After
func (e *Executor) handleShutdownAwaitCompletion(
    ctx context.Context, 
    log logr.Logger, 
    client RDSClient, 
    params Parameters, 
    stats *operationStats,
) string
```

**Internal changes in `handleShutdownAwaitCompletion`**:

1. After `completionWg.Wait()` and stats updates (after line 527)
2. Call `msg := formatShutdownMessage(stats)` to get base message with accurate counts
3. Append supplementary info (failures, timeouts) as currently done (lines 535-547)
4. Return complete message

**Parallel changes in `WakeUp()` and `handleWakeupAwaitCompletion`**:

Apply identical pattern for consistency:
- Remove `msg` parameter from `handleWakeupAwaitCompletion`
- Format message after await completion and stats updates
- Same logic for base message + supplementary info

## Data Flow

### Scenario: Shutdown with Pending Resources

```
1. processResources() discovers resources:
   - 3 available → Stop() with callback → restore data saved ✓
   - 1 creating → marked pending
   - stats: applied=3, pending=1

2. handleShutdownAwaitCompletion():
   - Goroutine for pending resource:
     - WaitForAvailable() → resource becomes 'available'
     - Stop() with callback → restore data saved ✓
     - pendingApplied.Add(1)
     - WaitForStopped() → confirms stopped
   
   - After completionWg.Wait():
     - Update stats: applied += 1, pending -= 1
     - Final stats: applied=4, pending=0
   
   - Format message:
     - "stopped 4 RDS resource(s); all resources confirmed stopped"
     - Accurate final state ✓

3. Return accurate message to user
```

## Component Details

### Modified Functions

#### 1. `handleShutdownAwaitCompletion`

**Before**:
```go
func (e *Executor) handleShutdownAwaitCompletion(ctx context.Context, log logr.Logger, client RDSClient, params Parameters, msg string, stats *operationStats) string {
    // ... await logic ...
    
    // Update stats
    stats.applied += pendingCount
    stats.pending -= pendingCount
    
    // Append to pre-formatted message
    msg += fmt.Sprintf("; %d pending...", failedCount)
    return msg
}
```

**After**:
```go
func (e *Executor) handleShutdownAwaitCompletion(ctx context.Context, log logr.Logger, client RDSClient, params Parameters, stats *operationStats) string {
    // ... await logic ...
    
    // Update stats
    stats.applied += pendingCount
    stats.pending -= pendingCount
    
    // Format base message with accurate stats
    msg := formatShutdownMessage(stats)
    
    // Append supplementary info
    if failedCount > 0 {
        msg += fmt.Sprintf("; %d pending...", failedCount)
    }
    if timedOutCount > 0 {
        msg += fmt.Sprintf("; %d of %d resource(s) not yet stopped...", timedOutCount, totalOperations)
    } else if failedCount == 0 {
        msg += "; all resources confirmed stopped"
    }
    
    return msg
}
```

**Key changes**:
- Line 484: Pass `spec.ReportStateCallback` instead of `nil`
- Remove `msg` parameter from signature
- Call `formatShutdownMessage(stats)` after stats updates
- Append supplementary info to newly formatted message

#### 2. `handleWakeupAwaitCompletion`

Apply identical pattern:
- Line 596: No callback change needed (Start() doesn't persist state)
- Remove `msg` parameter
- Format message after stats updates
- Same structure for supplementary info

### Thread Safety Analysis

**Concurrent callback invocations**:
- ✓ Accumulator protects state map with `sync.Mutex`
- ✓ Each goroutine uses unique resource key (no collisions)
- ✓ Callback is immutable (function pointer doesn't change)

**Stats updates**:
- ✓ Atomic counters used during concurrent phase (`atomic.Int32`)
- ✓ Final stats update happens after `completionWg.Wait()` (single-threaded)
- ✓ Message formatting happens after all updates (single-threaded)

## Testing Strategy

### Verification (Manual)

1. **Test callback is passed correctly**:
   - Add debug logging in accumulator.add()
   - Trigger shutdown with pending resources
   - Verify callback invoked for pending resources

2. **Test message accuracy**:
   - Shutdown with pending resources
   - Verify final message shows accurate counts
   - No "pending X resource" if all stopped successfully

### Existing Test Coverage

All existing tests continue to pass without modification:
- Unit tests for `Stop()` already test with/without callback
- Wait tests verify timeout and success paths
- No behavioral changes beyond the bug fixes

### Future Test Improvements (hib-bop)

Integration tests to add (separate task):
- Pending resource → wait → stop → verify restore data saved
- Message accuracy with/without await completion
- Concurrent callback invocations with multiple pending resources

## Error Handling

No changes to error handling behavior:

1. **Callback errors**: Already logged non-fatally (instance_strategy.go:189)
2. **Wait failures**: Tracked in `failures` ErrorList, reflected in message
3. **Stop failures**: Propagated and tracked in stats.failed
4. **Stats updates**: Adjust pending/applied/failed counts appropriately

## Migration & Rollout

**Risk**: Low - surgical changes only
**Rollback**: Simple code revert if issues found
**Deployment**: Standard - no config changes or migrations needed

## Future Work (hib-bop)

The message builder pattern enhancement will:
- Eliminate string concatenation/modification
- Single source of truth for message formatting
- Easier to test and maintain
- More flexible for future enhancements

**Not included in this fix** (intentionally deferred):
- Message builder abstraction
- Structured logging enhancements
- Alternative output formats
- Testing improvements

## Acceptance Criteria

1. ✓ Restore data is persisted for pending resources that transition and stop during await
2. ✓ Shutdown message accurately reflects final state after await completion
3. ✓ All existing tests continue to pass
4. ✓ Thread safety is maintained (callback is thread-safe via mutex)
5. ✓ Same fixes applied to both Shutdown and WakeUp flows for consistency

## References

- Issue: hib-9tp (this bug fix)
- Follow-up: hib-bop (message builder pattern)
- Code: internal/executor/rds/rds.go
- Code: cmd/runner/state/accumulator.go (callback implementation)
- Code: internal/executor/rds/instance_strategy.go (Stop implementation)
- Code: internal/executor/rds/cluster_strategy.go (Stop implementation)
