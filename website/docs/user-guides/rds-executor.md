# Hibernating RDS Databases

This guide covers how to hibernate AWS RDS database instances and Aurora clusters using the `rds` executor.

## Prerequisites

- A `CloudProvider` resource configured for your AWS account
- IAM permissions: `rds:DescribeDBInstances`, `rds:DescribeDBClusters`, `rds:StopDBInstance`, `rds:StartDBInstance`, `rds:StopDBCluster`, `rds:StartDBCluster`
- If using snapshots: `rds:CreateDBSnapshot` or `rds:CreateDBClusterSnapshot`

## Basic Setup

### 1. Create the CloudProvider

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: CloudProvider
metadata:
  name: aws-production
  namespace: hibernator-system
spec:
  type: aws
  aws:
    accountId: "123456789012"
    region: us-west-2
    assumeRoleArn: arn:aws:iam::123456789012:role/HibernatorRole
    auth:
      serviceAccount: {}
```

### 2. Create the HibernatePlan

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: rds-hibernate
  namespace: hibernator-system
spec:
  schedule:
    timezone: America/New_York
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
    - name: staging-db
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        selector:
          instanceIds:
            - staging-db-01
        snapshotBeforeStop: false
        awaitCompletion:
          enabled: true
          timeout: "15m"
```

## Understanding RDS Selectors

The RDS executor has three mutually exclusive selection modes. Choosing the right one depends on your use case.

### Mode 1: Explicit IDs

Target specific database instances and/or clusters by their identifiers. This is the simplest and most predictable mode.

```yaml
parameters:
  selector:
    instanceIds:
      - my-db-instance-1
      - my-db-instance-2
    clusterIds:
      - my-aurora-cluster
```

Resource types are inferred automatically:

- `instanceIds` present → discovers DB instances
- `clusterIds` present → discovers DB clusters
- Both present → discovers both

### Mode 2: Tag-Based Selection

Target databases by their AWS resource tags. Requires explicit opt-in via discovery flags.

```yaml
parameters:
  selector:
    tags:
      Environment: staging
      Team: backend
    discoverInstances: true   # opt-in to discover DB instances
    discoverClusters: false   # do not discover DB clusters
```

!!! warning "Discovery flags are required"
    Setting `tags` alone without `discoverInstances` or `discoverClusters` is a **no-op** — nothing will be discovered. This is a safety measure to prevent accidentally targeting more resources than intended.

Use `excludeTags` to match everything _except_ certain tags:

```yaml
parameters:
  selector:
    excludeTags:
      Critical: "true"
    discoverInstances: true
    discoverClusters: true
```

!!! note
    `tags` and `excludeTags` are mutually exclusive — you cannot use both in the same selector.

### Mode 3: Include All

Discover all databases in the account and region:

```yaml
parameters:
  selector:
    includeAll: true
    discoverInstances: true
    discoverClusters: true
```

!!! danger
    Use `includeAll` with caution in production accounts. It will target every RDS instance and cluster visible to the IAM role in the configured region.

## Use Cases

### Stop a Single Production Database with Snapshot

```yaml
targets:
  - name: prod-db
    type: rds
    connectorRef:
      kind: CloudProvider
      name: aws-production
    parameters:
      selector:
        instanceIds:
          - production-db-primary
      snapshotBeforeStop: true
      awaitCompletion:
        enabled: true
        timeout: "20m"
```

The executor creates a snapshot named `production-db-primary-hibernate-{timestamp}` and waits for it to complete before stopping the instance.

### Hibernate All Staging Databases by Tag

```yaml
targets:
  - name: staging-databases
    type: rds
    connectorRef:
      kind: CloudProvider
      name: aws-staging
    parameters:
      selector:
        tags:
          Environment: staging
        discoverInstances: true
        discoverClusters: true
      snapshotBeforeStop: false
      awaitCompletion:
        enabled: true
```

### Hibernate Aurora Clusters

```yaml
targets:
  - name: aurora-clusters
    type: rds
    connectorRef:
      kind: CloudProvider
      name: aws-production
    parameters:
      selector:
        clusterIds:
          - aurora-staging
          - aurora-dev
      snapshotBeforeStop: true
      awaitCompletion:
        enabled: true
        timeout: "20m"
```

### Hibernate Everything Except Critical Databases

```yaml
targets:
  - name: non-critical-dbs
    type: rds
    connectorRef:
      kind: CloudProvider
      name: aws-production
    parameters:
      selector:
        excludeTags:
          Critical: "true"
        discoverInstances: true
        discoverClusters: true
      snapshotBeforeStop: false
      awaitCompletion:
        enabled: true
```

### Full Stack: Apps → Database (DAG Order)

Ensure application servers are stopped before the database:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: full-stack-hibernate
  namespace: hibernator-system
spec:
  schedule:
    timezone: America/New_York
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
  execution:
    strategy:
      type: DAG
      dependencies:
        - from: app-workloads
          to: database
  behavior:
    mode: BestEffort
    retries: 3
  targets:
    - name: app-workloads
      type: workloadscaler
      connectorRef:
        kind: K8SCluster
        name: eks-production
      parameters:
        namespace:
          literals: [default]
        workloadSelector:
          matchLabels:
            tier: application
        awaitCompletion:
          enabled: true

    - name: database
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        selector:
          instanceIds:
            - production-db
        snapshotBeforeStop: true
        awaitCompletion:
          enabled: true
          timeout: "15m"
```

## What Happens During Hibernation

1. The executor discovers databases based on the selector mode
2. Only databases with status `available` are eligible for stopping
3. If `snapshotBeforeStop` is enabled, a snapshot is created and the executor waits for it to complete (up to 30 minutes)
4. The database is stopped via `StopDBInstance` or `StopDBCluster`
5. State is saved: instance/cluster ID, previous status, snapshot ID, instance type

## What Happens During Wakeup

1. Databases that were running before hibernation are started via `StartDBInstance` or `StartDBCluster`
2. Databases that were already stopped before hibernation remain stopped
3. The executor polls until databases return to `available` status

## Important Considerations

!!! warning "AWS 7-day auto-restart"
    AWS automatically restarts any RDS instance that has been stopped for more than 7 consecutive days. If your hibernation schedule leaves databases stopped for longer (e.g., over a long holiday), AWS will restart them automatically. Plan accordingly.

!!! info "Snapshot cleanup"
    Snapshots created by `snapshotBeforeStop` are not automatically deleted. You are responsible for managing snapshot lifecycle and cleanup to avoid unexpected storage costs.

## Troubleshooting

### Database not stopping

- Verify the database status is `available` — databases in `modifying`, `backing-up`, or other intermediate states cannot be stopped
- Check IAM permissions include `rds:StopDBInstance`
- Multi-AZ failover may temporarily put the instance in a non-stoppable state

### Snapshot taking too long

- RDS snapshots for large databases can take significant time
- The executor uses a 30-minute internal timeout for snapshot creation
- Consider disabling `snapshotBeforeStop` if automated backups are already configured

### Tags not matching

- RDS tag matching is case-sensitive
- Verify tags in the AWS Console match exactly
- Remember: empty tag value matches any instance with that tag key
