# Hibernation Lifecycle

This guide walks through the complete lifecycle of a HibernatePlan from creation to steady-state operation.

## Creating a Plan

### 1. Set Up Connectors

Before creating a plan, ensure your connectors are ready:

```bash
# Verify CloudProvider is ready
kubectl get cloudprovider -n hibernator-system
# NAME             TYPE   READY   AGE
# aws-production   aws    true    5d

# Verify K8SCluster is ready (if using Karpenter/WorkloadScaler)
kubectl get k8scluster -n hibernator-system
# NAME             TYPE   READY   AGE
# eks-production   eks    true    5d
```

### 2. Apply the Plan

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: dev-offhours
  namespace: hibernator-system
spec:
  schedule:
    timezone: "Asia/Jakarta"
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
  execution:
    strategy:
      type: Sequential
  targets:
    - name: dev-database
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        selector:
          instanceIds: ["dev-db"]
        snapshotBeforeStop: true
```

```bash
kubectl apply -f plan.yaml
```

### 3. Verify Creation

```bash
kubectl get hibernateplan -n hibernator-system
# NAME            PHASE    AGE
# dev-offhours    Active   10s
```

## Monitoring a Cycle

### Watch Phase Transitions

```bash
kubectl get hibernateplan dev-offhours -n hibernator-system -w
```

You'll see transitions like:

```
NAME           PHASE         AGE
dev-offhours   Active        2h
dev-offhours   Hibernating   2h
dev-offhours   Hibernated    2h
```

### Check Execution Details

```bash
kubectl get hibernateplan dev-offhours -n hibernator-system \
  -o jsonpath='{.status.executions[*]}' | jq
```

Example output:

```json
[
  {
    "target": "rds/dev-database",
    "executor": "rds",
    "state": "Completed",
    "startedAt": "2026-02-01T13:00:00Z",
    "finishedAt": "2026-02-01T13:02:30Z",
    "attempts": 1,
    "message": "Successfully stopped RDS instance"
  }
]
```

### View Runner Logs

```bash
# List runner jobs
kubectl get jobs -n hibernator-system -l hibernator/plan=dev-offhours

# View logs from the most recent runner
kubectl logs -n hibernator-system -l hibernator/plan=dev-offhours --tail=50
```

## Phase Flow

### Shutdown Flow

1. **Active → Hibernating**: Controller detects the schedule window has started
2. Controller creates runner Jobs for each target (ordered by execution strategy)
3. Each runner executes the `Shutdown` operation on its target
4. Restore metadata is captured and stored in ConfigMaps
5. **Hibernating → Hibernated**: All targets successfully shut down

### Wakeup Flow

1. **Hibernated → WakingUp**: Controller detects the schedule window has ended
2. Controller creates runner Jobs in reverse execution order
3. Each runner reads restore metadata and executes the `WakeUp` operation
4. **WakingUp → Active**: All targets successfully restored

## Checking Restore Data

Restore metadata is stored in a ConfigMap:

```bash
kubectl get configmap restore-data-dev-offhours -n hibernator-system -o yaml
```

Keys follow the format `{executor}_{target-name}`:

```yaml
data:
  rds_dev-database: '{"instanceId":"dev-db","state":"available","engineVersion":"15.4"}'
```

## Execution History

The plan status maintains a history of recent execution cycles:

```bash
kubectl get hibernateplan dev-offhours -n hibernator-system \
  -o jsonpath='{.status.executionHistory}' | jq
```

Up to 5 recent cycles are retained, each with shutdown and wakeup operation summaries.

## Next Steps

- [Execution Strategies](execution-strategies.md) — Configure how targets are ordered
- [Error Recovery](error-recovery.md) — Handle failures during execution
