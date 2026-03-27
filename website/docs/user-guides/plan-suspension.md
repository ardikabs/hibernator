# Plan Suspension

Temporarily disable a HibernatePlan without deleting it or losing its configuration.

## Suspending a Plan

Set `spec.suspend: true` to pause all operations:

```bash
kubectl patch hibernateplan dev-offhours -n hibernator-system \
  --type=merge -p '{"spec":{"suspend":true}}'
```

Or edit the YAML directly:

```yaml
spec:
  suspend: true
```

### What Happens

1. The plan transitions to the `Suspended` phase
2. No new hibernation or wakeup cycles are started
3. **Running jobs complete naturally** — the controller doesn't kill active runners
4. The plan retains all configuration and execution history

## Resuming a Plan

Set `spec.suspend: false` to resume:

```bash
kubectl patch hibernateplan dev-offhours -n hibernator-system \
  --type=merge -p '{"spec":{"suspend":false}}'
```

The plan transitions back to `Active` and resumes schedule evaluation immediately.

## Use Cases

- **Debugging**: Pause hibernation while investigating a resource issue
- **Manual operations**: Prevent interference during cluster maintenance
- **Cost control investigation**: Temporarily keep resources running to measure baseline costs
- **Holiday freeze**: Pause all automated changes during a change freeze period

!!! tip
    For time-bounded pauses, prefer [Schedule Exceptions](schedule-exceptions.md) (type `suspend`) which automatically expire. Use `spec.suspend` for indefinite pauses that require manual re-enablement.
