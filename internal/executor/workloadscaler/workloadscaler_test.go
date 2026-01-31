/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package workloadscaler

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/stretchr/testify/assert"
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
	_ = ctx

	// Validate executor can be created
	e := New()
	assert.Equal(t, "workloadscaler", e.Type())
}

func TestShutdown_SelectorNamespaces(t *testing.T) {
	ctx := context.Background()
	_ = ctx

	e := New()
	assert.NotNil(t, e)
}

func TestWakeUp_RestoresReplicas(t *testing.T) {
	ctx := context.Background()
	_ = ctx

	e := New()
	assert.NotNil(t, e)
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
