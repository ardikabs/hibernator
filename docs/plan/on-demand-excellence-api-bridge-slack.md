# Plan: On-Demand Excellence via Slack API Bridge

## Summary

Build a direct Hibernator API entrypoint for Slack-driven on-demand intent, while keeping GitOps as the standard operating baseline.

The API acts as a bridge that translates validated interface input into Kubernetes resources (`HibernatePlan` and `ScheduleException`) so Hibernator continues to rely on Kubernetes admission/webhook validation and native reconciliation.

For initial rollout, the interface is Slack via webhook-backed interaction flow and dedicated endpoint path:

- `POST /external/slack`

## Product Direction

- **Baseline**: GitOps remains the expected default for operating Hibernator.
- **Enhancement**: API bridge enables internal platform teams to execute on-demand intent quickly.
- **Governance posture**:
  - Strict enforcement for intent-changing operations (schedule/exception/hibernation behavior).
  - Warning-first for cosmetic or typography-only concerns.

## Problem

Internal platform teams increasingly need day-2, on-demand operations through interfaces (e.g., Slack) rather than manual YAML updates.

Without an official bridge:

- Teams build ad-hoc automations outside Hibernator contract boundaries.
- Governance and audit consistency degrade.
- User experience is fragmented between GitOps and ad-hoc workflows.

## Goals

1. Expose direct API endpoints per interface, starting with Slack at `/external/slack`.
2. Convert interface payloads into first-class Kubernetes resources (`HibernatePlan`/`ScheduleException`).
3. Reuse Kubernetes admission and Hibernator webhook validation; no bypass logic.
4. Host API-generated resources in Hibernator controller root namespace.
5. Use system-generated names for API-created resources to reduce naming conflict with GitOps-managed resources.
6. Attach `OwnerReference` to Hibernator Deployment to enforce cascading deletion on uninstall.
7. Publish OpenAPI spec for endpoint contract and testing automation.

## Non-Goals

- Replacing GitOps workflows.
- Building approval logic inside Hibernator.
- Supporting all interfaces in phase 1 (Slack only).
- Implementing a generic workflow engine for approvals.

## Core Design Decisions

### 1. API as Bridge, Not Source of Truth Replacement

The API does not execute hibernation directly. It only materializes Kubernetes resources that are reconciled by existing Hibernator flow.

### 2. Namespace and Naming Strategy

- API-created resources are placed in Hibernator controller namespace (root namespace).
- Resource names are system-generated (e.g., prefix + ULID/timestamp hash).
- Optional labels/annotations identify source as API-generated.

### 3. Ownership and Lifecycle

All API-generated resources must set `OwnerReference` to the Hibernator Deployment.

Expected behavior:

- If Hibernator is uninstalled, Kubernetes garbage collection cascades deletion to API-generated resources.

User warning to document prominently:

- API-generated resources are lifecycle-bound to Hibernator installation and may be deleted during uninstall.

### 4. Approval Boundary

Approval remains external (Slack/workflow platform).

Hibernator expects only approved payloads reaching `/external/slack`.

### 5. OpenAPI Contract

Endpoint contract must be generated/maintained with OpenAPI.

- If Slack provides compatible OpenAPI material for incoming payload shape, adopt and adapt.
- Otherwise, define Hibernator-owned OpenAPI spec based on official Slack webhook interaction docs.

## API and Resource Mapping

### Endpoint

- `POST /external/slack`

### Request Model (High-Level)

- `interface`: `slack`
- `operationType`: `hibernateplan.create|scheduleexception.create|scheduleexception.extend|scheduleexception.suspend|retry|suspend|resume`
- `requestedBy`
- `approvalRef` (external approval identifier)
- `targetRef` / payload details
- `intentPayload`

### Output Model (High-Level)

- `requestId`
- `accepted`: `true|false`
- `resourceKind`
- `resourceName`
- `namespace`
- `warnings[]`
- `errors[]`

### Validation Sequence

