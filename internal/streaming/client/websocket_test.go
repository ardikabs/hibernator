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
	"github.com/gorilla/websocket"

	streamingv1alpha1 "github.com/ardikabs/hibernator/api/streaming/v1alpha1"
)

func TestNewWebSocketClient(t *testing.T) {
	opts := WebSocketClientOptions{
		URL:         "http://localhost:8080",
		ExecutionID: "exec-123",
		TokenPath:   "/custom/token",
		Log:         logr.Discard(),
	}

	client := NewWebSocketClient(opts)
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.url != opts.URL {
		t.Errorf("url = %s, want %s", client.url, opts.URL)
	}
	if client.executionID != opts.ExecutionID {
		t.Errorf("executionID = %s, want %s", client.executionID, opts.ExecutionID)
	}
	if client.tokenPath != opts.TokenPath {
		t.Errorf("tokenPath = %s, want %s", client.tokenPath, opts.TokenPath)
	}
}

func TestNewWebSocketClient_DefaultTokenPath(t *testing.T) {
	opts := WebSocketClientOptions{
		URL:         "http://localhost:8080",
		ExecutionID: "exec-123",
		Log:         logr.Discard(),
	}

	client := NewWebSocketClient(opts)
	if client.tokenPath != DefaultTokenPath {
		t.Errorf("tokenPath = %s, want %s", client.tokenPath, DefaultTokenPath)
	}
}

func TestWebSocketClient_BuildURL(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		executionID string
		wantURL     string
		wantErr     bool
	}{
		{
			name:        "http scheme",
			baseURL:     "http://localhost:8080",
			executionID: "exec-123",
			wantURL:     "ws://localhost:8080/v1alpha1/stream/exec-123",
			wantErr:     false,
		},
		{
			name:        "https scheme",
			baseURL:     "https://example.com",
			executionID: "exec-456",
			wantURL:     "wss://example.com/v1alpha1/stream/exec-456",
			wantErr:     false,
		},
		{
			name:        "ws scheme",
			baseURL:     "ws://localhost:8080",
			executionID: "exec-789",
			wantURL:     "ws://localhost:8080/v1alpha1/stream/exec-789",
			wantErr:     false,
		},
		{
			name:        "wss scheme",
			baseURL:     "wss://example.com",
			executionID: "exec-abc",
			wantURL:     "wss://example.com/v1alpha1/stream/exec-abc",
			wantErr:     false,
		},
		{
			name:        "invalid scheme",
			baseURL:     "ftp://example.com",
			executionID: "exec-fail",
			wantURL:     "",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &WebSocketClient{
				url:         tt.baseURL,
				executionID: tt.executionID,
			}

			got, err := client.buildURL()
			if (err != nil) != tt.wantErr {
				t.Errorf("buildURL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.wantURL {
				t.Errorf("buildURL() = %v, want %v", got, tt.wantURL)
			}
		})
	}
}

func TestWebSocketClient_Connect(t *testing.T) {
	// Create mock WebSocket server
	var upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check auth header
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		// Check execution ID header
		execID := r.Header.Get("X-Execution-ID")
		if execID == "" {
			http.Error(w, "missing execution ID", http.StatusBadRequest)
			return
		}

		// Upgrade to WebSocket
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("failed to upgrade: %v", err)
			return
		}
		defer conn.Close()

		// Keep connection alive briefly
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	// Create temp token file
	tempDir := t.TempDir()
	tokenPath := filepath.Join(tempDir, "token")
	if err := os.WriteFile(tokenPath, []byte("test-token"), 0600); err != nil {
		t.Fatalf("failed to write token file: %v", err)
	}

	opts := WebSocketClientOptions{
		URL:         server.URL,
		ExecutionID: "exec-connect",
		TokenPath:   tokenPath,
		Log:         logr.Discard(),
	}

	client := NewWebSocketClient(opts)
	err := client.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	if client.conn == nil {
		t.Error("expected connection to be established")
	}

	// Cleanup
	client.Close()
}

