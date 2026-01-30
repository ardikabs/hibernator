/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package rds

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
	if e.Type() != "rds" {
		t.Errorf("expected type 'rds', got %s", e.Type())
	}
}

func TestValidate_MissingAWSConfig(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		Parameters: json.RawMessage(`{}`),
	}
	err := e.Validate(spec)
	if err == nil {
		t.Error("expected error for missing AWS config")
	}
}

func TestValidate_WithAWSConfig(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		Parameters: json.RawMessage(`{}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{
				Region: "us-east-1",
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
		check   func(Parameters) bool
	}{
		{
			name:    "empty parameters",
			json:    `{}`,
			wantErr: false,
		},
		{
			name:    "with snapshot before stop",
			json:    `{"snapshotBeforeStop": true}`,
			wantErr: false,
			check:   func(p Parameters) bool { return p.SnapshotBeforeStop },
		},
		{
			name:    "with instance ID",
			json:    `{"instanceId": "db-instance-1"}`,
			wantErr: false,
			check:   func(p Parameters) bool { return p.InstanceID == "db-instance-1" },
		},
		{
			name:    "with cluster ID",
			json:    `{"clusterId": "aurora-cluster-1"}`,
			wantErr: false,
			check:   func(p Parameters) bool { return p.ClusterID == "aurora-cluster-1" },
		},
		{
			name:    "full config",
			json:    `{"instanceId": "db-1", "snapshotBeforeStop": true}`,
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
			if tt.check != nil && !tt.check(params) {
				t.Error("check failed")
			}
		})
	}
}

func TestRestoreState_Marshal(t *testing.T) {
	state := RestoreState{
		InstanceID:   "db-instance-1",
		SnapshotID:   "snap-123",
		WasStopped:   false,
		InstanceType: "db.t3.medium",
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded RestoreState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.InstanceID != "db-instance-1" {
		t.Errorf("expected instance ID 'db-instance-1', got %s", decoded.InstanceID)
	}
	if decoded.SnapshotID != "snap-123" {
		t.Errorf("expected snapshot ID 'snap-123', got %s", decoded.SnapshotID)
	}
	if decoded.WasStopped {
		t.Error("expected WasStopped to be false")
	}
	if decoded.InstanceType != "db.t3.medium" {
		t.Errorf("expected instance type 'db.t3.medium', got %s", decoded.InstanceType)
	}
}

func TestRestoreState_Cluster(t *testing.T) {
	state := RestoreState{
		ClusterID:  "aurora-cluster-1",
		WasStopped: false,
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded RestoreState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.ClusterID != "aurora-cluster-1" {
		t.Errorf("expected cluster ID 'aurora-cluster-1', got %s", decoded.ClusterID)
	}
}

func TestParameters_InstanceAndCluster(t *testing.T) {
	params := Parameters{
		InstanceID:         "db-1",
		ClusterID:          "cluster-1",
		SnapshotBeforeStop: true,
	}

	if params.InstanceID != "db-1" {
		t.Errorf("expected instance ID 'db-1', got %s", params.InstanceID)
	}
	if params.ClusterID != "cluster-1" {
		t.Errorf("expected cluster ID 'cluster-1', got %s", params.ClusterID)
	}
	if !params.SnapshotBeforeStop {
		t.Error("expected SnapshotBeforeStop to be true")
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
			name:    "empty json",
			raw:     json.RawMessage(`{}`),
			wantErr: false,
		},
		{
			name:    "with instance ID",
			raw:     json.RawMessage(`{"instanceId": "db-instance-1"}`),
			wantErr: false,
			check:   func(p Parameters) bool { return p.InstanceID == "db-instance-1" },
		},
		{
			name:    "with cluster ID",
			raw:     json.RawMessage(`{"clusterId": "aurora-cluster-1"}`),
			wantErr: false,
			check:   func(p Parameters) bool { return p.ClusterID == "aurora-cluster-1" },
		},
		{
			name:    "with snapshot option",
			raw:     json.RawMessage(`{"snapshotBeforeStop": true}`),
			wantErr: false,
			check:   func(p Parameters) bool { return p.SnapshotBeforeStop },
		},
		{
			name:    "full parameters",
			raw:     json.RawMessage(`{"instanceId": "db-1", "snapshotBeforeStop": true}`),
			wantErr: false,
			check: func(p Parameters) bool {
				return p.InstanceID == "db-1" && p.SnapshotBeforeStop
			},
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

func TestExecutor_LoadAWSConfig_NilConfig(t *testing.T) {
	e := New()
	ctx := context.Background()

	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
	}

	_, err := e.loadAWSConfig(ctx, spec)
	if err == nil {
		t.Error("expected error for nil AWS config")
	}
	if err.Error() != "AWS connector config is required" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExecutor_Shutdown_MissingTarget(t *testing.T) {
	e := New()
	ctx := context.Background()

	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		Parameters: json.RawMessage(`{}`), // No instanceId or clusterId
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{
				Region: "us-east-1",
			},
		},
	}

	// This will fail because neither instanceId nor clusterId is specified
	_, err := e.Shutdown(ctx, spec)
	if err == nil {
		t.Error("expected error for missing target")
	}
}

func TestExecutor_Shutdown_InvalidParams(t *testing.T) {
	e := New()
	ctx := context.Background()

	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		Parameters: json.RawMessage(`{invalid json}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{
				Region: "us-east-1",
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
		TargetName: "test-db",
		TargetType: "rds",
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{
				Region: "us-east-1",
			},
		},
	}

	restore := executor.RestoreData{
		Type: "rds",
		Data: json.RawMessage(`{invalid json}`),
	}

	err := e.WakeUp(ctx, spec, restore)
	if err == nil {
		t.Error("expected error for invalid restore data")
	}
}

func TestRestoreState_AllFields(t *testing.T) {
	state := RestoreState{
		InstanceID:   "db-instance",
		ClusterID:    "db-cluster",
		SnapshotID:   "snap-123",
		WasStopped:   true,
		InstanceType: "db.r5.large",
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded RestoreState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.InstanceID != state.InstanceID {
		t.Errorf("InstanceID mismatch: got %s, want %s", decoded.InstanceID, state.InstanceID)
	}
	if decoded.ClusterID != state.ClusterID {
		t.Errorf("ClusterID mismatch: got %s, want %s", decoded.ClusterID, state.ClusterID)
	}
	if decoded.SnapshotID != state.SnapshotID {
		t.Errorf("SnapshotID mismatch: got %s, want %s", decoded.SnapshotID, state.SnapshotID)
	}
	if decoded.WasStopped != state.WasStopped {
		t.Errorf("WasStopped mismatch: got %v, want %v", decoded.WasStopped, state.WasStopped)
	}
	if decoded.InstanceType != state.InstanceType {
		t.Errorf("InstanceType mismatch: got %s, want %s", decoded.InstanceType, state.InstanceType)
	}
}

func TestExecutorType_Constant(t *testing.T) {
	if ExecutorType != "rds" {
		t.Errorf("ExecutorType = %s, want 'rds'", ExecutorType)
	}
}

func TestParameters_Defaults(t *testing.T) {
	var params Parameters

	if params.SnapshotBeforeStop {
		t.Error("default SnapshotBeforeStop should be false")
	}
	if params.InstanceID != "" {
		t.Error("default InstanceID should be empty")
	}
	if params.ClusterID != "" {
		t.Error("default ClusterID should be empty")
	}
}
