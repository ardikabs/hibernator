/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package executor

import "fmt"

// Connector kinds an executor can require.
const (
	ConnectorKindCloudProvider = "CloudProvider"
	ConnectorKindK8SCluster    = "K8SCluster"
)

// K8SRule is a Kubernetes RBAC-style permission an executor needs.
type K8SRule struct {
	Group       string
	Resource    string
	Subresource string
	Verbs       []string
}

// OperationAccess lists the permissions needed to run one operation.
type OperationAccess struct {
	AWSActions []string
	K8SRules   []K8SRule
}

// AccessRequirement declares the connector kind an executor uses and the
// permissions each operation needs on it.
type AccessRequirement struct {
	ConnectorKind string
	Shutdown      OperationAccess
	WakeUp        OperationAccess
}

// ConnectorRequirer is optionally implemented by executors that declare the
// access they need so it can be verified against their connector.
type ConnectorRequirer interface {
	Requires() AccessRequirement
}

// Validate reports a malformed requirement.
func (r AccessRequirement) Validate() error {
	ops := []OperationAccess{r.Shutdown, r.WakeUp}
	switch r.ConnectorKind {
	case ConnectorKindCloudProvider:
		for _, op := range ops {
			if len(op.K8SRules) > 0 {
				return fmt.Errorf("K8S rules are not valid for connector kind %s", r.ConnectorKind)
			}
		}
	case ConnectorKindK8SCluster:
		for _, op := range ops {
			if len(op.AWSActions) > 0 {
				return fmt.Errorf("AWS actions are not valid for connector kind %s", r.ConnectorKind)
			}
		}
	case "":
		return fmt.Errorf("connector kind is required")
	default:
		return fmt.Errorf("unknown connector kind %q", r.ConnectorKind)
	}
	return nil
}
