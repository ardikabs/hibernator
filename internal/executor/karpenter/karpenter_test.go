/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package karpenter

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/internal/executor/karpenter/mocks"
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// ============================================================================
// Constructor and Factory Tests
// ============================================================================

func TestNew(t *testing.T) {
	e := New()
	assert.NotNil(t, e)
	assert.NotNil(t, e.clientFactory)
}

func TestNewWithClients(t *testing.T) {
	clientFactory := func(ctx context.Context, spec *executor.Spec) (Client, error) {
		return nil, nil
	}

	e := NewWithClients(clientFactory)
	assert.NotNil(t, e)
}

func TestExecutorType(t *testing.T) {
	e := New()
	assert.Equal(t, "karpenter", e.Type())
}

// ============================================================================
// Validation Tests
// ============================================================================

func TestValidate_MissingK8SConfig(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName:      "test-cluster",
		TargetType:      "karpenter",
		Parameters:      json.RawMessage(`{}`),
		ConnectorConfig: executor.ConnectorConfig{},
	}
	err := e.Validate(spec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "K8S connector config is required")
}

func TestValidate_MissingClusterName(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "karpenter",
		Parameters: json.RawMessage(`{}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{},
		},
	}
	err := e.Validate(spec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cluster name is required")
}

func TestValidate_MissingRegion(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "karpenter",
		Parameters: json.RawMessage(`{}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{
				ClusterName: "my-cluster",
			},
		},
	}
	err := e.Validate(spec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "region is required")
}

func TestValidate_Valid(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "karpenter",
		Parameters: json.RawMessage(`{}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{
				ClusterName: "my-cluster",
				Region:      "us-east-1",
			},
		},
	}
	err := e.Validate(spec)
	assert.NoError(t, err)
}

func TestValidate_ValidNodePoolList(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "karpenter",
		Parameters: json.RawMessage(`{"nodePools": ["default", "spot"]}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{
				ClusterName: "my-cluster",
				Region:      "us-east-1",
			},
		},
	}
	err := e.Validate(spec)
	assert.NoError(t, err)
}

// ============================================================================
// Shutdown and WakeUp Error Handling Tests
// ============================================================================

func TestShutdown_InvalidParameters(t *testing.T) {
	e := New()
	ctx := context.Background()

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "karpenter",
		Parameters: json.RawMessage(`{invalid json}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{
				ClusterName: "my-cluster",
				Region:      "us-east-1",
			},
		},
	}

	err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.Error(t, err)
}

func TestShutdown_K8sFactoryError(t *testing.T) {
	clientFactory := func(ctx context.Context, spec *executor.Spec) (Client, error) {
		return nil, errors.New("failed to create k8s client")
	}

	e := NewWithClients(clientFactory)
	ctx := context.Background()

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "karpenter",
		Parameters: json.RawMessage(`{"nodePools": ["default"]}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{
				ClusterName: "my-cluster",
				Region:      "us-east-1",
			},
		},
	}

	err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "build kubernetes client")
}

func TestWakeUp_InvalidRestoreData(t *testing.T) {
	e := New()
	ctx := context.Background()

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "karpenter",
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{
				ClusterName: "my-cluster",
				Region:      "us-east-1",
			},
		},
	}

	restore := executor.RestoreData{
		Type: "karpenter",
		Data: map[string]json.RawMessage{
			"invalid": json.RawMessage(`{invalid json}`),
		},
	}

	err := e.WakeUp(ctx, logr.Discard(), spec, restore)
	assert.Error(t, err)
}

func TestWakeUp_K8sFactoryError(t *testing.T) {
	clientFactory := func(ctx context.Context, spec *executor.Spec) (Client, error) {
		return nil, errors.New("failed to create k8s client")
	}

	e := NewWithClients(clientFactory)
	ctx := context.Background()

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "karpenter",
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{
				ClusterName: "my-cluster",
				Region:      "us-east-1",
			},
		},
	}

	restoreData, _ := json.Marshal(map[string]NodePoolState{})
	restore := executor.RestoreData{
		Type: "karpenter",
		Data: map[string]json.RawMessage{
			"state": restoreData,
		},
	}

	err := e.WakeUp(ctx, logr.Discard(), spec, restore)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "build kubernetes client")
}

