/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-logr/logr"

	streamingv1alpha1 "github.com/ardikabs/hibernator/api/streaming/v1alpha1"
)

func TestNewWebhookClient(t *testing.T) {
	opts := WebhookClientOptions{
		BaseURL:     "http://localhost:8080",
		ExecutionID: "exec-123",
		TokenPath:   "/custom/token",
		Timeout:     60 * time.Second,
		Log:         logr.Discard(),
	}

	client := NewWebhookClient(opts)
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.baseURL != opts.BaseURL {
		t.Errorf("baseURL = %s, want %s", client.baseURL, opts.BaseURL)
	}
	if client.executionID != opts.ExecutionID {
		t.Errorf("executionID = %s, want %s", client.executionID, opts.ExecutionID)
	}
	if client.tokenPath != opts.TokenPath {
		t.Errorf("tokenPath = %s, want %s", client.tokenPath, opts.TokenPath)
	}
}

func TestNewWebhookClient_Defaults(t *testing.T) {
	opts := WebhookClientOptions{
		BaseURL:     "http://localhost:8080",
		ExecutionID: "exec-123",
		Log:         logr.Discard(),
	}

	client := NewWebhookClient(opts)
	if client.tokenPath != DefaultTokenPath {
		t.Errorf("tokenPath = %s, want %s", client.tokenPath, DefaultTokenPath)
	}
	if client.httpClient.Timeout != 30*time.Second {
		t.Errorf("timeout = %v, want 30s", client.httpClient.Timeout)
	}
}

func TestWebhookClient_Connect_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	opts := WebhookClientOptions{
		BaseURL:     server.URL,
		ExecutionID: "exec-123",
		Log:         logr.Discard(),
	}
	client := NewWebhookClient(opts)

	err := client.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
}

func TestWebhookClient_Connect_Failure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	opts := WebhookClientOptions{
		BaseURL:     server.URL,
		ExecutionID: "exec-123",
		Log:         logr.Discard(),
	}
	client := NewWebhookClient(opts)

	err := client.Connect(context.Background())
	if err == nil {
		t.Error("expected error for failed health check")
	}
}

func TestWebhookClient_Log_WithServer(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "token")
	_ = os.WriteFile(tokenPath, []byte("test-token"), 0600)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1alpha1/logs" && r.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	opts := WebhookClientOptions{
		BaseURL:     server.URL,
		ExecutionID: "exec-123",
		TokenPath:   tokenPath,
		Log:         logr.Discard(),
	}
	client := NewWebhookClient(opts)

	// Log should send immediately (no buffering)
	err := client.Log(context.Background(), "INFO", "test message", map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("Log() error = %v", err)
	}
}

func TestWebhookClient_HeartbeatLifecycle(t *testing.T) {
	opts := WebhookClientOptions{
		BaseURL:     "http://localhost:8080",
		ExecutionID: "exec-123",
		Log:         logr.Discard(),
	}
	client := NewWebhookClient(opts)

	// Start heartbeat
	client.StartHeartbeat(10 * time.Millisecond)

	// Starting again should be a no-op
	client.StartHeartbeat(10 * time.Millisecond)

	// Let it run briefly (won't actually send without a server)
	time.Sleep(30 * time.Millisecond)

	// Stop
	client.StopHeartbeat()

	// Stopping again should be safe
	client.StopHeartbeat()
}

func TestWebhookClient_Close(t *testing.T) {
	opts := WebhookClientOptions{
		BaseURL:     "http://localhost:8080",
		ExecutionID: "exec-123",
		Log:         logr.Discard(),
	}
	client := NewWebhookClient(opts)

	err := client.Close()
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestWebhookClient_ReadToken(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "token")
	expectedToken := "test-webhook-token"

	err := os.WriteFile(tokenPath, []byte(expectedToken), 0600)
	if err != nil {
		t.Fatalf("failed to write token file: %v", err)
	}

	opts := WebhookClientOptions{
		BaseURL:     "http://localhost:8080",
		ExecutionID: "exec-123",
		TokenPath:   tokenPath,
		Log:         logr.Discard(),
	}
	client := NewWebhookClient(opts)

	token, err := client.readToken()
	if err != nil {
		t.Fatalf("readToken() error = %v", err)
	}
	if token != expectedToken {
		t.Errorf("token = %s, want %s", token, expectedToken)
	}
}

func TestWebhookClient_ReadToken_NotFound(t *testing.T) {
	opts := WebhookClientOptions{
		BaseURL:     "http://localhost:8080",
		ExecutionID: "exec-123",
		TokenPath:   "/nonexistent/path",
		Log:         logr.Discard(),
	}
	client := NewWebhookClient(opts)

	_, err := client.readToken()
	if err == nil {
		t.Error("expected error for non-existent token file")
	}
}

