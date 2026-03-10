/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package app tests the runner execution pipeline using a fakeExecutor and a
// controller-runtime in-memory fake client.  No envtest binaries or real
// kubeconfig are required: tests run as ordinary `go test` unit tests.
//
// The executor contract (Shutdown / WakeUp semantics) is intentionally NOT
// exercised here; that belongs in each executor's own unit tests.  What we
// verify is the runner's orchestration layer:
//   - restore data accumulation and flush on Shutdown
//   - empty restore-point guarantee when a no-op Shutdown emits no keys
//   - restore data loading and delivery to the executor on Wakeup
//   - error propagation when the executor or pipeline step fails
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/internal/restore"
)

// ----------------------------------------------------------------------------
// fakeExecutor – fully controllable executor for pipeline tests
// ----------------------------------------------------------------------------

// fakeExecutor records every call made to it and allows callers to inject
// pre-canned errors or restore-data emissions.
type fakeExecutor struct {
	typeVal     string
	validateErr error
	shutdownErr error
	wakeupErr   error

	// restoreKeysToEmit: if non-nil, Shutdown will call spec.SaveRestoreData
	// once per entry, simulating an executor that emits restore state.
	restoreKeysToEmit map[string]any

	// Captured during execution for use in assertions.
	shutdownCalled  bool
	wakeupCalled    bool
	receivedRestore executor.RestoreData
}

func (f *fakeExecutor) Type() string                   { return f.typeVal }
func (f *fakeExecutor) Validate(_ executor.Spec) error { return f.validateErr }

func (f *fakeExecutor) Shutdown(_ context.Context, _ logr.Logger, spec executor.Spec) error {
	f.shutdownCalled = true
	if spec.SaveRestoreData != nil {
		for k, v := range f.restoreKeysToEmit {
			if err := spec.SaveRestoreData(k, v, true); err != nil {
				return fmt.Errorf("save restore data for %s: %w", k, err)
			}
		}
	}
	return f.shutdownErr
}

func (f *fakeExecutor) WakeUp(_ context.Context, _ logr.Logger, _ executor.Spec, rd executor.RestoreData) error {
	f.wakeupCalled = true
	f.receivedRestore = rd
	return f.wakeupErr
}

// ----------------------------------------------------------------------------
// Test helpers
// ----------------------------------------------------------------------------

// newTestRunner builds a runner with an in-memory fake client and an injected
// executor registry – no kubeconfig resolution or real executor factory.
// Any client.Object values in preloaded are seeded into the fake store.
func newTestRunner(cfg *Config, fakeExec *fakeExecutor, preloaded ...client.Object) *runner {
	reg := executor.NewRegistry()
	reg.Register(fakeExec)

	// Reuse the package-level scheme (already has corev1 + hibernatorv1alpha1).
	fc := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(preloaded...).
		Build()

	return &runner{
		cfg:       cfg,
		log:       logr.Discard(),
		k8sClient: fc,
		registry:  reg,
		// streamClient nil  → no streaming, reportProgress/reportCompletion log-only
	}
}

// baseConfig returns a minimal Config for the given operation and executor type.
// ConnectorKind is intentionally empty so buildExecutorSpec skips all API lookups.
func baseConfig(operation, executorType string) *Config {
	return &Config{
		Operation:     operation,
		TargetType:    executorType,
		Target:        "my-target",
		Plan:          "test-plan",
		Namespace:     "default",
		ExecutionID:   "exec-001",
		ConnectorKind: "", // skip CloudProvider / K8SCluster resolution
	}
}

