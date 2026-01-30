/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-logr/logr"

	streamingv1alpha1 "github.com/ardikabs/hibernator/api/streaming/v1alpha1"
)

// testWebhookServer creates a webhook server with mocked dependencies for testing
func testWebhookServer(t *testing.T) *WebhookServer {
	log := logr.Discard()
	execService := NewExecutionServiceServer(log, nil, nil)

	return &WebhookServer{
		executionService: execService,
		validator:        nil, // Will bypass auth for these tests
		log:              log,
	}
}

func TestHandleHealthz(t *testing.T) {
	ws := testWebhookServer(t)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	ws.handleHealthz(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != "ok" {
		t.Errorf("body = %s, want 'ok'", w.Body.String())
	}
}

func TestHandleLogs_MethodNotAllowed(t *testing.T) {
	ws := testWebhookServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1alpha1/logs", nil)
	w := httptest.NewRecorder()

	ws.handleLogs(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleProgress_MethodNotAllowed(t *testing.T) {
	ws := testWebhookServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1alpha1/progress", nil)
	w := httptest.NewRecorder()

	ws.handleProgress(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleCompletion_MethodNotAllowed(t *testing.T) {
	ws := testWebhookServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1alpha1/completion", nil)
	w := httptest.NewRecorder()

	ws.handleCompletion(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleHeartbeat_MethodNotAllowed(t *testing.T) {
	ws := testWebhookServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1alpha1/heartbeat", nil)
	w := httptest.NewRecorder()

	ws.handleHeartbeat(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleCallback_MethodNotAllowed(t *testing.T) {
	ws := testWebhookServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1alpha1/callback", nil)
	w := httptest.NewRecorder()

	ws.handleCallback(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestProcessLog(t *testing.T) {
	ws := testWebhookServer(t)

	tests := []struct {
		name  string
		entry *streamingv1alpha1.LogEntry
	}{
		{
			name: "info level",
			entry: &streamingv1alpha1.LogEntry{
				ExecutionID: "exec-log-1",
				Level:       "INFO",
				Message:     "Test info message",
				Timestamp:   time.Now(),
			},
		},
		{
			name: "error level",
			entry: &streamingv1alpha1.LogEntry{
				ExecutionID: "exec-log-2",
				Level:       "ERROR",
				Message:     "Test error message",
				Timestamp:   time.Now(),
				Fields:      map[string]string{"error": "test"},
			},
		},
		{
			name: "warn level",
			entry: &streamingv1alpha1.LogEntry{
				ExecutionID: "exec-log-3",
				Level:       "WARN",
				Message:     "Test warning message",
				Timestamp:   time.Now(),
			},
		},
		{
			name: "debug level",
			entry: &streamingv1alpha1.LogEntry{
				ExecutionID: "exec-log-4",
				Level:       "DEBUG",
				Message:     "Test debug message",
				Timestamp:   time.Now(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ws.processLog(tt.entry)

			// Verify log was stored
			logs := ws.executionService.GetExecutionLogs(tt.entry.ExecutionID)
			if len(logs) != 1 {
				t.Errorf("expected 1 log, got %d", len(logs))
			}
			if logs[0].Message != tt.entry.Message {
				t.Errorf("message = %s, want %s", logs[0].Message, tt.entry.Message)
			}
			if logs[0].Level != tt.entry.Level {
				t.Errorf("level = %s, want %s", logs[0].Level, tt.entry.Level)
			}
		})
	}
}

func TestProcessLog_MultipleEntries(t *testing.T) {
	ws := testWebhookServer(t)
	execID := "exec-multi-log"

	for i := 0; i < 5; i++ {
		ws.processLog(&streamingv1alpha1.LogEntry{
			ExecutionID: execID,
			Level:       "INFO",
			Message:     "Log entry",
			Timestamp:   time.Now(),
		})
	}

	logs := ws.executionService.GetExecutionLogs(execID)
	if len(logs) != 5 {
		t.Errorf("expected 5 logs, got %d", len(logs))
	}
}

func TestValidateRequest_MissingAuth(t *testing.T) {
	ws := testWebhookServer(t)

	req := httptest.NewRequest(http.MethodPost, "/test", nil)

	_, err := ws.validateRequest(req)
	if err == nil {
		t.Error("expected error for missing authorization")
	}
}

func TestValidateRequest_InvalidFormat(t *testing.T) {
	ws := testWebhookServer(t)

	tests := []struct {
		name   string
		header string
	}{
		{
			name:   "no bearer prefix",
			header: "just-a-token",
		},
		{
			name:   "wrong auth type",
			header: "Basic dXNlcjpwYXNz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/test", nil)
			req.Header.Set("Authorization", tt.header)

			_, err := ws.validateRequest(req)
			if err == nil {
				t.Error("expected error for invalid authorization format")
			}
		})
	}
}

func TestWebhookPayload_UnmarshalLog(t *testing.T) {
	payload := streamingv1alpha1.WebhookPayload{
		Type: "log",
		Log: &streamingv1alpha1.LogEntry{
			ExecutionID: "exec-123",
			Level:       "INFO",
			Message:     "Test message",
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded streamingv1alpha1.WebhookPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Type != "log" {
		t.Errorf("type = %s, want 'log'", decoded.Type)
	}
	if decoded.Log == nil {
		t.Fatal("expected Log to be non-nil")
	}
	if decoded.Log.ExecutionID != "exec-123" {
		t.Errorf("ExecutionID = %s, want 'exec-123'", decoded.Log.ExecutionID)
	}
}

func TestWebhookPayload_UnmarshalProgress(t *testing.T) {
	payload := streamingv1alpha1.WebhookPayload{
		Type: "progress",
		Progress: &streamingv1alpha1.ProgressReport{
			ExecutionID:     "exec-456",
			Phase:           "Running",
			ProgressPercent: 50,
			Message:         "Processing",
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded streamingv1alpha1.WebhookPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Type != "progress" {
		t.Errorf("type = %s, want 'progress'", decoded.Type)
	}
	if decoded.Progress.ProgressPercent != 50 {
		t.Errorf("ProgressPercent = %d, want 50", decoded.Progress.ProgressPercent)
	}
}

func TestWebhookPayload_UnmarshalCompletion(t *testing.T) {
	payload := streamingv1alpha1.WebhookPayload{
		Type: "completion",
		Completion: &streamingv1alpha1.CompletionReport{
			ExecutionID: "exec-789",
			Success:     true,
			DurationMs:  5000,
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded streamingv1alpha1.WebhookPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Type != "completion" {
		t.Errorf("type = %s, want 'completion'", decoded.Type)
	}
	if decoded.Completion == nil {
		t.Fatal("expected Completion to be non-nil")
	}
	if !decoded.Completion.Success {
		t.Error("expected Success to be true")
	}
}

func TestWebhookPayload_UnmarshalHeartbeat(t *testing.T) {
	payload := streamingv1alpha1.WebhookPayload{
		Type: "heartbeat",
		Heartbeat: &streamingv1alpha1.HeartbeatRequest{
			ExecutionID: "exec-hb",
			Timestamp:   time.Now(),
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded streamingv1alpha1.WebhookPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Type != "heartbeat" {
		t.Errorf("type = %s, want 'heartbeat'", decoded.Type)
	}
	if decoded.Heartbeat.ExecutionID != "exec-hb" {
		t.Errorf("ExecutionID = %s, want 'exec-hb'", decoded.Heartbeat.ExecutionID)
	}
}

func TestWebhookResponse(t *testing.T) {
	resp := streamingv1alpha1.WebhookResponse{
		Acknowledged: true,
		Error:        "",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded streamingv1alpha1.WebhookResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if !decoded.Acknowledged {
		t.Error("expected Acknowledged to be true")
	}
	if decoded.Error != "" {
		t.Errorf("Error = %s, want empty", decoded.Error)
	}
}

func TestInvalidPayloadBytes(t *testing.T) {
	// Test parsing various invalid payloads
	invalidPayloads := [][]byte{
		[]byte(""),
		[]byte("invalid json"),
		[]byte("{incomplete"),
	}

	for _, payload := range invalidPayloads {
		var data map[string]interface{}
		err := json.Unmarshal(payload, &data)
		// These should all fail
		if len(payload) > 0 && string(payload) != "" && err == nil {
			t.Errorf("expected error for invalid payload: %s", string(payload))
		}
	}
}

func TestExecutionState_Lifecycle(t *testing.T) {
	log := logr.Discard()
	server := NewExecutionServiceServer(log, nil, nil)

	execID := "exec-lifecycle"

	// Start execution
	_, _ = server.ReportProgress(nil, &streamingv1alpha1.ProgressReport{
		ExecutionID:     execID,
		Phase:           "Starting",
		ProgressPercent: 0,
	})

	state := server.GetExecutionState(execID)
	if state.Completed {
		t.Error("should not be completed yet")
	}

	// Update progress
	_, _ = server.ReportProgress(nil, &streamingv1alpha1.ProgressReport{
		ExecutionID:     execID,
		Phase:           "Running",
		ProgressPercent: 50,
	})

	state = server.GetExecutionState(execID)
	if state.ProgressPercent != 50 {
		t.Errorf("progress = %d, want 50", state.ProgressPercent)
	}

	// Complete execution
	_, _ = server.ReportCompletion(nil, &streamingv1alpha1.CompletionReport{
		ExecutionID: execID,
		Success:     true,
		DurationMs:  10000,
	})

	state = server.GetExecutionState(execID)
	if !state.Completed {
		t.Error("should be completed")
	}
	if !state.Success {
		t.Error("should be successful")
	}
}