// TestWebhookClient_FlushLogs tests are removed - Log now sends immediately
// and FlushLogs was removed from the StreamingClient interface.

func TestWebhookClient_ReportProgress_WithServer(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "token")
	_ = os.WriteFile(tokenPath, []byte("test-token"), 0600)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1alpha1/progress" && r.Method == http.MethodPost {
			resp := &streamingv1alpha1.ProgressResponse{Acknowledged: true}
			json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	opts := WebhookClientOptions{
		BaseURL:     server.URL,
		ExecutionID: "exec-123",
		TokenPath:   tokenPath,
		Log:         logr.Discard(),
	}
	client := NewWebhookClient(opts)

	err := client.ReportProgress(context.Background(), "Running", 50, "Processing")
	if err != nil {
		t.Fatalf("ReportProgress() error = %v", err)
	}
}

func TestWebhookClient_ReportProgress_NotAcknowledged(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "token")
	_ = os.WriteFile(tokenPath, []byte("test-token"), 0600)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1alpha1/progress" {
			resp := &streamingv1alpha1.ProgressResponse{Acknowledged: false}
			json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	opts := WebhookClientOptions{
		BaseURL:     server.URL,
		ExecutionID: "exec-123",
		TokenPath:   tokenPath,
		Log:         logr.Discard(),
	}
	client := NewWebhookClient(opts)

	err := client.ReportProgress(context.Background(), "Running", 50, "Processing")
	if err == nil {
		t.Error("expected error for not acknowledged response")
	}
}

func TestWebhookClient_ReportCompletion_WithServer(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "token")
	_ = os.WriteFile(tokenPath, []byte("test-token"), 0600)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1alpha1/logs" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/v1alpha1/completion" && r.Method == http.MethodPost {
			resp := &streamingv1alpha1.CompletionResponse{
				Acknowledged: true,
				RestoreRef:   "restore-123",
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	opts := WebhookClientOptions{
		BaseURL:     server.URL,
		ExecutionID: "exec-123",
		TokenPath:   tokenPath,
		Log:         logr.Discard(),
	}
	client := NewWebhookClient(opts)

	err := client.ReportCompletion(context.Background(), true, "", 5000, []byte(`{"key":"value"}`))
	if err != nil {
		t.Fatalf("ReportCompletion() error = %v", err)
	}
}

func TestWebhookClient_DoRequest_Unauthorized(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "token")
	_ = os.WriteFile(tokenPath, []byte("bad-token"), 0600)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	opts := WebhookClientOptions{
		BaseURL:     server.URL,
		ExecutionID: "exec-123",
		TokenPath:   tokenPath,
		Log:         logr.Discard(),
	}
	client := NewWebhookClient(opts)

	_, err := client.doRequest(context.Background(), "POST", "/test", nil)
	if err == nil {
		t.Error("expected error for unauthorized request")
	}
	if err.Error() != "authentication failed" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWebhookClient_DoRequest_Forbidden(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "token")
	_ = os.WriteFile(tokenPath, []byte("valid-token"), 0600)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	opts := WebhookClientOptions{
		BaseURL:     server.URL,
		ExecutionID: "exec-123",
		TokenPath:   tokenPath,
		Log:         logr.Discard(),
	}
	client := NewWebhookClient(opts)

	_, err := client.doRequest(context.Background(), "POST", "/test", nil)
	if err == nil {
		t.Error("expected error for forbidden request")
	}
	if err.Error() != "access denied" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWebhookClient_DoRequest_ServerError(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "token")
	_ = os.WriteFile(tokenPath, []byte("valid-token"), 0600)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	opts := WebhookClientOptions{
		BaseURL:     server.URL,
		ExecutionID: "exec-123",
		TokenPath:   tokenPath,
		Log:         logr.Discard(),
	}
	client := NewWebhookClient(opts)

	_, err := client.doRequest(context.Background(), "POST", "/test", nil)
	if err == nil {
		t.Error("expected error for server error")
	}
}

func TestWebhookClientOptions_Struct(t *testing.T) {
	opts := WebhookClientOptions{
		BaseURL:     "http://example.com",
		ExecutionID: "exec-test",
		TokenPath:   "/path/to/token",
		Timeout:     45 * time.Second,
		Log:         logr.Discard(),
	}

	if opts.BaseURL != "http://example.com" {
		t.Error("BaseURL mismatch")
	}
	if opts.ExecutionID != "exec-test" {
		t.Error("ExecutionID mismatch")
	}
	if opts.TokenPath != "/path/to/token" {
		t.Error("TokenPath mismatch")
	}
	if opts.Timeout != 45*time.Second {
		t.Error("Timeout mismatch")
	}
}
