/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package executorparams provides unified parameter types for executor validation
// and runtime use. This package is shared between API/webhook admission validation
// and internal executor implementations to ensure schema consistency.
//
// Design rationale:
//   - Single source of truth for parameter schemas
//   - Avoids duplication between api/v1alpha1/validation and internal/executor
//   - Keeps executors independent from API/webhook packages
//   - Pure Go types with no Kubernetes dependencies
package executorparams

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// AwaitCompletion configures whether to wait for operations to complete and timeout settings.
// When Enabled=true, executors will poll asynchronously until operations reach the desired state.
// Progress is logged through streamlogs at regular intervals (15s) for observability.
//
// Timeout behavior:
//   - If Enabled=false: no waiting, operation returns immediately after API call (default behavior)
//   - If Timeout is set (e.g., "5m"): operation will fail if not completed within duration
//   - If Timeout is empty string: it subjected to each executor default timeout
//
// Defaults vary by executor based on expected operation duration:
//   - EC2: 5m
//   - EKS: 10m
//   - RDS: 15m
//   - Karpenter: 5m
//   - WorkloadScaler: 5m
type AwaitCompletion struct {
	// Enabled controls whether to wait for operation completion.
	// Default: false
	Enabled bool `json:"enabled,omitempty"`

	// Timeout is the maximum duration to wait for operation completion.
	// Format: duration string (e.g., "5m", "10m", "15m30s")
	// Empty string means no timeout (wait indefinitely).
	// Only applies when Enabled=true.
	Timeout string `json:"timeout,omitempty"`
}

// EC2Parameters defines the expected parameters for the EC2 executor.
type EC2Parameters struct {
	Selector        EC2Selector     `json:"selector"`
	AwaitCompletion AwaitCompletion `json:"awaitCompletion"`
}

// EC2Selector defines how to find EC2 instances.
type EC2Selector struct {
	Tags        map[string]string `json:"tags,omitempty"`
	InstanceIDs []string          `json:"instanceIds,omitempty"`
}

// RDSParameters defines the expected parameters for the RDS executor.
type RDSParameters struct {
	SnapshotBeforeStop bool            `json:"snapshotBeforeStop,omitempty"`
	Selector           RDSSelector     `json:"selector"`
	AwaitCompletion    AwaitCompletion `json:"awaitCompletion"`
}

// RDSSelector defines how to find RDS instances and clusters.
//
// MUTUAL EXCLUSIVITY RULES:
// Only ONE of the following selection methods can be used:
//  1. Tag-based selection: Tags OR ExcludeTags (mutually exclusive with each other)
//  2. Explicit IDs: InstanceIDs and/or ClusterIDs (intent-based, discovers exactly what you specify)
//  3. Discovery mode: IncludeAll
//
// RESOURCE TYPE CONTROL:
// For intent-based selection (InstanceIDs/ClusterIDs), resource types are implicit:
//   - If InstanceIDs specified → discovers instances
//   - If ClusterIDs specified → discovers clusters
//   - If both specified → discovers both
//
// For dynamic discovery (Tags/ExcludeTags/IncludeAll), DiscoverInstances and DiscoverClusters
// must be explicitly enabled (opt-out by default):
//   - Neither set: no resources discovered (no-op)
//   - DiscoverInstances: true only: discovers only DB instances
//   - DiscoverClusters: true only: discovers only DB clusters
//   - Both true: discovers both instances and clusters
//
// Examples:
//   - Valid: {Tags: {"env": "prod"}, DiscoverInstances: true}  // only instances with tag
//   - Valid: {ExcludeTags: {"critical": "true"}, DiscoverClusters: true}  // only clusters
//   - Valid: {InstanceIDs: ["db-1", "db-2"], ClusterIDs: ["cluster-1"]}  // explicit both
//   - Valid: {IncludeAll: true, DiscoverInstances: true, DiscoverClusters: true}  // all resources
//   - No-op: {Tags: {"env": "prod"}}  // no discovery flags set, nothing discovered
//   - Invalid: {Tags: {...}, InstanceIDs: [...]} - cannot mix tag-based with explicit IDs
//   - Invalid: {Tags: {...}, ExcludeTags: {...}} - tags and excludeTags are mutually exclusive
//   - Invalid: {IncludeAll: true, Tags: {...}} - includeAll cannot be combined with other methods
type RDSSelector struct {
	// Tags for inclusion. If value is empty string "", matches any instance with that key.
	// If value is non-empty, matches only exact key=value.
	// Mutually exclusive with: ExcludeTags, InstanceIDs, ClusterIDs, IncludeAll.
	Tags map[string]string `json:"tags,omitempty"`

	// ExcludeTags for exclusion. Same logic: empty value = exclude if key exists.
	// Mutually exclusive with: Tags, InstanceIDs, ClusterIDs, IncludeAll.
	ExcludeTags map[string]string `json:"excludeTags,omitempty"`

	// Explicit DB instance IDs to target.
	// Can be combined with ClusterIDs, but mutually exclusive with tag-based selection or IncludeAll.
	InstanceIDs []string `json:"instanceIds,omitempty"`

	// Explicit DB cluster IDs to target.
	// Can be combined with InstanceIDs, but mutually exclusive with tag-based selection or IncludeAll.
	ClusterIDs []string `json:"clusterIds,omitempty"`

	// IncludeAll discovers all DB instances and clusters in the account/region.
	// Mutually exclusive with all other selection methods.
	IncludeAll bool `json:"includeAll,omitempty"`

	// DiscoverInstances controls whether to discover DB instances for dynamic selection methods.
	// Only used with Tags, ExcludeTags, or IncludeAll (ignored for explicit InstanceIDs/ClusterIDs).
	// Must be explicitly set to true to discover instances. Default: false (opt-out, no-op).
	DiscoverInstances bool `json:"discoverInstances,omitempty"`

	// DiscoverClusters controls whether to discover DB clusters for dynamic selection methods.
	// Only used with Tags, ExcludeTags, or IncludeAll (ignored for explicit InstanceIDs/ClusterIDs).
	// Must be explicitly set to true to discover clusters. Default: false (opt-out, no-op).
	DiscoverClusters bool `json:"discoverClusters,omitempty"`
}

