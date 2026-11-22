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

// ConnectorTypeAWS is the CloudProvider type backed by AWS (CloudProvider.spec.type).
const ConnectorTypeAWS = "aws"

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
	// ConnectorTypes restricts the requirement to connectors of these types
	// (CloudProvider.spec.type). Empty applies to every type of the kind.
	// Only CloudProvider connectors carry a type today.
	ConnectorTypes []string
	Shutdown       OperationAccess
	WakeUp         OperationAccess
}

// ConnectorRequirer is optionally implemented by executors that declare the
// access they need so it can be verified against their connector.
type ConnectorRequirer interface {
	Requires() AccessRequirement
}

// AppliesTo reports whether the requirement covers a connector of the given
// type. A restricted requirement never applies to an unknown (empty) type.
func (r AccessRequirement) AppliesTo(connectorType string) bool {
	if len(r.ConnectorTypes) == 0 {
		return true
	}
	for _, t := range r.ConnectorTypes {
		if t == connectorType {
			return true
		}
	}
	return false
}

// Validate reports a malformed requirement.
func (r AccessRequirement) Validate() error {
	for _, t := range r.ConnectorTypes {
		if t == "" {
			return fmt.Errorf("connector type must not be empty")
		}
	}
	ops := []OperationAccess{r.Shutdown, r.WakeUp}
	switch r.ConnectorKind {
	case ConnectorKindCloudProvider:
		for _, op := range ops {
			if len(op.K8SRules) > 0 {
				return fmt.Errorf("K8S rules are not valid for connector kind %s", r.ConnectorKind)
			}
		}
	case ConnectorKindK8SCluster:
		if len(r.ConnectorTypes) > 0 {
			return fmt.Errorf("connector types are not supported for connector kind %s", r.ConnectorKind)
		}
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
