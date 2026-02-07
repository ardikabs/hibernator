---
date: February 7, 2026
status: Investigated
component: Runner, Logging, gRPC Streaming
---

# Findings: Runner Process Blocking on Shutdown during Network Failures

## Problem Description

The Hibernator runner (`cmd/runner`) may hang indefinitely (or until TCP timeout) during shutdown if the gRPC streaming connection to the control plane is unresponsive, broken, or if the server is down. This occurs even after the main logic has completed successfully (after "execution completed successfully" log).

As a result, the Kubernetes Job pod remains in a `Running` or `Terminating` state longer than necessary, delaying cleanup and potentially causing confusion about the actual execution state.

## Root Cause Analysis

The blocking behavior is caused by a chain of synchronous dependencies in the shutdown sequence:

1.  **Main Exit Trigger**: `main()` finishes and calls `defer r.close()`.
2.  **Sink Shutdown**: `r.close()` calls `r.logSink.Stop()` to ensure all logs are flushed.
3.  **Drain Wait**: `r.logSink.Stop()` calls `s.shared.wg.Wait()`, waiting for the background `streamLoop` to finish.
4.  **Drain Execution**: The `streamLoop` calls `s.drainChannel()`, which attempts to send remaining buffered logs via `s.sender.Log()`.
5.  **Sender Implementation**: The `streamingLogSender` delegates to `GRPCClient.Log()`.
6.  **Channel Blocking**: `GRPCClient.Log()` attempts to write to `c.logChannel`:
    ```go
    c.logChannel <- &streamingv1alpha1.LogEntry{...}
    ```
    **Critical Issue**: `c.logChannel` is **unbuffered** (`make(chan ...)`).
7.  **Consumer Stalled**: The consumer of `c.logChannel` (in `openLogStream`) calls `stream.Send(entry)`.
    ```go
    if err := stream.Send(entry); err != nil { ... }
    ```
    If the gRPC connection is stuck (e.g., network partition, server unresponsive but TCP connection open), `stream.Send()` blocks.
8.  **Deadlock/Block**: Because the consumer is blocked on `stream.Send()`, it cannot read from `c.logChannel`. The producer (`drainChannel` -> `Log`) blocks trying to write to `c.logChannel`. The `Stop()` method blocks waiting for `drainChannel` to finish.

## Impact

-   **Stuck Pods**: Runner pods do not terminate immediately after work is done.
-   **Resource Waste**: Pods consume resources while waiting for timeouts.
-   **Operational Noise**: Alerts might fire for long-running jobs that have actually finished their logic.

## Proposed Solutions

To align with the "best effort" philosophy of the streaming logs (archival/monitoring should not block core operations), we propose the following solutions.

### Option A: Non-blocking Log Sending (Recommended)

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
        // Optional: Bump a dropped_logs metric
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

### Option B: Buffered Channel + Non-blocking Send

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

## Recommendation

**Implement Option B (Buffered Channel + Non-blocking Send)**.

1.  **Buffer `c.logChannel`** with a size of ~100. This handles high-volume logging bursts typical in loops.
2.  **Use `select/default`** when writing to `c.logChannel`. If the buffer is full (meaning the network is stuck), drop the log entry. This ensures the Runner *never* blocks on logging.

This aligns perfectly with the "best effort" dual-write requirement: local logs (stdout) are preserved (Kubernetes handles them), while remote streaming logs are sent if possible but sacrificed if they endanger the workload's lifecycle.
