// Package types contains internal streaming types with idiomatic Go naming.
// These types are used throughout the application and converted to/from
// proto types at the gRPC boundary.
package types

import (
	"time"

	streamingv1alpha1 "github.com/ardikabs/hibernator/api/streaming/v1alpha1"
)

// LogEntry represents a single log line from the runner (internal representation).
type LogEntry struct {
	ExecutionID string            `json:"executionId"`
	Timestamp   time.Time         `json:"timestamp"`
	Level       string            `json:"level"`
	Message     string            `json:"message"`
	Fields      map[string]string `json:"fields,omitempty"`
}

// ToProto converts internal LogEntry to proto LogEntry.
func (e *LogEntry) ToProto() *streamingv1alpha1.LogEntry {
	return &streamingv1alpha1.LogEntry{
		ExecutionId: e.ExecutionID,
		Timestamp:   e.Timestamp.Format(time.RFC3339),
		Level:       e.Level,
		Message:     e.Message,
		Fields:      e.Fields,
	}
}

// FromProtoLogEntry converts proto LogEntry to internal LogEntry.
func FromProtoLogEntry(p *streamingv1alpha1.LogEntry) (*LogEntry, error) {
	ts, err := time.Parse(time.RFC3339, p.Timestamp)
	if err != nil {
		ts = time.Now() // Fallback to current time
	}
	return &LogEntry{
		ExecutionID: p.ExecutionId,
		Timestamp:   ts,
		Level:       p.Level,
		Message:     p.Message,
		Fields:      p.Fields,
	}, nil
}

// StreamLogsResponse is the response after log streaming completes.
type StreamLogsResponse struct {
	ReceivedCount int64 `json:"receivedCount"`
}

// ProgressReport reports execution progress (internal representation).
type ProgressReport struct {
	ExecutionID     string    `json:"executionId"`
	Phase           string    `json:"phase"`
	ProgressPercent int32     `json:"progressPercent"`
	Message         string    `json:"message"`
	Timestamp       time.Time `json:"timestamp"`
}

// ToProto converts internal ProgressReport to proto ProgressReport.
func (p *ProgressReport) ToProto() *streamingv1alpha1.ProgressReport {
	return &streamingv1alpha1.ProgressReport{
		ExecutionId:     p.ExecutionID,
		Phase:           p.Phase,
		ProgressPercent: p.ProgressPercent,
		Message:         p.Message,
		Timestamp:       p.Timestamp.Format(time.RFC3339),
	}
}

// ProgressResponse acknowledges a progress report.
type ProgressResponse struct {
	Acknowledged bool `json:"acknowledged"`
}

// CompletionReport reports the final result of an execution (internal representation).
type CompletionReport struct {
	ExecutionID  string    `json:"executionId"`
	Success      bool      `json:"success"`
	ErrorMessage string    `json:"errorMessage,omitempty"`
	DurationMs   int64     `json:"durationMs"`
	RestoreData  []byte    `json:"restoreData,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
}

// ToProto converts internal CompletionReport to proto CompletionReport.
func (c *CompletionReport) ToProto() *streamingv1alpha1.CompletionReport {
	return &streamingv1alpha1.CompletionReport{
		ExecutionId:  c.ExecutionID,
		Success:      c.Success,
		ErrorMessage: c.ErrorMessage,
		DurationMs:   c.DurationMs,
		RestoreData:  c.RestoreData,
		Timestamp:    c.Timestamp.Format(time.RFC3339),
	}
}

// CompletionResponse acknowledges a completion report.
type CompletionResponse struct {
	Acknowledged bool   `json:"acknowledged"`
	RestoreRef   string `json:"restoreRef,omitempty"`
}

// HeartbeatRequest is a keep-alive message (internal representation).
type HeartbeatRequest struct {
	ExecutionID string    `json:"executionId"`
	Timestamp   time.Time `json:"timestamp"`
}

// ToProto converts internal HeartbeatRequest to proto HeartbeatRequest.
func (h *HeartbeatRequest) ToProto() *streamingv1alpha1.HeartbeatRequest {
	return &streamingv1alpha1.HeartbeatRequest{
		ExecutionId: h.ExecutionID,
		Timestamp:   h.Timestamp.Format(time.RFC3339),
	}
}

// HeartbeatResponse acknowledges a heartbeat.
type HeartbeatResponse struct {
	Acknowledged bool      `json:"acknowledged"`
	ServerTime   time.Time `json:"serverTime"`
}

// WebhookPayload is the unified payload for webhook/HTTP callback.
type WebhookPayload struct {
	Type       string             `json:"type"`
	Log        *LogEntry          `json:"log,omitempty"`
	Progress   *ProgressReport    `json:"progress,omitempty"`
	Completion *CompletionReport  `json:"completion,omitempty"`
	Heartbeat  *HeartbeatRequest  `json:"heartbeat,omitempty"`
}

// WebhookResponse is the response from a webhook callback.
type WebhookResponse struct {
	Acknowledged bool   `json:"acknowledged"`
	RestoreRef   string `json:"restoreRef,omitempty"`
	Error        string `json:"error,omitempty"`
}
