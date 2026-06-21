# RDS Executor Await Completion Bugs - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix missing callback and stale message bugs in RDS executor's await completion handlers.

**Architecture:** Pass `spec.ReportStateCallback` to Stop() calls in await handlers (thread-safe via mutex). Move message formatting to after await completion when stats are finalized. Apply pattern to both Shutdown and WakeUp flows.

**Tech Stack:** Go, AWS SDK v2, RDS executor

**Related Issues:** hib-9tp (bug fix), hib-bop (future message builder pattern)

---

## File Structure

**Modified files:**
- `internal/executor/rds/rds.go` - Main executor with Shutdown/WakeUp and await handlers
  - Fix callback passing in handleShutdownAwaitCompletion (line 484)
  - Fix callback passing in handleWakeupAwaitCompletion (line 596)
  - Move message formatting after await completion (lines 248-251, 299-302)
  - Update handler signatures to remove `msg` parameter

**Test files:**
- `internal/executor/rds/rds_test.go` - Existing unit tests (should pass without changes)
- `internal/executor/rds/rds_wait_test.go` - Existing wait tests (should pass without changes)

---

### Task 1: Fix Missing Callback in handleShutdownAwaitCompletion

**Files:**
- Modify: `internal/executor/rds/rds.go:484`

- [ ] **Step 1: Read current implementation**

Read `internal/executor/rds/rds.go` lines 470-500 to understand the context around the Stop() call.

Expected: See the goroutine that processes pending resources with Stop() call at line 484.

- [ ] **Step 2: Update Stop() call to pass callback**

Edit line 484 from:
```go
stopState, err := s.Stop(deadlineCtx, log, client, p.id, p.snapshotBefore, params, nil)
```

To:
```go
stopState, err := s.Stop(deadlineCtx, log, client, p.id, p.snapshotBefore, params, spec.ReportStateCallback)
```

- [ ] **Step 3: Verify callback is available in scope**

Check that `spec` variable is available in the `handleShutdownAwaitCompletion` function scope.

Expected: The function should have access to `spec` parameter or need to add it to the signature.

- [ ] **Step 4: Update function signature if needed**

If `spec` is not available, update the function signature at line 444:

From:
```go
func (e *Executor) handleShutdownAwaitCompletion(ctx context.Context, log logr.Logger, client RDSClient, params Parameters, msg string, stats *operationStats) string {
```

To:
```go
func (e *Executor) handleShutdownAwaitCompletion(ctx context.Context, log logr.Logger, client RDSClient, params Parameters, msg string, stats *operationStats, callback executor.ReportStateCallback) string {
```

And update the call site at line 250:
```go
msg = e.handleShutdownAwaitCompletion(ctx, log, client, params, msg, stats, spec.ReportStateCallback)
```

- [ ] **Step 5: Run existing tests to verify no breakage**

Run: `go test ./internal/executor/rds/... -v -run TestExecutor`

Expected: All tests pass (behavior unchanged, just adding callback)

- [ ] **Step 6: Commit the callback fix**

```bash
git add internal/executor/rds/rds.go
git commit -m "fix(rds): pass ReportStateCallback to Stop in handleShutdownAwaitCompletion

Fixes missing restore data persistence for pending resources.
Callback is thread-safe (uses sync.Mutex in accumulator).

Related: hib-9tp"
```

---

### Task 2: Fix Missing Callback in handleWakeupAwaitCompletion

**Files:**
- Modify: `internal/executor/rds/rds.go:596`

- [ ] **Step 1: Read current implementation**

Read `internal/executor/rds/rds.go` lines 582-612 to understand the wakeup pending resource handling.

Expected: See the goroutine that processes pending resources with Start() call at line 596.

- [ ] **Step 2: Verify Start() signature**

Check the Start() method signature in instance_strategy.go and cluster_strategy.go.

Expected: Start() does NOT take a callback parameter (state already persisted from Shutdown).

- [ ] **Step 3: Confirm no callback needed**

Document in code comment why no callback is needed for Start():

Add comment above line 596:
```go
// Note: Start() doesn't take callback - restore data was already captured during Shutdown
startState, err := s.Start(deadlineCtx, log, client, p.id, params)
```

- [ ] **Step 4: Commit the documentation**

```bash
git add internal/executor/rds/rds.go
git commit -m "docs(rds): clarify why Start() doesn't need callback

Start operations don't persist state - data was already captured
during Shutdown phase.

Related: hib-9tp"
```

---

### Task 3: Refactor handleShutdownAwaitCompletion Signature

**Files:**
- Modify: `internal/executor/rds/rds.go:444,250`

- [ ] **Step 1: Update function signature**

Change the signature at line 444:

From:
```go
func (e *Executor) handleShutdownAwaitCompletion(ctx context.Context, log logr.Logger, client RDSClient, params Parameters, msg string, stats *operationStats, callback executor.ReportStateCallback) string {
```

