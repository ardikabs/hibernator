/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package v1alpha1 contains the streaming API types for Hibernator.
// These types mirror the protobuf definitions for use without code generation.
package v1alpha1

import "time"

// LogEntry represents a single log line from the runner.
type LogEntry struct {
	// ExecutionID is the unique identifier for this execution.
	ExecutionID string `json:"executionId"`

	// Timestamp is when the log was generated.
	Timestamp time.Time `json:"timestamp"`

	// Level is the log level (DEBUG, INFO, WARN, ERROR).
	Level string `json:"level"`

	// Message is the log content.
	Message string `json:"message"`

	// Fields are structured log fields.
	Fields map[string]string `json:"fields,omitempty"`
}

// StreamLogsResponse is the response after log streaming completes.
type StreamLogsResponse struct {
	// ReceivedCount is the number of log entries received.
	ReceivedCount int64 `json:"receivedCount"`
}

// ProgressReport reports execution progress.
type ProgressReport struct {
	// ExecutionID is the unique identifier for this execution.
	ExecutionID string `json:"executionId"`

	// Phase is the current execution phase.
	Phase string `json:"phase"`

	// ProgressPercent is the estimated progress (0-100).
	ProgressPercent int32 `json:"progressPercent"`

	// Message describes the current activity.
	Message string `json:"message"`

	// Timestamp is when this progress was reported.
	Timestamp time.Time `json:"timestamp"`
}

// ProgressResponse acknowledges a progress report.
type ProgressResponse struct {
	// Acknowledged indicates the progress was received.
	Acknowledged bool `json:"acknowledged"`
}

// CompletionReport reports the final result of an execution.
type CompletionReport struct {
	// ExecutionID is the unique identifier for this execution.
	ExecutionID string `json:"executionId"`

	// Success indicates whether the execution succeeded.
	Success bool `json:"success"`

	// ErrorMessage contains the error if success is false.
	ErrorMessage string `json:"errorMessage,omitempty"`

	// DurationMs is the execution duration in milliseconds.
	DurationMs int64 `json:"durationMs"`

	// RestoreData contains serialized restore metadata (JSON).
	RestoreData []byte `json:"restoreData,omitempty"`

	// Timestamp is when execution completed.
	Timestamp time.Time `json:"timestamp"`
}

// CompletionResponse acknowledges a completion report.
type CompletionResponse struct {
	// Acknowledged indicates the completion was received.
	Acknowledged bool `json:"acknowledged"`

	// RestoreRef is the reference where restore data was stored.
	RestoreRef string `json:"restoreRef,omitempty"`
}

// HeartbeatRequest is a keep-alive message.
type HeartbeatRequest struct {
	// ExecutionID is the unique identifier for this execution.
	ExecutionID string `json:"executionId"`

	// Timestamp is when the heartbeat was sent.
	Timestamp time.Time `json:"timestamp"`
}

// HeartbeatResponse acknowledges a heartbeat.
type HeartbeatResponse struct {
	// Acknowledged indicates the heartbeat was received.
	Acknowledged bool `json:"acknowledged"`

	// ServerTime is the server's current time.
	ServerTime time.Time `json:"serverTime"`
}

// WebhookPayload is the unified payload for webhook callbacks.
type WebhookPayload struct {
	// Type is the payload type: "log", "progress", "completion", "heartbeat".
	Type string `json:"type"`

	// Log is set when Type is "log".
	Log *LogEntry `json:"log,omitempty"`

	// Progress is set when Type is "progress".
	Progress *ProgressReport `json:"progress,omitempty"`

	// Completion is set when Type is "completion".
	Completion *CompletionReport `json:"completion,omitempty"`

	// Heartbeat is set when Type is "heartbeat".
	Heartbeat *HeartbeatRequest `json:"heartbeat,omitempty"`
}

// WebhookResponse is the response from a webhook callback.
type WebhookResponse struct {
	// Acknowledged indicates the webhook was processed.
	Acknowledged bool `json:"acknowledged"`

	// Error contains any error message.
	Error string `json:"error,omitempty"`

	// RestoreRef is set for completion callbacks.
	RestoreRef string `json:"restoreRef,omitempty"`
}
