/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestAccumulator_StructConversion verifies that the accumulator properly converts
// concrete struct values (like DBInstanceState) to map[string]any for compatibility
// with the restore manager.
func TestAccumulator_StructConversion(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	ctx := context.Background()

	// Create a test struct that mimics DBInstanceState
	type TestInstanceState struct {
		InstanceId   string `json:"instanceId"`
		WasRunning   bool   `json:"wasRunning"`
		SnapshotId   string `json:"snapshotId,omitempty"`
		InstanceType string `json:"instanceType,omitempty"`
	}

	// Create accumulator
	callback, flush := NewReportStateHandlers(ctx, fakeClient, logr.Discard(), "test-ns", "test-plan", "test-target", "rds", "cycle-001")

	// Add struct values (as executors do)
	state1 := TestInstanceState{
		InstanceId:   "i-12345678",
		WasRunning:   true,
		SnapshotId:   "snap-abc123",
		InstanceType: "db.t3.medium",
	}
	state2 := TestInstanceState{
		InstanceId:   "i-87654321",
		WasRunning:   false, // Not in demanded state
		SnapshotId:   "",
		InstanceType: "db.t3.small",
	}

	err := callback("instance:i-12345678", state1)
	require.NoError(t, err)

	err = callback("instance:i-87654321", state2)
	require.NoError(t, err)

	// Flush to ConfigMap
	err = flush()
	require.NoError(t, err)

	// Load and verify
	cmName := "hibernator-restore-test-plan"
	cm := &corev1.ConfigMap{}
	err = fakeClient.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: cmName}, cm)
	require.NoError(t, err)

	// Verify the data was saved
	dataStr, ok := cm.Data["test-target.json"]
	require.True(t, ok, "ConfigMap should have test-target.json key")

	var data restore.Data
	err = json.Unmarshal([]byte(dataStr), &data)
	require.NoError(t, err)

	// Verify State values are map[string]any, not structs
	state1Data, ok := data.State["instance:i-12345678"].(map[string]any)
	require.True(t, ok, "State value should be map[string]any, not struct")
	require.Equal(t, "i-12345678", state1Data["instanceId"])
	require.Equal(t, true, state1Data["wasRunning"])
	require.Equal(t, "snap-abc123", state1Data["snapshotId"])
	require.Equal(t, "db.t3.medium", state1Data["instanceType"])

	state2Data, ok := data.State["instance:i-87654321"].(map[string]any)
	require.True(t, ok, "State value should be map[string]any, not struct")
	require.Equal(t, "i-87654321", state2Data["instanceId"])
	require.Equal(t, false, state2Data["wasRunning"])

	// Verify ManagedByCycleIDs tracking works correctly
	// wasRunning=true resources should be tracked
	require.NotNil(t, data.ManagedByCycleIDs, "ManagedByCycleIDs should be initialized")
	require.Equal(t, "cycle-001", data.ManagedByCycleIDs["instance:i-12345678"],
		"Demanded state resource (wasRunning=true) should be tracked")
	require.Empty(t, data.ManagedByCycleIDs["instance:i-87654321"],
		"Non-demanded state resource should NOT be tracked")
}

// TestAccumulator_MapStringAnyPassthrough verifies that map[string]any values
// are passed through without conversion.
func TestAccumulator_MapStringAnyPassthrough(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	ctx := context.Background()

	callback, flush := NewReportStateHandlers(ctx, fakeClient, logr.Discard(), "test-ns", "test-plan", "test-target", "workloadscaler", "cycle-001")

	// Add map[string]any values (as workloadscaler might do)
	state1 := map[string]any{
		"namespace": "default",
		"kind":      "Deployment",
		"name":      "web-app",
		"replicas":  float64(3),
		"wasScaled": true,
	}

	err := callback("default/Deployment/web-app", state1)
	require.NoError(t, err)

	err = flush()
	require.NoError(t, err)

	// Load and verify
	cmName := "hibernator-restore-test-plan"
	cm := &corev1.ConfigMap{}
	err = fakeClient.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: cmName}, cm)
	require.NoError(t, err)

	var data restore.Data
	err = json.Unmarshal([]byte(cm.Data["test-target.json"]), &data)
	require.NoError(t, err)

	// Verify map was passed through
	stateData, ok := data.State["default/Deployment/web-app"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "web-app", stateData["name"])
	require.Equal(t, true, stateData["wasScaled"])

	// Verify tracking
	require.Equal(t, "cycle-001", data.ManagedByCycleIDs["default/Deployment/web-app"])
}

