/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package executor defines the executor interface and registry.
package executor

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/go-logr/logr"

	"github.com/ardikabs/hibernator/pkg/awsutil"
	"github.com/ardikabs/hibernator/pkg/k8sutil"
)

// RestoreData holds restore metadata produced by Shutdown.
type RestoreData struct {
	// Type of the executor that produced this data.
	Type string `json:"type"`

	// Data is a unified map of resource key → resource state (as JSON).
	// Keys are executor-specific:
	// - EC2: instanceID (e.g., "i-1234567890abcdef0")
	// - EKS: nodeGroupName (e.g., "ng-1")
	// - Karpenter: nodePoolName (e.g., "default")
	// - WorkloadScaler: namespace/kind/name (e.g., "team-a/Deployment/api")
	// - RDS: instanceID or clusterID with prefix (e.g., "instance:my-db", "cluster:my-cluster")
	// - Noop: operation ID (e.g., "noop-12345")
	Data map[string]json.RawMessage `json:"data"`

	// IsLive indicates whether data was captured from actual resource state via API (true)
	// or is cached/unknown state (false). When true, hibernator has direct knowledge of the
	// resource state from a fresh API call. When false, the data may be stale or from an
	// incomplete lifecycle. High-quality data (IsLive=true) is preserved over low-quality
	// data (IsLive=false) during save operations.
	IsLive bool `json:"isLive"`
}

// SaveRestoreDataFunc is a callback for incremental restore data persistence.
// Executors can call this after each successful sub-resource operation to save
// restore data incrementally, preventing data loss on partial failures.
// Parameters:
//
//	key: Resource-specific key (e.g., instanceID, nodeGroupName)
//	value: Resource state (will be JSON-marshaled by callback implementation)
//	isLive: Whether data was captured from actual resource state via API (true) or is
//	        cached/unknown (false). True indicates hibernator directly observed the
//	        resource state, false indicates data may be stale or from incomplete lifecycle.
type SaveRestoreDataFunc func(key string, value interface{}, isLive bool) error

// Spec holds target execution parameters.
type Spec struct {
	// TargetName is the name of the target.
	TargetName string
	// TargetType is the type of the target (eks, rds, ec2).
	TargetType string
	// Parameters is the executor-specific configuration.
	Parameters json.RawMessage
	// ConnectorConfig holds resolved connector configuration.
	ConnectorConfig ConnectorConfig
	// SaveRestoreData is an optional callback for incremental persistence.
	// If provided, executors should call this after each successful sub-resource
	// operation to enable partial-success data preservation.
	SaveRestoreData SaveRestoreDataFunc
}

// ConnectorConfig holds resolved connector settings.
type ConnectorConfig struct {
	// AWS holds AWS-specific configuration.
	AWS *AWSConnectorConfig
	// K8S holds Kubernetes-specific configuration.
	K8S *K8SConnectorConfig
}

// AWSConnectorConfig holds AWS connector settings.
type AWSConnectorConfig = awsutil.AWSConnectorConfig

// K8SConnectorConfig holds Kubernetes connector settings.
type K8SConnectorConfig = k8sutil.K8SConnectorConfig

// Result holds the outcome of an executor operation.
type Result struct {
	// Message is a human-readable summary of what the executor did.
	// For successful operations, this describes the action taken (e.g., "scaled 3 node groups to zero").
	// For failed operations, the error message is used instead.
	Message string

	// ElapsedMs is the wall-clock execution time in milliseconds.
	// This field is populated by the runner after the executor returns;
	// executor implementations should leave it at zero.
	ElapsedMs int64
}

// Executor is the interface that all executors must implement.
type Executor interface {
	// Type returns the executor type identifier.
	Type() string

	// Validate validates the executor spec.
	Validate(spec Spec) error

	// Shutdown performs the hibernation operation.
	// Restore data should be saved incrementally via spec.SaveRestoreData callback.
	// Returns a Result with a summary message on success.
	Shutdown(ctx context.Context, log logr.Logger, spec Spec) (*Result, error)

	// WakeUp performs the restore operation using saved restore data.
	// Returns a Result with a summary message on success.
	WakeUp(ctx context.Context, log logr.Logger, spec Spec, restore RestoreData) (*Result, error)
}

// Registry holds registered executors.
type Registry struct {
	mu        sync.RWMutex
	executors map[string]Executor
}

// NewRegistry creates a new executor registry.
func NewRegistry() *Registry {
	return &Registry{
		executors: make(map[string]Executor),
	}
}

// Register adds an executor to the registry.
func (r *Registry) Register(executor Executor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.executors[executor.Type()] = executor
}

// Get retrieves an executor by type.
func (r *Registry) Get(executorType string) (Executor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.executors[executorType]
	return e, ok
}

// List returns all registered executor types.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	types := make([]string, 0, len(r.executors))
	for t := range r.executors {
		types = append(types, t)
	}
	return types
}

// DefaultRegistry is the global executor registry.
var DefaultRegistry = NewRegistry()

// Register adds an executor to the default registry.
func Register(executor Executor) {
	DefaultRegistry.Register(executor)
}

// Get retrieves an executor from the default registry.
func Get(executorType string) (Executor, bool) {
	return DefaultRegistry.Get(executorType)
}
