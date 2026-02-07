/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package workloadscaler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/internal/executor/workloadscaler/mocks"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestExecutorType(t *testing.T) {
	e := New()
	assert.Equal(t, "workloadscaler", e.Type())
}

func TestValidate_MissingK8SConfig(t *testing.T) {
	e := New()
	spec := executor.Spec{TargetType: "workloadscaler"}
	err := e.Validate(spec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "K8S connector config is required")
}

func TestValidate_MissingNamespace(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetType:      "workloadscaler",
		ConnectorConfig: executor.ConnectorConfig{K8S: &executor.K8SConnectorConfig{}},
		Parameters:      json.RawMessage(`{"includedGroups": ["Deployment"]}`),
	}
	err := e.Validate(spec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "namespace must specify either literals or selector")
}

func TestShutdown_ScalesMatchingWorkloads(t *testing.T) {
	ctx := context.Background()
	mockClient := mocks.NewClient(t)

	// No need to mock ListNamespaces when using literal namespaces
	// (literals are returned directly without calling the API)

	// Mock workload listing
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	workloadList := &unstructured.UnstructuredList{
		Items: []unstructured.Unstructured{
			{
				Object: map[string]interface{}{
					"apiVersion": "apps/v1",
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name":      "test-deployment",
						"namespace": "default",
					},
				},
			},
		},
	}
	mockClient.EXPECT().ListWorkloads(ctx, gvr, "default", "").Return(workloadList, nil)

	// Mock get scale (current replicas = 3)
	scaleObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"spec": map[string]interface{}{
				"replicas": int64(3),
			},
			"status": map[string]interface{}{
				"replicas": int64(3),
			},
		},
	}
	mockClient.EXPECT().GetScale(ctx, gvr, "default", "test-deployment").Return(scaleObj, nil)

	// Mock update scale (set to 0)
	mockClient.EXPECT().UpdateScale(ctx, gvr, "default", scaleObj).Return(scaleObj, nil)

	clientFactory := func(ctx context.Context, spec *executor.Spec) (Client, error) {
		return mockClient, nil
	}

	e := NewWithClients(clientFactory)

	spec := executor.Spec{
		TargetName: "test-workloads",
		TargetType: "workloadscaler",
		Parameters: json.RawMessage(`{
			"includedGroups": ["Deployment"],
			"namespace": {"literals": ["default"]}
		}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{},
		},
	}

	err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)
}

func TestShutdown_SelectorNamespaces(t *testing.T) {
	ctx := context.Background()
	mockClient := mocks.NewClient(t)

	// Mock namespace listing with selector
	mockClient.EXPECT().ListNamespaces(ctx, "env=staging").Return(&corev1.NamespaceList{
		Items: []corev1.Namespace{
			{ObjectMeta: metav1.ObjectMeta{Name: "staging-1", Labels: map[string]string{"env": "staging"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "staging-2", Labels: map[string]string{"env": "staging"}}},
		},
	}, nil)

	// Mock workload listing for staging-1
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	mockClient.EXPECT().ListWorkloads(ctx, gvr, "staging-1", "").Return(&unstructured.UnstructuredList{Items: []unstructured.Unstructured{}}, nil)

	// Mock workload listing for staging-2
	mockClient.EXPECT().ListWorkloads(ctx, gvr, "staging-2", "").Return(&unstructured.UnstructuredList{Items: []unstructured.Unstructured{}}, nil)

	clientFactory := func(ctx context.Context, spec *executor.Spec) (Client, error) {
		return mockClient, nil
	}

	e := NewWithClients(clientFactory)

	spec := executor.Spec{
		TargetName: "test-workloads",
		TargetType: "workloadscaler",
		Parameters: json.RawMessage(`{
			"includedGroups": ["Deployment"],
			"namespace": {"selector": {"env": "staging"}}
		}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{},
		},
	}

	err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)
}

