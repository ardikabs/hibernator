/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package server

import (
	"encoding/json"
	"testing"
	"time"

	streamingv1alpha1 "github.com/ardikabs/hibernator/api/streaming/v1alpha1"
)

func TestLogEntryPayload(t *testing.T) {
	entry := streamingv1alpha1.LogEntry{
		ExecutionID: "exec-123",
		Timestamp:   time.Now(),
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
	report := streamingv1alpha1.ProgressReport{
		ExecutionID:     "exec-123",
		Phase:           "Executing",
		ProgressPercent: 50,
		Message:         "Half done",
		Timestamp:       time.Now(),
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
	report := streamingv1alpha1.CompletionReport{
		ExecutionID: "exec-123",
		Success:     true,
		DurationMs:  5000,
		RestoreData: []byte(`{"key": "value"}`),
		Timestamp:   time.Now(),
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
		entry := streamingv1alpha1.LogEntry{
			ExecutionID: "exec-123",
			Level:       level,
			Message:     "Test",
			Timestamp:   time.Now(),
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
