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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/log"

	streamingv1alpha1 "github.com/ardikabs/hibernator/api/streaming/v1alpha1"
	"github.com/ardikabs/hibernator/internal/streaming/auth"
)

func TestLogEntryPayload(t *testing.T) {
	entry := &streamingv1alpha1.LogEntry{
		ExecutionId: "exec-123",
		Timestamp:   time.Now().Format(time.RFC3339),
		Level:       "INFO",
		Message:     "Test message",
		Fields:      map[string]string{"key": "value"},
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty data")
	}
}

func TestProgressReportPayload(t *testing.T) {
	report := &streamingv1alpha1.ProgressReport{
		ExecutionId:     "exec-123",
		Phase:           "Executing",
		ProgressPercent: 50,
		Message:         "Half done",
		Timestamp:       time.Now().Format(time.RFC3339),
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty data")
	}
}

func TestCompletionReportPayload(t *testing.T) {
	report := &streamingv1alpha1.CompletionReport{
		ExecutionId: "exec-123",
		Success:     true,
		DurationMs:  5000,
		Timestamp:   time.Now().Format(time.RFC3339),
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty data")
	}
}

func TestLogEntry_Levels(t *testing.T) {
	levels := []string{"DEBUG", "INFO", "WARN", "ERROR"}
	for _, level := range levels {
		entry := &streamingv1alpha1.LogEntry{
			ExecutionId: "exec-123",
			Level:       level,
			Message:     "Test",
			Timestamp:   time.Now().Format(time.RFC3339),
		}
		if entry.Level != level {
			t.Errorf("expected level %s", level)
		}
	}
}

func TestStreamLogsResponse(t *testing.T) {
	resp := streamingv1alpha1.StreamLogsResponse{
		ReceivedCount: 100,
	}
	if resp.ReceivedCount != 100 {
		t.Errorf("expected 100, got %d", resp.ReceivedCount)
	}
}

func TestProgressResponse(t *testing.T) {
	resp := streamingv1alpha1.ProgressResponse{
		Acknowledged: true,
	}
	if !resp.Acknowledged {
		t.Error("expected acknowledged")
	}
}

func TestValidateExecutionAccess(t *testing.T) {
	logger := log.Log

	tests := []struct {
		name        string
		result      *auth.ValidationResult
		executionID string
		cacheMeta   *ExecutionMetadata
		wantErr     bool
		errContains string
	}{
		{
			name: "valid - matching namespace",
			result: &auth.ValidationResult{
				Valid:          true,
				Namespace:      "test-ns",
				ServiceAccount: "runner-sa",
			},
			executionID: "exec-123",
			cacheMeta: &ExecutionMetadata{
				Namespace:   "test-ns",
				PlanName:    "test-plan",
				TargetName:  "test-target",
				ExecutionID: "exec-123",
			},
			wantErr: false,
		},
		{
			name: "invalid - namespace mismatch",
			result: &auth.ValidationResult{
				Valid:          true,
				Namespace:      "other-ns",
				ServiceAccount: "runner-sa",
			},
			executionID: "exec-123",
			cacheMeta: &ExecutionMetadata{
				Namespace:   "test-ns",
				PlanName:    "test-plan",
				TargetName:  "test-target",
				ExecutionID: "exec-123",
			},
			wantErr:     true,
			errContains: "namespace mismatch",
		},
		{
			name: "invalid - token not valid",
			result: &auth.ValidationResult{
				Valid: false,
			},
			executionID: "exec-123",
			cacheMeta:   nil, // Not needed for this case
			wantErr:     true,
			errContains: "invalid token",
		},
		{
			name: "valid - unknown metadata allows first-time access",
			result: &auth.ValidationResult{
				Valid:          true,
				Namespace:      "test-ns",
				ServiceAccount: "runner-sa",
			},
			executionID: "exec-unknown", // No metadata cached, and k8sClient is nil
			cacheMeta:   nil,            // Will get "unknown" namespace from fallback, which is allowed
			wantErr:     false,          // Should pass since "unknown" namespace is permitted
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			execService := NewExecutionServiceServer(nil, nil, clk)

			// Pre-cache metadata if provided
			if tt.cacheMeta != nil {
				execService.metadataCacheMu.Lock()
				execService.metadataCache[tt.executionID] = tt.cacheMeta
				execService.metadataCacheMu.Unlock()
			}

			ws := &WebhookServer{
				execService: execService,
				log:         logger,
			}

			err := ws.validateExecutionAccess(context.Background(), tt.result, tt.executionID)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}
