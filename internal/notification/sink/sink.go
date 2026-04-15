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

	"github.com/go-logr/logr"
)

// PlanInfo carries plan metadata inside the Payload.
type PlanInfo struct {
	// Name represents the unique identifier of the plan.
	Name string
	// Namespace defines the scope, environment, or isolation boundary
	// where the plan belongs.
	Namespace string
	// Labels represents the HibernatePlan's labels, which are key-value pairs.
	Labels map[string]string
	// Annotations represents the HibernatePlan's annotations, which are key-value pairs.
	Annotations map[string]string
}

func (p PlanInfo) String() string {
	return fmt.Sprintf("%s/%s", p.Namespace, p.Name)
}

// TargetInfo holds execution state for a single target.
type TargetInfo struct {
	// Name is the target name.
	Name string

	// Executor is the executor type (e.g., "rds", "eks").
	Executor string

	// State is the execution state (e.g., "Completed", "Failed").
	State string

	// Message provides details for the target's execution state.
	Message string

	// Connector carries resolved metadata from the target's CloudProvider or
	// K8SCluster reference. Template authors access fields like
	// {{ .Connector.AccountID }} or {{ .Connector.ClusterName }}.
	Connector ConnectorInfo
}

// ConnectorInfo carries resolved connector metadata for template rendering.
// Fields are populated based on the connector kind — unused fields remain
// zero-valued. Template authors can use any combination of fields without
// branching on Kind.
type ConnectorInfo struct {
	// Kind is the connector type: "CloudProvider" or "K8SCluster".
	Kind string

	// Name is the connector resource name.
	Name string

	// Provider is the cloud provider type (e.g., "aws", "gcp").
	Provider string

	// AccountID is the cloud account identifier, it is relevant for AWS cloud provider.
	AccountID string

	// ProjectID is the cloud project identifier, it is relevant for GCP cloud provider.
	ProjectID string

	// Region is the cloud region (e.g., "us-east-1", "us-central1").
	Region string

	// ClusterName is the Kubernetes cluster name (populated for K8SCluster
	// connectors that reference EKS/GKE).
	ClusterName string
}

// Payload carries structured notification event data to a Sink.
// Well-established sinks (Slack, Telegram) render this into a formatted message;
// generic sinks (webhook) can forward it as raw JSON.
//
// The Plan field carries metadata derived from the actual HibernatePlan resource
// (including status). The remaining top-level fields act as a snapshot of the
// event — they may duplicate Plan values but reflect the exact state at the
// moment of notification dispatch.
type Payload struct {
	// Plan carries plan metadata (name, namespace, labels, status fields).
	Plan PlanInfo `json:"plan"`

	// Event is the hook point that triggered this notification (e.g., "Start", "Failure").
	Event string `json:"event"`

	// Timestamp is when the event occurred.
	Timestamp time.Time `json:"timestamp"`

	// Phase is the plan phase after the transition.
	Phase string `json:"phase"`

	// PreviousPhase is the plan phase before the transition (empty on Start).
	PreviousPhase string `json:"previousPhase"`

	// Operation is the current operation: "shutdown" or "wakeup".
	Operation string `json:"operation"`

	// CycleID is the current execution cycle identifier.
	CycleID string `json:"cycleId"`

	// Targets holds per-target execution state (available on Success/Failure).
	Targets []TargetInfo `json:"targets"`

	// TargetExecution holds the individual target whose execution state just
	// changed. Populated only for ExecutionProgress events; nil for plan-level
	// events (Start, Success, Failure, Recovery, PhaseChange).
	TargetExecution *TargetInfo `json:"targetExecution,omitempty"`

	// ErrorMessage provides error details (Failure/Recovery only).
	ErrorMessage string `json:"errorMessage"`

	// RetryCount is the current retry attempt number (Recovery/Failure only).
	RetryCount int32 `json:"retryCount"`

	// SinkName is the human-readable name of the sink being dispatched to.
	SinkName string `json:"sinkName"`

	// SinkType is the sink provider type (e.g., "slack", "telegram", "webhook").
	SinkType string `json:"sinkType"`
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
	Send(ctx context.Context, payload Payload, opts SendOptions) (SendResult, error)
}

// SendResult contains sink-specific delivery metadata captured during send.
// Fields are optional; zero value means no additional metadata.
type SendResult struct {
	// States carries sink-specific arbitrary key/value context emitted by sink
	// delivery implementations (for example thread identifiers).
	States map[string]string
}

// SendOptions carries the resolved sink configuration for a single dispatch.
// The dispatcher resolves external references (Secret, ConfigMap) and passes
// only the raw data here — sinks that need template rendering own their
// Renderer instance and invoke it internally.
type SendOptions struct {
	// Config is the raw content of the Secret's "config" key.
	// Each sink implementation defines and parses its own JSON schema from these bytes.
	Config []byte

	// CustomTemplate optionally references a user-provided Go template loaded
	// from a ConfigMap. When set, sinks should pass it to Renderer via
	// WithCustomTemplate so the engine uses it instead of the built-in default.
	CustomTemplate *CustomTemplate

	// SinkState carries sink-specific state restored from HibernateNotification
	// status tracked states for the current sink+plan+cycle+operation key.
	SinkState map[string]string

	// Log is the per-send logger scoped by the dispatcher/caller.
	// It is optional; sinks should handle zero-value logger gracefully.
	Log logr.Logger
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
