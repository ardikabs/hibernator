# Error Recovery

Hibernator includes automatic retry and manual recovery mechanisms for handling execution failures.

## Automatic Retries

When a runner Job fails, the controller automatically retries with exponential backoff:

- **Backoff formula**: `min(60s × 2^attempt, 30m)`
- **Default retries**: 3 (configurable via `spec.behavior.retries`)
- **Maximum retries**: 10

### Retry Schedule

| Attempt | Delay |
|---------|-------|
| 1 | 60 seconds |
| 2 | 120 seconds |
| 3 | 240 seconds |
| 4 | 480 seconds |
| ... | ... |
| Max | 30 minutes |

### Configure Retries

```yaml
behavior:
  retries: 5    # 0 to disable, max 10
```

## Error Classification

The controller classifies errors as:

| Type | Behavior | Examples |
|------|----------|---------|
| **Transient** | Automatic retry | Network timeout, API throttling, temporary unavailability |
| **Permanent** | No retry, plan enters Error phase | Invalid credentials, missing resource, permission denied |

## Manual Recovery

When automatic retries are exhausted or a permanent error occurs, the plan enters the `Error` phase.

### Retry via Annotation

Trigger a manual retry:

```bash
kubectl annotate hibernateplan dev-offhours -n hibernator-system \
  hibernator.ardikabs.com/retry-now=true
```

This resets the retry counter and immediately attempts the failed operation.

### Check Error Details

```bash
# View the error message
kubectl get hibernateplan dev-offhours -n hibernator-system \
  -o jsonpath='{.status.errorMessage}'

# Check retry count
kubectl get hibernateplan dev-offhours -n hibernator-system \
  -o jsonpath='{.status.retryCount}'

# View per-target execution state
kubectl get hibernateplan dev-offhours -n hibernator-system \
  -o jsonpath='{.status.executions}' | jq '.[] | select(.state == "Failed")'
```

### View Runner Logs

```bash
# Find the failed job
kubectl get jobs -n hibernator-system -l hibernator.ardikabs.com/plan=dev-offhours

# Check the pod logs
kubectl logs job/hibernate-runner-dev-offhours-dev-database -n hibernator-system
```

## BestEffort Mode

With `behavior.mode: BestEffort`, the plan continues executing other targets even when some fail:

```yaml
behavior:
  mode: BestEffort
  failFast: false
  retries: 3
```

- Failed targets are retried independently
- Downstream DAG dependents of failed targets are marked `Aborted`
- The plan enters `Error` only if all retries are exhausted and the failure affects overall completion

## Recovery Checklist

When a plan is stuck in `Error` phase:

1. **Check the error message**: `kubectl get hibernateplan <name> -o jsonpath='{.status.errorMessage}'`
2. **Check runner logs**: Look at the failed Job's pod logs
3. **Fix the root cause**: Credentials, permissions, resource state, etc.
4. **Retry**: Apply the `retry-now` annotation
5. **Monitor**: Watch the plan phase until it transitions back to a healthy state
