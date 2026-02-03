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
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
)

// ============================================================================
// Constructor and Factory Tests
// ============================================================================

func TestNew(t *testing.T) {
	e := New()
	assert.NotNil(t, e)
	assert.NotNil(t, e.k8sFactory)
}

func TestNewWithClients(t *testing.T) {
	k8sFactory := func(ctx context.Context, spec *executor.Spec) (K8sClient, error) {
		return nil, nil
	}

	e := NewWithClients(k8sFactory)
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

	_, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.Error(t, err)
}

func TestShutdown_K8sFactoryError(t *testing.T) {
	k8sFactory := func(ctx context.Context, spec *executor.Spec) (K8sClient, error) {
		return nil, errors.New("failed to create k8s client")
	}

	e := NewWithClients(k8sFactory)
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

	_, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create k8s client")
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
		Data: json.RawMessage(`{invalid json}`),
	}

	err := e.WakeUp(ctx, logr.Discard(), spec, restore)
	assert.Error(t, err)
}

func TestWakeUp_K8sFactoryError(t *testing.T) {
	k8sFactory := func(ctx context.Context, spec *executor.Spec) (K8sClient, error) {
		return nil, errors.New("failed to create k8s client")
	}

	e := NewWithClients(k8sFactory)
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
		Data: restoreData,
	}

	err := e.WakeUp(ctx, logr.Discard(), spec, restore)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create k8s client")
}

// ============================================================================
// Data Type Tests
// ============================================================================

func TestNodePoolState_JSON(t *testing.T) {
	state := NodePoolState{
		Name: "default",
		DisruptionBudgets: map[string]interface{}{
			"consolidateAfter": "30s",
		},
		Limits: map[string]interface{}{
			"cpu":    "1000",
			"memory": "1000Gi",
		},
	}

	data, err := json.Marshal(state)
	assert.NoError(t, err)

	var decoded NodePoolState
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, "default", decoded.Name)
	assert.NotNil(t, decoded.DisruptionBudgets)
	assert.NotNil(t, decoded.Limits)
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
			Name:              "default",
			DisruptionBudgets: map[string]interface{}{"consolidateAfter": "30s"},
			Limits:            map[string]interface{}{"cpu": "1000"},
		},
		"spot": {
			Name:   "spot",
			Limits: map[string]interface{}{"cpu": "500"},
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
		DisruptionBudgets: map[string]interface{}{
			"consolidateAfter":    "30s",
			"expireAfter":         "720h",
			"budgetPercentage":    10,
			"nodesMaxUnavailable": 5,
		},
		Limits: map[string]interface{}{
			"cpu":    "1000",
			"memory": "1000Gi",
		},
	}

	data, err := json.Marshal(state)
	assert.NoError(t, err)

	var decoded NodePoolState
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, "premium", decoded.Name)
	assert.NotNil(t, decoded.DisruptionBudgets)
	assert.NotNil(t, decoded.Limits)
}
