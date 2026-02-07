/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-logr/logr"

	"github.com/ardikabs/hibernator/internal/streaming/types"

	streamingv1alpha1 "github.com/ardikabs/hibernator/api/streaming/v1alpha1"
)

// testWebhookServer creates a webhook server with mocked dependencies for testing
func testWebhookServer(t *testing.T) *WebhookServer {
	log := logr.Discard()
	execService := NewExecutionServiceServer(nil, nil, clk)

	return &WebhookServer{
		execService: execService,
		validator:   nil, // Will bypass auth for these tests
		log:         log,
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
				ExecutionId: "exec-log-1",
				Level:       "INFO",
				Message:     "Test info message",
				Timestamp:   time.Now().Format(time.RFC3339),
			},
		},
		{
			name: "error level",
			entry: &streamingv1alpha1.LogEntry{
				ExecutionId: "exec-log-2",
				Level:       "ERROR",
				Message:     "Test error message",
				Timestamp:   time.Now().Format(time.RFC3339),
				Fields:      map[string]string{"error": "test"},
			},
		},
		{
			name: "warn level",
			entry: &streamingv1alpha1.LogEntry{
				ExecutionId: "exec-log-3",
				Level:       "WARN",
				Message:     "Test warning message",
				Timestamp:   time.Now().Format(time.RFC3339),
			},
		},
		{
			name: "debug level",
			entry: &streamingv1alpha1.LogEntry{
				ExecutionId: "exec-log-4",
				Level:       "DEBUG",
				Message:     "Test debug message",
				Timestamp:   time.Now().Format(time.RFC3339),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// processLog should not panic; logs are emitted to server log, not stored in-memory
			ws.processLog(context.Background(), tt.entry)
		})
	}
}

func TestProcessLog_MultipleEntries(t *testing.T) {
	ws := testWebhookServer(t)
	execID := "exec-multi-log"

	// processLog should not panic when called multiple times; logs are emitted to server log
	for i := 0; i < 5; i++ {
		ws.processLog(context.Background(), &streamingv1alpha1.LogEntry{
			ExecutionId: execID,
			Level:       "INFO",
			Message:     "Log entry",
			Timestamp:   time.Now().Format(time.RFC3339),
		})
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
	payload := types.WebhookPayload{
		Type: "log",
		Log: &types.LogEntry{
			ExecutionID: "exec-123",
			Level:       "INFO",
			Message:     "Test message",
			Timestamp:   time.Now(),
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded types.WebhookPayload
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
	payload := types.WebhookPayload{
		Type: "progress",
		Progress: &types.ProgressReport{
			ExecutionID:     "exec-456",
			Phase:           "Running",
			ProgressPercent: 50,
			Message:         "Processing",
			Timestamp:       time.Now(),
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded types.WebhookPayload
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
	payload := types.WebhookPayload{
		Type: "completion",
		Completion: &types.CompletionReport{
			ExecutionID: "exec-789",
			Success:     true,
			DurationMs:  5000,
			Timestamp:   time.Now(),
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded types.WebhookPayload
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
	payload := types.WebhookPayload{
		Type: "heartbeat",
		Heartbeat: &types.HeartbeatRequest{
			ExecutionID: "exec-hb",
			Timestamp:   time.Now(),
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded types.WebhookPayload
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
	resp := types.WebhookResponse{
		Acknowledged: true,
		Error:        "",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded types.WebhookResponse
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
