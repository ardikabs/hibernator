# Using the NoOp Executor

This guide covers how to use the `noop` executor for testing, development, and validation without cloud credentials or real resources.

## When to Use NoOp

The NoOp executor is a testing tool. It simulates hibernation operations so you can validate:

- Schedule logic (do off-hours trigger correctly?)
- Execution strategies (does DAG ordering work?)
- Error recovery (does retry logic handle failures?)
- Plan structure (is the YAML valid?)

It requires no cloud credentials, no external resources, and makes no API calls.

## Basic Setup

### 1. Create Dummy Connectors

NoOp validates that a connector exists but doesn't use it:

```yaml
---
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: CloudProvider
metadata:
  name: noop-aws
  namespace: hibernator-system
spec:
  type: aws
  aws:
    accountId: "000000000000"
    region: us-east-1
    auth:
      serviceAccount: {}
---
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: K8SCluster
metadata:
  name: noop-cluster
  namespace: hibernator-system
spec:
  k8s:
    inCluster: true
```

### 2. Create a NoOp HibernatePlan

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: noop-test
  namespace: hibernator-system
spec:
  schedule:
    timezone: Asia/Jakarta
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
  execution:
    strategy:
      type: Sequential
  behavior:
    mode: Strict
  targets:
    - name: noop-fast
      type: noop
      connectorRef:
        kind: CloudProvider
        name: noop-aws
      parameters:
        randomDelaySeconds: 1
        failureMode: "none"
```

## Use Cases

### Test Sequential Execution

Observe targets executing one at a time with different delays:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: noop-sequential
  namespace: hibernator-system
spec:
  schedule:
    timezone: Asia/Jakarta
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
  execution:
    strategy:
      type: Sequential
  behavior:
    mode: Strict
  targets:
    - name: step-1
      type: noop
      connectorRef:
        kind: CloudProvider
        name: noop-aws
      parameters:
        randomDelaySeconds: 1
        failureMode: "none"

    - name: step-2
      type: noop
      connectorRef:
        kind: CloudProvider
        name: noop-aws
      parameters:
        randomDelaySeconds: 2
        failureMode: "none"

    - name: step-3
      type: noop
      connectorRef:
        kind: CloudProvider
        name: noop-aws
      parameters:
        randomDelaySeconds: 1
        failureMode: "none"
```

### Test DAG Dependencies

Validate dependency ordering without real resources:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: noop-dag
  namespace: hibernator-system
spec:
  schedule:
    timezone: Asia/Jakarta
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
  execution:
    strategy:
      type: DAG
      maxConcurrency: 2
      dependencies:
        - from: frontend
          to: backend
        - from: cache
          to: database
        - from: backend
          to: database
  behavior:
    mode: BestEffort
  targets:
    - name: frontend
      type: noop
      connectorRef:
        kind: CloudProvider
        name: noop-aws
      parameters:
        randomDelaySeconds: 1

    - name: backend
      type: noop
      connectorRef:
        kind: CloudProvider
        name: noop-aws
      parameters:
        randomDelaySeconds: 2

    - name: cache
      type: noop
      connectorRef:
        kind: CloudProvider
        name: noop-aws
      parameters:
        randomDelaySeconds: 1

    - name: database
      type: noop
      connectorRef:
        kind: CloudProvider
        name: noop-aws
      parameters:
        randomDelaySeconds: 3
```

Expected shutdown order:

1. `frontend` and `cache` start in parallel (no upstream dependencies)
2. `backend` starts after `frontend` completes
3. `database` starts after both `backend` and `cache` complete

### Test Error Recovery

Simulate failures to validate retry and recovery behavior:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: noop-failure-test
  namespace: hibernator-system
spec:
  schedule:
    timezone: Asia/Jakarta
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
  execution:
    strategy:
      type: Sequential
  behavior:
    mode: BestEffort
    retries: 3
  targets:
    - name: will-succeed
      type: noop
      connectorRef:
        kind: CloudProvider
        name: noop-aws
      parameters:
        randomDelaySeconds: 1
        failureMode: "none"

    - name: will-fail-shutdown
      type: noop
      connectorRef:
        kind: CloudProvider
        name: noop-aws
      parameters:
        randomDelaySeconds: 1
        failureMode: "shutdown"
        failureMessage: "Simulated: network timeout connecting to resource"

    - name: will-fail-wakeup
      type: noop
      connectorRef:
        kind: CloudProvider
        name: noop-aws
      parameters:
        randomDelaySeconds: 1
        failureMode: "wakeup"
        failureMessage: "Simulated: resource in unexpected state"
```

### Test Staged Execution

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: noop-staged
  namespace: hibernator-system
spec:
  schedule:
    timezone: Asia/Jakarta
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
  execution:
    strategy:
      type: Staged
      stages:
        - name: tier-1
          parallel: true
          targets:
            - frontend-a
            - frontend-b
        - name: tier-2
          parallel: false
          targets:
            - backend
            - worker
        - name: tier-3
          parallel: false
          targets:
            - database
  behavior:
    mode: Strict
  targets:
    - name: frontend-a
      type: noop
      connectorRef:
        kind: CloudProvider
        name: noop-aws
      parameters:
        randomDelaySeconds: 1
    - name: frontend-b
      type: noop
      connectorRef:
        kind: CloudProvider
        name: noop-aws
      parameters:
        randomDelaySeconds: 2
    - name: backend
      type: noop
      connectorRef:
        kind: CloudProvider
        name: noop-aws
      parameters:
        randomDelaySeconds: 1
    - name: worker
      type: noop
      connectorRef:
        kind: CloudProvider
        name: noop-aws
      parameters:
        randomDelaySeconds: 1
    - name: database
      type: noop
      connectorRef:
        kind: CloudProvider
        name: noop-aws
      parameters:
        randomDelaySeconds: 3
```

## Parameters Reference

| Parameter | Type | Default | Range | Description |
|-----------|------|---------|-------|-------------|
| `randomDelaySeconds` | int | 1 | 0–30 | Maximum random delay in seconds |
| `failureMode` | string | `"none"` | `none`, `shutdown`, `wakeup`, `both` | When to simulate failures |
| `failureMessage` | string | *(auto-generated)* | — | Custom error message |

!!! tip "Deterministic testing"
    Set `randomDelaySeconds: 0` for consistent, repeatable test runs without timing variance.
