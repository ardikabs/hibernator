# Notification Dispatcher Revamp: Keyed FIFO Stream Workers

## Background

We received a race-condition report where sink notifications for `ExecutionProgress` and `Success` can be observed out of order. The current dispatcher uses a shared queue and fixed worker pool, which allows concurrent handling of requests from the same logical sink stream.

This revamp replaces the fixed global worker model with adaptive per-stream keyed workers to guarantee deterministic ordering per stream while preserving parallelism across different streams.

## Goals

- Guarantee FIFO ordering per notification stream at sink level.
- Keep parallelism across independent streams.
- Remove global overflow queue complexity.
- Keep non-blocking `Submit` behavior.
- Preserve graceful shutdown delivery for queued items.

## Stream Definition

Each request is routed by a stream key:

- plan namespace/name
- cycle ID
- notification namespace/name
- sink name
- sink type
- operation

This key ensures all requests belonging to the same sink execution stream are serialized by a single worker.

## Design

### 1) Dispatcher execution model

- Replace shared channel + fixed workers with `keyedworker.Pool[streamKey, Request]`.
- Use per-stream FIFO slot semantics.
- Enable adaptive worker lifecycle:
  - lazy spawn on first delivery,
  - idle reap after configurable TTL,
  - auto-remove idle stream entries.

### 2) Overflow deprecation

- Remove global overflow queue and drainer loop.
- Per-stream buffering uses bounded FIFO slots.
- On per-stream buffer saturation, drop request and increment `NotificationDropTotal`.

### 3) Shutdown behavior

- Keep `Submit` fast-path shutdown gate (`done` channel).
- On worker context cancellation, drain pending slot items with a bounded timeout before exit.
- Ensure no send-on-closed-channel risk (no shared request channel to close).

### 4) Configuration

- Remove legacy `DispatcherConfig.Workers` and `DispatcherConfig.MaxOverflowSize`.
- Continue using `ChannelSize` as **per-stream** buffer size.
- Add `WorkerIdleTTL` to tune adaptive stream worker reap behavior.

## Metrics and Observability

- Keep existing notification metrics (`sent`, `errors`, `latency`, `drop`).
- Add gauge for active notification stream workers.
- Emit structured logs on stream worker start/stop and stream-key context.

## Test Plan

1. **Ordering (same stream):** `ExecutionProgress` then `Success` must dispatch in order.
2. **Parallelism (different streams):** one blocked stream must not block another.
3. **Adaptive lifecycle:** worker spawns lazily and reaps after idle TTL.
4. **Shutdown drain:** queued per-stream items are drained before dispatcher stops.
5. **Saturation behavior:** per-stream full buffer causes controlled drops and increments drop metric.

## Implementation Checklist

- [ ] Introduce stream key and key builder in notification dispatcher.
- [ ] Replace shared queue worker model with keyedworker pool model.
- [ ] Implement per-stream FIFO slot with drop callback for metrics.
- [ ] Remove overflow queue/drainer logic from dispatcher.
- [ ] Add worker idle TTL config and defaults.
- [ ] Add active stream worker gauge metric.
- [ ] Update dispatcher tests to keyed-stream behavior.
- [ ] Run notification package tests and fix regressions.

## Out of Scope

- Durable notification queue/outbox persistence.
- Feature flag for old/new dispatcher selection.
- API-level breaking config cleanup (can be done later after stabilization).
