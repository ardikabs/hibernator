/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package karpenter

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/ardikabs/hibernator/pkg/k8sutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Executor implements hibernation for Karpenter NodePools.
type Executor struct {
	k8sFactory K8sClientFactory
}

// K8sClientFactory is a function type for creating Kubernetes dynamic clients.
type K8sClientFactory func(ctx context.Context, spec *executor.Spec) (K8sClient, error)

// New creates a new Karpenter executor with real Kubernetes clients.
func New() *Executor {
	return &Executor{
		k8sFactory: func(ctx context.Context, spec *executor.Spec) (K8sClient, error) {
			dynamicClient, _, err := k8sutil.BuildClients(ctx, spec.ConnectorConfig.K8S)
			if err != nil {
				return nil, err
			}
			return dynamicClient, nil
		},
	}
}

// NewWithClients creates a new Karpenter executor with injected client factory.
// This is useful for testing with mock clients.
func NewWithClients(k8sFactory K8sClientFactory) *Executor {
	return &Executor{
		k8sFactory: k8sFactory,
	}
}

// Type returns the executor type.
func (e *Executor) Type() string {
	return "karpenter"
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

	// NodePools is optional - empty means all NodePools
	var params executorparams.KarpenterParameters
	if len(spec.Parameters) > 0 {
		if err := json.Unmarshal(spec.Parameters, &params); err != nil {
			return fmt.Errorf("parse parameters: %w", err)
		}
	}

	return nil
}

