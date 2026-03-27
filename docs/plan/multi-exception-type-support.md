# feat: Support composable exception types with window-level overlap detection

## Summary

The current implementation enforces a strict single-exception rule per `HibernatePlan` — the admission webhook rejects any new `ScheduleException` whose validity period (`validFrom`/`validUntil`) overlaps with an existing one, regardless of exception type or whether their schedule windows actually collide. This makes it impossible to apply an `extend` and a `suspend` simultaneously for different schedule windows within the same validity period.

This issue proposes:

1. Redefining "overlap" to operate at the **schedule window** level (`start`/`end` + `daysOfWeek`), not the validity period level.
2. Allowing specific type combinations (`replace` + `extend`/`suspend`, `extend` + `suspend`) to coexist when their windows do not collide.
3. Updating the scheduler evaluator to compose multiple active exceptions and correctly predict next events.

---

## Background

[RFC-0003](../proposals/0003-schedule-exceptions.md) explicitly listed "multiple simultaneous active exceptions" as a non-goal for v1, choosing simplicity over expressiveness. The enforcement lives in three places:

1. **Admission webhook** — `validateNoOverlappingExceptions` in `internal/validationwebhook/scheduleexception_validator.go` — rejects any new exception whose `validFrom`/`validUntil` range overlaps with an existing `Active` or `Pending` exception for the same plan, regardless of exception type or window content.
2. **Provider reconciler** — `selectActiveException` in `internal/provider/provider.go` picks `lo.First()` (newest by `CreationTimestamp`) when multiple active exceptions exist, with a `TODO` noting that type-based prioritization is still needed.
3. **Scheduler evaluator** — `ScheduleEvaluator.Evaluate()` in `internal/scheduler/schedule.go` accepts a single `*scheduler.Exception`, making composition structurally impossible today.

---

## Problem / Motivation

### Concrete use case

An infrastructure team manages a platform cluster with the following base schedule:

```yaml
schedule:
  timezone: "Asia/Jakarta"
  offHours:
    - start: "20:00"
      end:   "06:00"
      daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday"]
```

> **Note on weekends**: The base schedule already implicitly covers weekends — the cluster hibernates at Friday 20:00 and stays hibernated until Monday 06:00. There is no gap on Saturday or Sunday. An `extend` exception targeting Saturday/Sunday would have no practical effect against this schedule.

They have two simultaneous operational needs for the same week:

| Need | Exception type | Window |
|------|----------------|--------|
| National public holiday falls on Wednesday — hibernate the cluster all day instead of just the overnight window | `extend` | Wed all day (`start: "00:00"`, `end: "23:59"`, `daysOfWeek: [Wednesday]`) |
| Thursday night deployment window — keep the cluster awake during what would normally be the late-night hibernation | `suspend` | Thu 23:00–03:00 |

These two exceptions have **non-colliding schedule windows** (different days). They are semantically orthogonal — `extend` adds a full-day hibernation window on Wednesday (a day that normally only hibernates overnight), while `suspend` carves out a keep-awake window on Thursday night.

Today the webhook rejects the second exception because the `validFrom`/`validUntil` ranges of both exceptions overlap (both span the same week), even though their schedule windows are completely disjoint.

### Why not just use one exception?

The general principle: if two exceptions of the **same type** apply within the same period, a single exception with adjusted windows is sufficient — the intent is clearer and administration simpler. But when two exceptions of **different types** apply (e.g., `extend` + `suspend`), they represent fundamentally different intents (broaden hibernation vs. carve it out) and cannot be collapsed into one. Forcing users to recalculate a `replace` exception that manually merges both intents defeats the purpose of having typed exceptions.

---

## Key Design Decisions

### 1. Window-level overlap detection

**An overlapping exception is defined as two exceptions whose schedule windows collide within the same time-of-day and day-of-week range — not merely exceptions whose validity periods intersect.**

Two windows collide when their time ranges overlap on at least one shared day:
- Exception A: `start=20:00, end=06:00, daysOfWeek=[Mon,Tue,Wed]`
- Exception B: `start=18:00, end=22:00, daysOfWeek=[Wed,Thu]`
- **Result: collides** — Wednesday overlaps, and the time ranges `20:00–06:00` and `18:00–22:00` intersect (`20:00–22:00` is a common range).

