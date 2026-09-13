/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ardikabs/hibernator/internal/restore"
)

func seedDBFixture(t *testing.T) *corev1.ConfigMap {
	t.Helper()
	return seedRestoreCM("p", map[string]string{
		"db.json": restoreEntry(t, "db", "rds", true, map[string]any{
			"instance:i-1": map[string]any{"id": "i-1", "state": "stopped"},
			"instance:i-2": map[string]any{"id": "i-2", "state": "stopped"},
		}),
	})
}

func TestRunDropRemovesResource(t *testing.T) {
	c := useFakeClient(t, seedPlan("p"), seedDBFixture(t))
	ctx, _ := testCtx()

	opts := &restorePointOptions{root: testRoot(), target: "db", resourceID: "instance:i-1"}
	require.NoError(t, runDrop(ctx, opts, "p"))

	var cm corev1.ConfigMap
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "hibernator-restore-p"}, &cm))
	var data restore.Data
	require.NoError(t, json.Unmarshal([]byte(cm.Data["db.json"]), &data))
	require.NotContains(t, data.State, "instance:i-1")
	require.Contains(t, data.State, "instance:i-2")
}
func TestRunDropMissingEntries(t *testing.T) {
	useFakeClient(t, seedPlan("p"), seedDBFixture(t))
	ctx, _ := testCtx()

	opts := &restorePointOptions{root: testRoot(), target: "db", resourceID: "instance:nope"}
	require.ErrorContains(t, runDrop(ctx, opts, "p"), "not found in target")

	opts = &restorePointOptions{root: testRoot(), target: "nope", resourceID: "instance:i-1"}
	require.ErrorContains(t, runDrop(ctx, opts, "p"), "not found in restore point")

	useFakeClient(t, seedPlan("q"))
	require.ErrorContains(t,
		runDrop(ctx, &restorePointOptions{root: testRoot(), target: "db", resourceID: "x"}, "q"),
		"no restore point")
}

func TestRunDropDryRunChangesNothing(t *testing.T) {
	c := useFakeClient(t, seedPlan("p"), seedDBFixture(t))
	ctx, buf := testCtx()

	opts := &restorePointOptions{root: testRoot(), target: "db", resourceID: "instance:i-1", dryRun: true}
	require.NoError(t, runDrop(ctx, opts, "p"))
	require.Contains(t, buf.String(), "DRY-RUN")

	var cm corev1.ConfigMap
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "hibernator-restore-p"}, &cm))
	require.Contains(t, cm.Data["db.json"], "instance:i-1")
}
