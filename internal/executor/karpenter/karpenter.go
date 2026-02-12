/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package karpenter

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/go-logr/logr"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/ardikabs/hibernator/pkg/k8sutil"
	"github.com/ardikabs/hibernator/pkg/waiter"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	ExecutorType       = "karpenter"
	DefaultWaitTimeout = "5m"
)

// Executor implements hibernation for Karpenter NodePools.
type Executor struct {
	clientFactory ClientFactory

	waitinglist  []string
	completionWg sync.WaitGroup
}

// ClientFactory is a function type for creating Kubernetes clients.
type ClientFactory func(ctx context.Context, spec *executor.Spec) (Client, error)

// New creates a new Karpenter executor with real Kubernetes clients.
func New() *Executor {
	return &Executor{
		clientFactory: func(ctx context.Context, spec *executor.Spec) (Client, error) {
			dynamic, typed, err := k8sutil.BuildClients(ctx, spec.ConnectorConfig.K8S)
			if err != nil {
				return nil, err
			}

			return &client{
				Dynamic: dynamic,
				Typed:   typed,
			}, nil
		},
	}
}

// NewWithClients creates a new Karpenter executor with injected client factory.
// This is useful for testing with mock clients.
func NewWithClients(clientFactory ClientFactory) *Executor {
	return &Executor{
		clientFactory: clientFactory,
	}
}

