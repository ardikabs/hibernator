# Design: Schedule Exception Suspend + Target Override (v1.7.0 RC Enhancement)

**Date:** 2026-09-05
**Type:** Feature Refinement (Bounded)
**Status:** Draft
**Release:** v1.7.0 RC

## 1. Problem Statement (Product View)

Customers hibernate fleets to save cost (e.g. 10 RDS instances stopped every night).

On weekends, developers sometimes need a small subset back online (e.g. 2 of 10) for testing.

Today they have two bad options:

1. Wake up all 10 (wastes money), or
2. Manually patch the restore point (slow, error-prone, easy to forget to revert).

v1.7.0 RC already ships **schedule exception + target override** for `extend` / `replace` types:

> "This weekend only, wake up with a different list. On Monday, go back to normal automatically."

This spec extends that same idea to the `suspend` exception type, keeping it simple for iterative delivery.

## 2. User Story

- **Base plan:** 10 RDS targets, hibernate nightly 20:00–06:00.
- **Friday night:** all 10 hibernate as usual.
- **Weekend exception (`suspend`, Sat–Sun):** system wakes only the 2 dev instances. The other 8 stay stopped.
- **Monday:** exception expires. Next scheduled wake-up brings back all 10. The 2 already running are skipped safely (runner is idempotent).

No manual patching. No leftover state. No surprise bill.

## 3. Design Principles

1. **Only what you specify changes. Everything else follows the base plan as-is.**
   - If an override lists `target-a` with new `parameters`, only `target-a.parameters` change.
   - Unlisted targets, unlisted fields, strategy, behavior, and schedule all stay exactly as defined in `HibernatePlan.spec`.
   - No merging of nested parameter blobs — `parameters` is a full replacement for that target only.
2. **Keep suspend simple.** For v1.7.0, `suspend` allows only `targetOverrides` (`disabled`, `parameters`). `executionOverride` (strategy/behavior) stays forbidden for `suspend`.
3. **Temporary by construction.** The weekend change is never saved as the new normal. Expiry always reverts to base.
4. **Reuse existing guardrails.** No new validation framework — extend what `extend`/`replace` already use.

## 4. Semantics

### 4.1 What `suspend` means (unchanged)

`suspend` is a carve-out: while active, `ShouldHibernate=false`.

- Plan is `Active` + suspend active → stay `Active` (no hibernation). Overrides are irrelevant on this path.
- Plan is `Hibernated` + suspend active → transition to `WakingUp`. This is where the new partial-wakeup applies.
- Scheduler (`internal/scheduler/schedule.go` `applySuspend`) needs no change — the trigger already exists.

### 4.2 What `suspend` + `targetOverrides` means (new)

When a `Hibernated → WakingUp` transition is triggered while a `suspend` exception with `targetOverrides` is active:

- Build the wakeup target list from base plan targets, then apply only the listed overrides:
  - `disabled: true` → exclude that target from this wakeup (stays stopped).
  - `parameters: {...}` → use these parameters for this wakeup instead of base parameters for that target.
  - Unlisted targets → base spec as-is.
  - Listed target with neither `disabled` nor `parameters` → no-op, base as-is.
- Example: base 10 targets, override disables 8 → wakeup runs 2 jobs.
- On expiry, next cycle rebuilds from live base spec → full 10. Already-running targets no-op via runner idempotency.

### 4.3 What is explicitly out of scope

- `executionOverride` on `suspend` (still rejected).
- Partial *hibernation* via `suspend` (suspend never triggers `Hibernating`; hibernate path continues to ignore `suspend` overrides).
- Overlapping override windows beyond current "only one override per plan" rule.
- Nested parameter merging.

## 5. Technical Approach

### 5.1 API (`api/v1alpha1/scheduleexception_types.go`)

Update doc comments only (no new fields):

- `TargetOverrides`: "Only valid when Type is `extend`, `replace`, or `suspend`. For `suspend`, only per-target `disabled`/`parameters` are honored; `executionOverride` remains forbidden."
- `ExecutionOverride`: "Only valid when Type is `extend` or `replace`."

### 5.2 Validation (`internal/validationwebhook/scheduleexception_validator.go`)