To:
```go
func (e *Executor) handleShutdownAwaitCompletion(ctx context.Context, log logr.Logger, client RDSClient, params Parameters, stats *operationStats, callback executor.ReportStateCallback) string {
```

Remove the `msg string` parameter.

- [ ] **Step 2: Update function to format message internally**

At the end of the function (after line 527 where stats are finalized), replace the message modification logic with:

```go
// Format base message with finalized stats
msg := formatShutdownMessage(stats)

// Append supplementary info
if failedCount > 0 {
	resourceNoun := "resource"
	if failedCount > 1 {
		resourceNoun = "resources"
	}
	msg += fmt.Sprintf("; %d pending %s failed to transition: %s", failedCount, resourceNoun, failures.Join(", "))
}

if timedOutCount := int(timedOut.Load()); timedOutCount > 0 {
	msg += fmt.Sprintf("; %d of %d resource(s) not yet stopped after %s timeout", timedOutCount, totalOperations, timeout)
} else if failedCount == 0 {
	msg += "; all resources confirmed stopped"
}

return msg
```

- [ ] **Step 3: Update call site in Shutdown method**

Update lines 248-251:

From:
```go
msg := formatShutdownMessage(stats)
if params.AwaitCompletion.Enabled {
	msg = e.handleShutdownAwaitCompletion(ctx, log, client, params, msg, stats, spec.ReportStateCallback)
}
```

To:
```go
var msg string
if params.AwaitCompletion.Enabled {
	msg = e.handleShutdownAwaitCompletion(ctx, log, client, params, stats, spec.ReportStateCallback)
} else {
	msg = formatShutdownMessage(stats)
}
```

- [ ] **Step 4: Run tests to verify correctness**

Run: `go test ./internal/executor/rds/... -v -run TestExecutor`

Expected: All tests pass, message formatting still correct.

- [ ] **Step 5: Commit the refactor**

```bash
git add internal/executor/rds/rds.go
git commit -m "refactor(rds): move message formatting after await completion in Shutdown

Fixes stale message counts. Message now reflects actual final state
after pending resources are processed.

Before: Message formatted before await → stale counts
After: Message formatted after await → accurate counts

Related: hib-9tp"
```

---

### Task 4: Refactor handleWakeupAwaitCompletion Signature

**Files:**
- Modify: `internal/executor/rds/rds.go:556,301`

- [ ] **Step 1: Update function signature**

Change the signature at line 556:

From:
```go
func (e *Executor) handleWakeupAwaitCompletion(ctx context.Context, log logr.Logger, client RDSClient, params Parameters, msg string, stats *operationStats) string {
```

To:
```go
func (e *Executor) handleWakeupAwaitCompletion(ctx context.Context, log logr.Logger, client RDSClient, params Parameters, stats *operationStats) string {
```

Remove the `msg string` parameter.

- [ ] **Step 2: Update function to format message internally**

At the end of the function (after line 639 where stats are finalized), replace the message modification logic with:

```go
// Format base message with finalized stats
msg := formatWakeUpMessage(stats)

// Append supplementary info
if failedCount > 0 {
	resourceNoun := "resource"
	if failedCount > 1 {
		resourceNoun = "resources"
	}
	msg += fmt.Sprintf("; %d pending %s failed to transition: %s", failedCount, resourceNoun, failures.Join(", "))
}

if timedOutCount := int(timedOut.Load()); timedOutCount > 0 {
	msg += fmt.Sprintf("; %d of %d resource(s) not yet available after %s timeout", timedOutCount, totalOperations, timeout)
} else if failedCount == 0 {
	msg += "; all resources confirmed available"
}

return msg
```

- [ ] **Step 3: Update call site in WakeUp method**

Update lines 299-302:

From:
```go
msg := formatWakeUpMessage(stats)
if params.AwaitCompletion.Enabled {
	msg = e.handleWakeupAwaitCompletion(ctx, log, client, params, msg, stats)
}
```

To:
```go
var msg string
if params.AwaitCompletion.Enabled {
	msg = e.handleWakeupAwaitCompletion(ctx, log, client, params, stats)
} else {
	msg = formatWakeUpMessage(stats)
}
```

- [ ] **Step 4: Run tests to verify correctness**

Run: `go test ./internal/executor/rds/... -v -run TestExecutor`

Expected: All tests pass, message formatting still correct.

- [ ] **Step 5: Commit the refactor**

```bash
git add internal/executor/rds/rds.go
git commit -m "refactor(rds): move message formatting after await completion in WakeUp

Applies same fix as Shutdown for consistency.
Message now reflects actual final state after pending resources processed.

Related: hib-9tp"
```

---

### Task 5: Run Full Test Suite

**Files:**
- Test: `internal/executor/rds/...`

