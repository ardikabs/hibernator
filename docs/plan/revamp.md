## Currently, the system manages multiple CRDs:

- HibernatePlan (primary resource) -> provider hibernateplan
- ScheduleException (overrides behavior for a specific plan, 1:1 relationship) -> provider schedule exception
- (upcoming) Notification (applies to multiple plans via label selectors, 1:N relationship) -> (not yet implemented) provider notification

The goal is to move away from a global reconciliation loop that scans all resources, into a more scalable and event-driven architecture.

The proposed design is:
- Use a single reconciler scoped to HibernatePlan only
- Register multiple watchers:
    - Watch HibernatePlan -> already implemented
    - Watch for changes to ScheduleException resources, and trigger reconciliation of the associated HibernatePlan when they change via spec.planRef.name
    - resolve matching HibernatePlans via label selector and enqueue them (on planning)
Avoid global scans; instead use event-driven enqueueing
So the contract is like we use single reconciler for only HibernatePlan, and only trigger reconciliation if any of CRDs related to HibernatePlan changes. Cause this operator mainly built in HibernatePlan as its core. Therefore instead of creating multiple reconcilers for each CRD, we can just use one reconciler for HibernatePlan and trigger it when any related CRD changes. As a result, there will be only 1 provider, just for data collection and will be dispatched through the watchable.Map for PlanContext that contains all related resource for the HibernatePlan, currently the only related resource is ScheduleException, but in the future we can add more related resources like Notification. This way we can avoid global scans and only trigger reconciliation when there are relevant changes to the resources that affect the HibernatePlan.

To achieve this and improve the performance, we will use field indexing:
- Full index on ScheduleException.spec.planRef.name
- We will figure this later for another upcoming resource like Notification.

The internal engine is a per-plan FSM (finite state machine) handling, which already exists in the current implementation, through the coordinator + worker model.
But we will create it for another handler of watchable.Map for the ScheduleException lifecycle (please help to figure this out).
One thing for sure regarding this revamp is, we currently face a known bug for ScheduleException where the handler of next requeue, even when it is triggered, i didnt update the ScheduleException in its lifecycle cause there is no change found in the ScheduleException itself, so the HandleSubscription never triggered what considered as update, due to that, i was thinking then why not just make it single reconciler for HibernatePlan, but can dispatch to all necessary resources associated with that Plan.