- `validateExecutionOverrides()` Rule 1: split the current `suspend` blanket ban.
  - Keep `Forbidden` for `executionOverride != nil` on `suspend`.
  - Remove `Forbidden` for `targetOverrides` on `suspend`; fall through to Rule 2/3/4.
- Rule 2 (target existence, `executorparams.ValidateParams`, `hasDependencyOn` DAG check) automatically covers `suspend` once fall-through is enabled.
- Rule 4 (only one active exception with overrides per plan) automatically covers `suspend` — overlapping `extend`+`suspend` both with overrides stays rejected. This is intentional for v1.7.0 simplicity.
- `overrideFieldsChanged()` + `checkMidCycleBlock()` are type-agnostic already — no change needed, mid-cycle edit/delete block extends to `suspend` overrides for free.

### 5.3 Runtime (`internal/provider/processor/plan/state/`)

Problem: `findActiveExceptionOverride()` (`state.go:236`) and `validateRuntimeOverrides()` (`state_execution.go:165`) both skip `suspend`, and `effectivePlan()` prefers `PlanSnapshot` (full 10) which would defeat a partial wakeup.

Approach — split hibernate vs wakeup paths:

1. Extract shared helper `applyTargetOverrides(base []Target, overrides []TargetOverride) []Target` from current `buildEffectivePlan()` second pass (disabled filter + parameter replacement + unknown-target skip with log).
2. Keep `findActiveExceptionOverride()` (extend/replace only) for the hibernate path: `transitionToHibernating()` and `execute()` during `Hibernating` unchanged.
3. Add `findActiveSuspendOverride()` (suspend only, `Active` state, time-window check, deletion-timestamp skip, `len(TargetOverrides)>0`).
4. `transitionToWakingUp()` (`state_idle.go:199`): if suspend override active, build `targetList` via helper instead of snapshot/live spec. Do NOT write `PlanSnapshot` / `AppliedExceptionOverride` — preserve the original hibernation snapshot so Monday full wakeup still has full intent. Set `Status.Executions` to the subset.
5. `effectivePlan()` (`state.go:269`): add branch — if `CurrentOperation==WakeUp` and active suspend override exists, return suspend-filtered copy ignoring snapshot. Otherwise existing snapshot-first logic. This ensures `execute()` during `WakingUp` dispatches only subset jobs.
6. `validateRuntimeOverrides()`: keep skipping `suspend` for hibernate validation; wakeup subset was already validated at webhook + re-validated via `validateTargetOverrides` against the filtered list (same helper as snapshot path).

Why not overwrite snapshot: overwriting with a 2-target subset would leak into Monday's cycle (same `CycleID`), causing the remaining 8 to be forgotten. Preserving the snapshot is the anti-leak mechanism.

### 5.4 Restore data interaction

Remaining 8 targets keep `IsLive=true` restore data from Friday hibernation. Weekend wakeup only creates jobs for 2 targets, so 8 entries stay live. Monday full wakeup finds all 10 live entries and wakes the 8 still-stopped; the 2 already-running no-op.

No `RestoreManager` change needed.

## 6. Unintended Scenarios Prevented

| Risk | Prevention |
|---|---|
| Override leaks beyond weekend | No snapshot overwrite; `effectivePlan` wakeup branch is time-gated (`ValidFrom/Until` + `Active` state); expiry rebuilds from base |
| Wrong targets woken | Webhook target-existence check + runtime `validateTargetOverrides` + unknown-target skip with log |
| Breaking DAG order by disabling a dependency | Reused `hasDependencyOn` check rejects it at admission |
| Two exceptions fighting over targets | Reused Rule 4 single-override-per-plan |
| Editing exception mid-wakeup corrupts run | Partially covered: the mid-cycle webhook block keys on `AppliedExceptionOverride`, which the suspend path deliberately never sets (to avoid locking partial intent into the snapshot). Editing/deleting a suspend exception during `WakingUp` is therefore NOT blocked — accepted gap for v1.7.0 (small window, next cycle rebuilds from live state). Extend/replace mid-cycle protection unchanged |
| Strategy accidentally changed on suspend | `executionOverride` stays `Forbidden` for `suspend` |
| Unspecified fields unintentionally reset | As-is merge rule: only listed `targetName` + set fields change; all else deep-copied from base |

