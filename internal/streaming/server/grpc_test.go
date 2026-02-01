/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package server

import (
	"testing"
)

func TestExecutionState_Fields(t *testing.T) {
	state := ExecutionState{
		ExecutionID:     "exec-123",
		Phase:           "Running",
		ProgressPercent: 50,
		Message:         "Processing",
		Completed:       false,
		Success:         false,
	}
	if state.ExecutionID != "exec-123" {
		t.Errorf("expected 'exec-123', got %s", state.ExecutionID)
	}
	if state.Phase != "Running" {
		t.Errorf("expected 'Running', got %s", state.Phase)
	}
	if state.ProgressPercent != 50 {
		t.Errorf("expected 50, got %d", state.ProgressPercent)
	}
}

func TestExecutionState_Completed(t *testing.T) {
	state := ExecutionState{
		ExecutionID: "exec-456",
		Completed:   true,
		Success:     true,
	}
	if !state.Completed {
		t.Error("expected completed")
	}
	if !state.Success {
		t.Error("expected success")
	}
}

func TestExecutionState_Failed(t *testing.T) {
	state := ExecutionState{
		ExecutionID: "exec-789",
		Completed:   true,
		Success:     false,
		Error:       "connection timeout",
	}
	if state.Success {
		t.Error("expected failure")
	}
	if state.Error != "connection timeout" {
		t.Errorf("expected error message")
	}
}