// TestAccumulator_StaleCountsAccumulation verifies that stale counts work correctly
// when resources are not reported in subsequent cycles.
func TestAccumulator_StaleCountsAccumulation(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	ctx := context.Background()

	// First cycle: Save 2 resources
	callback1, flush1 := NewReportStateHandlers(ctx, fakeClient, logr.Discard(), "test-ns", "test-plan", "test-target", "rds", "cycle-001")

	type TestState struct {
		InstanceId string `json:"instanceId"`
		WasRunning bool   `json:"wasRunning"`
	}

	_ = callback1("i-1", TestState{InstanceId: "i-1", WasRunning: true})
	_ = callback1("i-2", TestState{InstanceId: "i-2", WasRunning: true})
	_ = flush1()

	// Second cycle: Only report 1 resource
	callback2, flush2 := NewReportStateHandlers(ctx, fakeClient, logr.Discard(), "test-ns", "test-plan", "test-target", "rds", "cycle-002")
	_ = callback2("i-1", TestState{InstanceId: "i-1", WasRunning: true})
	_ = flush2()

	// Load and verify
	cm := &corev1.ConfigMap{}
	_ = fakeClient.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "hibernator-restore-test-plan"}, cm)

	var data restore.Data
	_ = json.Unmarshal([]byte(cm.Data["test-target.json"]), &data)

	// i-2 should have stale count of 1
	require.Equal(t, 1, data.StaleCounts["i-2"], "Missing resource should have stale count of 1")
	// i-1 should not be in stale counts
	require.Empty(t, data.StaleCounts["i-1"], "Reported resource should not be in stale counts")

	// Both should still be in state
	require.NotNil(t, data.State["i-1"])
	require.NotNil(t, data.State["i-2"])

	// i-2 should not be in ManagedByCycleIDs since it wasn't reported this cycle
	require.Empty(t, data.ManagedByCycleIDs["i-2"])
}

// TestAccumulator_BackwardCompatibility verifies that accumulator works with
// existing restore data that was saved before the struct conversion fix.
func TestAccumulator_BackwardCompatibility(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	ctx := context.Background()

	// Create existing ConfigMap with old-format data (map[string]any in State)
	oldData := restore.Data{
		Target:    "test-target",
		Executor:  "rds",
		Version:   1,
		IsLive:    true,
		CreatedAt: metav1.Now(),
		State: map[string]any{
			"old-instance": map[string]any{
				"instanceId": "old-123",
				"wasRunning": true,
			},
		},
		ManagedByCycleIDs: map[string]string{
			"old-instance": "cycle-old",
		},
	}

	oldDataBytes, _ := json.Marshal(oldData)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hibernator-restore-test-plan",
			Namespace: "test-ns",
			Labels: map[string]string{
				wellknown.LabelPlan: "test-plan",
			},
		},
		Data: map[string]string{
			"test-target.json": string(oldDataBytes),
		},
	}
	_ = fakeClient.Create(ctx, cm)

	// Add new data via accumulator
	callback, flush := NewReportStateHandlers(ctx, fakeClient, logr.Discard(), "test-ns", "test-plan", "test-target", "rds", "cycle-new")

	type TestState struct {
		InstanceId string `json:"instanceId"`
		WasRunning bool   `json:"wasRunning"`
	}

	_ = callback("new-instance", TestState{InstanceId: "new-456", WasRunning: true})
	_ = flush()

	// Load and verify merge
	var data restore.Data
	_ = fakeClient.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "hibernator-restore-test-plan"}, cm)
	_ = json.Unmarshal([]byte(cm.Data["test-target.json"]), &data)

	// Both old and new should be present
	require.NotNil(t, data.State["old-instance"])
	require.NotNil(t, data.State["new-instance"])

	// Old instance should be marked stale (not reported this cycle)
	require.Equal(t, 1, data.StaleCounts["old-instance"])

	// New instance should be tracked with new cycle
	require.Equal(t, "cycle-new", data.ManagedByCycleIDs["new-instance"])

	// Old instance should NOT be in ManagedByCycleIDs (dropped when not reported)
	require.Empty(t, data.ManagedByCycleIDs["old-instance"])
}

// TestAccumulator_NoOpShutdown verifies that an empty flush (no resources added)
// still creates a valid restore point.
func TestAccumulator_NoOpShutdown(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	ctx := context.Background()

	_, flush := NewReportStateHandlers(ctx, fakeClient, logr.Discard(), "test-ns", "test-plan", "test-target", "rds", "cycle-001")

	// Flush without adding any resources
	err := flush()
	require.NoError(t, err)

	// Verify ConfigMap was created with empty state
	cm := &corev1.ConfigMap{}
	err = fakeClient.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "hibernator-restore-test-plan"}, cm)
	require.NoError(t, err)

	var data restore.Data
	err = json.Unmarshal([]byte(cm.Data["test-target.json"]), &data)
	require.NoError(t, err)

	require.Empty(t, data.State)
	require.True(t, data.IsLive)
}
