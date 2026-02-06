/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package executorparams

import (
	"testing"
)

func TestValidateParams_UnknownExecutor(t *testing.T) {
	result := ValidateParams("unknown-executor", []byte(`{"foo": "bar"}`))
	if result != nil {
		t.Errorf("expected nil for unknown executor, got %+v", result)
	}
}

func TestValidateParams_EC2_Valid(t *testing.T) {
	params := []byte(`{"selector": {"tags": {"Environment": "dev"}}}`)
	result := ValidateParams("ec2", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.HasErrors() {
		t.Errorf("expected no errors, got: %v", result.Errors)
	}
}

func TestValidateParams_EC2_MissingSelector(t *testing.T) {
	params := []byte(`{}`)
	result := ValidateParams("ec2", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.HasErrors() {
		t.Error("expected errors for missing selector")
	}
}

func TestValidateParams_EC2_EmptyParams(t *testing.T) {
	result := ValidateParams("ec2", nil)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.HasErrors() {
		t.Error("expected errors for empty params")
	}
}

func TestValidateParams_EC2_UnknownField(t *testing.T) {
	params := []byte(`{"selector": {"instanceIds": ["i-123"]}, "unknownField": "value"}`)
	result := ValidateParams("ec2", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.HasErrors() {
		t.Errorf("unknown fields should not cause errors, got: %v", result.Errors)
	}
	if len(result.Warnings) == 0 {
		t.Error("expected warnings for unknown field")
	}
}

func TestValidateParams_RDS_Valid(t *testing.T) {
	params := []byte(`{"selector": {"instanceIds": ["my-db"]}}`)
	result := ValidateParams("rds", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.HasErrors() {
		t.Errorf("expected no errors, got: %v", result.Errors)
	}
}

func TestValidateParams_RDS_BothInstanceAndCluster(t *testing.T) {
	params := []byte(`{"selector": {"instanceIds": ["my-db"], "tags": {"env": "prod"}}}`)
	result := ValidateParams("rds", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.HasErrors() {
		t.Error("expected error when multiple selector methods specified")
	}
}

func TestValidateParams_RDS_MissingBoth(t *testing.T) {
	params := []byte(`{"snapshotBeforeStop": true, "selector": {}}`)
	result := ValidateParams("rds", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.HasErrors() {
		t.Error("expected error when selector has no selection method")
	}
}

func TestValidateParams_EKS_EmptyParams(t *testing.T) {
	// EKS requires clusterName
	result := ValidateParams("eks", nil)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.HasErrors() {
		t.Error("expected error for empty params (missing clusterName)")
	}
}

func TestValidateParams_EKS_Valid(t *testing.T) {
	params := []byte(`{"clusterName": "my-cluster"}`)
	result := ValidateParams("eks", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.HasErrors() {
		t.Errorf("expected no errors, got: %v", result.Errors)
	}
}

func TestValidateParams_EKS_WithNodeGroups(t *testing.T) {
	params := []byte(`{"clusterName": "my-cluster", "nodeGroups": [{"name": "ng-1"}, {"name": "ng-2"}]}`)
	result := ValidateParams("eks", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.HasErrors() {
		t.Errorf("expected no errors, got: %v", result.Errors)
	}
}

func TestValidateParams_EKS_MissingClusterName(t *testing.T) {
	params := []byte(`{"nodeGroups": [{"name": "ng-1"}]}`)
	result := ValidateParams("eks", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.HasErrors() {
		t.Error("expected error for missing clusterName")
	}
}

func TestValidateParams_Karpenter_Valid(t *testing.T) {
	params := []byte(`{"nodePools": ["default", "gpu"]}`)
	result := ValidateParams("karpenter", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.HasErrors() {
		t.Errorf("expected no errors, got: %v", result.Errors)
	}
}

func TestValidateParams_Karpenter_EmptyNodePools(t *testing.T) {
	// Empty nodePools is valid - means target all NodePools
	params := []byte(`{"nodePools": []}`)
	result := ValidateParams("karpenter", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.HasErrors() {
		t.Errorf("expected no errors for empty nodePools (means all), got: %v", result.Errors)
	}
}

func TestValidateParams_Karpenter_MissingNodePools(t *testing.T) {
	// Missing/nil params is valid - means target all NodePools
	result := ValidateParams("karpenter", nil)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.HasErrors() {
		t.Errorf("expected no errors for missing params (means all), got: %v", result.Errors)
	}
}

func TestValidateParams_GKE_Valid(t *testing.T) {
	params := []byte(`{"nodePools": ["default-pool"]}`)
	result := ValidateParams("gke", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.HasErrors() {
		t.Errorf("expected no errors, got: %v", result.Errors)
	}
}

func TestValidateParams_CloudSQL_Valid(t *testing.T) {
	params := []byte(`{"instanceName": "my-db", "project": "my-project"}`)
	result := ValidateParams("cloudsql", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.HasErrors() {
		t.Errorf("expected no errors, got: %v", result.Errors)
	}
}

func TestValidateParams_CloudSQL_MissingInstanceName(t *testing.T) {
	params := []byte(`{"project": "my-project"}`)
	result := ValidateParams("cloudsql", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.HasErrors() {
		t.Error("expected error for missing instanceName")
	}
}

func TestValidateParams_CloudSQL_MissingProject(t *testing.T) {
	params := []byte(`{"instanceName": "my-db"}`)
	result := ValidateParams("cloudsql", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.HasErrors() {
		t.Error("expected error for missing project")
	}
}

func TestResult_Merge(t *testing.T) {
	r1 := &Result{Errors: []string{"err1"}, Warnings: []string{"warn1"}}
	r2 := &Result{Errors: []string{"err2"}, Warnings: []string{"warn2"}}

	r1.Merge(r2)

	if len(r1.Errors) != 2 {
		t.Errorf("expected 2 errors, got %d", len(r1.Errors))
	}
	if len(r1.Warnings) != 2 {
		t.Errorf("expected 2 warnings, got %d", len(r1.Warnings))
	}
}

func TestResult_MergeNil(t *testing.T) {
	r1 := &Result{Errors: []string{"err1"}}
	r1.Merge(nil)

	if len(r1.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(r1.Errors))
	}
}

func TestRegisteredTypes(t *testing.T) {
	types := RegisteredTypes()
	if len(types) == 0 {
		t.Error("expected at least one registered type")
	}

	// Verify known types are registered
	expected := map[string]bool{
		"ec2":       false,
		"rds":       false,
		"eks":       false,
		"karpenter": false,
		"gke":       false,
		"cloudsql":  false,
	}

	for _, typ := range types {
		if _, ok := expected[typ]; ok {
			expected[typ] = true
		}
	}

	for typ, found := range expected {
		if !found {
			t.Errorf("expected type %q to be registered", typ)
		}
	}
}

func TestIsRegistered(t *testing.T) {
	if !IsRegistered("ec2") {
		t.Error("expected ec2 to be registered")
	}
	if IsRegistered("nonexistent") {
		t.Error("expected nonexistent to not be registered")
	}
}

func TestValidateParams_WorkloadScaler_Valid_NamespaceLiterals(t *testing.T) {
	params := []byte(`{"namespace": {"literals": ["default", "kube-system"]}}`)
	result := ValidateParams("workloadscaler", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.HasErrors() {
		t.Errorf("expected no errors, got: %v", result.Errors)
	}
}

func TestValidateParams_WorkloadScaler_Valid_NamespaceSelector(t *testing.T) {
	params := []byte(`{"namespace": {"selector": {"env": "prod"}}}`)
	result := ValidateParams("workloadscaler", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.HasErrors() {
		t.Errorf("expected no errors, got: %v", result.Errors)
	}
}

func TestValidateParams_WorkloadScaler_Valid_WithWorkloadSelector_MatchLabels(t *testing.T) {
	params := []byte(`{
		"namespace": {"literals": ["default"]},
		"workloadSelector": {
			"matchLabels": {
				"app": "nginx",
				"tier": "frontend"
			}
		}
	}`)
	result := ValidateParams("workloadscaler", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.HasErrors() {
		t.Errorf("expected no errors, got: %v", result.Errors)
	}
}

func TestValidateParams_WorkloadScaler_Valid_WithWorkloadSelector_MatchExpressions(t *testing.T) {
	params := []byte(`{
		"namespace": {"literals": ["default"]},
		"workloadSelector": {
			"matchExpressions": [
				{
					"key": "environment",
					"operator": "In",
					"values": ["prod", "staging"]
				},
				{
					"key": "app",
					"operator": "Exists"
				}
			]
		}
	}`)
	result := ValidateParams("workloadscaler", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.HasErrors() {
		t.Errorf("expected no errors, got: %v", result.Errors)
	}
}

func TestValidateParams_WorkloadScaler_Valid_WithIncludedGroups(t *testing.T) {
	params := []byte(`{
		"namespace": {"literals": ["default"]},
		"includedGroups": ["Deployment", "StatefulSet"]
	}`)
	result := ValidateParams("workloadscaler", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.HasErrors() {
		t.Errorf("expected no errors, got: %v", result.Errors)
	}
}

func TestValidateParams_WorkloadScaler_Invalid_LabelKey(t *testing.T) {
	params := []byte(`{
		"namespace": {"literals": ["default"]},
		"workloadSelector": {
			"matchLabels": {
				"invalid key with spaces": "value"
			}
		}
	}`)
	result := ValidateParams("workloadscaler", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.HasErrors() {
		t.Error("expected error for invalid label key with spaces")
	}
}

func TestValidateParams_WorkloadScaler_Invalid_MatchExpressionsOperator(t *testing.T) {
	params := []byte(`{
		"namespace": {"literals": ["default"]},
		"workloadSelector": {
			"matchExpressions": [
				{
					"key": "app",
					"operator": "InvalidOperator",
					"values": ["nginx"]
				}
			]
		}
	}`)
	result := ValidateParams("workloadscaler", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.HasErrors() {
		t.Error("expected error for invalid matchExpressions operator")
	}
}

func TestValidateParams_WorkloadScaler_Invalid_ExistsOperatorWithValues(t *testing.T) {
	params := []byte(`{
		"namespace": {"literals": ["default"]},
		"workloadSelector": {
			"matchExpressions": [
				{
					"key": "app",
					"operator": "Exists",
					"values": ["should-not-have-values"]
				}
			]
		}
	}`)
	result := ValidateParams("workloadscaler", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.HasErrors() {
		t.Error("expected error for Exists operator with values")
	}
}

func TestValidateParams_WorkloadScaler_Invalid_InOperatorWithoutValues(t *testing.T) {
	params := []byte(`{
		"namespace": {"literals": ["default"]},
		"workloadSelector": {
			"matchExpressions": [
				{
					"key": "app",
					"operator": "In",
					"values": []
				}
			]
		}
	}`)
	result := ValidateParams("workloadscaler", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.HasErrors() {
		t.Error("expected error for In operator without values")
	}
}

func TestValidateParams_WorkloadScaler_MissingNamespace(t *testing.T) {
	params := []byte(`{"includedGroups": ["Deployment"]}`)
	result := ValidateParams("workloadscaler", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.HasErrors() {
		t.Error("expected error for missing namespace")
	}
}

func TestValidateParams_WorkloadScaler_BothNamespaceLiteralsAndSelector(t *testing.T) {
	params := []byte(`{
		"namespace": {
			"literals": ["default"],
			"selector": {"env": "prod"}
		}
	}`)
	result := ValidateParams("workloadscaler", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.HasErrors() {
		t.Error("expected error when both namespace literals and selector are specified")
	}
}

func TestValidateParams_WorkloadScaler_EmptyParams(t *testing.T) {
	result := ValidateParams("workloadscaler", nil)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.HasErrors() {
		t.Error("expected error for empty params (missing namespace)")
	}
}

func TestValidateParams_WorkloadScaler_InvalidWaitTimeout(t *testing.T) {
	params := []byte(`{
		"namespace": {"literals": ["default"]},
		"waitConfig": {
			"enabled": true,
			"timeout": "invalid-duration"
		}
	}`)
	result := ValidateParams("workloadscaler", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.HasErrors() {
		t.Error("expected error for invalid wait timeout format")
	}
}

func TestValidateParams_WorkloadScaler_ValidWaitTimeout(t *testing.T) {
	params := []byte(`{
		"namespace": {"literals": ["default"]},
		"waitConfig": {
			"enabled": true,
			"timeout": "5m"
		}
	}`)
	result := ValidateParams("workloadscaler", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.HasErrors() {
		t.Errorf("expected no errors, got: %v", result.Errors)
	}
}

func TestValidateParams_WorkloadScaler_Valid_ComplexSelector(t *testing.T) {
	params := []byte(`{
		"namespace": {"literals": ["production", "staging"]},
		"includedGroups": ["Deployment", "StatefulSet", "DaemonSet"],
		"workloadSelector": {
			"matchLabels": {
				"app": "backend"
			},
			"matchExpressions": [
				{
					"key": "tier",
					"operator": "In",
					"values": ["api", "worker"]
				},
				{
					"key": "deprecated",
					"operator": "DoesNotExist"
				}
			]
		},
		"waitConfig": {
			"enabled": true,
			"timeout": "10m"
		}
	}`)
	result := ValidateParams("workloadscaler", params)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.HasErrors() {
		t.Errorf("expected no errors, got: %v", result.Errors)
	}
}
