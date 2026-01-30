/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/go-logr/logr"

	streamingv1alpha1 "github.com/ardikabs/hibernator/api/streaming/v1alpha1"
)

func TestNewExecutionServiceServer(t *testing.T) {
	log := logr.Discard()
	server := NewExecutionServiceServer(log, nil, nil)

	if server == nil {
		t.Fatal("expected non-nil server")
	}
	if server.executionLogs == nil {
		t.Error("expected executionLogs to be initialized")
	}
	if server.executionStatus == nil {
		t.Error("expected executionStatus to be initialized")
	}
}

func TestReportProgress(t *testing.T) {
	log := logr.Discard()
	server := NewExecutionServiceServer(log, nil, nil)

	tests := []struct {
		name   string
		report *streamingv1alpha1.ProgressReport
	}{
		{
			name: "new execution",
			report: &streamingv1alpha1.ProgressReport{
				ExecutionID:     "exec-001",
				Phase:           "Starting",
				ProgressPercent: 10,
				Message:         "Initializing",
			},
		},
		{
			name: "progress update",
			report: &streamingv1alpha1.ProgressReport{
				ExecutionID:     "exec-001",
				Phase:           "Running",
				ProgressPercent: 50,
				Message:         "Processing targets",
			},
		},
		{
			name: "near completion",
			report: &streamingv1alpha1.ProgressReport{
				ExecutionID:     "exec-001",
				Phase:           "Finishing",
				ProgressPercent: 90,
				Message:         "Finalizing",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := server.ReportProgress(context.Background(), tt.report)
			if err != nil {
				t.Fatalf("ReportProgress() error = %v", err)
			}
			if !resp.Acknowledged {
				t.Error("expected acknowledged response")
			}

			// Verify state was stored
			state := server.GetExecutionState(tt.report.ExecutionID)
			if state == nil {
				t.Fatal("expected state to be stored")
			}
			if state.Phase != tt.report.Phase {
				t.Errorf("phase = %s, want %s", state.Phase, tt.report.Phase)
			}
			if state.ProgressPercent != tt.report.ProgressPercent {
				t.Errorf("progress = %d, want %d", state.ProgressPercent, tt.report.ProgressPercent)
			}
			if state.Message != tt.report.Message {
				t.Errorf("message = %s, want %s", state.Message, tt.report.Message)
			}
		})
	}
}

func TestReportCompletion(t *testing.T) {
	log := logr.Discard()
	server := NewExecutionServiceServer(log, nil, nil)

	tests := []struct {
		name    string
		report  *streamingv1alpha1.CompletionReport
		wantErr bool
	}{
		{
			name: "successful completion",
			report: &streamingv1alpha1.CompletionReport{
				ExecutionID:  "exec-success",
				Success:      true,
				DurationMs:   5000,
				ErrorMessage: "",
			},
			wantErr: false,
		},
		{
			name: "failed completion",
			report: &streamingv1alpha1.CompletionReport{
				ExecutionID:  "exec-failed",
				Success:      false,
				DurationMs:   3000,
				ErrorMessage: "connection timeout",
			},
			wantErr: false,
		},
		{
			name: "completion with restore data",
			report: &streamingv1alpha1.CompletionReport{
				ExecutionID: "exec-restore",
				Success:     true,
				DurationMs:  10000,
				RestoreData: json.RawMessage(`{"instanceId": "i-12345", "state": "stopped"}`),
			},
			wantErr: false,
		},
		{
			name: "completion with invalid restore data",
			report: &streamingv1alpha1.CompletionReport{
				ExecutionID: "exec-invalid-restore",
				Success:     true,
				DurationMs:  1000,
				RestoreData: json.RawMessage(`{invalid json}`),
			},
			wantErr: false, // Should log error but not fail
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := server.ReportCompletion(context.Background(), tt.report)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ReportCompletion() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if !resp.Acknowledged {
					t.Error("expected acknowledged response")
				}

				state := server.GetExecutionState(tt.report.ExecutionID)
				if state == nil {
					t.Fatal("expected state to be stored")
				}
				if !state.Completed {
					t.Error("expected completed to be true")
				}
				if state.Success != tt.report.Success {
					t.Errorf("success = %v, want %v", state.Success, tt.report.Success)
				}
				if state.Error != tt.report.ErrorMessage {
					t.Errorf("error = %s, want %s", state.Error, tt.report.ErrorMessage)
				}
			}
		})
	}
}

func TestHeartbeat(t *testing.T) {
	log := logr.Discard()
	server := NewExecutionServiceServer(log, nil, nil)

	// First, create an execution state
	_, _ = server.ReportProgress(context.Background(), &streamingv1alpha1.ProgressReport{
		ExecutionID:     "exec-hb",
		Phase:           "Running",
		ProgressPercent: 25,
	})

	// Record initial heartbeat time
	state := server.GetExecutionState("exec-hb")
	initialHeartbeat := state.LastHeartbeat

	// Wait a bit and send heartbeat
	time.Sleep(10 * time.Millisecond)

	req := &streamingv1alpha1.HeartbeatRequest{
		ExecutionID: "exec-hb",
	}

	resp, err := server.Heartbeat(context.Background(), req)
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	if !resp.Acknowledged {
		t.Error("expected acknowledged response")
	}
	if resp.ServerTime.IsZero() {
		t.Error("expected non-zero server time")
	}

	// Verify heartbeat was updated
	state = server.GetExecutionState("exec-hb")
	if !state.LastHeartbeat.After(initialHeartbeat) {
		t.Error("expected heartbeat time to be updated")
	}
}