// Type returns the executor type.
func (e *Executor) Type() string {
	return ExecutorType
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
func (e *Executor) Shutdown(ctx context.Context, log logr.Logger, spec executor.Spec) error {
	log.Info("Karpenter executor starting shutdown",
		"target", spec.TargetName,
		"targetType", spec.TargetType,
	)

	var params executorparams.KarpenterParameters
	if len(spec.Parameters) > 0 {
		if err := json.Unmarshal(spec.Parameters, &params); err != nil {
			log.Error(err, "failed to parse parameters")
			return fmt.Errorf("parse parameters: %w", err)
		}
	}

	log.Info("parameters parsed",
		"nodePoolCount", len(params.NodePools),
		"isAllNodePools", len(params.NodePools) == 0,
	)

	// Build clients using injected factory
	client, err := e.clientFactory(ctx, &spec)
	if err != nil {
		log.Error(err, "failed to build kubernetes client")
		return fmt.Errorf("build kubernetes client: %w", err)
	}

	// Determine target NodePools
	targetNodePools := params.NodePools
	if len(targetNodePools) == 0 {
		// Empty nodePools means all NodePools in the cluster
		log.Info("discovering all NodePools in cluster")
		discovered, err := e.listAllNodePools(ctx, client)
		if err != nil {
			log.Error(err, "failed to list all NodePools")
			return fmt.Errorf("list all NodePools: %w", err)
		}
		targetNodePools = discovered
	}

	log.Info("target NodePools determined", "count", len(targetNodePools))

	if len(targetNodePools) == 0 {
		log.Error(nil, "no NodePools found in cluster")
		return fmt.Errorf("no NodePools found in cluster")
	}

	// Process each NodePool
	for _, nodePoolName := range targetNodePools {
		log.Info("scaling down NodePool", "nodePool", nodePoolName)
		if err := e.scaleDownNodePool(ctx, log, client, nodePoolName, params, spec.SaveRestoreData); err != nil {
			log.Error(err, "failed to scale down NodePool", "nodePool", nodePoolName)
			return fmt.Errorf("scale down NodePool %s: %w", nodePoolName, err)
		}
	}

	// Wait for all nodes corresponding to deleted NodePools to be removed if configured
	if params.AwaitCompletion.Enabled {
		timeout := params.AwaitCompletion.Timeout
		if timeout == "" {
			timeout = DefaultWaitTimeout
		}

		for _, nodePoolName := range e.waitinglist {
			e.completionWg.Add(1)

			go func(npName string) {
				defer e.completionWg.Done()
				if err := e.waitForNodePoolDeleted(ctx, log, client, npName, timeout); err != nil {
					log.Error(err, "failed to wait for NodePool deletion", "nodePool", npName)
				}
			}(nodePoolName)
		}

		e.completionWg.Wait()
	}

	log.Info("Karpenter shutdown completed successfully",
		"nodePoolCount", len(targetNodePools),
	)

	return nil
}

// WakeUp restores Karpenter NodePools from hibernation.
func (e *Executor) WakeUp(ctx context.Context, log logr.Logger, spec executor.Spec, restore executor.RestoreData) error {
	log.Info("Karpenter executor starting wakeup",
		"target", spec.TargetName,
		"targetType", spec.TargetType,
	)

	// Parse parameters
	var params executorparams.KarpenterParameters
	if len(spec.Parameters) > 0 {
		if err := json.Unmarshal(spec.Parameters, &params); err != nil {
			log.Error(err, "failed to parse parameters")
			return fmt.Errorf("parse parameters: %w", err)
		}
	}

	if len(restore.Data) == 0 {
		log.Error(nil, "restore data is empty")
		return fmt.Errorf("restore data is required for wake-up")
	}

	log.Info("restore state loaded", "nodePoolCount", len(restore.Data))

	// Build clients using injected factory
	client, err := e.clientFactory(ctx, &spec)
	if err != nil {
		log.Error(err, "failed to build kubernetes client")
		return fmt.Errorf("build kubernetes client: %w", err)
	}

	// Restore each NodePool
	for nodePoolName, stateBytes := range restore.Data {
		var state NodePoolState
		if err := json.Unmarshal(stateBytes, &state); err != nil {
			log.Error(err, "failed to unmarshal NodePool state", "nodePool", nodePoolName)
			return fmt.Errorf("unmarshal NodePool state %s: %w", nodePoolName, err)
		}

		log.Info("restoring NodePool",
			"nodePool", nodePoolName,
			"hasSpec", state.Spec != nil,
			"hasLabels", len(state.Labels) > 0,
		)
		if err := e.restoreNodePool(ctx, log, client, nodePoolName, state, params); err != nil {
			log.Error(err, "failed to restore NodePool", "nodePool", nodePoolName)
			return fmt.Errorf("restore NodePool %s: %w", nodePoolName, err)
		}
	}

	// Wait for all restored NodePools to be ready if configured
	if params.AwaitCompletion.Enabled {
		timeout := params.AwaitCompletion.Timeout
		if timeout == "" {
			timeout = DefaultWaitTimeout
		}
		for _, nodePoolName := range e.waitinglist {
			e.completionWg.Add(1)
			go func(npName string) {
				defer e.completionWg.Done()

				if err := e.waitForNodePoolReady(ctx, log, client, npName, timeout); err != nil {
					log.Error(err, "failed to wait for NodePool ready", "nodePool", npName)
				}
			}(nodePoolName)

		}

		e.completionWg.Wait()
	}

	log.Info("Karpenter wakeup completed successfully",
		"nodePoolCount", len(restore.Data),
	)
	return nil
}

// listAllNodePools discovers all Karpenter NodePools in the cluster.
func (e *Executor) listAllNodePools(ctx context.Context, client Client) ([]string, error) {
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

// NodePoolState stores the complete NodePool manifest for recreation after hibernation.
type NodePoolState struct {
	Name   string                 `json:"name"`
	Spec   map[string]interface{} `json:"spec"`
	Labels map[string]string      `json:"labels,omitempty"`
}

// scaleDownNodePool deletes the NodePool to remove all managed nodes.
// Returns: (state, existed, error)
// - existed: true if NodePool was found and deleted, false if already NotFound
func (e *Executor) scaleDownNodePool(ctx context.Context, log logr.Logger, client Client, nodePoolName string, params executorparams.KarpenterParameters, callback executor.SaveRestoreDataFunc) error {
	nodePoolGVR := schema.GroupVersionResource{
		Group:    "karpenter.sh",
		Version:  "v1",
		Resource: "nodepools",
	}

	// Get the NodePool
	nodePool, err := client.Resource(nodePoolGVR).Get(ctx, nodePoolName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("get NodePool: %w", err)
	}

	// Save complete spec for recreation
	spec, found, err := unstructured.NestedMap(nodePool.Object, "spec")
	if err != nil || !found {
		return fmt.Errorf("get NodePool spec: %w", err)
	}

	// Save labels if present
	labels := nodePool.GetLabels()

	state := NodePoolState{
		Name:   nodePoolName,
		Spec:   spec,
		Labels: labels,
	}

	log.Info("deleting NodePool to trigger node removal", "nodePool", nodePoolName)

	// Delete the NodePool - Karpenter will handle node cleanup
	if err := client.Resource(nodePoolGVR).Delete(ctx, nodePoolName, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("delete NodePool: %w", err)
	}

	// Add to waiting list for awaiting completion if configured
	if params.AwaitCompletion.Enabled {
		e.waitinglist = append(e.waitinglist, nodePoolName)
	}

	log.Info("NodePool deleted successfully",
		"nodePool", nodePoolName,
		"hasSpec", state.Spec != nil,
		"hasLabels", len(state.Labels) > 0,
	)

	// Incremental save: persist this NodePool's restore data immediately
	if callback != nil {
		if err := callback(nodePoolName, state, true); err != nil {
			log.Error(err, "failed to save restore data incrementally", "nodePool", nodePoolName)
			// Continue processing - save at end as fallback
		}
	}

	return nil
}

// restoreNodePool recreates the NodePool from saved state.
func (e *Executor) restoreNodePool(ctx context.Context, log logr.Logger, client Client, nodePoolName string, state NodePoolState, params executorparams.KarpenterParameters) error {
	nodePoolGVR := schema.GroupVersionResource{
		Group:    "karpenter.sh",
		Version:  "v1",
		Resource: "nodepools",
	}

	log.Info("recreating NodePool from saved state", "nodePool", nodePoolName)

	// Construct the NodePool object
	nodePool := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "karpenter.sh/v1",
			"kind":       "NodePool",
			"metadata": map[string]interface{}{
				"name": nodePoolName,
			},
			"spec": state.Spec,
		},
	}

	// Restore labels if present
	if len(state.Labels) > 0 {
		nodePool.SetLabels(state.Labels)
	}

	// Create the NodePool
	if _, err := client.Resource(nodePoolGVR).Create(ctx, nodePool, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create NodePool: %w", err)
	}

	log.Info("NodePool restored successfully", "nodePool", nodePoolName)

	// Add to waiting list for awaiting completion if configured
	if params.AwaitCompletion.Enabled {
		e.waitinglist = append(e.waitinglist, nodePoolName)
	}

	return nil
}