func TestShutdown_DeletesNodePools(t *testing.T) {
	ctx := context.Background()
	mockClient := mocks.NewClient(t)

	// Setup GVR for NodePool
	gvr := schema.GroupVersionResource{
		Group:    "karpenter.sh",
		Version:  "v1",
		Resource: "nodepools",
	}

	// Create fake dynamic client with NodePool
	scheme := runtime.NewScheme()
	nodePool := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "karpenter.sh/v1",
			"kind":       "NodePool",
			"metadata": map[string]interface{}{
				"name": "default",
			},
			"spec": map[string]interface{}{
				"disruption": map[string]interface{}{
					"consolidateAfter": "30s",
				},
				"limits": map[string]interface{}{
					"cpu":    "1000",
					"memory": "1000Gi",
				},
			},
		},
	}
	nodePool.SetLabels(map[string]string{"env": "test"})

	fakeDynamic := dynamicfake.NewSimpleDynamicClient(scheme, nodePool)

	// Mock Resource to return fake dynamic client
	mockClient.On("Resource", gvr).Return(fakeDynamic.Resource(gvr))

	// Mock ListNode to verify nodes are deleted (empty list) - only called if waitConfig.enabled=true
	mockClient.On("ListNode", ctx, "karpenter.sh/nodepool=default").Return(&corev1.NodeList{
		Items: []corev1.Node{},
	}, nil)

	clientFactory := func(ctx context.Context, spec *executor.Spec) (Client, error) {
		return mockClient, nil
	}

	e := NewWithClients(clientFactory)

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "karpenter",
		Parameters: json.RawMessage(`{"nodePools": ["default"], "waitConfig": {"enabled": true, "timeout": "1m"}}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{
				ClusterName: "my-cluster",
				Region:      "us-east-1",
			},
		},
	}

	err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)
}

func TestShutdown_MultipleNodePools(t *testing.T) {
	ctx := context.Background()
	mockClient := mocks.NewClient(t)

	gvr := schema.GroupVersionResource{
		Group:    "karpenter.sh",
		Version:  "v1",
		Resource: "nodepools",
	}

	// Create fake dynamic client with multiple NodePools
	scheme := runtime.NewScheme()
	defaultPool := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "karpenter.sh/v1",
			"kind":       "NodePool",
			"metadata": map[string]interface{}{
				"name": "default",
			},
			"spec": map[string]interface{}{
				"limits": map[string]interface{}{"cpu": "1000"},
			},
		},
	}
	spotPool := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "karpenter.sh/v1",
			"kind":       "NodePool",
			"metadata": map[string]interface{}{
				"name": "spot",
			},
			"spec": map[string]interface{}{
				"limits": map[string]interface{}{"cpu": "500"},
			},
		},
	}

	fakeDynamic := dynamicfake.NewSimpleDynamicClient(scheme, defaultPool, spotPool)

	// Mock Resource calls for both NodePools
	mockClient.On("Resource", gvr).Return(fakeDynamic.Resource(gvr))

	// Mock ListNode for both NodePools
	mockClient.On("ListNode", ctx, "karpenter.sh/nodepool=default").Return(&corev1.NodeList{Items: []corev1.Node{}}, nil)
	mockClient.On("ListNode", ctx, "karpenter.sh/nodepool=spot").Return(&corev1.NodeList{Items: []corev1.Node{}}, nil)

	clientFactory := func(ctx context.Context, spec *executor.Spec) (Client, error) {
		return mockClient, nil
	}

	e := NewWithClients(clientFactory)

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "karpenter",
		Parameters: json.RawMessage(`{"nodePools": ["default", "spot"], "waitConfig": {"enabled": true, "timeout": "1m"}}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{
				ClusterName: "my-cluster",
				Region:      "us-east-1",
			},
		},
	}

	err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)
}

