/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunExportTarget(t *testing.T) {
	useFakeClient(t, seedPlan("p"), seedDBFixture(t))
	ctx, _ := testCtx()

	path := filepath.Join(t.TempDir(), "db.json")
	opts := &exportOptions{root: testRoot(), target: "db", file: path}
	require.NoError(t, runExport(ctx, opts, "p"))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"target":"db"`)
	require.Contains(t, string(raw), "instance:i-1")
	require.Contains(t, string(raw), "instance:i-2")
}

func TestRunExportResource(t *testing.T) {
	useFakeClient(t, seedPlan("p"), seedDBFixture(t))
	ctx, _ := testCtx()

	path := filepath.Join(t.TempDir(), "res.json")
	opts := &exportOptions{root: testRoot(), target: "db", resourceID: "instance:i-1", file: path}
	require.NoError(t, runExport(ctx, opts, "p"))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"id": "i-1"`)
	require.NotContains(t, string(raw), "instance:i-2")
}

func TestRunExportMissingEntries(t *testing.T) {
	useFakeClient(t, seedPlan("p"), seedDBFixture(t))
	ctx, _ := testCtx()
	path := filepath.Join(t.TempDir(), "out.json")

	opts := &exportOptions{root: testRoot(), target: "nope", file: path}
	require.ErrorContains(t, runExport(ctx, opts, "p"), "not found in restore point")

	opts = &exportOptions{root: testRoot(), target: "db", resourceID: "instance:nope", file: path}
	require.ErrorContains(t, runExport(ctx, opts, "p"), "not found in target")

	// Nothing written on failure.
	_, err := os.Stat(path)
	require.True(t, os.IsNotExist(err))
}
