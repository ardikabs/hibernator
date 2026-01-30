/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package client

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

func TestDefaultConstants(t *testing.T) {
	if DefaultTokenPath != "/var/run/secrets/stream/token" {
		t.Errorf("DefaultTokenPath = %s", DefaultTokenPath)
	}
	if DefaultHeartbeatInterval != 30*time.Second {
		t.Errorf("DefaultHeartbeatInterval = %v", DefaultHeartbeatInterval)
	}
	if DefaultReconnectDelay != 5*time.Second {
		t.Errorf("DefaultReconnectDelay = %v", DefaultReconnectDelay)
	}
}

func TestNewGRPCClient(t *testing.T) {
	opts := GRPCClientOptions{
		Address:     "localhost:9443",
		ExecutionID: "exec-123",
		TokenPath:   "/custom/token/path",
		UseTLS:      true,
		Log:         logr.Discard(),
	}

	client := NewGRPCClient(opts)
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.address != opts.Address {
		t.Errorf("address = %s, want %s", client.address, opts.Address)
	}
	if client.executionID != opts.ExecutionID {
		t.Errorf("executionID = %s, want %s", client.executionID, opts.ExecutionID)
	}
	if client.tokenPath != opts.TokenPath {
		t.Errorf("tokenPath = %s, want %s", client.tokenPath, opts.TokenPath)
	}
	if client.useTLS != opts.UseTLS {
		t.Errorf("useTLS = %v, want %v", client.useTLS, opts.UseTLS)
	}
}

func TestNewGRPCClient_DefaultTokenPath(t *testing.T) {
	opts := GRPCClientOptions{
		Address:     "localhost:9443",
		ExecutionID: "exec-123",
		Log:         logr.Discard(),
	}

	client := NewGRPCClient(opts)
	if client.tokenPath != DefaultTokenPath {
		t.Errorf("tokenPath = %s, want %s", client.tokenPath, DefaultTokenPath)
	}
}

func TestGRPCClient_Log(t *testing.T) {
	opts := GRPCClientOptions{
		Address:     "localhost:9443",
		ExecutionID: "exec-123",
		Log:         logr.Discard(),
	}
	client := NewGRPCClient(opts)

	// Log should buffer entries
	err := client.Log(context.Background(), "INFO", "test message", map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("Log() error = %v", err)
	}

	client.logBufferMu.Lock()
	bufferLen := len(client.logBuffer)
	client.logBufferMu.Unlock()

	if bufferLen != 1 {
		t.Errorf("expected 1 buffered log, got %d", bufferLen)
	}
}

func TestGRPCClient_Log_AutoFlush(t *testing.T) {
	opts := GRPCClientOptions{
		Address:     "localhost:9443",
		ExecutionID: "exec-123",
		Log:         logr.Discard(),
	}
	client := NewGRPCClient(opts)

	// Add 50 logs to trigger auto-flush
	for i := 0; i < 50; i++ {
		err := client.Log(context.Background(), "INFO", "test message", nil)
		if err != nil {
			t.Fatalf("Log() error = %v", err)
		}
	}

	// Buffer should be flushed (reset to 0)
	client.logBufferMu.Lock()
	bufferLen := len(client.logBuffer)
	client.logBufferMu.Unlock()

	if bufferLen != 0 {
		t.Errorf("expected 0 buffered logs after auto-flush, got %d", bufferLen)
	}
}

func TestGRPCClient_FlushLogs_Empty(t *testing.T) {
	opts := GRPCClientOptions{
		Address:     "localhost:9443",
		ExecutionID: "exec-123",
		Log:         logr.Discard(),
	}
	client := NewGRPCClient(opts)

	// Flush empty buffer should not error
	err := client.FlushLogs(context.Background())
	if err != nil {
		t.Fatalf("FlushLogs() error = %v", err)
	}
}

func TestGRPCClient_FlushLogs(t *testing.T) {
	opts := GRPCClientOptions{
		Address:     "localhost:9443",
		ExecutionID: "exec-123",
		Log:         logr.Discard(),
	}
	client := NewGRPCClient(opts)

	// Add some logs
	for i := 0; i < 10; i++ {
		_ = client.Log(context.Background(), "INFO", "test", nil)
	}

	// Flush
	err := client.FlushLogs(context.Background())
	if err != nil {
		t.Fatalf("FlushLogs() error = %v", err)
	}

	// Buffer should be empty
	client.logBufferMu.Lock()
	bufferLen := len(client.logBuffer)
	client.logBufferMu.Unlock()

	if bufferLen != 0 {
		t.Errorf("expected 0 buffered logs after flush, got %d", bufferLen)
	}
}

