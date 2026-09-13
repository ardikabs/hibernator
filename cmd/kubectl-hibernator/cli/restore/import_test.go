/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ardikabs/hibernator/internal/restore"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "backup.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
	return path
}

func readTargetData(t *testing.T, ctx context.Context, c client.Client, plan, target string) restore.Data {
	t.Helper()
	var cm corev1.ConfigMap
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: restore.GetRestoreConfigMap(plan)}, &cm))
	_, _, data, err := loadTargetEntry(&cm, target)
	require.NoError(t, err)
	return data
}

func TestRunImportTargetReplacesWholesale(t *testing.T) {
	c := useFakeClient(t, seedPlan("p"), seedDBFixture(t))
	ctx, _ := testCtx()

	// Backup holds only one resource: import must drop the other.
	path := writeFile(t, `{"target":"db","executor":"rds","version":1,"isLive":true,"state":{"instance:i-9":{"id":"i-9"}}}`)
	opts := &importOptions{root: testRoot(), target: "db", file: path, yes: true}
	require.NoError(t, runImport(ctx, opts, "p"))

	data := readTargetData(t, ctx, c, "p", "db")
	require.Len(t, data.State, 1)
	require.Contains(t, data.State, "instance:i-9")
}

func TestRunImportRefusesCrossTarget(t *testing.T) {
	useFakeClient(t, seedPlan("p"), seedDBFixture(t))
	ctx, _ := testCtx()

	path := writeFile(t, `{"target":"other","state":{"x":{}}}`)
	opts := &importOptions{root: testRoot(), target: "db", file: path, yes: true}
	require.ErrorContains(t, runImport(ctx, opts, "p"), `backup targets "other", not "db"`)
}

func TestRunImportRejectsBadFile(t *testing.T) {
	useFakeClient(t, seedPlan("p"), seedDBFixture(t))
	ctx, _ := testCtx()

	opts := &importOptions{root: testRoot(), target: "db", file: filepath.Join(t.TempDir(), "missing.json"), yes: true}
	require.ErrorContains(t, runImport(ctx, opts, "p"), "failed to read import file")

	path := writeFile(t, `not json`)
	opts = &importOptions{root: testRoot(), target: "db", file: path, yes: true}
	require.ErrorContains(t, runImport(ctx, opts, "p"), "invalid target backup")
}

func TestRunImportDryRunChangesNothing(t *testing.T) {
	c := useFakeClient(t, seedPlan("p"), seedDBFixture(t))
	ctx, buf := testCtx()

	path := writeFile(t, `{"target":"db","state":{"instance:i-9":{}}}`)
	opts := &importOptions{root: testRoot(), target: "db", file: path, dryRun: true}
	require.NoError(t, runImport(ctx, opts, "p"))
	require.Contains(t, buf.String(), "DRY-RUN")

	data := readTargetData(t, ctx, c, "p", "db")
	require.Contains(t, data.State, "instance:i-1")
	require.NotContains(t, data.State, "instance:i-9")
}

func TestRunImportResource(t *testing.T) {
	c := useFakeClient(t, seedPlan("p"), seedDBFixture(t))
	ctx, _ := testCtx()

	path := writeFile(t, `{"id":"i-1","state":"available"}`)
	opts := &importOptions{root: testRoot(), target: "db", resourceID: "instance:i-1", file: path, yes: true}
	require.NoError(t, runImport(ctx, opts, "p"))

	data := readTargetData(t, ctx, c, "p", "db")
	raw, err := json.Marshal(data.State["instance:i-1"])
	require.NoError(t, err)
	require.Contains(t, string(raw), "available")
	// Sibling untouched.
	require.Contains(t, data.State, "instance:i-2")
}

func TestDiffResourceIDs(t *testing.T) {
	mk := func(ids ...string) restore.Data {
		d := restore.Data{State: map[string]any{}}
		for _, id := range ids {
			d.State[id] = map[string]any{"v": id}
		}
		return d
	}
	added, removed, changed := diffResourceIDs(mk("a", "b"), mk("b", "c"))
	require.Equal(t, []string{"c"}, added)
	require.Equal(t, []string{"a"}, removed)
	require.Empty(t, changed, "identical content is not a change")

	// Same IDs, different content: changed.
	from := mk("a")
	to := mk("a")
	to.State["a"] = map[string]any{"v": "other"}
	_, _, changed = diffResourceIDs(from, to)
	require.Equal(t, []string{"a"}, changed)

	// Identical sets: nothing reported.
	added, removed, changed = diffResourceIDs(mk("a"), mk("a"))
	require.Empty(t, added)
	require.Empty(t, removed)
	require.Empty(t, changed)
}
