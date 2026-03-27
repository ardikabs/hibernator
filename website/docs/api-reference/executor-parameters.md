# Executor Parameters Reference

Each executor type accepts a specific parameter schema in the `parameters` field of a HibernatePlan target.
See [API Reference](index.md) for the core CRD types.

## Executor Types

- [EKSParameters (`type: eks`)](#eksparameters)
- [KarpenterParameters (`type: karpenter`)](#karpenterparameters)
- [EC2Parameters (`type: ec2`)](#ec2parameters)
- [RDSParameters (`type: rds`)](#rdsparameters)
- [GKEParameters (`type: gke`)](#gkeparameters)
- [CloudSQLParameters (`type: cloudsql`)](#cloudsqlparameters)
- [WorkloadScalerParameters (`type: workloadscaler`)](#workloadscalerparameters)
- [NoOpParameters (`type: noop`)](#noopparameters)

### EKSParameters

_Executor type: `eks`_

EKSParameters defines the expected parameters for the EKS executor.<br />EKS executor only handles Managed Node Groups via AWS API.<br />For Karpenter NodePools, use the separate Karpenter executor.

| Field | Type | Description |
| ----- | ---- | ----------- |
| `clusterName` | _string_ | ClusterName is the EKS cluster name (required). |
| `nodeGroups` | _[][EKSNodeGroup](#eksnodegroup)_ | NodeGroups to hibernate. If empty, all node groups in the cluster are targeted. |
| `awaitCompletion` | _[AwaitCompletion](#awaitcompletion)_ | AwaitCompletion configures whether to wait for node groups to reach the desired state. |

### EKSNodeGroup

EKSNodeGroup specifies a managed node group to hibernate.

| Field | Type | Description |
| ----- | ---- | ----------- |
| `name` | _string_ | Name is the name of the managed node group. |

### AwaitCompletion

AwaitCompletion configures whether to wait for operations to complete and timeout settings.<br />When Enabled=true, executors will poll asynchronously until operations reach the desired state.<br />Progress is logged through streamlogs at regular intervals (15s) for observability.<br /><br />Timeout behavior:<br />- If Enabled=false: no waiting, operation returns immediately after API call (default behavior)<br />- If Timeout is set (e.g., "5m"): operation will fail if not completed within duration<br />- If Timeout is empty string: it subjected to each executor default timeout<br /><br />Defaults vary by executor based on expected operation duration:<br />- EC2: 5m<br />- EKS: 10m<br />- RDS: 15m<br />- Karpenter: 5m<br />- WorkloadScaler: 5m

| Field | Type | Description |
| ----- | ---- | ----------- |
| `enabled` | _bool_ | Enabled controls whether to wait for operation completion.<br />Default: false |
| `timeout` | _string_ | Timeout is the maximum duration to wait for operation completion.<br />Format: duration string (e.g., "5m", "10m", "15m30s")<br />Empty string means no timeout (wait indefinitely).<br />Only applies when Enabled=true. |

### KarpenterParameters

_Executor type: `karpenter`_

KarpenterParameters defines the expected parameters for the Karpenter executor.

| Field | Type | Description |
| ----- | ---- | ----------- |
| `nodePools` | _[]string_ | NodePools is a list of Karpenter NodePool names to hibernate. |
| `awaitCompletion` | _[AwaitCompletion](#awaitcompletion)_ | AwaitCompletion configures whether to wait for node pools to drain. |

### EC2Parameters

_Executor type: `ec2`_

EC2Parameters defines the expected parameters for the EC2 executor.

| Field | Type | Description |
| ----- | ---- | ----------- |
| `selector` | _[EC2Selector](#ec2selector)_ | Selector defines how to find EC2 instances to hibernate. |
| `awaitCompletion` | _[AwaitCompletion](#awaitcompletion)_ | AwaitCompletion configures whether to wait for EC2 instances to reach the desired state. |

### EC2Selector

EC2Selector defines how to find EC2 instances.

| Field | Type | Description |
| ----- | ---- | ----------- |
| `tags` | _map[string]string_ | Tags filters instances by AWS resource tags. |
| `instanceIds` | _[]string_ | InstanceIDs is a list of explicit EC2 instance IDs to target. |

### RDSParameters

_Executor type: `rds`_

RDSParameters defines the expected parameters for the RDS executor.

| Field | Type | Description |
| ----- | ---- | ----------- |
| `snapshotBeforeStop` | _bool_ | SnapshotBeforeStop creates a final snapshot before stopping RDS instances. |
| `selector` | _[RDSSelector](#rdsselector)_ | Selector defines how to find RDS instances and clusters to hibernate. |
| `awaitCompletion` | _[AwaitCompletion](#awaitcompletion)_ | AwaitCompletion configures whether to wait for RDS resources to reach the desired state. |

### RDSSelector

RDSSelector defines how to find RDS instances and clusters.<br /><br />MUTUAL EXCLUSIVITY RULES:<br />Only ONE of the following selection methods can be used:<br />1. Tag-based selection: `tags` OR `excludeTags` (mutually exclusive with each other)<br />2. Explicit IDs: `instanceIds` and/or `clusterIds` (intent-based, discovers exactly what you specify)<br />3. Discovery mode: `includeAll`<br /><br />RESOURCE TYPE CONTROL:<br />For intent-based selection (`instanceIds`/`clusterIds`), resource types are implicit:<br />- If `instanceIds` specified → discovers instances<br />- If `clusterIds` specified → discovers clusters<br />- If both specified → discovers both<br /><br />For dynamic discovery (`tags`/`excludeTags`/`includeAll`), `discoverInstances` and `discoverClusters`<br />must be explicitly enabled (opt-out by default):<br />- Neither set: no resources discovered (no-op)<br />- `discoverInstances`: true only: discovers only DB instances<br />- `discoverClusters`: true only: discovers only DB clusters<br />- Both true: discovers both instances and clusters<br /><br />Examples (valid):<br />- `{tags: {"env": "prod"}, discoverInstances: true}` — tag-based, discovers only DB instances<br />- `{excludeTags: {"critical": "true"}, discoverClusters: true}` — exclusion-based, discovers only DB clusters<br />- `{instanceIds: ["db-1", "db-2"], clusterIds: ["cluster-1"]}` — explicit IDs; resource types inferred from which IDs are provided<br />- `{includeAll: true, discoverInstances: true, discoverClusters: true}` — discovers all instances and clusters in the region<br /><br />Examples (no-op — nothing will be discovered):<br />- `{tags: {"env": "prod"}}` — tag-based selection requires at least one of `discoverInstances` or `discoverClusters`<br /><br />Examples (invalid — rejected at validation):<br />- `{tags: {...}, instanceIds: [...]}` — cannot mix tag-based selection with explicit IDs<br />- `{tags: {...}, excludeTags: {...}}` — tags and excludeTags are mutually exclusive<br />- `{includeAll: true, tags: {...}}` — includeAll cannot be combined with any other selector

| Field | Type | Description |
| ----- | ---- | ----------- |
| `tags` | _map[string]string_ | Tags for inclusion. If value is empty string "", matches any instance with that key.<br />If value is non-empty, matches only exact key=value.<br />Mutually exclusive with: ExcludeTags, InstanceIDs, ClusterIDs, IncludeAll. |
| `excludeTags` | _map[string]string_ | ExcludeTags for exclusion. Same logic: empty value = exclude if key exists.<br />Mutually exclusive with: Tags, InstanceIDs, ClusterIDs, IncludeAll. |
| `instanceIds` | _[]string_ | Explicit DB instance IDs to target.<br />Can be combined with ClusterIDs, but mutually exclusive with tag-based selection or IncludeAll. |
| `clusterIds` | _[]string_ | Explicit DB cluster IDs to target.<br />Can be combined with InstanceIDs, but mutually exclusive with tag-based selection or IncludeAll. |
| `includeAll` | _bool_ | IncludeAll discovers all DB instances and clusters in the account/region.<br />Mutually exclusive with all other selection methods. |
| `discoverInstances` | _bool_ | DiscoverInstances controls whether to discover DB instances for dynamic selection methods.<br />Only used with `tags`, `excludeTags`, or `includeAll` (ignored for explicit `instanceIds`/`clusterIds`).<br />Must be explicitly set to true to discover instances. Default: false (opt-out, no-op). |
| `discoverClusters` | _bool_ | DiscoverClusters controls whether to discover DB clusters for dynamic selection methods.<br />Only used with `tags`, `excludeTags`, or `includeAll` (ignored for explicit `instanceIds`/`clusterIds`).<br />Must be explicitly set to true to discover clusters. Default: false (opt-out, no-op). |

### GKEParameters

_Executor type: `gke`_

GKEParameters defines the expected parameters for the GKE executor.

| Field | Type | Description |
| ----- | ---- | ----------- |
| `nodePools` | _[]string_ | NodePools is a list of GKE node pool names to hibernate. |

### CloudSQLParameters

_Executor type: `cloudsql`_

CloudSQLParameters defines the expected parameters for the Cloud SQL executor.

| Field | Type | Description |
| ----- | ---- | ----------- |
| `instanceName` | _string_ | InstanceName is the Cloud SQL instance name. |
| `project` | _string_ | Project is the GCP project ID containing the instance. |

### WorkloadScalerParameters

_Executor type: `workloadscaler`_

WorkloadScalerParameters defines the expected parameters for the workloadscaler executor.

| Field | Type | Description |
| ----- | ---- | ----------- |
| `includedGroups` | _[]string_ | IncludedGroups specifies which workload kinds to scale. Defaults to [Deployment]. |
| `namespace` | _[NamespaceSelector](#namespaceselector)_ | Namespace specifies the namespace scope for discovery (exactly one must be set). |
| `workloadSelector` | _*[LabelSelector](#labelselector)_ | WorkloadSelector filters workloads by labels (optional). |
| `awaitCompletion` | _[AwaitCompletion](#awaitcompletion)_ | AwaitCompletion controls whether to wait for replica counts to match desired state. |

### NamespaceSelector

NamespaceSelector defines how to select namespaces.

| Field | Type | Description |
| ----- | ---- | ----------- |
| `literals` | _[]string_ | Literals is a list of explicit namespace names. |
| `selector` | _map[string]string_ | Selector is a label selector for namespaces (mutually exclusive with Literals). |

### LabelSelector

LabelSelector defines a label selector for Kubernetes resources.

| Field | Type | Description |
| ----- | ---- | ----------- |
| `matchLabels` | _map[string]string_ | MatchLabels is a map of {key,value} pairs. A single {key,value} in the matchLabels<br />map is equivalent to an element of matchExpressions, whose key field is "key", the<br />operator is "In", and the values array contains only "value". |
| `matchExpressions` | _[][LabelSelectorRequirement](#labelselectorrequirement)_ | MatchExpressions is a list of label selector requirements. The requirements are ANDed. |

### LabelSelectorRequirement

LabelSelectorRequirement is a selector that contains values, a key, and an operator that<br />relates the key and values.

| Field | Type | Description |
| ----- | ---- | ----------- |
| `key` | _string_ | Key is the label key that the selector applies to. |
| `operator` | _string_ | Operator represents a key's relationship to a set of values.<br />Valid operators are In, NotIn, Exists and DoesNotExist. |
| `values` | _[]string_ | Values is an array of string values. If the operator is In or NotIn,<br />the values array must be non-empty. If the operator is Exists or DoesNotExist,<br />the values array must be empty. |

### NoOpParameters

_Executor type: `noop`_

NoOpParameters defines the expected parameters for the noop executor.

| Field | Type | Description |
| ----- | ---- | ----------- |
| `randomDelaySeconds` | _int_ | RandomDelaySeconds specifies the maximum duration in seconds for random sleep during operations.<br />The actual delay will be randomly chosen between 0 and this value.<br />Maximum allowed is 30 seconds. Defaults to 1 if not specified. |
| `failureMode` | _string_ | FailureMode specifies when to simulate failures. Valid values: "none", "shutdown", "wakeup", "both".<br />Defaults to "none". |
| `failureMessage` | _string_ | FailureMessage allows customizing the error message for simulated failures.<br />If empty, a default message will be used. |