func TestWakeUp_RestoresNodePools(t *testing.T) {
	ctx := context.Background()
	mockClient := mocks.NewClient(t)

	gvr := schema.GroupVersionResource{
		Group:    "karpenter.sh",
		Version:  "v1",
		Resource: "nodepools",
	}

	// Create fake dynamic client with proper GVR registration
	scheme := runtime.NewScheme()
	// Register the NodePool list GVK
	gvks := map[schema.GroupVersionResource]string{
		gvr: "NodePoolList",
	}
	fakeDynamic := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvks)

	// Mock Resource for creating NodePool
	mockClient.On("Resource", gvr).Return(fakeDynamic.Resource(gvr))

	clientFactory := func(ctx context.Context, spec *executor.Spec) (Client, error) {
		return mockClient, nil
	}

	e := NewWithClients(clientFactory)

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "karpenter",
		Parameters: json.RawMessage(`{"nodePools": ["default"]}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{
				ClusterName: "my-cluster",
				Region:      "us-east-1",
			},
		},
	}

	// Create per-nodepool restore data (key = nodepool name)
	nodePoolState := NodePoolState{
		Name: "default",
		Spec: map[string]interface{}{
			"disruption": map[string]interface{}{
				"consolidateAfter": "30s",
			},
			"limits": map[string]interface{}{
				"cpu":    "1000",
				"memory": "1000Gi",
			},
		},
		Labels: map[string]string{
			"env": "test",
		},
	}
	nodePoolStateBytes, _ := json.Marshal(nodePoolState)
	restoreData := executor.RestoreData{
		Type: "karpenter",
		Data: map[string]json.RawMessage{
			"default": nodePoolStateBytes,
		},
	}

	err := e.WakeUp(ctx, logr.Discard(), spec, restoreData)
	assert.NoError(t, err)

	// Verify NodePool was created in the fake client
	nodePoolList, err := fakeDynamic.Resource(gvr).List(ctx, metav1.ListOptions{})
	assert.NoError(t, err)
	assert.Len(t, nodePoolList.Items, 1)
	assert.Equal(t, "default", nodePoolList.Items[0].GetName())
}

func TestShutdown_AllNodePools(t *testing.T) {
	ctx := context.Background()
	mockClient := mocks.NewClient(t)

	gvr := schema.GroupVersionResource{
		Group:    "karpenter.sh",
		Version:  "v1",
		Resource: "nodepools",
	}

	// Create fake dynamic client with NodePools and proper GVR registration
	scheme := runtime.NewScheme()
	defaultPool := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "karpenter.sh/v1",
			"kind":       "NodePool",
			"metadata": map[string]interface{}{
				"name": "default",
			},
			"spec": map[string]interface{}{
				"limits": map[string]interface{}{"cpu": "1000"},
			},
		},
	}
	spotPool := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "karpenter.sh/v1",
			"kind":       "NodePool",
			"metadata": map[string]interface{}{
				"name": "spot",
			},
			"spec": map[string]interface{}{
				"limits": map[string]interface{}{"cpu": "500"},
			},
		},
	}

	gvks := map[schema.GroupVersionResource]string{
		gvr: "NodePoolList",
	}
	fakeDynamic := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvks, defaultPool, spotPool)

	// Mock Resource - should be called multiple times (once for list, once for each delete)
	mockClient.On("Resource", gvr).Return(fakeDynamic.Resource(gvr))

	// Mock ListNode for both NodePools
	mockClient.On("ListNode", ctx, "karpenter.sh/nodepool=default").Return(&corev1.NodeList{Items: []corev1.Node{}}, nil)
	mockClient.On("ListNode", ctx, "karpenter.sh/nodepool=spot").Return(&corev1.NodeList{Items: []corev1.Node{}}, nil)

	clientFactory := func(ctx context.Context, spec *executor.Spec) (Client, error) {
		return mockClient, nil
	}

	e := NewWithClients(clientFactory)

	// Empty nodePools means target all NodePools
	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "karpenter",
		Parameters: json.RawMessage(`{"waitConfig": {"enabled": true, "timeout": "1m"}}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{
				ClusterName: "my-cluster",
				Region:      "us-east-1",
			},
		},
	}

	err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)
}

