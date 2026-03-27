# Hibernating EC2 Instances

This guide covers how to hibernate standalone AWS EC2 instances using the `ec2` executor.

## Prerequisites

- A `CloudProvider` resource configured for your AWS account
- IAM permissions: `ec2:DescribeInstances`, `ec2:StopInstances`, `ec2:StartInstances`

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
  name: ec2-hibernate
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
    - name: dev-instances
      type: ec2
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        selector:
          tags:
            Environment: development
        awaitCompletion:
          enabled: true
          timeout: "5m"
```

## Use Cases

### Select by Tags

Target instances matching specific AWS resource tags:

```yaml
targets:
  - name: dev-servers
    type: ec2
    connectorRef:
      kind: CloudProvider
      name: aws-production
    parameters:
      selector:
        tags:
          Environment: development
          Team: backend
      awaitCompletion:
        enabled: true
```

Tags are matched with AND logic — an instance must have **all** specified tag key-value pairs. If a tag value is an empty string, the executor matches any instance with that tag key regardless of value.

### Select by Instance IDs

Target specific instances by their IDs:

```yaml
targets:
  - name: specific-instances
    type: ec2
    connectorRef:
      kind: CloudProvider
      name: aws-production
    parameters:
      selector:
        instanceIds:
          - i-0abc123def456789a
          - i-0def456789abc0123
          - i-0789abc0123def456
      awaitCompletion:
        enabled: true
```

### Multi-Region EC2 Hibernation

Use separate targets with different CloudProvider connectors per region:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: multi-region-ec2
  namespace: hibernator-system
spec:
  schedule:
    timezone: UTC
    offHours:
      - start: "22:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
  execution:
    strategy:
      type: Parallel
  targets:
    - name: us-west-instances
      type: ec2
      connectorRef:
        kind: CloudProvider
        name: aws-us-west
      parameters:
        selector:
          tags:
            Environment: staging
        awaitCompletion:
          enabled: true

    - name: eu-west-instances
      type: ec2
      connectorRef:
        kind: CloudProvider
        name: aws-eu-west
      parameters:
        selector:
          tags:
            Environment: staging
        awaitCompletion:
          enabled: true
```

### EC2 Combined with RDS (DAG Strategy)

Stop application servers before databases to ensure clean shutdown:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: app-stack-hibernate
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
        - from: app-servers
          to: database
  behavior:
    mode: BestEffort
    retries: 3
  targets:
    - name: app-servers
      type: ec2
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        selector:
          tags:
            Component: application
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
```

## What Happens During Hibernation

1. The executor discovers instances matching the selector (tag filter or explicit IDs)
2. Instances managed by Auto Scaling Groups or Karpenter are automatically **excluded**
3. For each running instance, the state is recorded (`wasRunning: true`)
4. All running instances are stopped via `StopInstances`
5. EBS volumes remain attached; Elastic IPs stay associated

## What Happens During Wakeup

1. The executor reads the saved instance states
2. Only instances that were running before hibernation are started
3. Instances that were already stopped are left as-is
4. After startup, instances get new public IPs unless an Elastic IP is associated

## Troubleshooting

### Instance not being stopped

- Verify the instance is **not** managed by an Auto Scaling Group (check for `aws:autoscaling:groupName` tag)
- Verify the instance is **not** managed by Karpenter (check for `karpenter.sh/nodepool` tag)
- Check that the tag selector matches the instance

### Timeout on stop

- Some instance types take longer to stop (e.g., large memory instances)
- Increase timeout: `awaitCompletion.timeout: "10m"`

### Instance store data lost

- EC2 instance store volumes lose data on stop — this is standard AWS behavior
- Use EBS-backed instances if data persistence through stop/start cycles is needed
