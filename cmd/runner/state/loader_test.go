/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ardikabs/hibernator/internal/restore"
)

func schemeWithRestore() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	return scheme
}

func restoreCMWithStaleKeys(t *testing.T, state map[string]any, staleCounts map[string]int) *corev1.ConfigMap {
	t.Helper()
	data := restore.Data{
		Target:      "my-target",
		Executor:    "eks",
		Version:     1,
		CreatedAt:   metav1.Now(),
		IsLive:      true,
		State:       state,
		StaleCounts: staleCounts,
	}
	dataBytes, err := json.Marshal(data)
	require.NoError(t, err)
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hibernator-restore-test-plan",
			Namespace: "default",
		},
		Data: map[string]string{
			"my-target.json": string(dataBytes),
		},
	}
}

func TestLoadRestoreData_ExcludesStaleKeys(t *testing.T) {
	fakeClient := fake.NewClientBuilder().
		WithScheme(schemeWithRestore()).
		WithObjects(restoreCMWithStaleKeys(t,
			map[string]any{
				"keyA": "valueA",
				"keyB": "valueB",
				"keyC": "valueC",
			},
			map[string]int{
				"keyA": 1, // stale: reported 1 cycle ago, should be excluded
				"keyB": 0, // not stale
			},
		)).
		Build()

	ctx := context.Background()
	log := logr.Discard()

	result, err := LoadRestoreData(ctx, fakeClient, log, "default", "test-plan", "my-target")
	require.NoError(t, err)

	assert.NotContains(t, result.Data, "keyA", "keyA should be excluded: StaleCount=1")
	assert.Contains(t, result.Data, "keyB", "keyB should be included: StaleCount=0")
	assert.Contains(t, result.Data, "keyC", "keyC should be included: not in StaleCounts")
}

func TestLoadRestoreData_AllKeysStale_EmptyResult(t *testing.T) {
	fakeClient := fake.NewClientBuilder().
		WithScheme(schemeWithRestore()).
		WithObjects(restoreCMWithStaleKeys(t,
			map[string]any{
				"keyA": "valueA",
			},
			map[string]int{
				"keyA": 3, // stale count exceeds maxStaleCount (3)
			},
		)).
		Build()

	ctx := context.Background()
	log := logr.Discard()

	result, err := LoadRestoreData(ctx, fakeClient, log, "default", "test-plan", "my-target")
	require.NoError(t, err)

	assert.Empty(t, result.Data, "all keys stale should yield empty Data map")
}

func TestLoadRestoreData_NoStaleKeys_LoadsAll(t *testing.T) {
	fakeClient := fake.NewClientBuilder().
		WithScheme(schemeWithRestore()).
		WithObjects(restoreCMWithStaleKeys(t,
			map[string]any{
				"instance-1": map[string]any{"minSize": 0, "maxSize": 3},
				"instance-2": map[string]any{"minSize": 0, "maxSize": 5},
			},
			nil, // no stale counts
		)).
		Build()

	ctx := context.Background()
	log := logr.Discard()

	result, err := LoadRestoreData(ctx, fakeClient, log, "default", "test-plan", "my-target")
	require.NoError(t, err)

	assert.Len(t, result.Data, 2)
	assert.Contains(t, result.Data, "instance-1")
	assert.Contains(t, result.Data, "instance-2")
	assert.True(t, result.IsLive)
	assert.Equal(t, "eks", result.Type)
}

func TestLoadRestoreData_MissingConfigMap_ReturnsError(t *testing.T) {
	fakeClient := fake.NewClientBuilder().
		WithScheme(schemeWithRestore()).
		Build()

	ctx := context.Background()
	log := logr.Discard()

	_, err := LoadRestoreData(ctx, fakeClient, log, "default", "test-plan", "my-target")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no restore data found")
}

func TestLoadRestoreData_MissingTargetInConfigMap_ReturnsError(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hibernator-restore-test-plan",
			Namespace: "default",
		},
		Data: map[string]string{
			"other-target.json": `{"target":"other","executor":"eks","version":1,"state":{}}`,
		},
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(schemeWithRestore()).
		WithObjects(cm).
		Build()

	ctx := context.Background()
	log := logr.Discard()

	_, err := LoadRestoreData(ctx, fakeClient, log, "default", "test-plan", "my-target")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no restore data found")
}

func TestLoadRestoreData_ValueTransformation(t *testing.T) {
	fakeClient := fake.NewClientBuilder().
		WithScheme(schemeWithRestore()).
		WithObjects(restoreCMWithStaleKeys(t,
			map[string]any{
				"nodeGroup": map[string]any{
					"name":    "ng-1",
					"minSize": float64(0),
					"maxSize": float64(5),
				},
			},
			nil,
		)).
		Build()

	ctx := context.Background()
	log := logr.Discard()

	result, err := LoadRestoreData(ctx, fakeClient, log, "default", "test-plan", "my-target")
	require.NoError(t, err)

	require.Contains(t, result.Data, "nodeGroup")
	var ng map[string]any
	err = json.Unmarshal(result.Data["nodeGroup"], &ng)
	require.NoError(t, err)
	assert.Equal(t, "ng-1", ng["name"])
	assert.Equal(t, float64(0), ng["minSize"])
}
