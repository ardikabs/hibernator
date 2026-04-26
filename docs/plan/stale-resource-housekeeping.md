# Plan: Stale Resource Housekeeping

## Context
As detailed in the finding [Stale Resource Housekeeping in RestorePoint](../findings/stale-resource-housekeeping.md), the `RestorePoint` ConfigMap accumulates state for resources that have been deleted or have become permanently unmanageable (stale). Currently, these resources are simply skipped during the shutdown process, meaning they are never cleared from the existing state and persist across all subsequent hibernation cycles.

## Objectives
1. Safely remove persistently stale resources from the `RestorePoint` ConfigMap.
2. Ensure temporary absences (e.g., transient API failures or accidental untagging) do not lead to immediate, irreversible removal of restore data.
3. Keep the solution localized, abstract, and transparent to the various `Executor` implementations.

## Solution Path

We will implement **Cycle-Based Staleness Tracking** within the `Runner` and `RestoreManager`.

### 1. Augment `RestoreData` with Staleness Tracking
We will expand the `restore.Data` model to track staleness over consecutive cycles.
- Add `StaleCounts map[string]int` to `restore.Data`. This maps a resource key to the number of consecutive `shutdown` cycles it has been absent from the executor's reported data.

### 2. Implement Housekeeping Logic in `RestoreManager`
Add a dedicated method `HousekeepStaleResources` to `restore.Manager`. This method will:
- Accept a list of all resource keys discovered/processed during the current cycle (`reportedKeys`).
- Iterate over the `existing.State` from the ConfigMap.
- For each existing key, check if it's in `reportedKeys`:
  - **If missing**: Increment `StaleCount`. If `StaleCount >= maxStaleCount`, remove the key from the `State`.
  - **If present**: Remove the key from `StaleCounts` (resetting its staleness).
- Save the updated `existing.State` back to the ConfigMap.

### 3. Integrate with the Runner
The `Runner` orchestrates the execution and maintains the accumulated state before persisting it via `flush()`.
- After `accumulator.flush()` writes the `liveKeys` and `nonLiveKeys` to the ConfigMap via `SaveOrPreserve`, it will compile all keys from both maps into `reportedKeys`.
- The `Runner` will then invoke `HousekeepStaleResources` passing `reportedKeys` and a hardcoded threshold (`maxStaleCount = 3`).

## Justification
This approach requires **zero changes** to individual executors (like `RDS`, `EKS`, or `Karpenter`). It centrally manages the staleness metric by observing the behavior of the executors. The 3-cycle buffer provides sufficient resilience against transient discovery failures without permanently retaining garbage data.
