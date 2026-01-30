/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package eks

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
	if e.Type() != ExecutorType {
		t.Errorf("expected type '%s', got %s", ExecutorType, e.Type())
	}
	if e.Type() != "eks" {
		t.Errorf("expected type 'eks', got %s", e.Type())
	}
}

func TestValidate_MissingK8SConfig(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
		Parameters: json.RawMessage(`{}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}
	err := e.Validate(spec)
	if err == nil {
		t.Error("expected error for missing K8S config")
	}
}

func TestValidate_WithK8SConfig(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
		Parameters: json.RawMessage(`{}`),
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

func TestParameters_Unmarshal(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr bool
	}{
		{
			name:    "empty parameters",
			json:    `{}`,
			wantErr: false,
		},
		{
			name:    "with compute policy",
			json:    `{"computePolicy": {"mode": "Both", "order": ["karpenter", "managedNodeGroups"]}}`,
			wantErr: false,
		},
		{
			name:    "with karpenter config",
			json:    `{"karpenter": {"targetNodePools": ["default"], "strategy": "DeleteNodes"}}`,
			wantErr: false,
		},
		{
			name:    "with managed node groups",
			json:    `{"managedNodeGroups": {"strategy": "ScaleToZero"}}`,
			wantErr: false,
		},
		{
			name:    "invalid json",
			json:    `{invalid}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var params Parameters
			err := json.Unmarshal([]byte(tt.json), &params)
			if (err != nil) != tt.wantErr {
				t.Errorf("Unmarshal() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRestoreState_Marshal(t *testing.T) {
	state := RestoreState{
		ManagedNodeGroups: map[string]NodeGroupState{
			"ng-1": {DesiredSize: 3, MinSize: 1, MaxSize: 5},
		},
		Karpenter: &KarpenterState{
			NodePools: []string{"default"},
		},
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded RestoreState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.ManagedNodeGroups["ng-1"].DesiredSize != 3 {
		t.Error("expected desired size 3")
	}
	if len(decoded.Karpenter.NodePools) != 1 {
		t.Error("expected 1 node pool")
	}
}

func TestNodeGroupState(t *testing.T) {
	state := NodeGroupState{
		DesiredSize: 2,
		MinSize:     1,
		MaxSize:     10,
	}
	if state.DesiredSize != 2 {
		t.Errorf("expected desired 2, got %d", state.DesiredSize)
	}
	if state.MinSize != 1 {
		t.Errorf("expected min 1, got %d", state.MinSize)
	}
	if state.MaxSize != 10 {
		t.Errorf("expected max 10, got %d", state.MaxSize)
	}
}

func TestComputePolicy(t *testing.T) {
	policy := ComputePolicy{
		Mode:  "Both",
		Order: []string{"karpenter", "managedNodeGroups"},
	}
	if policy.Mode != "Both" {
		t.Errorf("expected mode 'Both', got %s", policy.Mode)
	}
	if len(policy.Order) != 2 {
		t.Errorf("expected 2 order items, got %d", len(policy.Order))
	}
}

func TestKarpenterConfig(t *testing.T) {
	karpenter := Karpenter{
		TargetNodePools: []string{"default", "gpu"},
		Strategy:        "DeleteNodes",
	}
	if karpenter.Strategy != "DeleteNodes" {
		t.Errorf("expected strategy 'DeleteNodes', got %s", karpenter.Strategy)
	}
	if len(karpenter.TargetNodePools) != 2 {
		t.Errorf("expected 2 node pools, got %d", len(karpenter.TargetNodePools))
	}
}

func TestManagedNGsConfig(t *testing.T) {
	mng := ManagedNGs{
		Strategy: "ScaleToZero",
	}
	if mng.Strategy != "ScaleToZero" {
		t.Errorf("expected strategy 'ScaleToZero', got %s", mng.Strategy)
	}
}

func TestExecutor_ParseParams(t *testing.T) {
	e := New()

	tests := []struct {
		name    string
		raw     json.RawMessage
		wantErr bool
		check   func(Parameters) bool
	}{
		{
			name:    "empty raw message",
			raw:     nil,
			wantErr: false,
		},
		{
			name:    "empty json object",
			raw:     json.RawMessage(`{}`),
			wantErr: false,
		},
		{
			name:    "with compute policy mode",
			raw:     json.RawMessage(`{"computePolicy": {"mode": "Both"}}`),
			wantErr: false,
			check:   func(p Parameters) bool { return p.ComputePolicy.Mode == "Both" },
		},
		{
			name:    "with karpenter config",
			raw:     json.RawMessage(`{"karpenter": {"targetNodePools": ["default"], "strategy": "DeleteNodes"}}`),
			wantErr: false,
			check: func(p Parameters) bool {
				return p.Karpenter != nil && p.Karpenter.Strategy == "DeleteNodes" && len(p.Karpenter.TargetNodePools) == 1
			},
		},
		{
			name:    "with managed node groups",
			raw:     json.RawMessage(`{"managedNodeGroups": {"strategy": "ScaleToZero"}}`),
			wantErr: false,
			check:   func(p Parameters) bool { return p.ManagedNGs != nil && p.ManagedNGs.Strategy == "ScaleToZero" },
		},
		{
			name:    "invalid json",
			raw:     json.RawMessage(`{invalid`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, err := e.parseParams(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseParams() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.check != nil && !tt.check(params) {
				t.Error("check failed")
			}
		})
	}
}

func TestExecutor_Shutdown_InvalidParams(t *testing.T) {
	e := New()
	ctx := context.Background()

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
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
		TargetName: "test-cluster",
		TargetType: "eks",
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{
				ClusterName: "test-cluster",
				Region:      "us-east-1",
			},
		},
	}

	restore := executor.RestoreData{
		Type: "eks",
		Data: json.RawMessage(`{invalid json}`),
	}

	err := e.WakeUp(ctx, spec, restore)
	if err == nil {
		t.Error("expected error for invalid restore data")
	}
}

func TestRestoreState_AllFields(t *testing.T) {
	state := RestoreState{
		ManagedNodeGroups: map[string]NodeGroupState{
			"ng-1": {DesiredSize: 3, MinSize: 1, MaxSize: 5},
			"ng-2": {DesiredSize: 5, MinSize: 2, MaxSize: 10},
		},
		Karpenter: &KarpenterState{
			NodePools: []string{"default", "gpu"},
		},
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded RestoreState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(decoded.ManagedNodeGroups) != 2 {
		t.Errorf("expected 2 node groups, got %d", len(decoded.ManagedNodeGroups))
	}

	ng1 := decoded.ManagedNodeGroups["ng-1"]
	if ng1.DesiredSize != 3 || ng1.MinSize != 1 || ng1.MaxSize != 5 {
		t.Error("ng-1 state mismatch")
	}

	if decoded.Karpenter == nil {
		t.Fatal("expected Karpenter state")
	}
	if len(decoded.Karpenter.NodePools) != 2 {
		t.Errorf("expected 2 node pools, got %d", len(decoded.Karpenter.NodePools))
	}
}

func TestExecutorType_Constant(t *testing.T) {
	if ExecutorType != "eks" {
		t.Errorf("ExecutorType = %s, want 'eks'", ExecutorType)
	}
}

func TestParameters_Defaults(t *testing.T) {
	var params Parameters

	if params.ComputePolicy.Mode != "" {
		t.Error("default ComputePolicy.Mode should be empty")
	}
	if params.Karpenter != nil {
		t.Error("default Karpenter should be nil")
	}
	if params.ManagedNGs != nil {
		t.Error("default ManagedNGs should be nil")
	}
}

func TestKarpenterState_Marshal(t *testing.T) {
	state := KarpenterState{
		NodePools: []string{"default", "spot", "gpu"},
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded KarpenterState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(decoded.NodePools) != 3 {
		t.Errorf("expected 3 node pools, got %d", len(decoded.NodePools))
	}
}

func TestRestoreState_EmptyNodeGroups(t *testing.T) {
	state := RestoreState{
		ManagedNodeGroups: make(map[string]NodeGroupState),
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded RestoreState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(decoded.ManagedNodeGroups) != 0 {
		t.Errorf("expected 0 node groups, got %d", len(decoded.ManagedNodeGroups))
	}
}

func TestComputePolicy_WithOrder(t *testing.T) {
	policy := ComputePolicy{
		Mode:  "Both",
		Order: []string{"managedNodeGroups", "karpenter"},
	}

	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded ComputePolicy
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.Mode != "Both" {
		t.Errorf("expected mode 'Both', got %s", decoded.Mode)
	}
	if len(decoded.Order) != 2 {
		t.Errorf("expected 2 order items, got %d", len(decoded.Order))
	}
	if decoded.Order[0] != "managedNodeGroups" {
		t.Errorf("expected first order 'managedNodeGroups', got %s", decoded.Order[0])
	}
}
