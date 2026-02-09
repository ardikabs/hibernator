# E2E Test Suite

## Overview

Comprehensive end-to-end tests for the Hibernator Operator covering hibernation cycles, wakeup operations, schedule evaluation, and error recovery. These tests use `envtest` to simulate a real Kubernetes cluster environment.

## Test Structure

```bash
test/e2e/
├── e2e_test.go           # Ginkgo entry point
├── README.md             # This file
├── tests/
│   ├── suite.go          # Test suite setup (envtest, manager, controllers)
│   └── lifecycle.go      # Golden path: Active -> Hibernated -> WakingUp -> Active
└── testutil/             # Reusable test utilities and assertions
    ├── assertions.go     # EventuallyPhase, EventuallyJobCreated, etc.
    ├── builder.go        # HibernatePlanBuilder for fluent CR creation
    └── reconcile.go      # Manual reconciliation triggers
```

## Test Utilities

The suite provides several helpers in `test/e2e/testutil/` to simplify test code:

- **HibernatePlanBuilder**: Fluent API for creating complex `HibernatePlan` objects.
- **EventuallyPhase**: Waits for a plan to reach a specific phase (e.g., `Active`, `Hibernated`).
- **EventuallyJobCreated**: Waits for the runner Job to be spawned for a specific operation.
- **SimulateJobSuccess**: Updates a Job's status to simulate successful completion, including conditions.
- **EnsureDeleted**: Deletes an object and waits until it is fully removed from the API.
- **TriggerReconcile**: Forces a reconciliation loop by updating an annotation (useful with fake clocks).

## Running Tests

### Prerequisites

The tests require `envtest` binaries. You can install them using:

```bash
make envtest
```

### Execute Tests

```bash
# Run all E2E tests
go test ./test/e2e/... -v -tags=e2e -ginkgo.v

# Run with Ginkgo for better output
ginkgo -v test/e2e/
```

## Test Coverage: Lifecycle (`lifecycle.go`)

The lifecycle suite validates the "Golden Path" of a resource hibernation:

1. **Initialization**: Plan starts in `Active` phase.
2. **Hibernation Trigger**: Advancing time into the off-hours window triggers transition to `Hibernating`.
3. **Runner Execution**: A runner Job is created for the `shutdown` operation.
4. **Hibernated**: Upon Job completion, the plan transitions to `Hibernated` and restore data is saved.
5. **Wakeup Trigger**: Advancing time into the on-hours window triggers transition to `WakingUp`.
6. **Wakeup Execution**: A runner Job is created for the `wakeup` operation.
7. **Restored**: Upon Job completion, the plan returns to `Active` phase.

## Integration Points

The E2E tests validate:

- **Controller Reconciliation**: Full reconcile loop execution.
- **Job Management**: Runner Job lifecycle and label selector logic.
- **ConfigMap Operations**: Restore data persistence and retrieval.
- **ServiceAccount Management**: Correct SA usage for runners.
- **Status Tracking**: Phase transitions and execution ledger consistency.
- **Schedule Evaluation**: Timezone-aware window calculation.

## Documentation References

- [API Types](../../api/v1alpha1/)
- [Core Design Principles](../../.github/instructions/core-design-principles.md)
- [RFC-0001 (Hibernation Operator)](../../enhancements/0001-hibernate-operator.md)