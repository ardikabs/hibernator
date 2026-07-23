# Selective RDS Hibernation with tagSelector

You manage dozens of RDS databases across dev, staging, and production. Nightly hibernation would save real money — but you must **never** stop production databases, and you must **never** touch anything tagged `critical: true`. You also want to avoid the deprecated `tags` and `excludeTags` maps in favor of expression-based selection.

The RDS executor's `tagSelector` gives you exact control: include only databases in `dev` or `staging`, and explicitly exclude anything carrying a `critical` label.

## Goal

- Hibernate only non-critical dev and staging databases.
- Leave production and critical databases completely untouched.
- Use modern expression-based selection instead of legacy tag maps.

## The Selection Rule

```yaml
selector:
  tagSelector:
    matchExpressions:
      - key: Environment
        operator: In
        values: ["dev", "staging"]
      - key: critical
        operator: DoesNotExist
  discoverInstances: true
```

This reads as: "discover DB instances whose `Environment` tag is `dev` or `staging`, AND which do not have a `critical` tag at all."

!!! note "`discoverInstances: true` is required"
    Dynamic selection methods (`tagSelector`, `tags`, `excludeTags`, `includeAll`) are **no-ops** without an explicit `discoverInstances` and/or `discoverClusters`. See the [RDS executor parameters reference](../reference/executor-parameters.md#rdsparameters).

## Configuration

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: CloudProvider
metadata:
  name: aws-staging
  namespace: hibernator-system
spec:
  type: aws
  aws:
    accountId: "210987654321"
    region: us-west-2
    assumeRoleArn: arn:aws:iam::210987654321:role/hibernator-runner
    auth:
      serviceAccount: {}
---
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: rds-selective
  namespace: hibernator-system
spec:
  schedule:
    timezone: "America/Los_Angeles"
    offHours:
      - start: "21:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]

  execution:
    strategy:
      type: Parallel

  behavior:
    mode: Strict
    retries: 2

  targets:
    - name: selective-databases
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-staging
      parameters:
        selector:
          tagSelector:
            matchExpressions:
              - key: Environment
                operator: In
                values: ["dev", "staging"]
              - key: critical
                operator: DoesNotExist
          discoverInstances: true
        snapshotBeforeStop: true
        awaitCompletion:
          enabled: true
          timeout: "15m"
```

```bash
kubectl apply -f rds-selective.yaml
```

## How It Executes

At **21:00**, the RDS executor discovers all DB instances in the account/region via the AWS API, then applies the client-side `tagSelector`:

| Database | Tags | Result |
|----------|------|--------|
| `dev-analytics` | `Environment=dev` | ✅ Hibernated |
| `staging-api-db` | `Environment=staging` | ✅ Hibernated |
| `dev-cache` | `Environment=dev`, `critical=true` | ❌ Skipped (`DoesNotExist` rejects it) |
| `prod-billing` | `Environment=production` | ❌ Skipped (`In [dev, staging]` rejects it) |
| `prod-auth` | (no `Environment` tag) | ❌ Skipped |

At **06:00**, only the matched instances are restored from their snapshots and saved restore data.

## Verification

```bash
# Confirm only the intended instances were discovered
kubectl get hibernateplan rds-selective -n hibernator-system \
  -o jsonpath='{range .status.executions[*]}{.target}{"\t"}{.state}{"\t"}{.message}{"\n"}{end}'
```

You can also check the controller logs for the discovery result:

```bash
kubectl logs -n hibernator-system -l hibernator.ardikabs.com/plan=rds-selective \
  | grep "discovered"
```

## Variations

- **Exact tag match with `matchTags`** — for simple key=value matching without operators:

    ```yaml
    tagSelector:
      matchTags:
        Environment: dev
    ```

    This is equivalent to a single `matchExpressions` with `operator: In` and one value, but more concise.

- **Exclude by presence** — use `operator: Exists` to target anything that *has* a specific tag (regardless of value), or `NotIn` to exclude a list of values.

- **Cluster support** — add `discoverClusters: true` to also discover Aurora clusters matching the same tag expression.

- **Combine with other executors** — add `workloadscaler` and `eks` targets to the same plan for a full-environment selective shutdown: see [Partial Hibernation](partial-hibernation-critical-services.md).

## Next Steps

- [RDS Executor](../user-guides/rds-executor.md) — stopping instances, clusters, and snapshots
- [Executor Parameters Reference](../reference/executor-parameters.md#rdsparameters) — full `RDSSelector` schema and mutual-exclusivity rules
- [Partial Hibernation](partial-hibernation-critical-services.md) — applying the same selective pattern to Kubernetes workloads and compute
