/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package webhook

import (
	"time"

	"k8s.io/apimachinery/pkg/types"

	"github.com/ardikabs/hibernator/internal/notification/sink"
)

// DefaultTemplate is the built-in Go template for webhook notifications.
// It produces a concise one-line summary suitable for generic integrations.
var DefaultTemplate = `[{{ .Event }}] {{ .Operation }} — {{ .Plan.Namespace }}/{{ .Plan.Name }} | Phase: {{ .Phase }}{{ if .ErrorMessage }} | Error: {{ .ErrorMessage }}{{ end }}`

// config is the expected JSON schema for the Secret's "config" key.
type config struct {
	// URL is the endpoint to POST notifications to.
	URL string `json:"url"`
	// Headers are additional HTTP headers to include in the request.
	Headers map[string]string `json:"headers,omitempty"`
	// EnableRenderer when true, renders the payload through the template engine
	// and includes the result in the "rendered" field of the JSON body.
	EnableRenderer bool `json:"enable_renderer,omitempty"`
}

// webhookBody is the JSON body sent to the webhook endpoint.
type webhookBody struct {
	// Context carries the structured notification event data as a webhook-specific DTO.
	Context webhookContext `json:"context"`
	// Rendered is the template-rendered message string.
	// Omitted when `enable_renderer` is false or unset.
	Rendered string `json:"rendered,omitempty"`
}

// webhookContext is the DTO representation of notification event data
// for webhook JSON payloads. It mirrors sink.Payload with explicit JSON
// tags on all nested types for clean, predictable serialization.
type webhookContext struct {
	// ID represents the identifier of the associated object.
	ID types.NamespacedName `json:"id"`
	// Labels are the labels of the associated object.
	Labels map[string]string `json:"labels,omitempty"`
	// Event is the hook point that triggered this notification (e.g., "Start", "Failure").
	Event string `json:"event"`
	// Timestamp is when the event occurred.
	Timestamp time.Time `json:"timestamp"`
	// Phase is the plan phase after the transition.
	Phase string `json:"phase"`
	// PreviousPhase is the plan phase before the transition (empty on Start).
	PreviousPhase string `json:"previousPhase,omitempty"`
	// Operation is the current operation: "Hibernate" or "WakeUp".
	Operation string `json:"operation"`
	// CycleID is the current execution cycle identifier.
	CycleID string `json:"cycleId"`
	// Targets holds per-target execution state (available on Success/Failure).
	Targets []webhookTargetInfo `json:"targets,omitempty"`
	// ErrorMessage provides error details (Failure/Recovery only).
	ErrorMessage string `json:"errorMessage,omitempty"`
	// RetryCount is the current retry attempt number (Recovery/Failure only).
	RetryCount int32 `json:"retryCount,omitempty"`
	// SinkName is the human-readable name of the sink being dispatched to.
	SinkName string `json:"sinkName"`
	// SinkType is the sink provider type (e.g., "slack", "telegram", "webhook").
	SinkType string `json:"sinkType"`
}

// webhookTargetInfo is the DTO representation of a target's execution state
// for webhook JSON payloads. It mirrors sink.TargetInfo with explicit JSON tags.
type webhookTargetInfo struct {
	// Name is the target name.
	Name string `json:"name"`
	// Executor is the executor type (e.g., "rds", "eks").
	Executor string `json:"executor"`
	// State is the execution state (e.g., "Completed", "Failed").
	State string `json:"state"`
	// Message provides details for the target's execution state.
	Message string `json:"message,omitempty"`
}

// toWebhookContext converts a sink.Payload to the webhook-specific DTO.
func toWebhookContext(p sink.Payload) webhookContext {
	targets := make([]webhookTargetInfo, len(p.Targets))
	for i, t := range p.Targets {
		targets[i] = webhookTargetInfo{
			Name:     t.Name,
			Executor: t.Executor,
			State:    t.State,
			Message:  t.Message,
		}
	}
	return webhookContext{
		ID:            p.ID,
		Labels:        p.Labels,
		Event:         p.Event,
		Timestamp:     p.Timestamp,
		Phase:         p.Phase,
		PreviousPhase: p.PreviousPhase,
		Operation:     p.Operation,
		CycleID:       p.CycleID,
		Targets:       targets,
		ErrorMessage:  p.ErrorMessage,
		RetryCount:    p.RetryCount,
		SinkName:      p.SinkName,
		SinkType:      p.SinkType,
	}
}
