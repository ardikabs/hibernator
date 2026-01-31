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

	"github.com/ardikabs/hibernator/pkg/awsutil"
	"github.com/ardikabs/hibernator/pkg/k8sutil"
)

// RestoreData holds restore metadata produced by Shutdown.
type RestoreData struct {
	// Type of the executor that produced this data.
	Type string `json:"type"`
	// Data is the executor-specific restore state.
	Data json.RawMessage `json:"data"`
}

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

// Executor is the interface that all executors must implement.
type Executor interface {
	// Type returns the executor type identifier.
	Type() string

	// Validate validates the executor spec.
	Validate(spec Spec) error

	// Shutdown performs the hibernation operation and returns restore data.
	Shutdown(ctx context.Context, spec Spec) (RestoreData, error)

	// WakeUp performs the restore operation using saved restore data.
	WakeUp(ctx context.Context, spec Spec, restore RestoreData) error
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