// EKSParameters defines the expected parameters for the EKS executor.
// EKS executor only handles Managed Node Groups via AWS API.
// For Karpenter NodePools, use the separate Karpenter executor.
type EKSParameters struct {
	// ClusterName is the EKS cluster name (required).
	ClusterName string `json:"clusterName"`
	// NodeGroups to hibernate. If empty, all node groups in the cluster are targeted.
	NodeGroups      []EKSNodeGroup  `json:"nodeGroups,omitempty"`
	AwaitCompletion AwaitCompletion `json:"awaitCompletion"`
}

// EKSNodeGroup specifies a managed node group to hibernate.
type EKSNodeGroup struct {
	Name string `json:"name"`
}

// KarpenterParameters defines the expected parameters for the Karpenter executor.
type KarpenterParameters struct {
	NodePools       []string        `json:"nodePools"`
	AwaitCompletion AwaitCompletion `json:"awaitCompletion"`
}

// GKEParameters defines the expected parameters for the GKE executor.
type GKEParameters struct {
	NodePools []string `json:"nodePools"`
}

// CloudSQLParameters defines the expected parameters for the Cloud SQL executor.
type CloudSQLParameters struct {
	InstanceName string `json:"instanceName"`
	Project      string `json:"project"`
}

// WorkloadScalerParameters defines the expected parameters for the workloadscaler executor.
type WorkloadScalerParameters struct {
	// IncludedGroups specifies which workload kinds to scale. Defaults to [Deployment].
	IncludedGroups []string `json:"includedGroups,omitempty"`

	// Namespace specifies the namespace scope for discovery (exactly one must be set).
	Namespace NamespaceSelector `json:"namespace"`

	// WorkloadSelector filters workloads by labels (optional).
	WorkloadSelector *metav1.LabelSelector `json:"workloadSelector,omitempty"`

	// AwaitCompletion controls whether to wait for replica counts to match desired state.
	AwaitCompletion AwaitCompletion `json:"awaitCompletion"`
}

// NamespaceSelector defines how to select namespaces.
type NamespaceSelector struct {
	// Literals is a list of explicit namespace names.
	Literals []string `json:"literals,omitempty"`

	// Selector is a label selector for namespaces (mutually exclusive with Literals).
	Selector map[string]string `json:"selector,omitempty"`
}

// LabelSelector defines a label selector for Kubernetes resources.
type LabelSelector struct {
	// MatchLabels is a map of {key,value} pairs. A single {key,value} in the matchLabels
	// map is equivalent to an element of matchExpressions, whose key field is "key", the
	// operator is "In", and the values array contains only "value".
	MatchLabels map[string]string `json:"matchLabels,omitempty"`

	// MatchExpressions is a list of label selector requirements. The requirements are ANDed.
	MatchExpressions []LabelSelectorRequirement `json:"matchExpressions,omitempty"`
}

// LabelSelectorRequirement is a selector that contains values, a key, and an operator that
// relates the key and values.
type LabelSelectorRequirement struct {
	// Key is the label key that the selector applies to.
	Key string `json:"key"`

	// Operator represents a key's relationship to a set of values.
	// Valid operators are In, NotIn, Exists and DoesNotExist.
	Operator string `json:"operator"`

	// Values is an array of string values. If the operator is In or NotIn,
	// the values array must be non-empty. If the operator is Exists or DoesNotExist,
	// the values array must be empty.
	Values []string `json:"values,omitempty"`
}

// NoOpParameters defines the expected parameters for the noop executor.
type NoOpParameters struct {
	// RandomDelaySeconds specifies the maximum duration in seconds for random sleep during operations.
	// The actual delay will be randomly chosen between 0 and this value.
	// Maximum allowed is 30 seconds. Defaults to 1 if not specified.
	RandomDelaySeconds int `json:"randomDelaySeconds,omitempty"`

	// FailureMode specifies when to simulate failures. Valid values: "none", "shutdown", "wakeup", "both".
	// Defaults to "none".
	FailureMode string `json:"failureMode,omitempty"`

	// FailureMessage allows customizing the error message for simulated failures.
	// If empty, a default message will be used.
	FailureMessage string `json:"failureMessage,omitempty"`
}