// restoreCM builds a ConfigMap that pre-seeds restore data for "my-target"
// under plan "test-plan" in namespace "default".
func restoreCM(t *testing.T, state map[string]any) *corev1.ConfigMap {
	t.Helper()
	data := restore.Data{
		Target:   "my-target",
		Executor: "fake",
		Version:  1,
		IsLive:   true,
		State:    state,
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

// readRestoreData retrieves and unmarshals the restore.Data written to the fake
// client's ConfigMap by a Shutdown run.
func readRestoreData(t *testing.T, fc client.Client) restore.Data {
	t.Helper()
	cm := &corev1.ConfigMap{}
	err := fc.Get(context.Background(), client.ObjectKey{
		Namespace: "default",
		Name:      "hibernator-restore-test-plan",
	}, cm)
	require.NoError(t, err, "restore ConfigMap should exist after shutdown")

	require.Contains(t, cm.Data, "my-target.json", "restore data key must be present")
	var rd restore.Data
	require.NoError(t, json.Unmarshal([]byte(cm.Data["my-target.json"]), &rd))
	return rd
}

// ----------------------------------------------------------------------------
// Tests
// ----------------------------------------------------------------------------

// TestRunner_Shutdown_WritesRestorePoint verifies that when an executor emits
// restore keys via spec.SaveRestoreData, flush persists them to a ConfigMap.
func TestRunner_Shutdown_WritesRestorePoint(t *testing.T) {
	fakeExec := &fakeExecutor{
		typeVal: "fake",
		restoreKeysToEmit: map[string]any{
			"instance-1": map[string]any{"minSize": 0, "maxSize": 3},
			"instance-2": map[string]any{"minSize": 0, "maxSize": 5},
		},
	}
	r := newTestRunner(baseConfig("shutdown", "fake"), fakeExec)

	require.NoError(t, r.run(context.Background()))

	assert.True(t, fakeExec.shutdownCalled)

	rd := readRestoreData(t, r.k8sClient)
	assert.True(t, rd.IsLive)
	assert.Contains(t, rd.State, "instance-1")
	assert.Contains(t, rd.State, "instance-2")
}

// TestRunner_Shutdown_NoOp_WritesEmptyRestorePoint verifies that when an
// executor never calls spec.SaveRestoreData, flush still writes an empty but
// valid restore point.  This prevents a subsequent wakeup from failing with
// "no restore data found".
func TestRunner_Shutdown_NoOp_WritesEmptyRestorePoint(t *testing.T) {
	// Executor does nothing — zero restore keys emitted.
	fakeExec := &fakeExecutor{typeVal: "fake"}
	r := newTestRunner(baseConfig("shutdown", "fake"), fakeExec)

	require.NoError(t, r.run(context.Background()))
	assert.True(t, fakeExec.shutdownCalled)

	rd := readRestoreData(t, r.k8sClient)
	assert.True(t, rd.IsLive, "empty restore point should be marked IsLive")
	assert.Nil(t, rd.State, "empty restore point should have nil State")
}

// TestRunner_Wakeup_ReadsRestorePoint verifies that the runner loads restore
// data from the ConfigMap and passes it to the executor's WakeUp method.
func TestRunner_Wakeup_ReadsRestorePoint(t *testing.T) {
	state := map[string]any{
		"instance-1": map[string]any{"desiredCapacity": float64(3)},
	}
	fakeExec := &fakeExecutor{typeVal: "fake"}
	r := newTestRunner(baseConfig("wakeup", "fake"), fakeExec, restoreCM(t, state))

	require.NoError(t, r.run(context.Background()))

	assert.True(t, fakeExec.wakeupCalled)
	assert.True(t, fakeExec.receivedRestore.IsLive)
	require.Contains(t, fakeExec.receivedRestore.Data, "instance-1",
		"restore data should contain the key written during shutdown")
}

// TestRunner_Wakeup_EmptyRestorePoint_Succeeds verifies that a wakeup
// succeeds even when the restore point has nil State (the no-op shutdown case).
// The executor receives an empty Data map and is expected to handle it gracefully.
func TestRunner_Wakeup_EmptyRestorePoint_Succeeds(t *testing.T) {
	fakeExec := &fakeExecutor{typeVal: "fake"}
	// Pre-seed the empty restore point that a no-op shutdown would have written.
	r := newTestRunner(baseConfig("wakeup", "fake"), fakeExec, restoreCM(t, nil))

	require.NoError(t, r.run(context.Background()))

	assert.True(t, fakeExec.wakeupCalled)
	assert.Empty(t, fakeExec.receivedRestore.Data,
		"empty restore point should yield an empty (not nil) Data map")
}

// TestRunner_Shutdown_ExecutorError_ReturnsError verifies that when the
// executor's Shutdown method returns an error, run() surfaces it.
func TestRunner_Shutdown_ExecutorError_ReturnsError(t *testing.T) {
	fakeExec := &fakeExecutor{
		typeVal:     "fake",
		shutdownErr: fmt.Errorf("disk full: cannot proceed"),
	}
	r := newTestRunner(baseConfig("shutdown", "fake"), fakeExec)

	err := r.run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disk full")
	assert.True(t, fakeExec.shutdownCalled)
}

// TestRunner_Wakeup_MissingRestoreData_ReturnsError verifies that wakeup fails
// when no ConfigMap / restore data exists for the target.
func TestRunner_Wakeup_MissingRestoreData_ReturnsError(t *testing.T) {
	fakeExec := &fakeExecutor{typeVal: "fake"}
	// No preloaded ConfigMap.
	r := newTestRunner(baseConfig("wakeup", "fake"), fakeExec)

	err := r.run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no restore data found")
	assert.False(t, fakeExec.wakeupCalled, "WakeUp should not be called when restore data is missing")
}

// TestRunner_UnknownExecutorType_ReturnsError verifies that requesting an
// executor type not in the registry returns a descriptive error immediately.
func TestRunner_UnknownExecutorType_ReturnsError(t *testing.T) {
	fakeExec := &fakeExecutor{typeVal: "fake"}
	// Config requests "nonexistent", but only "fake" is registered.
	r := newTestRunner(baseConfig("shutdown", "nonexistent"), fakeExec)

	err := r.run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "executor not found")
}
