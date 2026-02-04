# RDS Database Hibernation

**Tier:** `[MVP]`

**Personas:** Platform Engineer, SRE, Database Administrator

**When:** Running RDS databases (instances or clusters) that don't need to run 24/7 (dev, staging, lab environments)

**Why:** RDS charges by instance-hour; stopping during off-hours can reduce costs by 50%+ for non-production environments.

---

## User Stories

**Story 1:** As a **Platform Engineer**, I want to **automatically stop RDS databases during off-hours with optional snapshots**, so that **I can reduce database costs for non-production environments**.

---

## When/Context

- **Cost reduction:** Stop paying for unused database capacity
- **Safety first:** Optional automatic snapshots protect against data loss
- **Automation:** No manual stop/start clicks
- **Non-disruptive:** Applications have time to drain connections before stop

---

## Business Outcome

Automatically stop RDS databases during off-hours, with optional snapshots before stopping, then restart during business hours.

---

## Step-by-Step Flow

### 1. **Identify RDS resources to hibernate**

List your RDS instances or clusters:

```bash
# List RDS instances
aws rds describe-db-instances --region us-east-1 \
  --query 'DBInstances[*].[DBInstanceIdentifier,DBInstanceStatus]'

# Output:
# - stg-postgres: available
# - stg-mysql: available
```

### 2. **Configure in HibernationPlan**

The RDS executor supports multiple selection patterns for flexible targeting:

#### **Option A: Explicit Instance/Cluster IDs (Intent-Based)**

Target specific databases by name:

```yaml
targets:
  - name: stg-databases
    type: rds
    connectorRef:
      kind: CloudProvider
      name: aws-prod
    parameters:
      snapshotBeforeStop: true    # Optional: create snapshot before stopping
      selector:
        instanceIds:              # List specific RDS instances
          - stg-postgres
          - stg-mysql
        clusterIds:               # List specific RDS Aurora clusters
          - stg-aurora-cluster
```

#### **Option B: Tag-Based Selection (Dynamic Discovery)**

Target databases by tags - requires explicit discovery flags (opt-out by default):

```yaml
targets:
  - name: dev-databases
    type: rds
    connectorRef:
      kind: CloudProvider
      name: aws-prod
    parameters:
      snapshotBeforeStop: false
      selector:
        tags:
          Environment: dev        # Match databases with this tag
          Team: backend           # Multiple tags = AND condition
        discoverInstances: true   # REQUIRED: opt-in to discover instances
        discoverClusters: true    # REQUIRED: opt-in to discover clusters
```

**Tag matching rules:**

- **Key-value match:** `Environment: dev` → Only matches `Environment=dev`
- **Key-only match:** `Environment: ""` → Matches any database with `Environment` key (any value)
- **Multiple tags:** AND logic (all tags must match)

#### **Option C: Exclude Tags (Inverse Selection)**

Select all databases EXCEPT those with specific tags:

```yaml
targets:
  - name: non-critical-databases
    type: rds
    connectorRef:
      kind: CloudProvider
      name: aws-prod
    parameters:
      selector:
        excludeTags:
          Critical: "true"        # Exclude databases tagged Critical=true
          Production: "true"      # Also exclude Production=true
        discoverInstances: true   # Must opt-in
        discoverClusters: true    # Must opt-in
```

#### **Option D: Include All (Blanket Selection)**

Hibernate ALL databases in the account/region (use with caution):

```yaml
targets:
  - name: all-databases
    type: rds
    connectorRef:
      kind: CloudProvider
      name: aws-prod
    parameters:
      selector:
        includeAll: true          # Include everything
        discoverInstances: true   # Opt-in to instances
        discoverClusters: false   # Opt-out of clusters (instances only)
```

**Key concepts:**

- **`snapshotBeforeStop`**: `true` = create snapshot before stop (safety), `false` = skip snapshot (faster)
- **Discovery flags (opt-out)**: Must explicitly set `discoverInstances: true` and/or `discoverClusters: true` for dynamic discovery
- **Intent-based selection**: Using `instanceIds`/`clusterIds` doesn't require discovery flags (implicit resource type)
- **Mutual exclusivity**: Can't combine `tags` + `excludeTags`, or `includeAll` + other selectors

### 3. **Understand what happens at hibernation**

When schedule triggers hibernation:

1. **Snapshot created** (if enabled) → Takes 5-15 minutes depending on size
2. **Database stopped** → Connections closed, instance paused
3. **Restore metadata saved** → Instance identifiers and region captured
4. **Billing paused** → No compute charges while stopped (storage still charges)

Example state transitions:

```
Before hibernation:
  stg-postgres: Status=available, MultiAZ=true

During hibernation:
  stg-postgres: Status=stopping (1-2 min)
  stg-postgres: Status=stopped (billing paused)

After wakeup:
  stg-postgres: Status=starting (1-2 min)
  stg-postgres: Status=available
```