// Shutdown scales Karpenter NodePools to zero by setting disruption budgets and resource limits.
func (e *Executor) Shutdown(ctx context.Context, spec executor.Spec) (executor.RestoreData, error) {
	var params executorparams.KarpenterParameters
	if len(spec.Parameters) > 0 {
		if err := json.Unmarshal(spec.Parameters, &params); err != nil {
			return executor.RestoreData{}, fmt.Errorf("parse parameters: %w", err)
		}
	}

	// Build clients using injected factory
	dynamicClient, err := e.k8sFactory(ctx, &spec)
	if err != nil {
		return executor.RestoreData{}, fmt.Errorf("build kubernetes client: %w", err)
	}

	// Determine target NodePools
	targetNodePools := params.NodePools
	if len(targetNodePools) == 0 {
		// Empty nodePools means all NodePools in the cluster
		discovered, err := e.listAllNodePools(ctx, dynamicClient)
		if err != nil {
			return executor.RestoreData{}, fmt.Errorf("list all NodePools: %w", err)
		}
		targetNodePools = discovered
	}

	if len(targetNodePools) == 0 {
		return executor.RestoreData{}, fmt.Errorf("no NodePools found in cluster")
	}

	// Store original state
	nodePoolStates := make(map[string]NodePoolState)

	// Process each NodePool
	for _, nodePoolName := range targetNodePools {
		state, err := e.scaleDownNodePool(ctx, dynamicClient, nodePoolName)
		if err != nil {
			return executor.RestoreData{}, fmt.Errorf("scale down NodePool %s: %w", nodePoolName, err)
		}
		nodePoolStates[nodePoolName] = state
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

// WakeUp restores Karpenter NodePools from hibernation.
func (e *Executor) WakeUp(ctx context.Context, spec executor.Spec, restore executor.RestoreData) error {
	if len(restore.Data) == 0 {
		return fmt.Errorf("restore data is required for wake-up")
	}

	var nodePoolStates map[string]NodePoolState
	if err := json.Unmarshal(restore.Data, &nodePoolStates); err != nil {
		return fmt.Errorf("unmarshal restore data: %w", err)
	}

	// Build clients using injected factory
	dynamicClient, err := e.k8sFactory(ctx, &spec)
	if err != nil {
		return fmt.Errorf("build kubernetes client: %w", err)
	}

	// Restore each NodePool
	for nodePoolName, state := range nodePoolStates {
		if err := e.restoreNodePool(ctx, dynamicClient, nodePoolName, state); err != nil {
			return fmt.Errorf("restore NodePool %s: %w", nodePoolName, err)
		}
	}

	return nil
}

// listAllNodePools discovers all Karpenter NodePools in the cluster.
func (e *Executor) listAllNodePools(ctx context.Context, client K8sClient) ([]string, error) {
	nodePoolGVR := schema.GroupVersionResource{
		Group:    "karpenter.sh",
		Version:  "v1",
		Resource: "nodepools",
	}

	list, err := client.Resource(nodePoolGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list NodePools: %w", err)
	}

	var names []string
	for _, item := range list.Items {
		names = append(names, item.GetName())
	}

	return names, nil
}

// NodePoolState stores the original state of a NodePool before hibernation.
type NodePoolState struct {
	Name              string                 `json:"name"`
	DisruptionBudgets interface{}            `json:"disruptionBudgets,omitempty"`
	Limits            map[string]interface{} `json:"limits,omitempty"`
}

// scaleDownNodePool scales a NodePool to prevent new nodes from being created.
func (e *Executor) scaleDownNodePool(ctx context.Context, client K8sClient, nodePoolName string) (NodePoolState, error) {
	nodePoolGVR := schema.GroupVersionResource{
		Group:    "karpenter.sh",
		Version:  "v1",
		Resource: "nodepools",
	}

	// Get the NodePool
	nodePool, err := client.Resource(nodePoolGVR).Get(ctx, nodePoolName, metav1.GetOptions{})
	if err != nil {
		return NodePoolState{}, fmt.Errorf("get NodePool: %w", err)
	}

	// Save original state
	spec, found, err := unstructured.NestedMap(nodePool.Object, "spec")
	if err != nil || !found {
		return NodePoolState{}, fmt.Errorf("get NodePool spec: %w", err)
	}

	state := NodePoolState{Name: nodePoolName}

	// Save disruption budgets
	if budgets, found, _ := unstructured.NestedFieldCopy(spec, "disruption"); found {
		state.DisruptionBudgets = budgets
	}

	// Save limits
	if limits, found, _ := unstructured.NestedMap(spec, "limits"); found {
		state.Limits = limits
	}

	// Update NodePool to prevent new nodes: set limits to zero
	if err := unstructured.SetNestedMap(nodePool.Object, map[string]interface{}{
		"cpu":    "0",
		"memory": "0Gi",
	}, "spec", "limits", "resources"); err != nil {
		return NodePoolState{}, fmt.Errorf("set resource limits: %w", err)
	}

	// Update the NodePool
	if _, err := client.Resource(nodePoolGVR).Update(ctx, nodePool, metav1.UpdateOptions{}); err != nil {
		return NodePoolState{}, fmt.Errorf("update NodePool: %w", err)
	}

	return state, nil
}

// restoreNodePool restores a NodePool to its original configuration.
func (e *Executor) restoreNodePool(ctx context.Context, client K8sClient, nodePoolName string, state NodePoolState) error {
	nodePoolGVR := schema.GroupVersionResource{
		Group:    "karpenter.sh",
		Version:  "v1",
		Resource: "nodepools",
	}

	// Get the current NodePool
	nodePool, err := client.Resource(nodePoolGVR).Get(ctx, nodePoolName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get NodePool: %w", err)
	}

	// Restore disruption budgets
	if state.DisruptionBudgets != nil {
		if err := unstructured.SetNestedField(nodePool.Object, state.DisruptionBudgets, "spec", "disruption"); err != nil {
			return fmt.Errorf("restore disruption: %w", err)
		}
	}

	// Restore limits
	if state.Limits != nil {
		if err := unstructured.SetNestedMap(nodePool.Object, state.Limits, "spec", "limits"); err != nil {
			return fmt.Errorf("restore limits: %w", err)
		}
	}

	// Update the NodePool
	if _, err := client.Resource(nodePoolGVR).Update(ctx, nodePool, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update NodePool: %w", err)
	}

	return nil
}
