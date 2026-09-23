---
date: September 23, 2026
status: resolved
component: Restore Manager, Runner, Wakeup Finalize
---

# Findings: Lost-update race on the shared restore ConfigMap leaves restore data locked

## Problem Description

`TestScenarios/partial-failure-besteffort` (`test/kind`) fails intermittently with:

```
wakeup must consume restore data for target ns-shop
```

from `ScnAssertRestoreConsumed` (`test/kind/suite/steps.go:29-38`), which requires
per-target restore data to reach `!IsLive && CycleID == ""` within 2 minutes.
Only this scenario flakes; all sequential scenarios are stable.

The same race is reachable in production (not test-only code): any plan waking
≥2 targets concurrently (`Parallel` strategy, parallel `Staged` stages, or
independent `DAG` branches) can hit it. Production consequences are worse than
a test flake — silent, persistent state corruption (see below), visible only in
runner logs.

## Root Cause Analysis

The `CycleID` for a target is cleared only by `UnlockRestoreData`, which runs
only when `MarkAllTargetsRestored` observes `restored-*` annotations for **all**
plan targets (`internal/provider/processor/plan/state/state_wakingup.go:134-165`,
`postWakeupCleanup`).

The writers of that single shared ConfigMap (`hibernator-restore-<plan>`) are:

1. One wakeup runner Job per target → `MarkTargetRestored`
   (`cmd/runner/app/runner.go:199-209`). This scenario runs 3 runners in
   parallel (`Parallel / maxConcurrency: 3`,
   `test/kind/suite/testdata/scenarios/partial-failure-besteffort.yaml:6-12`).
2. The controller itself → `UnlockRestoreData` in `postWakeupCleanup`.

Both `MarkTargetRestored` (`internal/restore/manager.go:220-258`) and
`UnlockRestoreData` (`manager.go:291-333`) are plain **Get → mutate → Update**
with **no `RetryOnConflict`**. Concurrent updates collide on
`resourceVersion`; the loser gets a `Conflict` error, which the runner swallows
as `"failed to mark target as restored (non-fatal)"` while the Job still
succeeds (`runner.go:201-202`). That target's annotation is lost forever.

`postWakeupCleanup` runs **once** per cycle: with an annotation missing it logs
`"not all targets restored yet, keeping restore data locked"` and returns while
the phase is already moving to `Active` — nothing ever retries it, so `CycleID`
stays set and the 2-minute assertion can never pass. The error names `ns-shop`
only because it is first in `Consume: [ns-shop, ns-reports]`
(`test/kind/scenarios_test.go:149-152`); any lost mark blocks the unlock for
all targets.

Corroborating evidence for a failed run: runner log
`"failed to mark target as restored (non-fatal)"` with a `Conflict` error,
controller log `"not all targets restored yet, keeping restore data locked"`,
and a restore ConfigMap missing one `restored-*` annotation despite all wakeup
Jobs succeeding. (Note: annotation-only late writes are additionally ignored by
`configMapDataChangedPredicate`, so they wouldn't even trigger a reconcile.)

Production fallout of a lost mark: restore data stays locked (`IsLive=true`,
`CycleID` set) with no self-healing. The next hibernation cycle then reads
stale live data — `getExistingCycleIDForHibernation` (`state_idle.go`) may reuse
the old cycle ID and `HasRestoreData` stays true — leaking the corruption into
the next cycle's identity and scheduling inputs. Manual recovery means
hand-editing the restore ConfigMap.

## Resolution

Chosen: Option A, implemented in `internal/restore/manager.go`.
`MarkTargetRestored` and `UnlockRestoreData` now do Get → mutate →
`client.MergeFrom` patch inside `retry.RetryOnConflict(retry.DefaultBackoff)`
(same pattern `status/processor.go` uses). The merge patch keeps each
concurrent mark to its disjoint annotation/data keys so parallel runners no
longer clobber each other, and the retry re-reads on any residual collision
(e.g. with the controller-side unlock). Genuine failures stay non-fatal in the
runner as before.

Regression tests in `internal/restore/manager_concurrency_test.go`:
`TestMarkTargetRestored_RetriesOnConflict` and
`TestUnlockRestoreData_RetriesOnConflict` inject a `Conflict` via fake-client
interceptors and assert retry + correct end state;
`TestConcurrentMarkTargetRestored_NoLostMarks` releases 3 parallel markers at
a barrier (the kind-flake shape) and asserts no annotation is lost — this
fails deterministically against the old full-object `Update` code path, which
the fake client rejects with stale-`resourceVersion` conflicts.
`TestConcurrentMarkTargetRestored_NoLostMarks` passes 10/10 consecutive runs.

## Proposed Solutions

### Option A: RetryOnConflict on the writers (recommended, the actual fix — implemented)

Wrap the Get→Update in `MarkTargetRestored` and `UnlockRestoreData` with
`retry.RetryOnConflict` (same pattern `status/processor.go` already uses).
Closes the lost-mark hole for all current and future parallel shapes.
Genuine failures stay non-fatal in the runner as today.

- **Pros**: Minimal, shared `restore.Manager` fix covers prod and tests; no
  behavior change on the happy path.
- **Cons**: None identified; conflict retries add negligible latency.

### Option B: Re-drive postWakeupCleanup instead of one-shot (defense in depth)

Return `RequeueAfter` (instead of returning quietly) when not all targets are
restored yet, so legitimately late marks still get an unlock pass.

- **Pros**: Covers residual paths where a mark arrives after finalize.
- **Cons**: Does not fix the lost mark itself; adds timer churn for a case
  Option A already eliminates. Best as a complement, not a substitute.

### Option C: Patch / Server-Side Apply for annotations (not recommended alone)

Reduces the clobber window for annotation writes but still needs conflict
handling for the `IsLive` data flips; more invasive than Option A for less
coverage.

## Impact

- Deterministic `partial-failure-besteffort` kind runs (currently the only
  parallel-wakeup scenario, hence the only flake).
- Eliminates a silent production corruption vector (permanently locked restore
  data leaking stale cycle identity into subsequent cycles).
- No happy-path behavior change: one ConfigMap write per target as today, plus
  retries only on actual conflicts.
