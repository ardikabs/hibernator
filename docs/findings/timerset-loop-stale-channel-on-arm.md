---
date: May 14, 2026
status: investigated
component: Plan Worker Timer
---

# Findings: TimerSet.loop select captures stale nil channels on dynamic Arm

## Problem Description

When a `Schedule` timer (e.g., Requeue) is `Arm`ed **after** the `TimerSet.loop()` goroutine has already entered its `select` statement, the newly armed timer is never detected. The `select` evaluated `ts.Requeue.C()` as `nil` on entry (timer wasn't armed yet), and Go treats `<-nil` as "block forever" — permanently disabling that case for the current iteration.

**Impact**: Any timer armed on-the-fly (e.g., Requeue set via `Apply()` or `SetRequeue()` during reconciliation) is silently ignored. The callback never fires, causing the worker to miss requeue cycles, timeouts, or deadlines until the loop happens to re-iterate due to another timer (typically the 30-minute inactivity timer).

**Reproduction scenario**:
1. `TimerSet.Start()` → loop goroutine enters `select` with only Inactivity armed
2. External code calls `ts.SetRequeue(5s)` → `Requeue.Arm(5s)` creates a new timer
3. 5 seconds pass → Requeue timer fires into its channel
4. But loop's `select` captured `Requeue.C()` as `nil` → event is missed
5. Loop remains blocked until Inactivity fires (30 min later)

## Root Cause Analysis

Go's `select` statement evaluates all channel operands **exactly once** when entering the select. If a channel expression returns `nil`, that case is permanently blocked for the duration of that select evaluation. The `select` does NOT re-evaluate channel expressions while it's waiting.

```go
// timer.go:192-234 — The problematic loop
func (ts *TimerSet) loop(ctx context.Context) {
    defer ts.wg.Done()
    for {
        select {
        case <-ctx.Done():
            return
        // ⚠️ These channel expressions are evaluated ONCE per iteration.
        // If Requeue.C() returns nil here, this case is dead until the
        // loop re-iterates (which requires another case to fire first).
        case <-ts.Requeue.C():   // nil if not armed → blocked forever
            ts.send(...)
        case <-ts.Timeout.C():   // nil if not armed → blocked forever
            ts.send(...)
        case <-ts.Deadline.C():  // nil if not armed → blocked forever
            ts.send(...)
        case <-ts.Inactivity.C():
            ts.send(...)
        }
    }
}
```

When `Schedule.Arm()` is called from another goroutine, it creates a **new** timer with a **new** channel. But the loop's `select` is still waiting on the old `nil` channel reference — it has no way to know a new channel was created.

Additionally, `Schedule.Arm()` calls `disarmLocked()` first (stops + drains old timer), then creates a new timer. Even if the old timer's channel was captured by `select`, it would be stopped and never fire. The new timer's channel is invisible to the current `select` iteration.

## Proposed Solutions

### Option A: Notify channel (poke pattern)

Add a buffered `notify chan struct{}` to `TimerSet`. Whenever a timer is armed or disarmed via `TimerSet` methods, send a non-blocking signal to this channel. The loop includes `<-ts.notify` as a case, which causes re-iteration and re-evaluation of all channel expressions.

```go
// TimerSet struct addition
notify chan struct{}

// New method
func (ts *TimerSet) poke() {
    select {
    case ts.notify <- struct{}{}:
    default: // already notified
    }
}

// loop addition
case <-ts.notify:
    // Timer was armed/disarmed. Re-evaluate channels.
```

- **Pros**: Minimal change, zero-allocation (buffered chan), well-understood Go pattern, no polling
- **Cons**: Extra goroutine wakeup per arm/disarm (negligible cost)

### Option B: Reflect-based dynamic select

Use `reflect.Select` to build the select cases dynamically each iteration, including only armed timers.

- **Pros**: Eliminates nil channel cases entirely
- **Cons**: Reflection overhead, harder to read, non-idiomatic for this use case

## Impact

- Timer events (requeue, timeout, deadline) armed after loop start are reliably detected
- Eliminates silent 30-minute delays caused by missed requeue cycles
- Fixes race condition in existing tests that only pass due to goroutine scheduling luck
