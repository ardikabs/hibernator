# E2E Test Suite Adjustment Plan

## Overview
This document outlines the necessary adjustments to the E2E test suite to ensure alignment with the current API definitions and recent controller implementation changes (specifically the infinite loop fix in ScheduleException controller).

## Findings

### 1. API Consistency
- **Schedule Struct**: The `Schedule` struct in `api/v1alpha1/hibernateplan_types.go` uses `OffHours` (not `OffHourWindows`). The E2E tests correctly use `OffHours`, confirming that the `README.md` warning about this field has been addressed.
- **CloudProvider Spec**: The `CloudProvider` spec in `api/v1alpha1/cloudprovider_types.go` requires `Type` (enum) and `AWS` (*AWSConfig). The tests correctly structure this, though they often use string literals (e.g., `"aws"`) instead of defined constants.
- **Parameters**: The `Parameters` field in `Target` is correctly handled as `*Parameters` with `Raw` byte slice in tests.

### 2. Webhook Validation
- **Overlapping Exceptions**: `api/v1alpha1/scheduleexception_webhook.go` enforces that no two exceptions for the same plan can overlap in time.
- **Test Validation**: `test/e2e/exception_test.go` contains a test case "Should enforce single active exception per plan" which expects an error when creating an overlapping exception. This aligns with the webhook logic.

### 3. Infinite Loop Fix Relevancy
- The recent fix in `internal/controller/scheduleexception/controller.go` resolved a Watch-Patch feedback loop when multiple exceptions referenced the same plan.
- The existing tests do not explicitly verify the absence of this loop (e.g., by creating multiple *non-overlapping* exceptions and checking for controller stability). However, the current tests are safe and will not trigger the loop because they mostly deal with single exceptions or overlapping ones that get rejected by the webhook.

## Planned Adjustments

### 1. Refactor String Literals to Constants
To improve maintainability and type safety, replace string literals with exported constants from the API package.

**Target Files:**
- `test/e2e/concurrency_test.go`
- `test/e2e/exception_test.go`
- `test/e2e/hibernation_test.go`
- `test/e2e/recovery_test.go`
- `test/e2e/schedule_test.go`
- `test/e2e/wakeup_test.go`

**Changes:**
- Replace `"aws"` with `hibernatorv1alpha1.CloudProviderAWS`
- Replace `"ec2"`, `"rds"`, `"eks"` with appropriate constants if available (or define them in tests if not exported)
- Replace strategy types (e.g., `"Sequential"`) with constants like `hibernatorv1alpha1.StrategySequential`
- Replace phase constants (e.g., `hibernatorv1alpha1.PhaseHibernating`) where missing

### 2. Enhance Error Checking in Exception Tests
The test "Should enforce single active exception per plan" in `test/e2e/exception_test.go` currently only checks `Expect(err).To(HaveOccurred())`.

**Change:**
- Update to verify the error message contains specific details about the overlap, ensuring the rejection is due to the webhook logic and not some other failure.
- Example: `Expect(err.Error()).To(ContainSubstring("overlaps with existing"))`

### 3. Typo Fixes
- **`test/e2e/recovery_test.go`**: Fix variable name `stalJob` to `staleJob`.

### 4. Verify Schedule Evaluator Initialization
- `test/e2e/suite_test.go` uses `scheduler.NewScheduleEvaluator()`. Ensure this matches the internal package signature (confirmed as correct).

## implementation Steps
1.  Modify `test/e2e/*.go` files to use API constants.
2.  Refine assertion logic in `test/e2e/exception_test.go`.
3.  Fix identified typos.
4.  Run `go test ./test/e2e/...` (if environment allows) to verify compilation and execution.

## Phase 2: Expanded Scenario Coverage

To ensure the operator handles nuanced real-world conditions, we will implement the following five specific scenarios.

