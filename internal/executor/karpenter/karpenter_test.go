/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package karpenter

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ardikabs/hibernator/internal/executor"
)

func TestNew(t *testing.T) {
	e := New()
	if e == nil {
		t.Fatal("expected non-nil executor")
	}
}

func TestExecutorType(t *testing.T) {
	e := New()
	if e.Type() != "karpenter" {
		t.Errorf("expected type 'karpenter', got %s", e.Type())
	}
}

func TestValidate_MissingK8SConfig(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-nodepool",
		TargetType: "karpenter",
		Parameters: json.RawMessage(`{"nodePools": ["default"]}`),
	}
	err := e.Validate(spec)
	if err == nil {
		t.Error("expected error for missing K8S config")
	}
}

func TestValidate_MissingClusterName(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-nodepool",
		TargetType: "karpenter",
		Parameters: json.RawMessage(`{"nodePools": ["default"]}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{
				Region: "us-east-1",
			},
		},
	}
	err := e.Validate(spec)
	if err == nil {
		t.Error("expected error for missing cluster name")
	}
}

func TestValidate_MissingRegion(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-nodepool",
		TargetType: "karpenter",
		Parameters: json.RawMessage(`{"nodePools": ["default"]}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{
				ClusterName: "test-cluster",
			},
		},
	}
	err := e.Validate(spec)
	if err == nil {
		t.Error("expected error for missing region")
	}
}

func TestValidate_MissingNodePools(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-nodepool",
		TargetType: "karpenter",
		Parameters: json.RawMessage(`{}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{
				ClusterName: "test-cluster",
				Region:      "us-east-1",
			},
		},
	}
	err := e.Validate(spec)
	if err == nil {
		t.Error("expected error for missing node pools")
	}
}

func TestValidate_EmptyNodePools(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-nodepool",
		TargetType: "karpenter",
		Parameters: json.RawMessage(`{"nodePools": []}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{
				ClusterName: "test-cluster",
				Region:      "us-east-1",
			},
		},
	}
	err := e.Validate(spec)
	if err == nil {
		t.Error("expected error for empty node pools")
	}
}

func TestValidate_Valid(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-nodepool",
		TargetType: "karpenter",
		Parameters: json.RawMessage(`{"nodePools": ["default", "gpu"]}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{
				ClusterName: "test-cluster",
				Region:      "us-east-1",
			},
		},
	}
	err := e.Validate(spec)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNodePoolState_Marshal(t *testing.T) {
	state := NodePoolState{
		Name: "default",
		DisruptionBudgets: map[string]interface{}{
			"consolidationPolicy": "WhenEmpty",
		},
		Limits: map[string]interface{}{
			"cpu":    "1000",
			"memory": "1000Gi",
		},
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded NodePoolState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Name != "default" {
		t.Errorf("expected name 'default', got %s", decoded.Name)
	}
	if decoded.Limits == nil {
		t.Error("expected limits to be non-nil")
	}
	if decoded.DisruptionBudgets == nil {
		t.Error("expected disruption budgets to be non-nil")
	}
}

func TestNodePoolStates_Map(t *testing.T) {
	states := map[string]NodePoolState{
		"default": {
			Name: "default",
			Limits: map[string]interface{}{
				"cpu": "100",
			},
		},
		"gpu": {
			Name: "gpu",
			Limits: map[string]interface{}{
				"nvidia.com/gpu": "10",
			},
		},
	}

	data, err := json.Marshal(states)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded map[string]NodePoolState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if len(decoded) != 2 {
		t.Errorf("expected 2 states, got %d", len(decoded))
	}
	if decoded["default"].Name != "default" {
		t.Error("expected default state")
	}
	if decoded["gpu"].Name != "gpu" {
		t.Error("expected gpu state")
	}
}

func TestParameters_Parse(t *testing.T) {
	tests := []struct {
		name      string
		json      string
		wantErr   bool
		wantPools int
	}{
		{
			name:      "single nodepool",
			json:      `{"nodePools": ["default"]}`,
			wantPools: 1,
		},
		{
			name:      "multiple nodepools",
			json:      `{"nodePools": ["default", "gpu", "spot"]}`,
			wantPools: 3,
		},
		{
			name:    "invalid json",
			json:    `{invalid}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var params struct {
				NodePools []string `json:"nodePools"`
			}
			err := json.Unmarshal([]byte(tt.json), &params)
			if (err != nil) != tt.wantErr {
				t.Errorf("Unmarshal() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && len(params.NodePools) != tt.wantPools {
				t.Errorf("expected %d pools, got %d", tt.wantPools, len(params.NodePools))
			}
		})
	}
}

func TestExecutor_Shutdown_InvalidParams(t *testing.T) {
	e := New()
	ctx := context.Background()

	spec := executor.Spec{
		TargetName: "test-nodepool",
		TargetType: "karpenter",
		Parameters: json.RawMessage(`{invalid json}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{
				ClusterName: "test-cluster",
				Region:      "us-east-1",
			},
		},
	}

	_, err := e.Shutdown(ctx, spec)
	if err == nil {
		t.Error("expected error for invalid params")
	}
}

func TestExecutor_WakeUp_InvalidRestoreData(t *testing.T) {
	e := New()
	ctx := context.Background()

	spec := executor.Spec{
		TargetName: "test-nodepool",
		TargetType: "karpenter",
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{
				ClusterName: "test-cluster",
				Region:      "us-east-1",
			},
		},
	}

	restore := executor.RestoreData{
		Type: "karpenter",
		Data: json.RawMessage(`{invalid json}`),
	}

	err := e.WakeUp(ctx, spec, restore)
	if err == nil {
		t.Error("expected error for invalid restore data")
	}
}

func TestNodePoolState_AllFields(t *testing.T) {
	state := NodePoolState{
		Name: "default",
		Limits: map[string]interface{}{
			"cpu":    "100",
			"memory": "1000Gi",
		},
		DisruptionBudgets: map[string]interface{}{
			"consolidationPolicy": "WhenEmpty",
		},
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded NodePoolState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.Name != "default" {
		t.Errorf("expected name 'default', got %s", decoded.Name)
	}
	if decoded.Limits == nil {
		t.Error("expected non-nil Limits")
	}
	if decoded.DisruptionBudgets == nil {
		t.Error("expected non-nil DisruptionBudgets")
	}
}

func TestNodePoolState_Empty(t *testing.T) {
	state := NodePoolState{
		Name: "empty",
	}

	if state.Name != "empty" {
		t.Errorf("expected name 'empty', got %s", state.Name)
	}
	if state.Limits != nil {
		t.Error("expected nil Limits")
	}
	if state.DisruptionBudgets != nil {
		t.Error("expected nil DisruptionBudgets")
	}
}

func TestValidate_InvalidParams(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-nodepool",
		TargetType: "karpenter",
		Parameters: json.RawMessage(`{invalid json}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{
				ClusterName: "test-cluster",
				Region:      "us-east-1",
			},
		},
	}
	err := e.Validate(spec)
	if err == nil {
		t.Error("expected error for invalid params")
	}
}