### 4. **Configure CloudProvider with RDS permissions**

Ensure CloudProvider has appropriate permissions based on selection pattern:

**For intent-based selection (explicit IDs):**

```yaml
# Minimal permissions - no tag operations needed
- rds:DescribeDBInstances
- rds:DescribeDBClusters
- rds:StopDBInstance
- rds:StartDBInstance
- rds:StopDBCluster
- rds:StartDBCluster
- rds:CreateDBSnapshot          # If snapshotBeforeStop: true
- rds:CreateDBClusterSnapshot   # If snapshotBeforeStop: true
```

**For dynamic discovery (tags/excludeTags/includeAll):**

```yaml
# Additional permissions for tag-based selection
- rds:ListTagsForResource  # Required for tag filtering
# (plus all permissions above)
```

### 5. **Monitor hibernation**

**Check status before hibernation:**

```bash
kubectl describe hibernateplan prod-offhours
# status.executions:
#   - target: rds/stg-db
#     state: Pending
#     message: "Waiting for hibernation window"
```

**During hibernation (20:00):**

```bash
kubectl describe hibernateplan prod-offhours
# status.executions:
#   - target: rds/stg-db
#     state: Running
#     message: "Creating snapshot + stopping instances"

# In AWS console:
aws rds describe-db-instances --query 'DBInstances[0].DBInstanceStatus'
# Output: stopping (then stopped)
```

**After wakeup (06:00):**

```bash
kubectl describe hibernateplan prod-offhours
# status.executions:
#   - target: rds/stg-db
#     state: Completed
#     message: "Instances restarted successfully"

# Verify:
aws rds describe-db-instances --query 'DBInstances[0].DBInstanceStatus'
# Output: available
```

---

## Decision Branches

| Decision | Option | Notes |
| -------- | ------ | ----- |
| **Snapshot before stop?** | Yes (safe) | Creates backup; takes 5-15 min, adds storage cost |
| | No (fast) | Stops immediately; no backup |
| **Selection pattern?** | Explicit IDs | Most precise; list specific instances/clusters |
| | Tag-based | Flexible; auto-includes new tagged resources |
| | Exclude tags | Inverse selection; "all except critical" |
| | Include all | Broadest; hibernates everything (use cautiously) |
| **Resource types?** | Instances only | Set `discoverInstances: true` only |
| | Clusters only | Set `discoverClusters: true` only |
| | Both | Set both flags to `true` |
| **Handle stop failures?** | Strict mode | Fail entire plan if any database fails to stop |
| | BestEffort | Stop what you can, continue others |

---

## Common Patterns

### Pattern 1: Tag all dev databases for auto-hibernation

```bash
# Tag all dev databases
aws rds add-tags-to-resource \
  --resource-name arn:aws:rds:us-east-1:123456789012:db:stg-postgres \
  --tags Key=Environment,Value=dev Key=AutoHibernate,Value=true

# Use tag-based selector in HibernationPlan
selector:
  tags:
    AutoHibernate: "true"
  discoverInstances: true
  discoverClusters: true
```

### Pattern 2: Protect production databases with exclude tags

```bash
# Tag production databases to exclude them
aws rds add-tags-to-resource \
  --resource-name arn:aws:rds:us-east-1:123456789012:db:prod-postgres \
  --tags Key=Critical,Value=true

# Use excludeTags to hibernate everything else
selector:
  excludeTags:
    Critical: "true"
  discoverInstances: true
```

### Pattern 3: Mixed selection (some explicit + some by tag)

```yaml
# Create multiple targets for different patterns
targets:
  # Explicit: critical staging databases with snapshots
  - name: stg-critical-dbs
    type: rds
    parameters:
      snapshotBeforeStop: true
      selector:
        instanceIds:
          - stg-postgres-master
          - stg-mysql-master

  # Tag-based: all other dev databases without snapshots
  - name: dev-dbs
    type: rds
    parameters:
      snapshotBeforeStop: false
      selector:
        tags:
          Environment: dev
        excludeTags:
          Critical: "true"  # Exclude the ones above
        discoverInstances: true
```

---

## RDS Parameters Reference

### Complete Parameter Schema

