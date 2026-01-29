/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package gke

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ardikabs/hibernator/internal/executor"
)

// Executor implements hibernation for GKE node pools.
type Executor struct{}

// New creates a new GKE executor.
func New() *Executor {
	return &Executor{}
}

// Type returns the executor type.
func (e *Executor) Type() string {
	return "gke"
}

// Validate validates the executor spec.
func (e *Executor) Validate(spec executor.Spec) error {
	if spec.ConnectorConfig.K8S == nil {
		return fmt.Errorf("K8S connector config is required")
	}
	if spec.ConnectorConfig.K8S.ClusterName == "" {
		return fmt.Errorf("cluster name is required")
	}
	if spec.ConnectorConfig.K8S.Region == "" {
		return fmt.Errorf("region is required")
	}

	var params struct {
		NodePools []string `json:"nodePools"`
	}
	if err := json.Unmarshal(spec.Parameters, &params); err != nil {
		return fmt.Errorf("parse parameters: %w", err)
	}

	if len(params.NodePools) == 0 {
		return fmt.Errorf("at least one NodePool must be specified")
	}

	return nil
}

// Shutdown scales GKE node pools to zero.
func (e *Executor) Shutdown(ctx context.Context, spec executor.Spec) (executor.RestoreData, error) {
	var params struct {
		NodePools []string `json:"nodePools"`
	}
	if err := json.Unmarshal(spec.Parameters, &params); err != nil {
		return executor.RestoreData{}, fmt.Errorf("parse parameters: %w", err)
	}

	// Store original state
	nodePoolStates := make(map[string]NodePoolState)

	// TODO: Implement actual GKE API calls using google.golang.org/api/container/v1
	// For now, return a placeholder implementation
	for _, npName := range params.NodePools {
		nodePoolStates[npName] = NodePoolState{
			Name:         npName,
			NodeCount:    0, // Would be fetched from GKE API
			MinNodeCount: 0,
			MaxNodeCount: 0,
		}
	}

	// Build restore data
	stateBytes, err := json.Marshal(nodePoolStates)
	if err != nil {
		return executor.RestoreData{}, fmt.Errorf("marshal restore data: %w", err)
	}

	return executor.RestoreData{
		Type: e.Type(),
		Data: stateBytes,
	}, nil
}

// WakeUp restores GKE node pools from hibernation.
func (e *Executor) WakeUp(ctx context.Context, spec executor.Spec, restore executor.RestoreData) error {
	if len(restore.Data) == 0 {
		return fmt.Errorf("restore data is required for wake-up")
	}

	var nodePoolStates map[string]NodePoolState
	if err := json.Unmarshal(restore.Data, &nodePoolStates); err != nil {
		return fmt.Errorf("unmarshal restore data: %w", err)
	}

	// TODO: Implement actual GKE API calls to restore node pools
	// For now, this is a placeholder
	_ = nodePoolStates

	return nil
}

// NodePoolState stores the original state of a GKE NodePool.
type NodePoolState struct {
	Name         string `json:"name"`
	NodeCount    int    `json:"nodeCount"`
	MinNodeCount int    `json:"minNodeCount"`
	MaxNodeCount int    `json:"maxNodeCount"`
}