func TestWebSocketClient_Connect_MissingToken(t *testing.T) {
	opts := WebSocketClientOptions{
		URL:         "ws://localhost:8080",
		ExecutionID: "exec-fail",
		TokenPath:   "/nonexistent/token",
		Log:         logr.Discard(),
	}

	client := NewWebSocketClient(opts)
	err := client.Connect(context.Background())
	if err == nil {
		t.Error("expected error for missing token file")
	}
}

func TestWebSocketClient_Log(t *testing.T) {
	msgChan := make(chan WebSocketMessage, 1)

	var upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := upgrader.Upgrade(w, r, nil)
		defer conn.Close()

		// Read message
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var msg WebSocketMessage
		json.Unmarshal(data, &msg)
		msgChan <- msg
	}))
	defer server.Close()

	tempDir := t.TempDir()
	tokenPath := filepath.Join(tempDir, "token")
	os.WriteFile(tokenPath, []byte("test-token"), 0600)

	client := &WebSocketClient{
		url:         server.URL,
		executionID: "exec-log",
		tokenPath:   tokenPath,
		log:         logr.Discard(),
	}

	client.Connect(context.Background())
	defer client.Close()

	// Send log
	err := client.Log(context.Background(), "INFO", "test message", map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("Log() error = %v", err)
	}

	// Wait for message
	select {
	case receivedMsg := <-msgChan:
		if receivedMsg.Type != "log" {
			t.Errorf("message type = %s, want 'log'", receivedMsg.Type)
		}

		var logEntry streamingv1alpha1.LogEntry
		if err := json.Unmarshal(receivedMsg.Data, &logEntry); err != nil {
			t.Fatalf("failed to unmarshal log entry: %v", err)
		}

		if logEntry.ExecutionId != "exec-log" {
			t.Errorf("executionId = %s, want 'exec-log'", logEntry.ExecutionId)
		}
		if logEntry.Message != "test message" {
			t.Errorf("message = %s, want 'test message'", logEntry.Message)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timeout waiting for message")
	}
}

func TestWebSocketClient_ReportProgress(t *testing.T) {
	msgChan := make(chan WebSocketMessage, 1)

	var upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := upgrader.Upgrade(w, r, nil)
		defer conn.Close()

		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var msg WebSocketMessage
		json.Unmarshal(data, &msg)
		msgChan <- msg
	}))
	defer server.Close()

	tempDir := t.TempDir()
	tokenPath := filepath.Join(tempDir, "token")
	os.WriteFile(tokenPath, []byte("test-token"), 0600)

	client := &WebSocketClient{
		url:         server.URL,
		executionID: "exec-progress",
		tokenPath:   tokenPath,
		log:         logr.Discard(),
	}

	client.Connect(context.Background())
	defer client.Close()

	err := client.ReportProgress(context.Background(), "Running", 50, "processing targets")
	if err != nil {
		t.Fatalf("ReportProgress() error = %v", err)
	}

	select {
	case receivedMsg := <-msgChan:
		if receivedMsg.Type != "progress" {
			t.Errorf("message type = %s, want 'progress'", receivedMsg.Type)
		}

		var progress streamingv1alpha1.ProgressReport
		if err := json.Unmarshal(receivedMsg.Data, &progress); err != nil {
			t.Fatalf("failed to unmarshal progress: %v", err)
		}

		if progress.Phase != "Running" {
			t.Errorf("phase = %s, want 'Running'", progress.Phase)
		}
		if progress.ProgressPercent != 50 {
			t.Errorf("progressPercent = %d, want 50", progress.ProgressPercent)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timeout waiting for message")
	}
}