func TestWakeUp_RestoresReplicas(t *testing.T) {
	ctx := context.Background()
	mockClient := mocks.NewClient(t)

	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

	// Mock get scale (currently at 0)
	scaleObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"spec": map[string]interface{}{
				"replicas": int64(0),
			},
			"status": map[string]interface{}{
				"replicas": int64(0),
			},
		},
	}
	mockClient.EXPECT().GetScale(ctx, gvr, "default", "test-deployment").Return(scaleObj, nil)

	// Mock update scale (restore to 3)
	mockClient.EXPECT().UpdateScale(ctx, gvr, "default", scaleObj).Return(scaleObj, nil)

	clientFactory := func(ctx context.Context, spec *executor.Spec) (Client, error) {
		return mockClient, nil
	}

	e := NewWithClients(clientFactory)

	spec := executor.Spec{
		TargetName: "test-workloads",
		TargetType: "workloadscaler",
		Parameters: json.RawMessage(`{
			"includedGroups": ["Deployment"],
			"namespace": {"literals": ["default"]}
		}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{},
		},
	}

	// Create per-workload restore data (key = "<namespace>/<kind>/<name>")
	workloadState := WorkloadState{
		Group:     "apps",
		Version:   "v1",
		Resource:  "deployments",
		Kind:      "Deployment",
		Namespace: "default",
		Name:      "test-deployment",
		Replicas:  3,
	}
	workloadStateBytes, _ := json.Marshal(workloadState)
	restoreData := executor.RestoreData{
		Type: "workloadscaler",
		Data: map[string]json.RawMessage{
			"default/Deployment/test-deployment": workloadStateBytes,
		},
	}

	err := e.WakeUp(ctx, logr.Discard(), spec, restoreData)
	assert.NoError(t, err)
}

func TestShutdown_InvalidParameters(t *testing.T) {
	e := New()
	ctx := context.Background()

	spec := executor.Spec{
		TargetName: "test-workloads",
		TargetType: "workloadscaler",
		Parameters: json.RawMessage(`{invalid json}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{},
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
		TargetName: "test-workloads",
		TargetType: "workloadscaler",
		Parameters: json.RawMessage(`{
			"includedGroups": ["Deployment"],
			"namespace": {"literals": ["default"]}
		}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{},
		},
	}

	err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "build kubernetes clients")
}

func TestWakeUp_InvalidRestoreData(t *testing.T) {
	e := New()
	ctx := context.Background()

	spec := executor.Spec{
		TargetName: "test-workloads",
		TargetType: "workloadscaler",
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{},
		},
	}

	restore := executor.RestoreData{
		Type: "workloadscaler",
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
		TargetName: "test-workloads",
		TargetType: "workloadscaler",
		Parameters: json.RawMessage(`{
			"includedGroups": ["Deployment"],
			"namespace": {"literals": ["default"]}
		}`),
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &executor.K8SConnectorConfig{},
		},
	}

	restoreData, _ := json.Marshal(RestoreState{Items: []WorkloadState{}})
	restore := executor.RestoreData{
		Type: "workloadscaler",
		Data: map[string]json.RawMessage{
			"state": restoreData,
		},
	}

	err := e.WakeUp(ctx, logr.Discard(), spec, restore)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "build kubernetes clients")
}

// ===== GVR Resolution Tests =====

func TestResolveGVR_HardcodedDeployment(t *testing.T) {
	e := New()
	gvr, err := e.resolveGVR("Deployment")
	assert.NoError(t, err)
	assert.Equal(t, "apps", gvr.Group)
	assert.Equal(t, "v1", gvr.Version)
	assert.Equal(t, "deployments", gvr.Resource)
}

func TestResolveGVR_HardcodedStatefulSet(t *testing.T) {
	e := New()
	gvr, err := e.resolveGVR("StatefulSet")
	assert.NoError(t, err)
	assert.Equal(t, "apps", gvr.Group)
	assert.Equal(t, "v1", gvr.Version)
	assert.Equal(t, "statefulsets", gvr.Resource)
}

func TestResolveGVR_HardcodedReplicaSet(t *testing.T) {
	e := New()
	gvr, err := e.resolveGVR("ReplicaSet")
	assert.NoError(t, err)
	assert.Equal(t, "apps", gvr.Group)
	assert.Equal(t, "v1", gvr.Version)
	assert.Equal(t, "replicasets", gvr.Resource)
}

func TestResolveGVR_CustomCRD_ArgoRollout(t *testing.T) {
	e := New()
	gvr, err := e.resolveGVR("argoproj.io/v1alpha1/rollouts")
	assert.NoError(t, err)
	assert.Equal(t, "argoproj.io", gvr.Group)
	assert.Equal(t, "v1alpha1", gvr.Version)
	assert.Equal(t, "rollouts", gvr.Resource)
}

func TestResolveGVR_CustomCRD_FluxHelmRelease(t *testing.T) {
	e := New()
	gvr, err := e.resolveGVR("helm.fluxcd.io/v2beta1/helmreleases")
	assert.NoError(t, err)
	assert.Equal(t, "helm.fluxcd.io", gvr.Group)
	assert.Equal(t, "v2beta1", gvr.Version)
	assert.Equal(t, "helmreleases", gvr.Resource)
}

func TestResolveGVR_CustomCRD_KedroRun(t *testing.T) {
	e := New()
	gvr, err := e.resolveGVR("kedro.example.com/v1/kedrouruns")
	assert.NoError(t, err)
	assert.Equal(t, "kedro.example.com", gvr.Group)
	assert.Equal(t, "v1", gvr.Version)
	assert.Equal(t, "kedrouruns", gvr.Resource)
}

func TestResolveGVR_Invalid_TooFewParts(t *testing.T) {
	e := New()
	gvr, err := e.resolveGVR("invalid/v1alpha1")
	assert.Error(t, err)
	assert.Equal(t, schema.GroupVersionResource{}, gvr)
	assert.Contains(t, err.Error(), "is not a supported common resource")
}

func TestResolveGVR_Invalid_TooManyParts(t *testing.T) {
	e := New()
	gvr, err := e.resolveGVR("example.com/v1/resource/extra")
	assert.Error(t, err)
	assert.Equal(t, schema.GroupVersionResource{}, gvr)
	assert.Contains(t, err.Error(), "is not a supported common resource")
}

func TestResolveGVR_Invalid_EmptyGroup(t *testing.T) {
	e := New()
	gvr, err := e.resolveGVR("/v1alpha1/rollouts")
	assert.Error(t, err)
	assert.Equal(t, schema.GroupVersionResource{}, gvr)
	assert.Contains(t, err.Error(), "CRD format 'group/version/resource' has empty parts")
	assert.Contains(t, err.Error(), `group=""`)
}

func TestResolveGVR_Invalid_EmptyVersion(t *testing.T) {
	e := New()
	gvr, err := e.resolveGVR("example.com//rollouts")
	assert.Error(t, err)
	assert.Equal(t, schema.GroupVersionResource{}, gvr)
	assert.Contains(t, err.Error(), "CRD format 'group/version/resource' has empty parts")
	assert.Contains(t, err.Error(), `version=""`)
}

func TestResolveGVR_Invalid_EmptyResource(t *testing.T) {
	e := New()
	gvr, err := e.resolveGVR("example.com/v1alpha1/")
	assert.Error(t, err)
	assert.Equal(t, schema.GroupVersionResource{}, gvr)
	assert.Contains(t, err.Error(), "CRD format 'group/version/resource' has empty parts")
	assert.Contains(t, err.Error(), `resource=""`)
}

func TestResolveGVR_Unknown_NotInMap(t *testing.T) {
	e := New()
	gvr, err := e.resolveGVR("UnknownKind")
	assert.Error(t, err)
	assert.Equal(t, schema.GroupVersionResource{}, gvr)
	assert.Contains(t, err.Error(), "UnknownKind")
	assert.Contains(t, err.Error(), "is not a supported common resource")
}