Non-colliding example:
- Exception A: `start=20:00, end=06:00, daysOfWeek=[Mon,Tue]`
- Exception B: `start=10:00, end=14:00, daysOfWeek=[Wed,Thu]`
- **Result: no collision** — no shared day.

### 2. Evaluator must predict next events across composed exceptions

When multiple exceptions are active, the evaluator must produce a single `EvaluationResult` with correct `ShouldHibernate`, `NextHibernateTime`, and `NextWakeUpTime` that account for all active exceptions. The provider's `computeNextEvent` relies on these fields to schedule the next reconcile at the right moment.

### 3. Pairing allowance rules for colliding windows

The following pairing rules apply ONLY when multiple exceptions for the same plan have both **overlapping validity periods AND colliding schedule windows**:

| Pair | Allowed? | Rationale |
|------|----------|-----------|
| `replace` + `extend` | ✅ Yes | `replace` acts as a new base schedule; `extend` adds more hibernation windows on top, even if they overlap. |
| `replace` + `suspend` | ✅ Yes | `replace` acts as a new base schedule; `suspend` carves out from it. |
| `extend` + `suspend` | ✅ Yes | Orthogonal intents — `extend` broadens hibernation, `suspend` carves it out where it intersects. |
| `replace` + `replace` | ✅/❌ | Allowed if windows do **not** collide (each replaces the base for its own days); rejected if windows collide (ambiguous — which replacement wins for the overlapping period?). |
| `extend` + `extend` | ✅/❌ | Allowed if windows do **not** collide (merged into one combined extend during evaluation); rejected if windows collide (redundant — merge into one). |
| `suspend` + `suspend` | ✅/❌ | Allowed if windows do **not** collide (merged into one combined suspend during evaluation); rejected if windows collide (redundant — merge into one). |

If their schedule windows **do not collide**, ANY combination of types — including same-type pairs — is freely allowed to coexist. The evaluator merges windows of the same type before composition.

**Evaluation priority**: When `replace` is present, it is evaluated first — its windows become the effective base schedule, replacing the plan's original base. Then `extend` and `suspend` apply on top of that effective base in order.

---

## Current Behaviour vs. Desired Behaviour

| Scenario | Current | Desired |
|----------|---------|---------|
| Single exception of any type | ✅ Works | ✅ Unchanged |
| `extend` + `suspend`, non-colliding windows | ❌ Webhook rejects (validity overlap) | ✅ Allowed |
| `extend` + `suspend`, colliding windows | ❌ Webhook rejects (validity overlap) | ✅ Allowed (suspend carves out of extend) |
| `extend` + `extend`, non-colliding windows | ❌ Webhook rejects (validity overlap) | ✅ Allowed (safe, disjoint schedules) |
| `extend` + `extend`, non-colliding windows | ❌ Webhook rejects (validity overlap) | ✅ Allowed (windows merged during evaluation) |
| `extend` + `extend`, colliding windows | ❌ Webhook rejects | ❌ Keep rejected (redundant, merge into one) |
| `replace` + `extend`, non-colliding windows | ❌ Webhook rejects | ✅ Allowed |
| `replace` + `replace`, non-colliding windows | ❌ Webhook rejects | ✅ Allowed (each replaces the base for its own days) |
| `replace` + `replace`, colliding windows | ❌ Webhook rejects | ❌ Keep rejected (ambiguous) |

---

## Proposed Changes

### 1. Admission Webhook — window collision check followed by type pairing

Replace the current validity-period–only overlap check in `validateNoOverlappingExceptions` with a two-tier validation. The priority is to allow users to schedule multiple future exceptions over the same validity period freely, as long as they don't logically conflict.

**Tier 1 — Window collision check**:
- First, check if the validity periods (`validFrom`/`validUntil`) of the incoming and existing exception intersect. If they don't, they are fully disjoint and allowed.
- If validity periods do intersect, check if their **schedule windows** collide. For each pair of windows across the two exceptions, check if they share at least one common `daysOfWeek` entry AND their time ranges overlap (handling overnight wraparound properly).
- If there is NO window collision (e.g., one exception targets Mon-Tue, the other targets Wed-Thu), the exceptions are allowed to coexist **regardless of their type** (meaning `extend` + `extend` is perfectly fine if they target different days).

