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
