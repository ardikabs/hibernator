## Description

The execution engine's current handling of the `DAG` (Directed Acyclic Graph) strategy combined with `Behavior.Mode = BestEffort` violates the semantic expectations of both DAGs and Best Effort execution.

Currently, if a parent node fails, the operator treats this as a fatal stage error and halts the **entire plan**, driving the hibernation cycle into `PhaseError`. This prevents completely independent branches of the DAG from executing.

## Expected Behavior

In `BestEffort` mode, a DAG execution must confine failures to the **broken branch only**.

Given the following topology:
- Branch A: `web` → `app`
- Branch B: `metrics-db` (independent)

If `web` fails:
1. `app` should be skipped (pruned) because its upstream dependency failed. It should be proactively marked as `StateFailed` with a clear message: `"Pruned: upstream dependency 'web' failed"`.
2. **`metrics-db` must still execute.** Its branch has no dependencies on the failed `web` node.
3. The overall plan should complete (`PhaseHibernated` or `PhaseActive`), not halt in `PhaseError`.

## Actual Behavior

The scheduler flattens the DAG into horizontal time-buckets called **Stages**. During stage advancement in `execute()` (inside `state_execution.go`), the engine evaluates `FindFailedDependencies` against the *entire next stage*.

If *any* target in the incoming stage has a failed dependency, the engine returns an immediate, plan-level error:
`"stage execution blocked: one or more upstream dependencies failed (web), downstream targets will not be dispatched"`

This aborts the entire execution cycle. In the scenario above, `metrics-db` never gets dispatched simply because it had the misfortune of naturally being scheduled into the same flattened stage as the blocked `app` node.

In essence, `DAG` + `BestEffort` currently functions exactly like `DAG` + `Strict` (fail-fast), just delayed by one tier.

## Root Cause Analysis

The logic enforces dependency validity at the **Stage Boundary** rather than the **Target Dispatch Boundary**.

By evaluating `FindFailedDependencies` in the `advancing to next stage` block and returning an `AsPlanError`, we artificially couple independent components together. The algorithm works perfectly for `Sequential`, `Parallel`, and `Staged` strategies, but `DAG` requires node-level evaluation.

## Proposed Solution

Shift the dependency enforcement into `executeForStage` inside the target-iteration loop.

1. **Remove** the `FindFailedDependencies` check from the stage-advancement block in `execute()`.
2. **Target-Level Pruning:** Inside `executeForStage`, when iterating over `stage.Targets`, check if *that specific target* has a failed dependency.
3. **If broken:** Skip creating the runner Job for that target, and explicitly mutate its Execution Status to `StateFailed` (e.g. `"Pruned: upstream dependency failed"`).
4. **Cascade:** Because the pruned target is now technically `StateFailed`, any subsequent targets depending on *it* will naturally get pruned in later stages. All independent targets are unaffected and dispatch normally.