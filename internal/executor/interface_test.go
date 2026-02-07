/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package executor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/go-logr/logr"
)

// MockExecutor is a mock implementation for testing.
type MockExecutor struct {
	TypeValue    string
	ValidateErr  error
	ShutdownData RestoreData
	ShutdownErr  error
	WakeUpErr    error
}

func (m *MockExecutor) Type() string { return m.TypeValue }

func (m *MockExecutor) Validate(spec Spec) error { return m.ValidateErr }

func (m *MockExecutor) Shutdown(ctx context.Context, log logr.Logger, spec Spec) error {
	_ = log
	return m.ShutdownErr
}

func (m *MockExecutor) WakeUp(ctx context.Context, log logr.Logger, spec Spec, restore RestoreData) error {
	_ = log
	return m.WakeUpErr
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&MockExecutor{TypeValue: "test"})

	exec, ok := registry.Get("test")
	if !ok {
		t.Fatal("expected executor to be found")
	}
	if exec.Type() != "test" {
		t.Errorf("expected type 'test', got %s", exec.Type())
	}
}

func TestRegistry_GetNotFound(t *testing.T) {
	registry := NewRegistry()
	_, ok := registry.Get("nonexistent")
	if ok {
		t.Error("expected executor not to be found")
	}
}

func TestRegistry_List(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&MockExecutor{TypeValue: "eks"})
	registry.Register(&MockExecutor{TypeValue: "rds"})

	types := registry.List()
	if len(types) != 2 {
		t.Errorf("expected 2 types, got %d", len(types))
	}
}

func TestRestoreData_Marshal(t *testing.T) {
	restore := RestoreData{
		Type: "eks",
		Data: map[string]json.RawMessage{"state": json.RawMessage(`{"key": "value"}`)},
	}
	data, err := json.Marshal(restore)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty data")
	}
}

func TestSpec_Fields(t *testing.T) {
	spec := Spec{
		TargetName: "test",
		TargetType: "eks",
		Parameters: json.RawMessage(`{}`),
		ConnectorConfig: ConnectorConfig{
			AWS: &AWSConnectorConfig{Region: "us-east-1"},
		},
	}
	if spec.TargetName != "test" {
		t.Error("expected target name 'test'")
	}
	if spec.ConnectorConfig.AWS == nil {
		t.Error("expected AWS config")
	}
}

func TestK8SConnectorConfig(t *testing.T) {
	cfg := K8SConnectorConfig{
		ClusterName: "cluster",
		Region:      "us-west-2",
	}
	if cfg.ClusterName != "cluster" {
		t.Errorf("expected 'cluster', got %s", cfg.ClusterName)
	}
}
