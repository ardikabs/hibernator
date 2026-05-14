# Plan: Operation Guide (Day-2 Tasks)

## Summary

Create a dedicated documentation section focused on day-2 operational tasks for Hibernator.

Working name: **Operation Guide**.

This section complements existing technical/reference material by providing practical, task-based guidance similar to the Kubernetes "Tasks" style.

## Why This Plan

Current documentation is strong on architecture and technical concepts, but less optimized for repetitive operational execution.

Internal platform teams need quick, actionable guidance for:

- on-demand operations,
- exception lifecycle handling,
- incident recovery,
- and GitOps/API coexistence decisions.

## Goals

1. Introduce a clear docs section for day-2 operations.
2. Organize content by operational task outcomes, not internals.
3. Cover both GitOps baseline workflows and API-bridge on-demand workflows.
4. Make operator decisions explicit with runbook-style guidance.
5. Improve usability for internal platform teams, SRE, and on-call operations.

## Non-Goals

- Replacing RFCs or architecture docs.
- Duplicating all existing user journey content verbatim.
- Providing deep implementation internals in task pages.

## Information Architecture Proposal

Suggested top-level structure under documentation site:

- Operation Guide
  - Get Started with Day-2 Operations
  - On-Demand Operations
  - Exception Management
  - Incident and Recovery Tasks
  - GitOps and API Coexistence
  - Troubleshooting Playbooks
  - Auditing and Cleanup Tasks

## Core Day-2 Task Backlog

### A. On-Demand Tasks (Priority 1)

- [ ] Trigger on-demand hibernation intent safely.
- [ ] Create/extend/suspend `ScheduleException` from operational request.
- [ ] Handle manual retry and resume workflows.
- [ ] Verify operation completion and rollback path.

### B. GitOps Baseline Tasks (Priority 1)

- [ ] Run standard GitOps flow for `HibernatePlan` changes.
- [ ] Detect and handle API-vs-GitOps drift scenarios.
- [ ] Establish source-of-change conventions and labels.

### C. API Bridge Tasks (Priority 1)

- [ ] Process Slack-driven request through `/external/slack`.
- [ ] Validate payload outcomes and Kubernetes resource materialization.
- [ ] Interpret strict rejection vs warning-first response.
- [ ] Understand ownership/cascade implications for API-generated resources.

### D. Incident and Recovery Tasks (Priority 2)

- [ ] Diagnose failed execution quickly.
- [ ] Recover from error state using approved retry workflow.
- [ ] Apply temporary exception strategy during incidents.
- [ ] Close incident and restore baseline schedule state.

### E. Cleanup and Lifecycle Tasks (Priority 2)

- [ ] Identify stale/expired exceptions.
- [ ] Safely prune no-longer-needed operational resources.
- [ ] Validate uninstall behavior and cascading deletion side effects.

## Content Standard for Each Operation Guide Page

Each page should use the same operator-first template:

1. **Task Goal**: What operator wants to achieve.
2. **When to Use**: Trigger conditions.
3. **Prerequisites**: Access, policy, approvals.
4. **Steps**: Ordered operational steps.
5. **Expected Result**: Observable success criteria.
6. **Warnings and Guardrails**:
   - strict enforcement boundaries,
   - warning-first cosmetic checks.
7. **Rollback/Recovery**: What to do when task fails.
8. **Audit Notes**: What to capture for governance.
9. **Related Tasks**: Next operational moves.

## Integration with Existing Docs

- Reuse concepts from user journeys and proposals; avoid contradictory guidance.
- Add cross-links:
  - from user journey pages to corresponding Operation Guide tasks,
  - from API docs to operational tasks,
  - from troubleshooting pages to recovery tasks.

## Phased Work Plan

### Phase 1: Section and Template Foundation

- [ ] Create Operation Guide section in docs nav.
- [ ] Define canonical task template.
- [ ] Set style rules (operator-focused, concise, task outcome driven).

### Phase 2: Priority-1 Task Pages

- [ ] Publish On-Demand core tasks.
- [ ] Publish GitOps baseline + drift handling tasks.
- [ ] Publish Slack API bridge operational tasks.

### Phase 3: Incident and Recovery Coverage

- [ ] Publish incident/recovery runbooks.
- [ ] Publish exception emergency handling tasks.
- [ ] Publish post-incident normalization checklist.

### Phase 4: Lifecycle and Governance Additions

- [ ] Publish cleanup and uninstall lifecycle tasks.
- [ ] Add audit checklist pages.
- [ ] Add governance quick-reference matrix.

### Phase 5: Usability Validation and Iteration

- [ ] Run doc walkthrough with internal platform team.
- [ ] Capture top friction points.
- [ ] Iterate wording, sequence, and troubleshooting depth.

## Acceptance Criteria

1. Operation Guide section exists and is visible in docs navigation.
2. Priority-1 day-2 tasks are documented end-to-end.
3. Slack/API bridge operations are documented with strict-vs-warning behavior.
4. GitOps baseline and coexistence boundaries are clearly documented.
5. Incident recovery playbooks are actionable for on-call users.
6. Internal platform team validates guide usefulness in real tasks.

## Naming Decision

Current chosen name: **Operation Guide**.

Future rename candidates (if needed):

- Day-2 Tasks
- Operational Tasks
- Runbooks

## Risks and Mitigations

- **Risk**: Becomes duplicate of user journey docs.
  - **Mitigation**: Keep operation pages procedural and link to journey/RFC for deeper context.
- **Risk**: Docs drift from product behavior.
  - **Mitigation**: Add doc ownership and review checklist per feature release.
- **Risk**: Too conceptual, not actionable.
  - **Mitigation**: Enforce task template and include expected results/rollback sections.

## Deliverables

- [ ] Operation Guide section scaffolding.
- [ ] Task template for all operation pages.
- [ ] Initial set of Priority-1 task pages.
- [ ] Cross-link updates from existing docs.

## References

- Existing user journey documentation
- RFC-0007 (CLI operations)
- RFC-0008 (main async reconciler)
- On-demand API bridge plan (Slack) in this same directory
