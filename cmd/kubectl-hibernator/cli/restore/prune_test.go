/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunPruneMarksExcludedByDefault(t *testing.T) {
	c := useFakeClient(t, seedPlan("p"), seedDBFixture(t))
	ctx, _ := testCtx()

	opts := &pruneOptions{root: testRoot(), target: "db", keep: []string{"instance:i-1"}, yes: true}
	require.NoError(t, runPrune(ctx, opts, "p"))

	data := readTargetData(t, ctx, c, "p", "db")
	// State payloads stay intact; only the marker is set.
	require.Contains(t, data.State, "instance:i-1")
	require.Contains(t, data.State, "instance:i-2")
	require.False(t, data.Status["instance:i-1"].Excluded)
	require.True(t, data.Status["instance:i-2"].Excluded, "non-kept resource must be marked excluded")
}

func TestRunPruneDeleteRemoves(t *testing.T) {
	c := useFakeClient(t, seedPlan("p"), seedDBFixture(t))
	ctx, _ := testCtx()

	opts := &pruneOptions{root: testRoot(), target: "db", keep: []string{"instance:i-1"}, yes: true, delete: true}
	require.NoError(t, runPrune(ctx, opts, "p"))

	data := readTargetData(t, ctx, c, "p", "db")
	require.Contains(t, data.State, "instance:i-1")
	require.NotContains(t, data.State, "instance:i-2")
}

func TestRunPruneNothingToDo(t *testing.T) {
	useFakeClient(t, seedPlan("p"), seedDBFixture(t))
	ctx, buf := testCtx()

	opts := &pruneOptions{root: testRoot(), target: "db", keep: []string{"instance:i-1", "instance:i-2"}, yes: true}
	require.NoError(t, runPrune(ctx, opts, "p"))
	require.Contains(t, buf.String(), "Nothing to prune")
}

func TestRunPruneDryRunChangesNothing(t *testing.T) {
	c := useFakeClient(t, seedPlan("p"), seedDBFixture(t))
	ctx, buf := testCtx()

	opts := &pruneOptions{root: testRoot(), target: "db", keep: []string{"instance:i-1"}, dryRun: true}
	require.NoError(t, runPrune(ctx, opts, "p"))
	require.Contains(t, buf.String(), "DRY-RUN")
	require.Contains(t, buf.String(), "instance:i-2")

	data := readTargetData(t, ctx, c, "p", "db")
	require.Contains(t, data.State, "instance:i-2")
	_, hasStatus := data.Status["instance:i-2"]
	require.False(t, hasStatus, "dry-run must not create status entries")
}

func TestRunPruneMissingTarget(t *testing.T) {
	useFakeClient(t, seedPlan("p"), seedDBFixture(t))
	ctx, _ := testCtx()

	opts := &pruneOptions{root: testRoot(), target: "nope", keep: []string{"x"}, yes: true}
	require.ErrorContains(t, runPrune(ctx, opts, "p"), "not found in restore point")
}