1. Validate request against OpenAPI schema.
2. Apply business contract validation (strict for intent-impacting fields).
3. Build target Kubernetes resource object.
4. Submit to Kubernetes API in controller root namespace.
5. Let admission webhook and CRD schema validate final object.
6. Return accepted/rejected response with warnings/errors.

## Policy and Severity Model

### Strict (Reject)

- Invalid intent semantics.
- Missing required fields for behavior-changing operations.
- Unsupported operation type.
- Approval reference missing when required by interface policy.
- Namespace/ownership violation.

### Warning-First (Allow + Warn)

- Cosmetic/typography inconsistencies.
- Non-functional formatting issues in optional metadata.
- Optional descriptive fields that do not affect reconcile behavior.

## Security and Governance

1. Authenticate interface caller (shared secret/signature or gateway policy).
2. Authorize allowed operations per endpoint policy.
3. Record immutable request metadata for audit (`requestId`, source, approver ref, actor).
4. Enforce labels on API-generated resources:
   - source interface
   - request id
   - approval reference
   - ownership class

## Phased Work Plan

### Phase 1: Contract and API Foundation

- [ ] Define endpoint contract for `/external/slack`.
- [ ] Produce OpenAPI spec (import or author from Slack docs).
- [ ] Scaffold API handler with request validation.
- [ ] Define error model and warning model.

### Phase 2: Bridge Logic and Kubernetes Resource Builder

- [ ] Build business logic mapper: request -> `HibernatePlan` or `ScheduleException` object.
- [ ] Enforce root namespace placement.
- [ ] Add system-generated naming strategy.
- [ ] Add labels/annotations for source tracking.

### Phase 3: Ownership and Lifecycle Safety

- [ ] Resolve Hibernator Deployment reference.
- [ ] Attach `OwnerReference` to API-created resources.
- [ ] Validate cascading deletion behavior in uninstall scenario.
- [ ] Add user-facing warning in docs and API response metadata.

### Phase 4: Governance Rules and Policy Severity

- [ ] Implement strict validation set for intent-impacting fields.
- [ ] Implement warning-first path for cosmetic fields.
- [ ] Add policy configuration toggles if needed.

### Phase 5: Testing and Rollout

- [ ] Unit tests for mapping, naming, validation severity.
- [ ] Integration tests for admission webhook pass/fail behavior.
- [ ] E2E tests for Slack request -> resource creation -> reconcile lifecycle.
- [ ] E2E tests for uninstall cascade behavior.

## Acceptance Criteria

1. `POST /external/slack` accepts valid payload and creates expected resource in controller root namespace.
2. Resource names are system-generated and avoid user naming conflicts.
3. Admission/webhook validation is exercised via Kubernetes API create path.
4. API-created resources have correct `OwnerReference` to Hibernator Deployment.
5. Uninstall simulation causes cascading deletion of API-created resources.
6. Strict policy rejects invalid intent payloads; cosmetic issues return warnings without blocking.
7. OpenAPI schema is available and used in tests.

## Risks and Mitigations

- **Risk**: Drift between Slack payload evolution and API parser.
  - **Mitigation**: OpenAPI contract versioning + compatibility tests.
- **Risk**: Over-coupling lifecycle to Deployment OwnerReference surprises users.
  - **Mitigation**: Explicit warning in API docs and operation guide.
- **Risk**: GitOps and API updates conflict.
  - **Mitigation**: Source labels, naming isolation, and reconciliation policy docs.

## Documentation Deliverables

- [ ] API reference for `/external/slack` (request/response + examples).
- [ ] OpenAPI spec publishing location and version policy.
- [ ] Operational warning note on cascading deletion.
- [ ] GitOps coexistence guide (API bridge behavior and boundaries).

## References

- RFC-0001 (core control-plane and CRD contract)
- RFC-0003 (schedule exception semantics)
- RFC-0007 (day-2 CLI operations)
- RFC-0008 (async reconciler as mainline)
