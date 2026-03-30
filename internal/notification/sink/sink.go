/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package sink defines the contract between the notification dispatcher and
// external delivery backends. Every concrete sink (Slack, Telegram, ...) lives in
// its own sub-package and implements the [Sink] interface.
package sink

import (
	"context"
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"
)

// PlanInfo carries plan metadata inside the Payload.
type PlanInfo struct {
	Name      string
	Namespace string
	Labels    map[string]string
}

// TargetInfo holds execution state for a single target.
type TargetInfo struct {
	// Name is the target name.
	Name string

	// Executor is the executor type (e.g., "rds", "eks").
	Executor string

	// State is the execution state (e.g., "Completed", "Failed").
	State string

	// ErrorMessage provides error details for failed targets.
	ErrorMessage string
}

// Payload carries structured notification event data to a Sink.
// Well-established sinks (Slack, Telegram) render this into a formatted message;
// generic sinks (webhook) can forward it as raw JSON.
type Payload struct {
	// ID represents the identifier of the associated object.
	ID types.NamespacedName

	// Labels are the labels of the associated object.
	Labels map[string]string

	// Event is the hook point that triggered this notification (e.g., "Start", "Failure").
	Event string

	// Timestamp is when the event occurred.
	Timestamp time.Time

	// Phase is the plan phase after the transition.
	Phase string

	// PreviousPhase is the plan phase before the transition (empty on Start).
	PreviousPhase string

	// Operation is the current operation: "Hibernate" or "WakeUp".
	Operation string

	// CycleID is the current execution cycle identifier.
	CycleID string

	// Targets holds per-target execution state (available on Success/Failure).
	Targets []TargetInfo

	// ErrorMessage provides error details (Failure/Recovery only).
	ErrorMessage string

	// RetryCount is the current retry attempt number (Recovery/Failure only).
	RetryCount int32

	// SinkName is the human-readable name of the sink being dispatched to.
	SinkName string

	// SinkType is the sink provider type (e.g., "slack", "telegram", "webhook").
	SinkType string
}

// Sink is the interface that all notification sink providers must implement.
// Each sink is responsible for delivering a notification payload to a specific
// external system (e.g., Slack, Telegram, generic webhook).
//
// Implementations must be safe for concurrent use from multiple goroutines.
type Sink interface {
	// Type returns the sink type identifier (e.g., "slack", "telegram", "webhook").
	// Must match the NotificationSinkType enum value.
	Type() string

	// Send delivers a notification payload to the external system.
	// The payload carries the structured event data. Each sink decides how to
	// format the payload — well-established sinks (Slack, Telegram) use their
	// injected Renderer to apply built-in or custom templates, while generic
	// sinks (webhook) may forward the raw payload as JSON.
	//
	// Implementations must respect ctx cancellation and should use short HTTP timeouts.
	// Errors are logged by the dispatcher but never propagate to the reconciler.
	Send(ctx context.Context, payload Payload, opts SendOptions) error
}

// SendOptions carries the resolved sink configuration for a single dispatch.
// The dispatcher resolves external references (Secret, ConfigMap) and passes
// only the raw data here — sinks that need template rendering own their
// Renderer instance and invoke it internally.
type SendOptions struct {
	// Config is the raw content of the Secret's "config" key.
	// Each sink implementation defines and parses its own JSON schema from these bytes.
	Config []byte

	// CustomTemplate optionally references a user-provided Go template string
	// loaded from a ConfigMap. When set, sinks that support template rendering
	// should prefer this over their built-in default template.
	CustomTemplate *string
}

// Registry holds registered notification sinks.
type Registry struct {
	mu    sync.RWMutex
	sinks map[string]Sink
}

// NewRegistry creates a new sink registry.
func NewRegistry() *Registry {
	return &Registry{
		sinks: make(map[string]Sink),
	}
}

// Register adds a sink to the registry.
func (r *Registry) Register(s Sink) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sinks[s.Type()] = s
}

// Get retrieves a sink by type.
func (r *Registry) Get(sinkType string) (Sink, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sinks[sinkType]
	return s, ok
}

// List returns all registered sink types.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	types := make([]string, 0, len(r.sinks))
	for t := range r.sinks {
		types = append(types, t)
	}
	return types
}

// Validate checks that all required sinks are registered for the given types.
func (r *Registry) Validate(sinkTypes []string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, t := range sinkTypes {
		if _, ok := r.sinks[t]; !ok {
			return fmt.Errorf("sink type %q is not registered", t)
		}
	}
	return nil
}
