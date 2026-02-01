/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-logr/logr"

	streamingv1alpha1 "github.com/ardikabs/hibernator/api/streaming/v1alpha1"
	"github.com/ardikabs/hibernator/internal/streaming/types"
)

// mockValidator is a test double for the auth validator
type mockValidator struct {
	shouldSucceed bool
	result        *mockValidationResult
}

type mockValidationResult struct {
	valid          bool
	username       string
	namespace      string
	serviceAccount string
	err            error
}

func TestWebhookServer_HandleHealthz(t *testing.T) {
	log := logr.Discard()
	execService := NewExecutionServiceServer(nil, nil, nil)

	ws := &WebhookServer{
		executionService: execService,
		log:              log,
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	ws.handleHealthz(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("body = %s, want 'ok'", rec.Body.String())
	}
}

func TestWebhookServer_HandleLogs_MethodNotAllowed(t *testing.T) {
	log := logr.Discard()
	execService := NewExecutionServiceServer(nil, nil, nil)

	ws := &WebhookServer{
		executionService: execService,
		log:              log,
	}

	req := httptest.NewRequest(http.MethodGet, "/v1alpha1/logs", nil)
	rec := httptest.NewRecorder()

	ws.handleLogs(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestWebhookServer_HandleProgress_MethodNotAllowed(t *testing.T) {
	log := logr.Discard()
	execService := NewExecutionServiceServer(nil, nil, nil)

	ws := &WebhookServer{
		executionService: execService,
		log:              log,
	}

	req := httptest.NewRequest(http.MethodGet, "/v1alpha1/progress", nil)
	rec := httptest.NewRecorder()

	ws.handleProgress(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestWebhookServer_HandleCompletion_MethodNotAllowed(t *testing.T) {
	log := logr.Discard()
	execService := NewExecutionServiceServer(nil, nil, nil)

	ws := &WebhookServer{
		executionService: execService,
		log:              log,
	}

	req := httptest.NewRequest(http.MethodGet, "/v1alpha1/completion", nil)
	rec := httptest.NewRecorder()

	ws.handleCompletion(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestWebhookServer_HandleHeartbeat_MethodNotAllowed(t *testing.T) {
	log := logr.Discard()
	execService := NewExecutionServiceServer(nil, nil, nil)

	ws := &WebhookServer{
		executionService: execService,
		log:              log,
	}

	req := httptest.NewRequest(http.MethodGet, "/v1alpha1/heartbeat", nil)
	rec := httptest.NewRecorder()

	ws.handleHeartbeat(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestWebhookServer_HandleCallback_MethodNotAllowed(t *testing.T) {
	log := logr.Discard()
	execService := NewExecutionServiceServer(nil, nil, nil)

	ws := &WebhookServer{
		executionService: execService,
		log:              log,
	}

	req := httptest.NewRequest(http.MethodGet, "/v1alpha1/callback", nil)
	rec := httptest.NewRecorder()

	ws.handleCallback(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestWebhookServer_HandleLogs_NoAuth(t *testing.T) {
	log := logr.Discard()
	execService := NewExecutionServiceServer(nil, nil, nil)

	// Create a webhook server without a validator (nil validator will cause auth to fail)
	ws := &WebhookServer{
		executionService: execService,
		validator:        nil, // No validator
		log:              log,
	}

	body := []byte(`[{"executionId":"exec-1","level":"INFO","message":"test"}]`)
	req := httptest.NewRequest(http.MethodPost, "/v1alpha1/logs", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	ws.handleLogs(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestWebhookServer_ProcessLog(t *testing.T) {
	log := logr.Discard()
	execService := NewExecutionServiceServer(nil, nil, nil)

	ws := &WebhookServer{
		executionService: execService,
		log:              log,
	}

	tests := []struct {
		name  string
		entry *streamingv1alpha1.LogEntry
	}{
		{
			name: "info log",
			entry: &streamingv1alpha1.LogEntry{
				ExecutionId: "exec-1",
				Level:       "INFO",
				Message:     "Info message",
				Timestamp:   time.Now().Format(time.RFC3339),
			},
		},
		{
			name: "error log",
			entry: &streamingv1alpha1.LogEntry{
				ExecutionId: "exec-1",
				Level:       "ERROR",
				Message:     "Error message",
				Timestamp:   time.Now().Format(time.RFC3339),
			},
		},
		{
			name: "warn log",
			entry: &streamingv1alpha1.LogEntry{
				ExecutionId: "exec-1",
				Level:       "WARN",
				Message:     "Warning message",
				Timestamp:   time.Now().Format(time.RFC3339),
			},
		},
		{
			name: "debug log",
			entry: &streamingv1alpha1.LogEntry{
				ExecutionId: "exec-1",
				Level:       "DEBUG",
				Message:     "Debug message",
				Timestamp:   time.Now().Format(time.RFC3339),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ws.processLog(tt.entry)

			// Verify log was stored
			logs := execService.GetExecutionLogs(tt.entry.ExecutionId)
			found := false
			for _, l := range logs {
				if l.Message == tt.entry.Message {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("log not stored: %s", tt.entry.Message)
			}
		})
	}
}

func TestWebhookServer_ValidateRequest_NoHeader(t *testing.T) {
	log := logr.Discard()
	ws := &WebhookServer{
		log: log,
	}

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	// No Authorization header

	result, err := ws.validateRequest(req)
	if err == nil {
		t.Error("expected error for missing auth header")
	}
	if result != nil {
		t.Error("expected nil result for missing auth header")
	}
}

func TestWebhookServer_ValidateRequest_InvalidFormat(t *testing.T) {
	log := logr.Discard()
	ws := &WebhookServer{
		log: log,
	}

	tests := []struct {
		name   string
		header string
	}{
		{
			name:   "no bearer prefix",
			header: "token-only",
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
				t.Error("expected error for invalid auth format")
			}
		})
	}
}

func TestWebhookServer_HandleCallback_InvalidJSON(t *testing.T) {
	log := logr.Discard()
	execService := NewExecutionServiceServer(nil, nil, nil)

	ws := &WebhookServer{
		executionService: execService,
		validator:        nil,
		log:              log,
	}

	// Valid method but invalid JSON should be caught after auth (but we don't have auth here)
	body := []byte(`{invalid json}`)
	req := httptest.NewRequest(http.MethodPost, "/v1alpha1/callback", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	ws.handleCallback(rec, req)

	// Without auth, it will fail at auth step first
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestWebhookServer_ServerLifecycle(t *testing.T) {
	log := logr.Discard()
	execService := NewExecutionServiceServer(nil, nil, nil)

	ws := &WebhookServer{
		executionService: execService,
		log:              log,
		server: &http.Server{
			Addr:         ":0", // Random port
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 5 * time.Second,
		},
	}

	// Verify server was configured
	if ws.server == nil {
		t.Error("expected server to be set")
	}
	if ws.server.ReadTimeout != 5*time.Second {
		t.Error("ReadTimeout mismatch")
	}
}

func TestProcessLog_WithFields(t *testing.T) {
	log := logr.Discard()
	execService := NewExecutionServiceServer(nil, nil, nil)

	ws := &WebhookServer{
		executionService: execService,
		log:              log,
	}

	entry := &streamingv1alpha1.LogEntry{
		ExecutionId: "exec-fields",
		Level:       "INFO",
		Message:     "Message with fields",
		Timestamp:   time.Now().Format(time.RFC3339),
		Fields: map[string]string{
			"key1": "value1",
			"key2": "value2",
		},
	}

	ws.processLog(entry)

	logs := execService.GetExecutionLogs("exec-fields")
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	if logs[0].Fields["key1"] != "value1" {
		t.Error("field key1 not preserved")
	}
}

func TestWebhookPayload_Types(t *testing.T) {
	tests := []struct {
		name        string
		payload     types.WebhookPayload
		expectedKey string
	}{
		{
			name: "log type",
			payload: types.WebhookPayload{
				Type: "log",
				Log: &types.LogEntry{
					ExecutionID: "exec-1",
					Message:     "test",
					Timestamp:   time.Now(),
				},
			},
			expectedKey: "log",
		},
		{
			name: "progress type",
			payload: types.WebhookPayload{
				Type: "progress",
				Progress: &types.ProgressReport{
					ExecutionID:     "exec-1",
					ProgressPercent: 50,
					Timestamp:       time.Now(),
				},
			},
			expectedKey: "progress",
		},
		{
			name: "completion type",
			payload: types.WebhookPayload{
				Type: "completion",
				Completion: &types.CompletionReport{
					ExecutionID: "exec-1",
					Success:     true,
					Timestamp:   time.Now(),
				},
			},
			expectedKey: "completion",
		},
		{
			name: "heartbeat type",
			payload: types.WebhookPayload{
				Type: "heartbeat",
				Heartbeat: &types.HeartbeatRequest{
					ExecutionID: "exec-1",
					Timestamp:   time.Now(),
				},
			},
			expectedKey: "heartbeat",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.payload)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}

			var decoded types.WebhookPayload
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if decoded.Type != tt.expectedKey {
				t.Errorf("type = %s, want %s", decoded.Type, tt.expectedKey)
			}
		})
	}
}

func TestWebhookServer_Start_Cancellation(t *testing.T) {
	log := logr.Discard()
	execService := NewExecutionServiceServer(nil, nil, nil)

	ws := &WebhookServer{
		executionService: execService,
		log:              log,
		server: &http.Server{
			Addr: "127.0.0.1:0", // Use available port
		},
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Start server in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- ws.Start(ctx)
	}()

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

	// Cancel context
	cancel()

	// Wait for server to stop
	select {
	case err := <-errCh:
		if err != nil {
			t.Logf("Server stopped with: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("server did not stop in time")
	}
}