// waitForNodePoolReady waits for a NodePool to reach ready status.
func (e *Executor) waitForNodePoolReady(ctx context.Context, log logr.Logger, client Client, nodePoolName string, timeout string) error {
	nodePoolGVR := schema.GroupVersionResource{
		Group:    "karpenter.sh",
		Version:  "v1",
		Resource: "nodepools",
	}

	log.Info("waiting for NodePool to be ready",
		"nodePool", nodePoolName,
		"timeout", timeout,
	)

	w, err := waiter.NewWaiter(ctx, log, timeout)
	if err != nil {
		return fmt.Errorf("create waiter: %w", err)
	}

	checkFn := func() (bool, string, error) {
		nodePool, err := client.Resource(nodePoolGVR).Get(ctx, nodePoolName, metav1.GetOptions{})
		if err != nil {
			return false, "", fmt.Errorf("get NodePool: %w", err)
		}

		// Check status.conditions for Ready condition
		conditions, found, err := unstructured.NestedSlice(nodePool.Object, "status", "conditions")
		if err != nil {
			return false, "", fmt.Errorf("get conditions: %w", err)
		}
		if !found || len(conditions) == 0 {
			return false, "no conditions available", nil
		}

		// Look for Ready condition
		for _, condRaw := range conditions {
			cond, ok := condRaw.(map[string]interface{})
			if !ok {
				continue
			}
			condType, _, _ := unstructured.NestedString(cond, "type")
			if condType != "Ready" {
				continue
			}
			status, _, _ := unstructured.NestedString(cond, "status")
			reason, _, _ := unstructured.NestedString(cond, "reason")
			message, _, _ := unstructured.NestedString(cond, "message")

			if status == "True" {
				return true, fmt.Sprintf("Ready (reason: %s)", reason), nil
			}
			return false, fmt.Sprintf("not ready: %s - %s", reason, message), nil
		}

		return false, "Ready condition not yet found", nil
	}

	if err := w.Poll(fmt.Sprintf("NodePool %s to be ready", nodePoolName), checkFn); err != nil {
		return err
	}

	log.Info("NodePool is ready", "nodePool", nodePoolName)
	return nil
}

// waitForNodePoolDeleted waits for all Nodes managed by the NodePool to be deleted.
func (e *Executor) waitForNodePoolDeleted(ctx context.Context, log logr.Logger, client Client, nodePoolName string, timeout string) error {
	log.Info("waiting for NodePool nodes to be deleted",
		"nodePool", nodePoolName,
		"timeout", timeout,
	)

	w, err := waiter.NewWaiter(ctx, log, timeout)
	if err != nil {
		return fmt.Errorf("create waiter: %w", err)
	}

	// List Nodes with the NodePool label
	labelSelector := fmt.Sprintf("karpenter.sh/nodepool=%s", nodePoolName)

	if err := w.Poll(fmt.Sprintf("NodePool %s nodes to be deleted", nodePoolName), func() (bool, string, error) {
		// List all nodes with the nodepool label
		nodes, err := client.ListNode(ctx, labelSelector)
		if err != nil {
			return false, "", fmt.Errorf("list nodes: %w", err)
		}

		nodeCount := len(nodes.Items)
		if nodeCount == 0 {
			return true, "all nodes deleted", nil
		}

		return false, fmt.Sprintf("%d node(s) still exist, waiting for deletion", nodeCount), nil
	}); err != nil {
		return err
	}

	log.Info("All NodePool nodes deleted successfully", "nodePool", nodePoolName)
	return nil
}
