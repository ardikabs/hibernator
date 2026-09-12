/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunListResourcesWide(t *testing.T) {
	useFakeClient(t, seedPlan("p"), seedDBFixture(t))
	ctx, buf := testCtx()

	opts := &restorePointOptions{root: testRoot()}
	require.NoError(t, runListResources(ctx, opts, "p"))
	require.Contains(t, buf.String(), "instance:i-1")
	require.Contains(t, buf.String(), "instance:i-2")
}

func TestRunListSummary(t *testing.T) {
	useFakeClient(t, seedPlan("p"), seedDBFixture(t))
	ctx, buf := testCtx()

	opts := &restorePointOptions{root: testRoot()}
	require.NoError(t, runList(ctx, opts, "p"))
	require.Contains(t, buf.String(), "db")
}

func TestRunListMissingConfigMap(t *testing.T) {
	useFakeClient(t, seedPlan("p"))
	ctx, buf := testCtx()

	opts := &restorePointOptions{root: testRoot()}
	require.ErrorContains(t, runListResources(ctx, opts, "p"), "no restore point found")

	// Summary hints instead of erroring.
	require.NoError(t, runList(ctx, opts, "p"))
	require.Contains(t, buf.String(), "No restore point")
}

func TestRunInspect(t *testing.T) {
	useFakeClient(t, seedPlan("p"), seedDBFixture(t))
	ctx, buf := testCtx()

	opts := &restorePointOptions{root: testRoot(), target: "db", resourceID: "instance:i-1"}
	require.NoError(t, runInspect(ctx, opts, "p"))
	require.Contains(t, buf.String(), "instance:i-1")
	require.Contains(t, buf.String(), "i-1")
}

func TestRunInspectRejectsNonObjectState(t *testing.T) {
	cm := seedRestoreCM("p", map[string]string{
		"db.json": `{"target":"db","state":{"instance:i-1":"just-a-string"}}`,
	})
	useFakeClient(t, seedPlan("p"), cm)
	ctx, _ := testCtx()

	// Regression: the unchecked type assertion used to panic here.
	opts := &restorePointOptions{root: testRoot(), target: "db", resourceID: "instance:i-1"}
	require.ErrorContains(t, runInspect(ctx, opts, "p"), "not an object")
}

func TestRunInspectShowsExcluded(t *testing.T) {
	cm := seedRestoreCM("p", map[string]string{
		"db.json": `{"target":"db","state":{"instance:i-1":{"id":"i-1"}},"status":{"instance:i-1":{"excluded":true}}}`,
	})
	useFakeClient(t, seedPlan("p"), cm)
	ctx, buf := testCtx()

	opts := &restorePointOptions{root: testRoot(), target: "db", resourceID: "instance:i-1"}
	require.NoError(t, runInspect(ctx, opts, "p"))
	require.Contains(t, buf.String(), "Excluded:")
	require.Contains(t, buf.String(), "yes")
}

func TestRunListWideShowsExcluded(t *testing.T) {
	cm := seedRestoreCM("p", map[string]string{
		"db.json": `{"target":"db","state":{"instance:i-1":{"id":"i-1"}},"status":{"instance:i-1":{"excluded":true}}}`,
	})
	useFakeClient(t, seedPlan("p"), cm)
	ctx, buf := testCtx()

	opts := &restorePointOptions{root: testRoot()}
	require.NoError(t, runListResources(ctx, opts, "p"))
	require.Contains(t, buf.String(), "EXCLUDED")
	require.Contains(t, buf.String(), "yes")
}

func TestRunInspectMissingEntries(t *testing.T) {
	useFakeClient(t, seedPlan("p"), seedDBFixture(t))
	ctx, _ := testCtx()

	opts := &restorePointOptions{root: testRoot(), target: "db", resourceID: "instance:nope"}
	require.ErrorContains(t, runInspect(ctx, opts, "p"), "not found in target")

	opts = &restorePointOptions{root: testRoot(), target: "nope", resourceID: "instance:i-1"}
	require.ErrorContains(t, runInspect(ctx, opts, "p"), "not found in restore point")
}