```yaml
parameters:
  # Optional: Create snapshot before stopping (default: false)
  snapshotBeforeStop: true | false

  # Required: Resource selection (choose ONE pattern)
  selector:
    # Pattern 1: Intent-based (explicit IDs)
    instanceIds: [string]      # List of RDS instance identifiers
    clusterIds: [string]       # List of RDS Aurora cluster identifiers

    # Pattern 2: Tag-based (dynamic discovery)
    tags:                      # Map of tag key-value pairs (AND logic)
      <key>: <value>           # Empty value = key-only match
    discoverInstances: bool    # REQUIRED: opt-in to discover instances
    discoverClusters: bool     # REQUIRED: opt-in to discover clusters

    # Pattern 3: Exclude tags (inverse selection)
    excludeTags:               # Map of tag key-value pairs to exclude
      <key>: <value>
    discoverInstances: bool    # REQUIRED
    discoverClusters: bool     # REQUIRED

    # Pattern 4: Include all (blanket selection)
    includeAll: bool           # Include all resources
    discoverInstances: bool    # Control which types to include
    discoverClusters: bool
```

### Field Descriptions

| Field | Type | Required | Default | Description |
| ----- | ---- | -------- | ------- | ----------- |
| `snapshotBeforeStop` | boolean | No | `false` | Create automated snapshot with timestamp before stopping. Adds 5-15 min to hibernation time. |
| `selector.instanceIds` | []string | No* | - | Explicit list of RDS instance identifiers to target. Mutually exclusive with dynamic discovery. |
| `selector.clusterIds` | []string | No* | - | Explicit list of RDS Aurora cluster identifiers to target. Mutually exclusive with dynamic discovery. |
| `selector.tags` | map[string]string | No* | - | Tag key-value pairs for filtering. Empty value (`""`) = key-only match. Multiple tags = AND logic. Mutually exclusive with `excludeTags` and `includeAll`. |
| `selector.excludeTags` | map[string]string | No* | - | Tag key-value pairs to exclude. Resources matching ANY tag are excluded. Mutually exclusive with `tags` and `includeAll`. |
| `selector.includeAll` | boolean | No | `false` | Include all RDS resources in account/region. Mutually exclusive with other selectors. Use with caution. |
| `selector.discoverInstances` | boolean | No | `false` | **Opt-in flag:** Must be `true` to discover RDS instances for dynamic selection (tags/excludeTags/includeAll). Not needed for intent-based selection. |
| `selector.discoverClusters` | boolean | No | `false` | **Opt-in flag:** Must be `true` to discover RDS Aurora clusters for dynamic selection. Not needed for intent-based selection. |

**\* At least one selection method required:** `instanceIds`, `clusterIds`, `tags`, `excludeTags`, or `includeAll`

### Validation Rules

| Rule | Description | Example Error |
| ---- | ----------- | ------------- |
| **At least one selector** | Must specify one of: `instanceIds`, `clusterIds`, `tags`, `excludeTags`, or `includeAll` | "selector must specify at least one selection method" |
| **tags ⊻ excludeTags** | Cannot use both simultaneously | "tags and excludeTags are mutually exclusive" |
| **includeAll exclusivity** | `includeAll` cannot combine with other selectors | "includeAll cannot be combined with tags/excludeTags/instanceIds/clusterIds" |
| **Dynamic discovery opt-in** | `tags`/`excludeTags`/`includeAll` require at least one discovery flag set to `true` | "must set discoverInstances or discoverClusters to true" |
| **Intent-based no flags** | Using `instanceIds`/`clusterIds` doesn't require (and ignores) discovery flags | Warning: "discovery flags ignored with explicit IDs" |

### Performance Characteristics

| Selection Pattern | API Calls | Latency | Cost |
| ----------------- | --------- | ------- | ---- |
| **Explicit IDs** (N instances) | N × `DescribeDBInstances` | ~100ms per instance | Low |
| **Tags** (N resources) | 1 × `DescribeDB*` + N × `ListTagsForResource` | ~500ms + 50ms per resource | Medium |
| **ExcludeTags** | Same as tags | Same as tags | Medium |
| **IncludeAll** (optimized) | 1 × `DescribeDB*` only | ~500ms (no tag fetching) | Low |

**Optimization note:** `includeAll` mode skips tag fetching entirely for best performance.

---

## Outcome

✓ RDS databases automatically stop during off-hours (with optional snapshots) and restart during business hours using flexible selection patterns.

---

## Related Journeys

- [Create CloudProvider Connector](create-cloudprovider-connector.md) — AWS credentials and IRSA setup
- [Hibernation Plan Initial Design](hibernation-plan-initial-design.md) — Overview of plan structure
- [Monitor Hibernation Execution](monitor-hibernation-execution.md) — Track execution status
- [Troubleshoot Hibernation Failure](troubleshoot-hibernation-failure.md) — Debug RDS issues

---

## Pain Points Solved

**RFC-0001:** Manual RDS stop/start is tedious and often forgotten, leaving dev DBs running unnecessarily. HibernationPlan automates with audit trail.

---

## RFC References

- **RFC-0001:** Control Plane + Runner Model (RDS executor, restore metadata)
