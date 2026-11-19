/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package executor

import "testing"

func TestAccessRequirementValidate(t *testing.T) {
	tests := []struct {
		name    string
		req     AccessRequirement
		wantErr bool
	}{
		{
			name: "valid AWS",
			req: AccessRequirement{
				ConnectorKind: ConnectorKindCloudProvider,
				Shutdown:      OperationAccess{AWSActions: []string{"ec2:StopInstances"}},
			},
		},
		{
			name: "valid K8s",
			req: AccessRequirement{
				ConnectorKind: ConnectorKindK8SCluster,
				WakeUp:        OperationAccess{K8SRules: []K8SRule{{Group: "apps", Resource: "deployments", Verbs: []string{"get"}}}},
			},
		},
		{name: "empty kind", req: AccessRequirement{}, wantErr: true},
		{name: "unknown kind", req: AccessRequirement{ConnectorKind: "Other"}, wantErr: true},
		{
			name: "AWS actions on K8SCluster",
			req: AccessRequirement{
				ConnectorKind: ConnectorKindK8SCluster,
				Shutdown:      OperationAccess{AWSActions: []string{"ec2:StopInstances"}},
			},
			wantErr: true,
		},
		{
			name: "K8S rules on CloudProvider",
			req: AccessRequirement{
				ConnectorKind: ConnectorKindCloudProvider,
				WakeUp:        OperationAccess{K8SRules: []K8SRule{{Resource: "nodes", Verbs: []string{"list"}}}},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.req.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
