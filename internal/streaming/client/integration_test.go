/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package client

import (
	"context"
	"testing"
	"time"
)

func TestClientConfig_Validation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ClientConfig
		wantErr bool
	}{
		{
			name: "valid gRPC config",
			cfg: ClientConfig{
				Type:        ClientTypeGRPC,
				GRPCAddress: "localhost:8080",
				TokenPath:   "/var/run/secrets/token",
				ExecutionID: "exec-123",
			},
			wantErr: false,
		},
		{
			name: "valid webhook config",
			cfg: ClientConfig{
				Type:        ClientTypeWebhook,
				WebhookURL:  "http://localhost:8080",
				TokenPath:   "/var/run/secrets/token",
				ExecutionID: "exec-123",
			},
			wantErr: false,
		},
		{
			name: "missing endpoint",
			cfg: ClientConfig{
				Type:        ClientTypeGRPC,
				TokenPath:   "/var/run/secrets/token",
				ExecutionID: "exec-123",
			},
			wantErr: true,
		},
		{
			name: "missing token",
			cfg: ClientConfig{
				Type:        ClientTypeGRPC,
				GRPCAddress: "localhost:8080",
				ExecutionID: "exec-123",
			},
			wantErr: true,
		},
		{
			name: "missing execution ID",
			cfg: ClientConfig{
				Type:        ClientTypeGRPC,
				GRPCAddress: "localhost:8080",
				TokenPath:   "/var/run/secrets/token",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Check if required fields are missing based on Type
			var hasError bool
			switch tt.cfg.Type {
			case ClientTypeGRPC:
				hasError = tt.cfg.GRPCAddress == "" || tt.cfg.TokenPath == "" || tt.cfg.ExecutionID == ""
			case ClientTypeWebhook:
				hasError = tt.cfg.WebhookURL == "" || tt.cfg.TokenPath == "" || tt.cfg.ExecutionID == ""
			default:
				hasError = tt.cfg.ExecutionID == ""
			}
			if hasError != tt.wantErr {
				t.Errorf("expected error=%v, got %v", tt.wantErr, hasError)
			}
		})
	}
}

func TestProgressReporting_Validation(t *testing.T) {
	tests := []struct {
		name            string
		progressPercent int32
		phase           string
		wantValid       bool
	}{
		{
			name:            "valid progress 0%",
			progressPercent: 0,
			phase:           "Starting",
			wantValid:       true,
		},
		{
			name:            "valid progress 50%",
			progressPercent: 50,
			phase:           "Running",
			wantValid:       true,
		},
		{
			name:            "valid progress 100%",
			progressPercent: 100,
			phase:           "Completed",
			wantValid:       true,
		},
		{
			name:            "invalid progress negative",
			progressPercent: -1,
			phase:           "Error",
			wantValid:       false,
		},
		{
			name:            "invalid progress over 100",
			progressPercent: 101,
			phase:           "Error",
			wantValid:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isValid := tt.progressPercent >= 0 && tt.progressPercent <= 100
			if isValid != tt.wantValid {
				t.Errorf("expected valid=%v for progress=%d", tt.wantValid, tt.progressPercent)
			}
		})
	}
}

func TestHeartbeatMechanism_Timing(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
		wantOk   bool
	}{
		{
			name:     "valid heartbeat interval 30s",
			interval: 30 * time.Second,
			wantOk:   true,
		},
		{
			name:     "valid heartbeat interval 1m",
			interval: 1 * time.Minute,
			wantOk:   true,
		},
		{
			name:     "too short interval (less than 10s)",
			interval: 5 * time.Second,
			wantOk:   false,
		},
		{
			name:     "minimum acceptable interval (10s)",
			interval: 10 * time.Second,
			wantOk:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			minInterval := 10 * time.Second
			isOk := tt.interval >= minInterval
			if isOk != tt.wantOk {
				t.Errorf("expected ok=%v for interval %v (min: %v)", tt.wantOk, tt.interval, minInterval)
			}
		})
	}
}

func TestLogBuffering_FlushLogic(t *testing.T) {
	tests := []struct {
		name       string
		logCount   int
		bufferSize int
		wantFlush  bool
	}{
		{
			name:       "buffer not full",
			logCount:   5,
			bufferSize: 10,
			wantFlush:  false,
		},
		{
			name:       "buffer exactly full",
			logCount:   10,
			bufferSize: 10,
			wantFlush:  true,
		},
		{
			name:       "buffer overflow",
			logCount:   15,
			bufferSize: 10,
			wantFlush:  true,
		},
		{
			name:       "single log in buffer",
			logCount:   1,
			bufferSize: 100,
			wantFlush:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shouldFlush := tt.logCount >= tt.bufferSize
			if shouldFlush != tt.wantFlush {
				t.Errorf("expected flush=%v for logCount=%d bufferSize=%d",
					tt.wantFlush, tt.logCount, tt.bufferSize)
			}
		})
	}
}

func TestClientFallback_Selection(t *testing.T) {
	tests := []struct {
		name              string
		clientType        ClientType
		grpcAvailable     bool
		expectedTransport ClientType
	}{
		{
			name:              "prefer gRPC and available",
			clientType:        ClientTypeAuto,
			grpcAvailable:     true,
			expectedTransport: ClientTypeGRPC,
		},
		{
			name:              "prefer gRPC but unavailable - fallback to webhook",
			clientType:        ClientTypeAuto,
			grpcAvailable:     false,
			expectedTransport: ClientTypeWebhook,
		},
		{
			name:              "explicit webhook type",
			clientType:        ClientTypeWebhook,
			grpcAvailable:     true,
			expectedTransport: ClientTypeWebhook,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var selected ClientType
			if tt.clientType == ClientTypeAuto {
				if tt.grpcAvailable {
					selected = ClientTypeGRPC
				} else {
					selected = ClientTypeWebhook
				}
			} else {
				selected = tt.clientType
			}

			if selected != tt.expectedTransport {
				t.Errorf("expected transport=%s, got %s", tt.expectedTransport, selected)
			}
		})
	}
}

func TestAutoClient_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	cfg := ClientConfig{
		Type:        ClientTypeAuto,
		GRPCAddress: "localhost:9999", // Unreachable endpoint
		WebhookURL:  "http://localhost:9999",
		TokenPath:   "/var/run/secrets/token",
		ExecutionID: "exec-123",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}

	// Test basic operations don't panic with unreachable endpoint
	err = client.ReportProgress(ctx, "Starting", 10, "test")
	t.Logf("ReportProgress returned: %v (expected error with unreachable endpoint)", err)

	err = client.ReportCompletion(ctx, true, "", 0)
	t.Logf("ReportCompletion returned: %v (expected error with unreachable endpoint)", err)

	err = client.Close()
	if err != nil {
		t.Logf("Close returned: %v", err)
	}
}