**Tier 2 — Type pairing check** (only evaluated if both validity AND windows collide):
- If the windows *do* collide, we check if the combination of exception types is a permitted composition.
- **Allowed:** If the types are an allowed pair (e.g., `extend` + `suspend`, where the `suspend` acts as a carve-out from the `extend`), the collision is permitted and the exception is accepted.
- **Rejected:** If the types are the same (`extend` + `extend`), or an invalid combination, reject the exception immediately with a descriptive error naming the colliding windows.

```
validateNoOverlappingExceptions(incoming):
  for each existing exception (Active|Pending) targeting same plan:
    // 1. Check validity period overlap
    if !validityPeriodsOverlap(incoming, existing):
      continue

    // 2. TIER 1: Check window collision
    if !windowsCollide(incoming.windows, existing.windows):
      continue // Safe! No actual schedule overlap

    // 3. TIER 2: Windows collide — check type pairing
    if same_type(incoming, existing):
      → reject("colliding same-type exceptions cannot coexist; adjust windows in a single exception")

    if !allowedPair(incoming.type, existing.type):
      → reject("unsupported colliding exception type combination")

    // If it reaches here (e.g., extend + suspend), the collision is intentionally allowed for composition!
```

### 2. Provider reconciler — convert and forward exceptions

Replace `selectActiveException` with a simple conversion approach:

1. Filter active exceptions by validity period.
2. Convert each `ScheduleException` API resource 1:1 to `*scheduler.Exception` via `convertException`.
3. Pass the raw slice to `ScheduleEvaluator.Evaluate` — same-type merging is now the evaluator's responsibility.

The provider does not need to group, sort, or merge exceptions before forwarding them. This keeps the provider as a pure data collector and places the composition invariant in the evaluator where it belongs.

### 3. Scheduler evaluator — composed multi-exception evaluation

Update `ScheduleEvaluator.Evaluate()` signature:

```go
// Before:
func (e *ScheduleEvaluator) Evaluate(baseWindows []OffHourWindow, timezone string, exception *Exception) (*EvaluationResult, error)

// After:
func (e *ScheduleEvaluator) Evaluate(baseWindows []OffHourWindow, timezone string, exceptions []*Exception) (*EvaluationResult, error)
```

Same-type exceptions (e.g., two `extend` exceptions for different days) are **merged internally** by the evaluator via `mergeByType` — windows concatenated, validity expanded to the widest bounds, max LeadTime used. This means callers may pass the raw exception list without pre-merging.

Composition logic (pseudocode):

```
Evaluate(baseWindows, timezone, exceptions):
  // Filter to active exceptions; merge same-type groups via mergeByType.
  effectiveBase = baseWindows

  // Phase 1: Replace (if present) substitutes the base entirely.
  if replace := mergeByType(active, "replace"); replace != nil:
    effectiveBase = replace.Windows

  ext = mergeByType(active, "extend")   // nil or one merged exception
  sus = mergeByType(active, "suspend")  // nil or one merged exception

  switch:
  case ext != nil && sus != nil:
    // Passing extendedBase = effectiveBase ∪ ext.Windows directly to evaluateSuspend
    // is unsafe: ConvertOffHoursToCron (used internally) only processes windows[0]
    // and would silently drop all extend windows.
    //
    // Solution: evaluate the extended schedule first via evaluateExtend (which OR-combines
    // each window set independently), then overlay the suspend carve-out — including
    // lead-time and grace-period logic — on top via applySuspendCarveOut.
    extResult = evaluateExtend(effectiveBase, ext.Windows, timezone)
    return applySuspendCarveOut(extResult, sus, timezone)

  case ext != nil:
    return evaluateExtend(effectiveBase, ext.Windows, timezone)

  case sus != nil:
    return evaluateSuspend(effectiveBase, sus, timezone)

  default:
    return evaluateWindows(effectiveBase, timezone)
```

The key correctness property: **`NextHibernateTime` and `NextWakeUpTime` must reflect the composed schedule**, not just the base or a single exception. This means the evaluator needs to consider all exceptions when computing next events — particularly when a `suspend` window's end should become the `NextHibernateTime`, or when an `extend` window's start should become an earlier `NextHibernateTime` than the base schedule alone would produce.

> **Why `evaluateExtend` → `applySuspendCarveOut` and not merging windows into one slice?** Passing a combined `effectiveBase ∪ ext.Windows` slice to `evaluateSuspend` is unsafe because `ConvertOffHoursToCron` (invoked internally) only processes `windows[0]` and silently drops all remaining windows — any extend windows would be invisible to `evaluateSuspend`. The two-step approach avoids this: `evaluateExtend` evaluates each window set independently and OR-combines the results, then `applySuspendCarveOut` applies the full lead-time, grace-period, and next-event logic on top of the pre-computed extended result without re-running `ConvertOffHoursToCron`.

