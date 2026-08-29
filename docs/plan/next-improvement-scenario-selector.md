# Plan: Next Improvement Scenario Selector

## Summary

This document helps choose execution mode for the next improvement period across two approved plans:

- Plan A: On-Demand Excellence via Slack API Bridge
- Plan B: Operation Guide (Day-2 Tasks)

Use this as a quick decision aid for scope, timeline, staffing, and risk tradeoffs.

## Scope Inputs

### Plan A (Product + Platform Capability)

Reference: [on-demand-excellence-api-bridge-slack.md](./on-demand-excellence-api-bridge-slack.md)

Primary outcomes:

- Slack endpoint at `/external/slack`
- API-to-Kubernetes bridge for `HibernatePlan`/`ScheduleException`
- OpenAPI contract and automated validation/testing foundation
- GitOps baseline preserved while enabling on-demand operations
- OwnerReference lifecycle with cascade behavior and explicit warnings

### Plan B (Operational Usability)

Reference: [operation-guide-day2.md](./operation-guide-day2.md)

Primary outcomes:

- New Operation Guide section (task-oriented day-2 docs)
- GitOps + API coexistence operational guidance
- On-demand playbooks, incident/recovery runbooks, and lifecycle tasks

## Scenarios

## Scenario 1: A Only

### Description

Execute only Plan A for this period. Operation Guide work deferred to next period.

### Estimated Timeline

- 8 to 10 weeks (single stream with testing hardening)

### Suggested Capacity

- 1 product owner
- 2 to 3 backend/platform engineers
- 1 QA/SDET shared
- 1 technical writer part-time (for API docs only)

### Pros

- Fastest path to on-demand capability and interface integration
- Strong governance and contract foundation lands sooner
- Maximum focus on highest product leverage track

### Cons

- Day-2 user enablement lags behind new capability
- Higher support burden after release
- Risk of operator misuse due to documentation gap

### Best Fit

- Capacity constrained team
- Strong urgency for on-demand API delivery
- Internal users can absorb temporary docs gap

## Scenario 2: B Only

### Description

Execute only Operation Guide and defer API bridge implementation.

### Estimated Timeline

- 4 to 6 weeks

### Suggested Capacity

- 1 product owner
- 1 technical writer
- 1 platform engineer part-time for validation
- 1 SRE/on-call reviewer

### Pros

- Quick usability gains for current workflows
- Low implementation risk
- Immediate reduction of day-2 confusion and support tickets

### Cons

- Does not deliver new on-demand product capability
- Slack/interface opportunity remains open
- Competitive and adoption momentum may slow

### Best Fit

- Need immediate operations clarity
- Engineering bandwidth unavailable for API build
- Preparing teams before next major feature wave

## Scenario 3: A + B Parallel

### Description

Run Plan A as primary stream and Plan B as companion stream in parallel.

### Estimated Timeline

- 8 to 10 weeks total (Plan B finishes earlier, then supports A rollout)

### Suggested Capacity

- Stream A (API bridge):
  - 1 product owner
  - 2 to 3 backend/platform engineers
  - 1 QA/SDET shared
- Stream B (Operation Guide):
  - 1 technical writer
  - 1 platform engineer reviewer (part-time)
  - 1 SRE reviewer (part-time)

### Pros

- Delivers new capability and adoption readiness together
- Lower rollout risk because docs/runbooks ship with feature
- Better internal platform-team experience from day 1

### Cons

- Higher coordination overhead
- Requires disciplined scope control across two streams
- More concurrent review and release management effort

### Best Fit

- Moderate team capacity available
- High importance on both capability and usability
- Desire to reduce post-release operational friction

## Comparison Matrix

| Criteria | Scenario 1: A Only | Scenario 2: B Only | Scenario 3: A+B Parallel |
|---|---|---|---|
| New product capability | High | Low | High |
| Day-2 usability impact | Medium | High | High |
| Governance progression | High | Medium | High |
| Delivery risk | Medium | Low | Medium |
| Support burden after release | Medium/High | Low | Low/Medium |
| Time to visible value | Medium | Fast | Medium/Fast |
| Strategic fit with current priority | High | Medium | Very High |

## Recommendation Logic

Use this decision rule:

1. If on-demand capability is urgent and capacity is tight: choose Scenario 1.
2. If immediate operator effectiveness is the sole short-term objective: choose Scenario 2.
3. If you want strongest combined outcome and can staff parallel work: choose Scenario 3.

## Preferred Choice (Current Context)

Recommended default: Scenario 3 (A+B Parallel).

Rationale:

- On-demand excellence is your explicit product priority.
- GitOps baseline must stay intact and documented.
- Internal platform teams benefit significantly when feature + operation guidance launch together.

## Proposed Execution Sequence for Scenario 3

### Weeks 1-2

- Stream A: API/OpenAPI contract, endpoint scaffolding, validation model
- Stream B: Operation Guide IA, templates, first core task pages

### Weeks 3-5

- Stream A: resource bridge logic, naming/namespace rules, ownership and lifecycle
- Stream B: GitOps coexistence tasks, on-demand operation tasks, strict vs warning guidance

### Weeks 6-8

- Stream A: integration tests, e2e, security hardening, rollout readiness
- Stream B: incident/recovery runbooks, review with internal platform team

### Weeks 9-10 (optional hardening buffer)

- Cross-stream QA, feedback incorporation, final docs alignment

## Exit Criteria by Scenario

### Scenario 1 Exit

- `/external/slack` endpoint live with validated OpenAPI contract
- API-generated resources reconciled successfully under policy rules

### Scenario 2 Exit

- Operation Guide section live with priority day-2 tasks and runbooks
- Internal platform team validation complete

### Scenario 3 Exit

- Scenario 1 + Scenario 2 exits both complete
- Joint rollout checklist approved by platform operations