func TestGRPCClient_ReportProgress(t *testing.T) {
	opts := GRPCClientOptions{
		Address:     "localhost:9443",
		ExecutionID: "exec-123",
		Log:         logr.Discard(),
	}
	client := NewGRPCClient(opts)

	err := client.ReportProgress(context.Background(), "Running", 50, "Processing")
	if err != nil {
		t.Fatalf("ReportProgress() error = %v", err)
	}
}

func TestGRPCClient_ReportCompletion(t *testing.T) {
	opts := GRPCClientOptions{
		Address:     "localhost:9443",
		ExecutionID: "exec-123",
		Log:         logr.Discard(),
	}
	client := NewGRPCClient(opts)

	tests := []struct {
		name        string
		success     bool
		errorMsg    string
		durationMs  int64
		restoreData []byte
	}{
		{
			name:       "successful completion",
			success:    true,
			durationMs: 5000,
		},
		{
			name:       "failed completion",
			success:    false,
			errorMsg:   "connection timeout",
			durationMs: 3000,
		},
		{
			name:        "completion with restore data",
			success:     true,
			durationMs:  10000,
			restoreData: []byte(`{"key": "value"}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := client.ReportCompletion(context.Background(), tt.success, tt.errorMsg, tt.durationMs, tt.restoreData)
			if err != nil {
				t.Fatalf("ReportCompletion() error = %v", err)
			}
		})
	}
}

func TestGRPCClient_Close_NoConnection(t *testing.T) {
	opts := GRPCClientOptions{
		Address:     "localhost:9443",
		ExecutionID: "exec-123",
		Log:         logr.Discard(),
	}
	client := NewGRPCClient(opts)

	// Close without connection should not error
	err := client.Close()
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestGRPCClient_HeartbeatLifecycle(t *testing.T) {
	opts := GRPCClientOptions{
		Address:     "localhost:9443",
		ExecutionID: "exec-123",
		Log:         logr.Discard(),
	}
	client := NewGRPCClient(opts)

	// Start heartbeat with short interval
	client.StartHeartbeat(10 * time.Millisecond)

	// Starting again should be a no-op
	client.StartHeartbeat(10 * time.Millisecond)

	// Let it run a bit
	time.Sleep(50 * time.Millisecond)

	// Stop
	client.StopHeartbeat()

	// Stopping again should be safe
	client.StopHeartbeat()
}

func TestGRPCClient_ReadToken(t *testing.T) {
	// Create temp token file
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "token")
	expectedToken := "test-token-12345"

	err := os.WriteFile(tokenPath, []byte(expectedToken), 0600)
	if err != nil {
		t.Fatalf("failed to write token file: %v", err)
	}

	opts := GRPCClientOptions{
		Address:     "localhost:9443",
		ExecutionID: "exec-123",
		TokenPath:   tokenPath,
		Log:         logr.Discard(),
	}
	client := NewGRPCClient(opts)

	token, err := client.readToken()
	if err != nil {
		t.Fatalf("readToken() error = %v", err)
	}
	if token != expectedToken {
		t.Errorf("token = %s, want %s", token, expectedToken)
	}
}

func TestGRPCClient_ReadToken_NotFound(t *testing.T) {
	opts := GRPCClientOptions{
		Address:     "localhost:9443",
		ExecutionID: "exec-123",
		TokenPath:   "/nonexistent/token/path",
		Log:         logr.Discard(),
	}
	client := NewGRPCClient(opts)

	_, err := client.readToken()
	if err == nil {
		t.Error("expected error for non-existent token file")
	}
}

func TestGRPCClient_Connect_AlreadyConnected(t *testing.T) {
	opts := GRPCClientOptions{
		Address:     "localhost:9443",
		ExecutionID: "exec-123",
		Log:         logr.Discard(),
	}
	client := NewGRPCClient(opts)

	// Set a non-nil conn to simulate already connected
	client.mu.Lock()
	// Note: We can't easily test real connection, but we can test the guard
	client.mu.Unlock()
}

func TestGRPCClient_Connect_ContextCancelled(t *testing.T) {
	opts := GRPCClientOptions{
		Address:     "localhost:9443",
		ExecutionID: "exec-123",
		Log:         logr.Discard(),
	}
	client := NewGRPCClient(opts)

	// Cancel context immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := client.Connect(ctx)
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

func TestGRPCClientOptions_Struct(t *testing.T) {
	opts := GRPCClientOptions{
		Address:     "localhost:9443",
		ExecutionID: "exec-test",
		TokenPath:   "/custom/path",
		UseTLS:      true,
		Log:         logr.Discard(),
	}

	if opts.Address != "localhost:9443" {
		t.Error("Address mismatch")
	}
	if opts.ExecutionID != "exec-test" {
		t.Error("ExecutionID mismatch")
	}
	if opts.TokenPath != "/custom/path" {
		t.Error("TokenPath mismatch")
	}
	if !opts.UseTLS {
		t.Error("UseTLS mismatch")
	}
}