### 1. Happy Path: Full Lifecycle with Persistence
**Goal:** Verify the "Golden Path" where data flows correctly from Hibernate -> State Storage -> Wakeup.
**Implementation:** `test/e2e/lifecycle_test.go` (New File)
- **Scenario:**
    1.  Create a HibernatePlan.
    2.  Wait for Hibernation.
    3.  Verify execution is `Completed`.
    4.  **Critical Check:** Verify `Status.Executions` contains a valid `RestoreConfigMapRef` and the referenced ConfigMap exists with correct data.
    5.  Update schedule to trigger Wakeup.
    6.  Verify Wakeup job starts and completes.
    7.  Verify Plan returns to `Active`.

### 2. Not-So-Happy Path: Resilience to External Interference
**Goal:** Verify controller recovers when an external actor interferes with its resources.
**Implementation:** `test/e2e/resilience_test.go` (New File)
- **Scenario:**
    1.  Trigger a long-running Hibernation job (simulated by not updating status immediately).
    2.  **Action:** Manually `Delete` the running Job from the cluster.
    3.  **Expectation:** The Controller should detect the Job is missing (or finished without status) and either:
        - Create a replacement Job (Idempotency).
        - Mark the execution as Failed (if configured to do so).
    - *Decision:* We will assert that a replacement job is created (Self-Healing).

### 3. Worst Path: Unrecoverable Configuration
**Goal:** Verify behavior when the system is misconfigured in a way that retries cannot fix.
**Implementation:** `test/e2e/validation_test.go` (New File)
- **Scenario:**
    1.  Create a HibernatePlan referencing a `CloudProvider` that does **not exist**.
    2.  **Expectation:**
        - The runner job might fail to start (ImagePullBackOff) or fail immediately if the runner checks dependencies.
        - *Better approach for Controller test:* The Controller creates the job, the Job fails with a specific exit code/reason indicating "Connector Not Found".
    3.  Verify Plan enters `Error` phase quickly.
    4.  Verify `Status.ErrorMessage` clearly indicates the configuration issue.

### 4. Happy Path But...: Boundary Conditions
**Goal:** Verify success under constrained or unusual valid conditions.
**Implementation:** Add to `test/e2e/schedule_test.go`
- **Scenario:** "Short Duration Hibernation"
    1.  Create a Schedule where the Off-Hour window is only **2 minutes** long and starts **1 minute** from now.
    2.  **Expectation:**
        - Plan transitions `Active` -> `Hibernating` (at T+1m).
        - Jobs complete.
        - Plan transitions `Hibernated`.
        - 1 minute later (at T+2m), Plan transitions `WakingUp`.
        - Jobs complete.
        - Plan transitions `Active`.
    - *Why:* Tests the controller's ability to handle rapid state changes without getting stuck.

### 5. Real Situation Case: Emergency Override
**Goal:** Verify a realistic Ops scenario where a user must force an override.
**Implementation:** `test/e2e/exception_test.go`
- **Scenario:** "Emergency Wakeup via Exception"
    1.  Plan is in `Hibernated` state (correctly following schedule).
    2.  **Event:** Ops team receives an alert and needs the system UP immediately.
    3.  **Action:** Create a `ScheduleException` of type `Suspend` (meaning "Suspend Hibernation" = "Wake Up") valid from `Now` to `Now + 4h`.
    4.  **Expectation:**
        - Controller detects the new Exception.
        - Evaluates that "Suspend" overrides the current "Off-Hour".
        - Transitions `Hibernated` -> `WakingUp` immediately.

## Phase 3: Build Tags and Environment Bootstrapping

To allow running E2E tests seamlessly across different environments (CLI, IDE, CI) without manual environment exports, we have implemented a Go Build Tag pattern.

### 1. Build Tag Isolation
- Added `//go:build e2e` to all files in `test/e2e/`.
- This ensures that E2E tests are only executed when `-tags e2e` is provided.

### 2. Programmatic Bootstrapping
- Implemented an `init()` function in `test/e2e/suite_test.go`.
- If `KUBEBUILDER_ASSETS` is missing, the test programmatically runs `setup-envtest use -p path` and exports the result.
- This enables running tests via `go test -v -tags e2e ./test/e2e/...` directly.


