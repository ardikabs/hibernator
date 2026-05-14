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

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ardikabs/hibernator/pkg/awsutil"
)

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
	// Selector defines how to find EC2 instances to hibernate.
	Selector EC2Selector `json:"selector"`

	// AwaitCompletion configures whether to wait for EC2 instances to reach the desired state.
	AwaitCompletion AwaitCompletion `json:"awaitCompletion"`
}

// EC2Selector defines how to find EC2 instances.
//
// SELECTION METHODS (mutually exclusive server-side filters):
//   - Tags: server-side filter via AWS DescribeInstances Filters
//   - InstanceIDs: server-side filter via explicit InstanceIds
//
// CLIENT-SIDE FILTER:
//   - TagSelector: applied AFTER instances are fetched. Can be used alone or combined
//     with InstanceIDs, but is mutually exclusive with Tags.
//
// At least one selection method must be specified.
type EC2Selector struct {
	// Tags filters instances by AWS resource tags using DescribeInstances Filters.
	// Applied server-side before instances are returned.
	// Mutually exclusive with InstanceIDs (both are server-side filters).
	Tags map[string]string `json:"tags,omitempty"`

	// TagSelector provides flexible expression-based tag matching.
	// Applied client-side after instances are fetched.
	// Mutually exclusive with Tags.
	TagSelector *awsutil.TagSelector `json:"tagSelector,omitempty"`

	// InstanceIDs is a list of explicit EC2 instance IDs to target.
	// Applied server-side via DescribeInstances InstanceIds.
	// Mutually exclusive with Tags (both are server-side filters).
	InstanceIDs []string `json:"instanceIds,omitempty"`
}

// RDSParameters defines the expected parameters for the RDS executor.
type RDSParameters struct {
	// SnapshotBeforeStop creates a final snapshot before stopping RDS instances.
	SnapshotBeforeStop bool `json:"snapshotBeforeStop,omitempty"`

	// Selector defines how to find RDS instances and clusters to hibernate.
	Selector RDSSelector `json:"selector"`

	// AwaitCompletion configures whether to wait for RDS resources to reach the desired state.
	AwaitCompletion AwaitCompletion `json:"awaitCompletion"`
}