func TestShutdown_WithoutWaitConfig(t *testing.T) {
	ctx := context.Background()
	mockClient := mocks.NewClient(t)

	gvr := schema.GroupVersionResource{
		Group:    "karpenter.sh",
		Version:  "v1",
		Resource: "nodepools",
	}

	// Create fake dynamic client with NodePool
	scheme := runtime.NewScheme()
	nodePool := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "karpenter.sh/v1",
			"kind":       "NodePool",
			"metadata": map[string]interface{}{
				"name": "default",
			},
			"spec": map[string]interface{}{
				"limits": map[string]interface{}{"cpu": "1000"},
			},
		},
	}

	fakeDynamic := dynamicfake.NewSimpleDynamicClient(scheme, nodePool)

	// Mock Resource to return fake dynamic client
	mockClient.On("Resource", gvr).Return(fakeDynamic.Resource(gvr))

	// ListNode should NOT be called when waitConfig is disabled
	// No mock setup for ListNode

	clientFactory := func(ctx context.Context, spec *executor.Spec) (Client, error) {
		return mockClient, nil
	}

	e := NewWithClients(clientFactory)

	// No waitConfig means wait is disabled
	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "karpenter",
		Parameters: json.RawMessage(`{"nodePools": ["default"]}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{
				ClusterName: "my-cluster",
				Region:      "us-east-1",
			},
		},
	}

	err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)
}

// ============================================================================
// Data Type Tests
// ============================================================================

func TestNodePoolState_JSON(t *testing.T) {
	state := NodePoolState{
		Name: "default",
		Spec: map[string]interface{}{
			"disruption": map[string]interface{}{
				"consolidateAfter": "30s",
			},
			"limits": map[string]interface{}{
				"cpu":    "1000",
				"memory": "1000Gi",
			},
		},
		Labels: map[string]string{
			"env": "test",
		},
	}

	data, err := json.Marshal(state)
	assert.NoError(t, err)

	var decoded NodePoolState
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, "default", decoded.Name)
	assert.NotNil(t, decoded.Spec)
	assert.NotNil(t, decoded.Labels)
	assert.Equal(t, "test", decoded.Labels["env"])
}

func TestKarpenterParameters_JSON(t *testing.T) {
	params := executorparams.KarpenterParameters{
		NodePools: []string{"default", "spot"},
	}

	data, _ := json.Marshal(params)
	var decoded executorparams.KarpenterParameters
	json.Unmarshal(data, &decoded)

	assert.Equal(t, 2, len(decoded.NodePools))
	assert.Equal(t, "default", decoded.NodePools[0])
	assert.Equal(t, "spot", decoded.NodePools[1])
}

func TestNodePoolState_Empty(t *testing.T) {
	emptyState := make(map[string]NodePoolState)
	data, _ := json.Marshal(emptyState)

	var decoded map[string]NodePoolState
	err := json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(decoded))
}

func TestRestoreData_NodePoolStates(t *testing.T) {
	states := map[string]NodePoolState{
		"default": {
			Name: "default",
			Spec: map[string]interface{}{
				"disruption": map[string]interface{}{
					"consolidateAfter": "30s",
				},
				"limits": map[string]interface{}{
					"cpu": "1000",
				},
			},
		},
		"spot": {
			Name: "spot",
			Spec: map[string]interface{}{
				"limits": map[string]interface{}{
					"cpu": "500",
				},
			},
		},
	}

	data, err := json.Marshal(states)
	assert.NoError(t, err)

	var decoded map[string]NodePoolState
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(decoded))
	assert.Equal(t, "default", decoded["default"].Name)
	assert.Equal(t, "spot", decoded["spot"].Name)
}

func TestExecutorType_Constant(t *testing.T) {
	e := New()
	assert.Equal(t, "karpenter", e.Type())
}

func TestKarpenterParameters_Empty(t *testing.T) {
	params := executorparams.KarpenterParameters{
		NodePools: []string{},
	}

	data, _ := json.Marshal(params)
	var decoded executorparams.KarpenterParameters
	json.Unmarshal(data, &decoded)

	assert.Equal(t, 0, len(decoded.NodePools))
}

func TestNodePoolState_AllFields(t *testing.T) {
	state := NodePoolState{
		Name: "premium",
		Spec: map[string]interface{}{
			"disruption": map[string]interface{}{
				"consolidateAfter":    "30s",
				"expireAfter":         "720h",
				"budgetPercentage":    10,
				"nodesMaxUnavailable": 5,
			},
			"limits": map[string]interface{}{
				"cpu":    "1000",
				"memory": "1000Gi",
			},
		},
		Labels: map[string]string{
			"tier":        "premium",
			"environment": "production",
		},
	}

	data, err := json.Marshal(state)
	assert.NoError(t, err)

	var decoded NodePoolState
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, "premium", decoded.Name)
	assert.NotNil(t, decoded.Spec)
	assert.NotNil(t, decoded.Labels)
	assert.Equal(t, "premium", decoded.Labels["tier"])
}
