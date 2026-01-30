/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package ec2

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
	if e.Type() != "ec2" {
		t.Errorf("expected type 'ec2', got %s", e.Type())
	}
}

func TestValidate_MissingAWSConfig(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-instances",
		TargetType: "ec2",
		Parameters: json.RawMessage(`{"selector": {"tags": {"Name": "test"}}}`),
	}
	err := e.Validate(spec)
	if err == nil {
		t.Error("expected error for missing AWS config")
	}
}

func TestValidate_MissingSelector(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-instances",
		TargetType: "ec2",
		Parameters: json.RawMessage(`{"selector": {}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}
	err := e.Validate(spec)
	if err == nil {
		t.Error("expected error for missing selector")
	}
}

func TestValidate_WithTags(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-instances",
		TargetType: "ec2",
		Parameters: json.RawMessage(`{"selector": {"tags": {"Environment": "dev"}}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}
	err := e.Validate(spec)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_WithInstanceIDs(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-instances",
		TargetType: "ec2",
		Parameters: json.RawMessage(`{"selector": {"instanceIds": ["i-123", "i-456"]}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
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
			name:    "with tags selector",
			json:    `{"selector": {"tags": {"Name": "test", "Env": "dev"}}}`,
			wantErr: false,
			check:   func(p Parameters) bool { return len(p.Selector.Tags) == 2 },
		},
		{
			name:    "with instance IDs",
			json:    `{"selector": {"instanceIds": ["i-1", "i-2", "i-3"]}}`,
			wantErr: false,
			check:   func(p Parameters) bool { return len(p.Selector.InstanceIDs) == 3 },
		},
		{
			name:    "mixed selector",
			json:    `{"selector": {"tags": {"Name": "test"}, "instanceIds": ["i-1"]}}`,
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
		Instances: []InstanceState{
			{InstanceID: "i-123", WasRunning: true},
			{InstanceID: "i-456", WasRunning: false},
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

	if len(decoded.Instances) != 2 {
		t.Errorf("expected 2 instances, got %d", len(decoded.Instances))
	}
	if decoded.Instances[0].InstanceID != "i-123" {
		t.Errorf("expected instance ID 'i-123', got %s", decoded.Instances[0].InstanceID)
	}
	if !decoded.Instances[0].WasRunning {
		t.Error("expected WasRunning to be true for first instance")
	}
	if decoded.Instances[1].WasRunning {
		t.Error("expected WasRunning to be false for second instance")
	}
}

func TestInstanceState(t *testing.T) {
	state := InstanceState{
		InstanceID: "i-abc123",
		WasRunning: true,
	}
	if state.InstanceID != "i-abc123" {
		t.Errorf("expected 'i-abc123', got %s", state.InstanceID)
	}
	if !state.WasRunning {
		t.Error("expected WasRunning to be true")
	}
}

func TestSelector(t *testing.T) {
	selector := Selector{
		Tags: map[string]string{
			"Environment": "production",
			"Team":        "platform",
		},
		InstanceIDs: []string{"i-1", "i-2"},
	}

	if len(selector.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(selector.Tags))
	}
	if selector.Tags["Environment"] != "production" {
		t.Error("expected Environment tag to be 'production'")
	}
	if len(selector.InstanceIDs) != 2 {
		t.Errorf("expected 2 instance IDs, got %d", len(selector.InstanceIDs))
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
			name:    "with tags",
			raw:     json.RawMessage(`{"selector": {"tags": {"Name": "test"}}}`),
			wantErr: false,
			check:   func(p Parameters) bool { return len(p.Selector.Tags) == 1 },
		},
		{
			name:    "with instance IDs",
			raw:     json.RawMessage(`{"selector": {"instanceIds": ["i-123"]}}`),
			wantErr: false,
			check:   func(p Parameters) bool { return len(p.Selector.InstanceIDs) == 1 },
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
		TargetName: "test-instances",
		TargetType: "ec2",
	}

	_, err := e.loadAWSConfig(ctx, spec)
	if err == nil {
		t.Error("expected error for nil AWS config")
	}
	if err.Error() != "AWS connector config is required" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExecutor_Shutdown_InvalidParams(t *testing.T) {
	e := New()
	ctx := context.Background()

	spec := executor.Spec{
		TargetName: "test-instances",
		TargetType: "ec2",
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
		TargetName: "test-instances",
		TargetType: "ec2",
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{
				Region: "us-east-1",
			},
		},
	}

	restore := executor.RestoreData{
		Type: "ec2",
		Data: json.RawMessage(`{invalid json}`),
	}

	err := e.WakeUp(ctx, spec, restore)
	if err == nil {
		t.Error("expected error for invalid restore data")
	}
}

func TestRestoreState_AllFields(t *testing.T) {
	state := RestoreState{
		Instances: []InstanceState{
			{InstanceID: "i-123", WasRunning: true},
			{InstanceID: "i-456", WasRunning: false},
			{InstanceID: "i-789", WasRunning: true},
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

	if len(decoded.Instances) != 3 {
		t.Errorf("expected 3 instances, got %d", len(decoded.Instances))
	}
}

func TestExecutorType_Constant(t *testing.T) {
	if ExecutorType != "ec2" {
		t.Errorf("ExecutorType = %s, want 'ec2'", ExecutorType)
	}
}

func TestParameters_Defaults(t *testing.T) {
	var params Parameters

	if params.Selector.Tags != nil {
		t.Error("default Tags should be nil")
	}
	if params.Selector.InstanceIDs != nil {
		t.Error("default InstanceIDs should be nil")
	}
}

func TestRestoreState_EmptyInstances(t *testing.T) {
	state := RestoreState{
		Instances: []InstanceState{},
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded RestoreState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(decoded.Instances) != 0 {
		t.Errorf("expected 0 instances, got %d", len(decoded.Instances))
	}
}

func TestSelector_Empty(t *testing.T) {
	selector := Selector{}

	if selector.Tags != nil {
		t.Error("expected nil Tags")
	}
	if selector.InstanceIDs != nil {
		t.Error("expected nil InstanceIDs")
	}
}

func TestValidate_InvalidParams(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-instances",
		TargetType: "ec2",
		Parameters: json.RawMessage(`{invalid json}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}
	err := e.Validate(spec)
	if err == nil {
		t.Error("expected error for invalid params")
	}
}