func TestWebSocketClient_ReportCompletion(t *testing.T) {
	msgChan := make(chan WebSocketMessage, 1)

	var upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := upgrader.Upgrade(w, r, nil)
		defer conn.Close()

		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var msg WebSocketMessage
		json.Unmarshal(data, &msg)
		msgChan <- msg
	}))
	defer server.Close()

	tempDir := t.TempDir()
	tokenPath := filepath.Join(tempDir, "token")
	os.WriteFile(tokenPath, []byte("test-token"), 0600)

	client := &WebSocketClient{
		url:         server.URL,
		executionID: "exec-complete",
		tokenPath:   tokenPath,
		log:         logr.Discard(),
	}

	client.Connect(context.Background())
	defer client.Close()

	restoreData := []byte(`{"state": "saved"}`)
	err := client.ReportCompletion(context.Background(), true, "", 5000, restoreData)
	if err != nil {
		t.Fatalf("ReportCompletion() error = %v", err)
	}

	select {
	case receivedMsg := <-msgChan:
		if receivedMsg.Type != "completion" {
			t.Errorf("message type = %s, want 'completion'", receivedMsg.Type)
		}

		var completion streamingv1alpha1.CompletionReport
		if err := json.Unmarshal(receivedMsg.Data, &completion); err != nil {
			t.Fatalf("failed to unmarshal completion: %v", err)
		}

		if !completion.Success {
			t.Error("expected success to be true")
		}
		if completion.DurationMs != 5000 {
			t.Errorf("durationMs = %d, want 5000", completion.DurationMs)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timeout waiting for message")
	}
}

func TestWebSocketClient_Heartbeat(t *testing.T) {
	client := &WebSocketClient{
		url:         "ws://localhost:8080",
		executionID: "exec-hb",
		log:         logr.Discard(),
	}

	// Test StartHeartbeat
	client.StartHeartbeat(100 * time.Millisecond)

	// Verify heartbeat context was created
	client.mu.Lock()
	hasHeartbeat := client.heartbeatCancel != nil
	client.mu.Unlock()

	if !hasHeartbeat {
		t.Error("expected heartbeat to be started")
	}

	// Stop heartbeat
	client.StopHeartbeat()

	// Verify heartbeat was stopped
	client.mu.Lock()
	hasHeartbeat = client.heartbeatCancel != nil
	client.mu.Unlock()

	if hasHeartbeat {
		t.Error("expected heartbeat to be stopped")
	}
}

// TestWebSocketClient_FlushLogs is removed - FlushLogs was removed from the
// StreamingClient interface. Logs are now sent immediately via Log().

func TestWebSocketClient_Close(t *testing.T) {
	var upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := upgrader.Upgrade(w, r, nil)
		defer conn.Close()

		// Keep connection alive
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	tokenPath := filepath.Join(tempDir, "token")
	os.WriteFile(tokenPath, []byte("test-token"), 0600)

	client := &WebSocketClient{
		url:         server.URL,
		executionID: "exec-close",
		tokenPath:   tokenPath,
		log:         logr.Discard(),
	}

	client.Connect(context.Background())

	// Verify connection is established
	if client.conn == nil {
		t.Fatal("expected connection to be established")
	}

	// Close connection
	err := client.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Verify connection is nil after close
	client.mu.Lock()
	conn := client.conn
	client.mu.Unlock()

	if conn != nil {
		t.Error("expected connection to be nil after close")
	}
}

func TestWebSocketClient_SendMessage_NoConnection(t *testing.T) {
	client := &WebSocketClient{
		url:         "ws://localhost:8080",
		executionID: "exec-no-conn",
		log:         logr.Discard(),
	}

	// Try to send message without connection
	msg := WebSocketMessage{
		Type: "log",
		Data: json.RawMessage(`{}`),
	}

	err := client.sendMessage(msg)
	if err == nil {
		t.Error("expected error when sending without connection")
	}
}

func TestWebSocketMessage_Marshal(t *testing.T) {
	msg := WebSocketMessage{
		Type: "log",
		Data: json.RawMessage(`{"message": "test"}`),
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded WebSocketMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Type != msg.Type {
		t.Errorf("type = %s, want %s", decoded.Type, msg.Type)
	}
}
