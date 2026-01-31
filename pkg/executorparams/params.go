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

// EC2Parameters defines the expected parameters for the EC2 executor.
type EC2Parameters struct {
	Selector EC2Selector `json:"selector"`
}

// EC2Selector defines how to find EC2 instances.
type EC2Selector struct {
	Tags        map[string]string `json:"tags,omitempty"`
	InstanceIDs []string          `json:"instanceIds,omitempty"`
}

// RDSParameters defines the expected parameters for the RDS executor.
type RDSParameters struct {
	SnapshotBeforeStop bool   `json:"snapshotBeforeStop,omitempty"`
	InstanceID         string `json:"instanceId,omitempty"`
	ClusterID          string `json:"clusterId,omitempty"`
}

// EKSParameters defines the expected parameters for the EKS executor.
// EKS executor only handles Managed Node Groups via AWS API.
// For Karpenter NodePools, use the separate Karpenter executor.
type EKSParameters struct {
	// ClusterName is the EKS cluster name (required).
	ClusterName string `json:"clusterName"`
	// NodeGroups to hibernate. If empty, all node groups in the cluster are targeted.
	NodeGroups []EKSNodeGroup `json:"nodeGroups,omitempty"`
}

// EKSNodeGroup specifies a managed node group to hibernate.
type EKSNodeGroup struct {
	Name string `json:"name"`
}

// KarpenterParameters defines the expected parameters for the Karpenter executor.
type KarpenterParameters struct {
	NodePools []string `json:"nodePools"`
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
	WorkloadSelector *LabelSelector `json:"workloadSelector,omitempty"`
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