- [ ] **Step 1: Run all RDS executor tests**

Run: `go test ./internal/executor/rds/... -v`

Expected: All tests pass including:
- Unit tests for Validate, Shutdown, WakeUp
- Wait tests for timeout and success scenarios
- Strategy tests for instances and clusters

- [ ] **Step 2: Check for race conditions**

Run: `go test ./internal/executor/rds/... -race -v`

Expected: No race condition warnings (callback is protected by mutex).

- [ ] **Step 3: Run targeted package tests**

Run: `go test ./internal/executor/rds/ -v -run TestExecutor_Shutdown`

Expected: Shutdown tests pass with new callback and message logic.

Run: `go test ./internal/executor/rds/ -v -run TestExecutor_WakeUp`

Expected: WakeUp tests pass with new message logic.

- [ ] **Step 4: Document test results**

Create a test summary:
```
RDS Executor Test Results
=========================
✓ All unit tests pass
✓ No race conditions detected
✓ Shutdown flow: callback passed, message accurate
✓ WakeUp flow: message accurate
```

- [ ] **Step 5: Commit test documentation**

```bash
git add -A
git commit -m "test(rds): verify all tests pass after await completion fixes

All existing tests continue to pass:
- Unit tests for Shutdown/WakeUp
- Wait tests for timeout scenarios
- No race conditions with concurrent callbacks

Related: hib-9tp"
```

---

### Task 6: Manual Verification & Integration Check

**Files:**
- Manual testing documentation

- [ ] **Step 1: Review the changes**

Run: `git diff main...HEAD`

Expected: See only the intended changes:
1. Callback parameter passed to Stop() calls
2. Message formatting moved after await completion
3. Function signatures updated (removed msg parameter)

- [ ] **Step 2: Check callback thread safety**

Verify in `cmd/runner/state/accumulator.go`:
- Line 24: `mu sync.Mutex`
- Lines 67-68: `a.mu.Lock()` and `defer a.mu.Unlock()`

Expected: Accumulator.add() method is protected by mutex.

- [ ] **Step 3: Verify message format consistency**

Check that both handlers use the same pattern:
1. Format base message with `formatShutdownMessage(stats)` or `formatWakeUpMessage(stats)`
2. Append supplementary info (failures, timeouts)
3. Append success confirmation if no failures/timeouts

Expected: Consistent message structure across both flows.

- [ ] **Step 4: Update beads issue with progress**

```bash
bd update hib-9tp --notes="Implementation complete. All 4 tasks done:
1. ✓ Pass callback to Stop in handleShutdownAwaitCompletion
2. ✓ Document why Start doesn't need callback
3. ✓ Refactor Shutdown message formatting
4. ✓ Refactor WakeUp message formatting
All tests passing, no race conditions."
```

- [ ] **Step 5: Prepare for PR**

Create summary of changes:
```
Bug Fixes:
1. Missing callback in handleShutdownAwaitCompletion (line 484)
   - Impact: Restore data now persisted for pending resources
   - Safety: Callback is thread-safe via mutex

2. Stale message after await completion (lines 248-251, 299-302)
   - Impact: Messages now show accurate final state
   - Pattern: Applied consistently to both Shutdown and WakeUp

Testing:
- All existing tests pass
- No race conditions detected
- Thread safety verified via mutex protection
```

---

## Self-Review

### Spec Coverage Check

✓ **Bug 1 (Missing Callback)**: Task 1 - passes callback to Stop() at line 484  
✓ **Bug 2 (Stale Message)**: Task 3-4 - moves message formatting after await  
✓ **Thread Safety**: Verified callback uses mutex (Task 6, Step 2)  
✓ **Consistency**: Both Shutdown and WakeUp flows fixed (Task 3-4)  
✓ **Testing**: Task 5 runs full test suite with race detection  

### Placeholder Scan

✓ No "TBD", "TODO", or "implement later"  
✓ All code changes shown explicitly  
✓ Exact file paths and line numbers provided  
✓ Test commands with expected output  

### Type Consistency

✓ Function signatures match across all tasks  
✓ Variable names consistent (msg, stats, callback)  
✓ Method calls match actual signatures (Stop, Start, formatShutdownMessage)  

---

## Execution Notes

**Estimated Time**: 20-30 minutes for full implementation and testing

**Dependencies**: 
- Go 1.21+
- AWS SDK v2
- Existing RDS executor tests

**Verification Commands**:
```bash
# Run tests
go test ./internal/executor/rds/... -v

# Check for race conditions
go test ./internal/executor/rds/... -race

# View changes
git diff main...HEAD
```

**Success Criteria**:
1. ✓ Callback passed to pending resource Stop() calls
2. ✓ Messages show accurate final state after await
3. ✓ All existing tests pass
4. ✓ No race conditions detected
5. ✓ Code follows existing patterns and style