### 4. Provider — next event calculation

`computeNextEvent` in the provider relies on `EvaluationResult.NextHibernateTime` and `NextWakeUpTime`. No change needed here since the evaluator will already return composed values. However, the `ScheduleEvaluation` struct passed to processors should carry the full list of active exceptions so that log context is complete:

```go
type ScheduleEvaluation struct {
    Exceptions      []ScheduleException  // already a slice — no change needed
    ShouldHibernate bool
    NextEvent       time.Time
}
```

---

## Out of Scope

- Supporting >1 **colliding** exception of the same type (users must merge into a single exception with combined windows).
- UI/CLI changes for managing composed exceptions.
- Approval workflows for exceptions (planned for a future phase per RFC-0003).

---

## Affected Components

| Component | File | Change |
|-----------|------|--------|
| Webhook | `internal/validationwebhook/scheduleexception_validator.go` | Replace validity-period overlap with type-pairing + window-collision check |
| Scheduler | `internal/scheduler/schedule.go` | Update `Evaluate()` to accept `[]*Exception`; add `mergeByType`, `applySuspendCarveOut`; composed evaluation logic |
| Provider | `internal/provider/provider.go` | Remove `selectActiveException`/`resolveActiveExceptions`; convert exceptions 1:1 and forward to `Evaluate` |
| Shared types | `internal/message/plan.go` | No change (`ScheduleEvaluation.Exceptions` is already a slice) |
| API | `api/v1alpha1/scheduleexception_types.go` | No change |
| Tests | All components above | New test cases for composition, window collision, and `mergeByType` |
| RFC | `proposals/0003-schedule-exceptions.md` | Addendum documenting relaxed constraint and composition semantics |

---

## Acceptance Criteria

- [x] **Webhook — window collision**: Permits ANY exceptions (including same-type like `extend`+`extend` or `replace`+`replace`) to coexist if their schedule windows do NOT collide (e.g., target different days).
- [x] **Webhook — type pairing**: When validity periods AND schedule windows BOTH collide, permits allowed cross-type compositions (`extend`+`suspend`, `replace`+`extend`, `replace`+`suspend`), but rejects same-type collisions and `replace`+`replace` collisions.
- [x] **Provider — exception conversion**: Converts each active exception 1:1 via `convertException` and forwards the raw slice to `Evaluate`. Same-type merging is now the evaluator's responsibility, not the provider's.
- [x] **Evaluator — same-type merging**: `mergeByType` internally merges all exceptions of the same type (windows concatenated, validity expanded to widest bounds, max LeadTime). Callers pass raw lists without pre-merging.
- [x] **Evaluator — extend+suspend composition**: Evaluates extended schedule via `evaluateExtend` first (OR-combines each window set independently), then overlays suspend carve-out via `applySuspendCarveOut`. Avoids the `ConvertOffHoursToCron` single-window limitation that would silently drop extend windows if passed as a combined slice.
- [x] **Evaluator — composed evaluation**: `Evaluate()` accepts `[]*Exception`, applies replace → extend → suspend in order, and produces correct `ShouldHibernate`, `NextHibernateTime`, `NextWakeUpTime`.
- [x] **Evaluator — next event accuracy**: Next events reflect the full composed schedule — e.g., a `suspend` window end becomes `NextHibernateTime` when it falls before the base schedule's next hibernate; large lead-time on a future suspension day does not bleed into an unrelated extend window on a prior day.
- [x] **Unit tests**: Cover single-exception (unchanged), each allowed pair, window collision detection (same-day overlap, overnight wraparound, no shared day), `mergeByType` semantics, and edge cases (replace + extend + suspend triple, large lead-time non-bleed, expired exceptions ignored).
- [ ] **RFC-0003 addendum**: Documents the relaxed constraint, pairing rules, window-level overlap definition, and composition semantics.

---

## Related

- RFC-0003: [proposals/0003-schedule-exceptions.md](../proposals/0003-schedule-exceptions.md)
- TODO in `selectActiveException`: `internal/provider/provider.go`
- Webhook validator: `internal/validationwebhook/scheduleexception_validator.go`
- Scheduler evaluator: `internal/scheduler/schedule.go`
