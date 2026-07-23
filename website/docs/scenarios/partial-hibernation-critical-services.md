# Partial Hibernation — Keep Critical Services Running

You run a shared staging cluster for a SaaS product. At night, most of it should go away — feature-team namespaces, batch node pools, the bursty node groups. But a handful of **critical services must stay up 24/7**: the auth service other teams integrate against, the config service, and the system node pool they run on. The shared database also stays up, because on-call engineers may need to inspect schemas at 3 AM.

This is the "partial shutdown" scenario: **different treatment for different services, inside one cluster, expressed through selection granularity.**

## Goal

- Every weeknight 19:00 → 07:00 (America/Los_Angeles):
    - Scale non-critical workloads in staging namespaces to zero.
    - Hibernate the `batch` Karpenter NodePools.
    - Scale the `spot-workers` EKS node group to zero.
- Leave untouched: the `core-services` namespace, critical workloads, system node pools, and the shared RDS instance.

## Resources Involved

| Target | Type | Hibernates | Keeps running |
|--------|------|------------|---------------|
| `non-critical-workloads` | `workloadscaler` | Deployments/StatefulSets in namespaces labeled `environment=staging` that do **not** carry the `critical` label | Anything labeled `critical`, workloads in unlabeled namespaces |
| `batch-nodepools` | `karpenter` | NodePools labeled `workload=batch` | All other NodePools (including `system`) |
| `staging-nodegroups` | `eks` | Only the `spot-workers` managed node group | All other node groups |
| *(not in the plan)* | — | — | Shared RDS, core-services, kube-system |

## Configuration

Two connectors this time: a `CloudProvider` for the AWS-side resources (EKS node groups) and a `K8SCluster` for the in-cluster resources (workloads, Karpenter NodePools).

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
kind: K8SCluster
metadata:
  name: staging-eks
  namespace: hibernator-system
spec:
  providerRef:
    name: aws-staging
    namespace: hibernator-system
  eks:
    name: staging-cluster
    region: us-west-2
---
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: staging-partial
  namespace: hibernator-system
spec:
  schedule:
    timezone: "America/Los_Angeles"
    offHours:
      - start: "19:00"
        end: "07:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]

  execution:
    strategy:
      type: Parallel
      maxConcurrency: 3

  behavior:
    mode: Strict
    retries: 2

  targets:
    - name: non-critical-workloads
      type: workloadscaler
      connectorRef:
        kind: K8SCluster
        name: staging-eks
      parameters:
        namespace:
          selector:
            environment: staging
        workloadSelector:
          matchExpressions:
            - key: critical
              operator: DoesNotExist
        includedGroups: [Deployment, StatefulSet]
        awaitCompletion:
          enabled: true

    - name: batch-nodepools
      type: karpenter
      connectorRef:
        kind: K8SCluster
        name: staging-eks
      parameters:
        nodeSelector:
          matchLabels:
            workload: batch
        awaitCompletion:
          enabled: true
          timeout: "10m"

    - name: staging-nodegroups
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-staging
      parameters:
        clusterName: staging-cluster
        nodeGroups:
          - name: spot-workers
        awaitCompletion:
          enabled: true
          timeout: "10m"
```

```bash
kubectl apply -f staging-partial.yaml
```

!!! tip "The key insight"
    Partial hibernation is expressed by **what you select**, not by a "partial" flag. A resource keeps running if it is not matched by any target — or not in the plan at all. Design your selectors so the critical surface simply never matches.

## How It Executes

At **19:00**, all three targets run in parallel:

1. **`non-critical-workloads`** — every Deployment and StatefulSet in namespaces labeled `environment=staging`, *except* workloads carrying a `critical` label, is scaled to **0 replicas**. The original replica counts are saved to restore data.
2. **`batch-nodepools`** — NodePools labeled `workload=batch` are deleted; Karpenter drains their nodes (pods evicted, instances terminated). Full pool specs are saved for exact reconstruction. See [what Karpenter hibernation does](../user-guides/karpenter-executor.md#what-happens-during-hibernation).
3. **`staging-nodegroups`** — the `spot-workers` managed node group is scaled to zero. Other node groups are not listed, so they are never touched.

Meanwhile, everything unmatched — `core-services`, the `system` NodePool, and the RDS instance that appears in no target — keeps running normally.

At **07:00**, wakeup reverses the effects: workloads are restored to their **original replica counts** (not a fixed number — see the [WorkloadScaler executor](../user-guides/workloadscaler-executor.md)), NodePools are recreated with their saved specs, and the node group scales back.

## Verification

```bash
# Confirm all three targets completed
kubectl get hibernateplan staging-partial -n hibernator-system \
  -o jsonpath='{range .status.executions[*]}{.target}{"\t"}{.state}{"\n"}{end}'

# Spot-check that critical workloads were NOT scaled
kubectl get deploy -n core-services -o wide
```

## Variations

- **Opt-in model** — instead of excluding `critical`, have teams *opt in* to hibernation per workload:

    ```yaml
    workloadSelector:
      matchLabels:
        hibernation: enabled
    ```

- **Explicit namespace list** — if namespace labels are unreliable, enumerate exactly what may hibernate:

    ```yaml
    namespace:
      literals: [team-a, team-b, team-c]
    ```

- **Protect a critical NodePool** — list only the pools that should hibernate instead of using `nodeSelector`. See [Protect Critical NodePools](../user-guides/karpenter-executor.md#protect-critical-nodepools).

## Next Steps

- [WorkloadScaler Executor](../user-guides/workloadscaler-executor.md) — namespace scoping, selectors, replica restoration
- [Karpenter Executor](../user-guides/karpenter-executor.md) — NodePool deletion and recreation semantics
- [Tolerating Partial Failures](partial-failure-besteffort.md) — next: what happens when some targets fail