func TestHeartbeat_NonExistentExecution(t *testing.T) {
	log := logr.Discard()
	server := NewExecutionServiceServer(log, nil, nil)

	req := &streamingv1alpha1.HeartbeatRequest{
		ExecutionID: "non-existent",
	}

	resp, err := server.Heartbeat(context.Background(), req)
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	if !resp.Acknowledged {
		t.Error("expected acknowledged response even for non-existent execution")
	}
}

func TestGetExecutionLogs(t *testing.T) {
	log := logr.Discard()
	server := NewExecutionServiceServer(log, nil, nil)

	// Add some logs directly
	server.executionLogsMu.Lock()
	server.executionLogs["exec-logs"] = []streamingv1alpha1.LogEntry{
		{ExecutionID: "exec-logs", Level: "INFO", Message: "Starting operation"},
		{ExecutionID: "exec-logs", Level: "DEBUG", Message: "Processing item 1"},
		{ExecutionID: "exec-logs", Level: "INFO", Message: "Operation complete"},
	}
	server.executionLogsMu.Unlock()

	logs := server.GetExecutionLogs("exec-logs")
	if len(logs) != 3 {
		t.Errorf("expected 3 logs, got %d", len(logs))
	}
	if logs[0].Message != "Starting operation" {
		t.Errorf("unexpected first log message: %s", logs[0].Message)
	}
}

func TestGetExecutionLogs_NonExistent(t *testing.T) {
	log := logr.Discard()
	server := NewExecutionServiceServer(log, nil, nil)

	logs := server.GetExecutionLogs("non-existent")
	if logs != nil {
		t.Errorf("expected nil for non-existent execution, got %v", logs)
	}
}

func TestGetExecutionState_NonExistent(t *testing.T) {
	log := logr.Discard()
	server := NewExecutionServiceServer(log, nil, nil)

	state := server.GetExecutionState("non-existent")
	if state != nil {
		t.Errorf("expected nil for non-existent execution, got %v", state)
	}
}

func TestGetExecutionLogs_ReturnsCopy(t *testing.T) {
	log := logr.Discard()
	server := NewExecutionServiceServer(log, nil, nil)

	// Add logs
	server.executionLogsMu.Lock()
	server.executionLogs["exec-copy"] = []streamingv1alpha1.LogEntry{
		{ExecutionID: "exec-copy", Message: "Original"},
	}
	server.executionLogsMu.Unlock()

	// Get logs and modify
	logs := server.GetExecutionLogs("exec-copy")
	logs[0].Message = "Modified"

	// Verify original is unchanged
	original := server.GetExecutionLogs("exec-copy")
	if original[0].Message != "Original" {
		t.Error("modifying returned slice should not affect original")
	}
}

func TestGetExecutionState_ReturnsCopy(t *testing.T) {
	log := logr.Discard()
	server := NewExecutionServiceServer(log, nil, nil)

	// Create state
	_, _ = server.ReportProgress(context.Background(), &streamingv1alpha1.ProgressReport{
		ExecutionID: "exec-state-copy",
		Phase:       "Running",
	})

	// Get state and modify
	state := server.GetExecutionState("exec-state-copy")
	state.Phase = "Modified"

	// Verify original is unchanged
	original := server.GetExecutionState("exec-state-copy")
	if original.Phase == "Modified" {
		t.Error("modifying returned state should not affect original")
	}
}

func TestConcurrentAccess(t *testing.T) {
	log := logr.Discard()
	server := NewExecutionServiceServer(log, nil, nil)

	done := make(chan bool)

	// Start multiple goroutines accessing the server
	for i := 0; i < 10; i++ {
		go func(id int) {
			execID := "exec-concurrent"
			for j := 0; j < 100; j++ {
				// Progress updates
				_, _ = server.ReportProgress(context.Background(), &streamingv1alpha1.ProgressReport{
					ExecutionID:     execID,
					ProgressPercent: int32((id*100 + j) % 100),
				})

				// Heartbeats
				_, _ = server.Heartbeat(context.Background(), &streamingv1alpha1.HeartbeatRequest{
					ExecutionID: execID,
				})

				// Read state
				_ = server.GetExecutionState(execID)
				_ = server.GetExecutionLogs(execID)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestMultipleExecutions(t *testing.T) {
	log := logr.Discard()
	server := NewExecutionServiceServer(log, nil, nil)

	execIDs := []string{"exec-1", "exec-2", "exec-3"}

	// Create progress for each execution
	for i, id := range execIDs {
		_, err := server.ReportProgress(context.Background(), &streamingv1alpha1.ProgressReport{
			ExecutionID:     id,
			Phase:           "Running",
			ProgressPercent: int32((i + 1) * 25),
		})
		if err != nil {
			t.Fatalf("ReportProgress() error = %v", err)
		}
	}

	// Verify each execution has its own state
	for i, id := range execIDs {
		state := server.GetExecutionState(id)
		if state == nil {
			t.Fatalf("expected state for %s", id)
		}
		expectedProgress := int32((i + 1) * 25)
		if state.ProgressPercent != expectedProgress {
			t.Errorf("execution %s: progress = %d, want %d", id, state.ProgressPercent, expectedProgress)
		}
	}
}