// RDSSelector defines how to find RDS instances and clusters.
//
// MUTUAL EXCLUSIVITY RULES:
// Only ONE of the following selection methods can be used:
//  1. Tag-based selection: `tags` OR `excludeTags` OR `tagSelector`
//  2. Explicit IDs: `instanceIds` and/or `clusterIds` (intent-based, discovers exactly what you specify)
//  3. Discovery mode: `includeAll`
//
// RESOURCE TYPE CONTROL:
// For intent-based selection (`instanceIds`/`clusterIds`), resource types are implicit:
//   - If `instanceIds` specified → discovers instances
//   - If `clusterIds` specified → discovers clusters
//   - If both specified → discovers both
//
// For dynamic discovery (`tags`/`excludeTags`/`tagSelector`/`includeAll`), `discoverInstances` and `discoverClusters`
// must be explicitly enabled (opt-out by default):
//   - Neither set: no resources discovered (no-op)
//   - `discoverInstances`: true only: discovers only DB instances
//   - `discoverClusters`: true only: discovers only DB clusters
//   - Both true: discovers both instances and clusters
//
// Examples (valid):
//   - `{tags: {"env": "prod"}, discoverInstances: true}` — tag-based, discovers only DB instances
//   - `{excludeTags: {"critical": "true"}, discoverClusters: true}` — exclusion-based, discovers only DB clusters
//   - `{tagSelector: {matchTags: {"env": "prod"}}, discoverInstances: true}` — expression-based
//   - `{instanceIds: ["db-1", "db-2"], clusterIds: ["cluster-1"]}` — explicit IDs; resource types inferred from which IDs are provided
//   - `{includeAll: true, discoverInstances: true, discoverClusters: true}` — discovers all instances and clusters in the region
//
// Examples (no-op — nothing will be discovered):
//   - `{tags: {"env": "prod"}}` — tag-based selection requires at least one of `discoverInstances` or `discoverClusters`
//
// Examples (invalid — rejected at validation):
//   - `{tags: {...}, instanceIds: [...]}` — cannot mix tag-based selection with explicit IDs
//   - `{tags: {...}, excludeTags: {...}}` — tags and excludeTags are mutually exclusive
//   - `{tags: {...}, tagSelector: {...}}` — tags and tagSelector are mutually exclusive
//   - `{includeAll: true, tags: {...}}` — includeAll cannot be combined with any other selector
type RDSSelector struct {
	// Tags for inclusion. If value is empty string "", matches any instance with that key.
	// If value is non-empty, matches only exact key=value.
	// DEPRECATED: Use tagSelector for expression-based matching.
	// Mutually exclusive with: ExcludeTags, TagSelector, InstanceIDs, ClusterIDs, IncludeAll.
	Tags map[string]string `json:"tags,omitempty"`

	// ExcludeTags for exclusion. Same logic: empty value = exclude if key exists.
	// DEPRECATED: Use tagSelector with DoesNotExist/NotIn operators instead.
	// Mutually exclusive with: Tags, TagSelector, InstanceIDs, ClusterIDs, IncludeAll.
	ExcludeTags map[string]string `json:"excludeTags,omitempty"`

	// TagSelector provides flexible expression-based tag matching.
	// Mutually exclusive with Tags and ExcludeTags.
	TagSelector *awsutil.TagSelector `json:"tagSelector,omitempty"`

	// Explicit DB instance IDs to target.
	// Can be combined with ClusterIDs, but mutually exclusive with tag-based selection or IncludeAll.
	InstanceIds []string `json:"instanceIds,omitempty"`

	// Explicit DB cluster IDs to target.
	// Can be combined with InstanceIDs, but mutually exclusive with tag-based selection or IncludeAll.
	ClusterIds []string `json:"clusterIds,omitempty"`

	// IncludeAll discovers all DB instances and clusters in the account/region.
	// Mutually exclusive with all other selection methods.
	IncludeAll bool `json:"includeAll,omitempty"`

	// DiscoverInstances controls whether to discover DB instances for dynamic selection methods.
	// Only used with `tags`, `excludeTags`, `tagSelector`, or `includeAll` (ignored for explicit `instanceIds`/`clusterIds`).
	// Must be explicitly set to true to discover instances. Default: false (opt-out, no-op).
	DiscoverInstances bool `json:"discoverInstances,omitempty"`

	// DiscoverClusters controls whether to discover DB clusters for dynamic selection methods.
	// Only used with `tags`, `excludeTags`, `tagSelector`, or `includeAll` (ignored for explicit `instanceIds`/`clusterIds`).
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
	NodeGroups []EKSNodeGroup `json:"nodeGroups,omitempty"`

	// AwaitCompletion configures whether to wait for node groups to reach the desired state.
	AwaitCompletion AwaitCompletion `json:"awaitCompletion"`
}

// EKSNodeGroup specifies a managed node group to hibernate.
type EKSNodeGroup struct {
	// Name is the name of the managed node group.
	Name string `json:"name"`
}

// KarpenterParameters defines the expected parameters for the Karpenter executor.
type KarpenterParameters struct {
	// NodePools is a list of Karpenter NodePool names to hibernate.
	// DEPRECATED: Use nodeSelector for label-based selection.
	// Mutually exclusive with NodeSelector.
	NodePools []string `json:"nodePools,omitempty"`

	// NodeSelector selects NodePools by labels using Kubernetes LabelSelector semantics.
	// Mutually exclusive with NodePools.
	NodeSelector *metav1.LabelSelector `json:"nodeSelector,omitempty"`

	// AwaitCompletion configures whether to wait for node pools to drain.
	AwaitCompletion AwaitCompletion `json:"awaitCompletion"`
}

// GKEParameters defines the expected parameters for the GKE executor.
type GKEParameters struct {
	// NodePools is a list of GKE node pool names to hibernate.
	NodePools []string `json:"nodePools"`
}

// CloudSQLParameters defines the expected parameters for the Cloud SQL executor.
type CloudSQLParameters struct {
	// InstanceName is the Cloud SQL instance name.
	InstanceName string `json:"instanceName"`

	// Project is the GCP project ID containing the instance.
	Project string `json:"project"`
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