## 7. Testing

- `go test ./internal/validationwebhook/...`: suspend+`targetOverrides` accepted; suspend+`executionOverride` rejected; suspend nonexistent target / bad params / DAG-disabled rejected; overlapping extend+suspend both with overrides rejected.
- `go test ./internal/provider/processor/plan/state/...`: hibernate ignores suspend override; `Hibernated→WakingUp` with suspend override yields subset executions; snapshot preserved; expiry (no active exception) yields full base list.
- `go test ./internal/scheduler/...`: no behavior change (suspend trigger unchanged).
- Manual: 10-target plan, weekend suspend disabling 8, verify 2 jobs, verify Monday full wakeup.

## 8. Rollout

- Behind no flag; gated by exception `type: suspend` + presence of `targetOverrides`. Existing `suspend` without overrides behaves exactly as before.
- Docs: update `website/docs` exception examples + release note one-liner.

## Appendix: Release Note (PM wording)

> v1.7.0 lets you run a temporary subset (e.g. 2 of 10 RDS) during a suspend window, with automatic revert to full capacity when the window ends.

---

## Delta 2026-09-06: disabled-as-skipped + wakeup freeze (supersedes filtering approach)

**Context:** Review of the filtering implementation (§5.3 as first built) showed that removing
disabled targets from `Spec.Targets` breaks all name-referencing strategy artifacts: `PlanDAG`
hard-errors on dangling edges (`ErrTargetNotFound` → `PhaseError` loop, and the webhook only guards
the upstream side), and `Staged` stages hang forever on ghost entries (`GetStageStatus` marks missing
entries `HasPending`). A Visitor that prunes the computed `ExecutionPlan` was considered and rejected:
seeding skipped entries is strictly simpler (no new infrastructure, no index-shift hazard).

**Agreed rules (uniform for extend/replace/suspend):**

- **D1 — `disabled` never removes targets.** `Spec.Targets` always keeps the full base list
  (params replacements still applied as-is). At each transition (`transitionToHibernating`,
  `transitionToWakingUp`) while an override exception is active, executions for disabled targets are
  seeded `StateCompleted` with message `Skipped: disabled by exception <name>`. `executeForStage`
  already skips terminal entries, so no jobs dispatch; `GetStageStatus` counts them terminal, so
  stages complete. DAG validation always sees whole graphs; Staged stages always resolve.
- **D2 — Drop the DAG-disable rejection.** A disabled upstream is instantly-complete, so dependents
  proceed with ordering semantics trivially preserved. `hasDependencyOn` gate deleted; disabled is
  always allowed on any strategy. (Parameters-only overrides were never affected.)
- **D3 — Freeze wakeup intent to `Executions` once started.** During `WakingUp` with non-empty
  `Status.Executions`, the working set derives from the frozen execution names, not the live
  exception: while the suspend exception is still active its param overrides keep applying;
  once it expires/is deleted, remaining stages run the frozen subset with base params. This aligns
  suspend with the snapshot philosophy (in-flight cycles run on locked intent; edits affect future
  cycles) and closes the expiry/deletion-mid-wakeup stranded-cycle state with no edit-block machinery.
- **D4 — No strategy/planner/validator-framework changes.** Sequential/Parallel work as before;
  DAG/Staged now work via D1. `executionOverride` stays forbidden on `suspend`; existence, params,
  single-slot, and time-gating rules unchanged.

**Supersessions:** §5.3 items 1/4/5 filtering helper becomes a params-only helper + seeding at
transitions; §6 DAG row (rejection → D2) and mid-cycle row (accepted gap → closed by D3) updated
accordingly; §7 suspend DAG-disabled test flips from rejected to seeded-skipped. Extend/replace
`disabled` behavior changes from removal to seeded-skipped (uniform rule; existing tests updated).

**Out of scope (documented):** extend-at-hibernate + suspend-at-wakeup overlap within one cycle
(Rule 4 blocks overlapping validity, but disjoint-validity same-cycle is possible). The wakeup uses
live-base params/strategy, not the extend snapshot's — extra wakeup jobs for never-hibernated targets
no-op via missing restore data, and revert always wakes the full snapshot. Manual revert likewise
wakes the full snapshot; suspend seeding never applies to revert.
