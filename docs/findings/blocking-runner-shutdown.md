---
date: February 7, 2026
status: resolved
component: Runner, Logging, gRPC Streaming
---

# Findings: Runner Process Blocking on Shutdown during Network Failures

## Problem Description

The Hibernator runner (`cmd/runner`) was found to hang indefinitely (or until TCP timeout) during shutdown if the gRPC streaming connection to the control plane was unresponsive. This occurred even after the main logic completed successfully.

## Root Cause Analysis

The blocking behavior was caused by a synchronous chain where `DualWriteSink.Stop()` waited for a background `streamLoop` to drain. This loop called `GRPCClient.Log()`, which was blocking on an unbuffered channel when the gRPC stream consumer was stalled due to network issues.

## Resolution: Option B (Implemented)

The issue was addressed by implementing a **Buffered Channel + Non-blocking Send** strategy in the gRPC streaming client.

### Implementation Details

1.  **Buffered Channel**: The `logChannel` in `GRPCClient` was updated to be buffered with a capacity of 100 entries. This handles bursts of logs without immediate dropping.
2.  **Non-blocking Send**: The `Log()` method now uses a `select` statement with a `default` case. If the buffer is full (indicating a stalled consumer/network), the log entry is dropped instead of blocking the caller.
3.  **Graceful Loop Exit**: The background sender goroutine now correctly handles channel closure and avoids log spam by only reporting the first streaming failure in a series.

```go
// internal/streaming/client/grpc.go

func (c *GRPCClient) Log(ctx context.Context, level, message string, fields map[string]string) error {
    // ...
    select {
    case c.logChannel <- entry:
        // Sent successfully to buffer
    default:
        // Buffer full, drop log to ensure runner never blocks
    }
    return nil
}
```

## Impact of Fix

-   **Deterministic Shutdown**: Runner pods now terminate immediately after completing their core tasks, regardless of the log streaming state.
-   **Resource Efficiency**: Prevents runner pods from lingering in `Running` state and consuming resources during network partitions.
-   **Robustness**: The "best effort" nature of remote logging is now enforced at the code level, prioritizing the workload lifecycle over log delivery.

This fix ensures that while we attempt to stream logs for observability, we never allow network instability in the monitoring path to interfere with the primary infrastructure operations.

## Appendix: Historical Proposed Solutions

To align with the "best effort" philosophy of the streaming logs (archival/monitoring should not block core operations), the following solutions were originally proposed during the investigation.

### Option A: Non-blocking Log Sending

Modify `GRPCClient.Log` to drop log entries if the consumer is not ready. This treats the streaming channel as a "best effort" pipe.

**Implementation**:
Use a `select` statement with a `default` case in `internal/streaming/client/grpc.go`.

```go
func (c *GRPCClient) Log(ctx context.Context, level, message string, fields map[string]string) error {
    if c.logChannel == nil {
        return nil
    }

    entry := &streamingv1alpha1.LogEntry{...}

    select {
    case c.logChannel <- entry:
        // Sent successfully
    default:
        // Channel full or consumer blocked - drop log to prevent caller blocking
    }
    return nil
}
```

**Pros**:
-   **Guarantees non-blocking behavior** for the main program execution and shutdown.
-   **Simplicity**: Minimal code change.
-   **Safety**: Protects the runner from any downstream gRPC slowness.

**Cons**:
-   **Log Loss**: Logs will be dropped immediately if the network is slightly slow, even if we could have waited a few milliseconds. (Can be mitigated by buffering the channel, see Option B).

### Option B: Buffered Channel + Non-blocking Send (Selected)

Combine Option A with a buffered channel in `GRPCClient`.

**Implementation**:
Change `c.logChannel = make(chan ...)` to `make(chan ..., 100)`. Keep the `select/default` logic from Option A.

**Pros**:
-   **Burst Tolerance**: Handles short network hiccups or bursts of logs without dropping them.
-   **Non-blocking**: Still guarantees exit if the buffer fills up.

**Cons**:
-   **Memory Usage**: Slight increase in memory (negligible for 100 pointers).

### Option C: Timeout in DualWriteSink Shutdown

Enforce a strict timeout in `pkg/logsink/dualwrite.go` during the `Stop()` sequence.

**Implementation**:
Modify `Stop()` to wait for `wg.Done()` with a timeout/select.

**Pros**:
-   **Centralized Control**: Ensures the sink always shuts down within N seconds regardless of the implementation (gRPC, Webhook, etc.).

**Cons**:
-   **Complexity**: Requires managing goroutine leaks (the `streamLoop` might still be blocked in the background even if `Stop` returns).
-   **Incomplete**: Doesn't fix the underlying blocking in `GRPCClient`, just ignores it during shutdown.

### Option D: Timeout on gRPC Send

Use a context with timeout for `stream.Send()`.

**Implementation**:
This is difficult because `stream.Send` doesn't accept a context directly (it uses the stream's context). We would need to recreate the stream or manage lower-level timeouts.

**Pros**:
-   Correct gRPC usage.

**Cons**:
-   **Implementation Complexity**: gRPC streaming API doesn't make per-message timeouts easy.
