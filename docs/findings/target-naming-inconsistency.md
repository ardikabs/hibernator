---
date: February 9, 2026
status: resolved
component: controller, runner, api
---

# Finding: Target Naming Inconsistency Across Components

## Problem Description

Standardized on **Simple Target Name (`<name>`)** across all components to eliminate ambiguity and logic complexity caused by mixed usage of qualified IDs (`type/name`) and simple names.

## Decision

Standardize all internal references, Kubernetes labels, status fields, and documentation to use the **Simple Target Name** (`target.Name`).

**Rationale:**

- **Simplicity**: Matches the natural identification of targets within a `HibernatePlan`.
- **Consistency**: Aligns with Job labeling and service account naming patterns already in use.
- **Uniqueness**: Target names are already required to be unique within a single `HibernatePlan` by the validation webhook.
- **Reliability**: Simplifies status lookup logic in the controller, reducing the risk of matching errors.

## Resolution

The following changes were implemented:

1.  **Standardized Status Ledger**: Updated `internal/controller/hibernateplan/operation_handler.go` to initialize `ExecutionStatus.Target` using only `target.Name`.
2.  **Job Labeling**: Verified `internal/controller/hibernateplan/helper.go` uses `wellknown.LabelTarget` with `target.Name`. Added `wellknown.LabelExecutor` for explicit type tracking.
3.  **Controller Logic**: Updated `internal/controller/hibernateplan/controller.go` to match jobs using both `wellknown.LabelTarget` and `wellknown.LabelExecutor` labels against the status ledger.
4.  **E2E Tests**: Refactored `lifecycle.go` and `execution_strategy.go` to assert on simple names.
5.  **Restore Manager**: Confirmed `internal/restore/manager.go` uses simple names for ConfigMap keys and data.
6.  **Documentation**: Updated all user journeys and RFCs to reflect the simple naming convention in examples and status outputs.

The qualified ID format (`type/name`) is no longer used for internal tracking or labeling.