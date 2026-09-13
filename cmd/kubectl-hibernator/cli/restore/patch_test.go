/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestConfirmPrompt(t *testing.T) {
	require.True(t, confirmPrompt(strings.NewReader("y\n"), "y"))
	require.True(t, confirmPrompt(strings.NewReader("Y\n"), "y"))
	require.True(t, confirmPrompt(strings.NewReader("  y  \n"), "y"))
	require.False(t, confirmPrompt(strings.NewReader("n\n"), "y"))
	require.False(t, confirmPrompt(strings.NewReader("\n"), "y"))
	// EOF (piped stdin, non-TTY) cancels instead of panicking.
	require.False(t, confirmPrompt(strings.NewReader(""), "y"))
}

func TestSetPathValue(t *testing.T) {
	obj := map[string]any{}
	require.NoError(t, setPathValue(obj, "desiredCapacity", "10"))
	// "10" parses as JSON number.
	require.Equal(t, float64(10), obj["desiredCapacity"])

	require.NoError(t, setPathValue(obj, "config.tags.env", "prod"))
	require.Equal(t, map[string]any{
		"tags": map[string]any{"env": "prod"},
	}, obj["config"])

	// Plain strings stay strings; booleans coerce via JSON.
	require.NoError(t, setPathValue(obj, "name", "my-db"))
	require.Equal(t, "my-db", obj["name"])
	require.NoError(t, setPathValue(obj, "enabled", "true"))
	require.Equal(t, true, obj["enabled"])

	// Traversing a scalar fails instead of corrupting.
	require.ErrorContains(t, setPathValue(obj, "name.first", "x"), "non-object")
}

func TestRemovePathValue(t *testing.T) {
	obj := map[string]any{"a": map[string]any{"b": 1, "c": 2}, "top": "x"}
	require.NoError(t, removePathValue(obj, "a.b"))
	require.Equal(t, map[string]any{"a": map[string]any{"c": 2}, "top": "x"}, obj)

	// Missing paths are a no-op, never an error.
	require.NoError(t, removePathValue(obj, "a.missing"))
	require.NoError(t, removePathValue(obj, "missing.deep.path"))
	require.NoError(t, removePathValue(obj, "top"))
	require.NotContains(t, obj, "top")
}

func TestMergePatchRFC7386(t *testing.T) {
	target := map[string]any{
		"keep":    "a",
		"replace": map[string]any{"x": 1, "y": 2},
		"drop":    "gone",
	}
	patch := map[string]any{
		"replace": map[string]any{"y": 3},
		"drop":    nil,
		"added":   true,
	}
	got := mergePatch(target, patch)
	require.Equal(t, map[string]any{
		"keep":    "a",
		"replace": map[string]any{"x": 1, "y": 3},
		"added":   true,
	}, got)
	// Input maps are not mutated.
	require.Equal(t, "gone", target["drop"])
}

func TestDeepCopyMapIsolation(t *testing.T) {
	orig := map[string]any{"nested": map[string]any{"v": []any{1}}}
	dup := deepCopyMap(orig)
	dup["nested"].(map[string]any)["v"].([]any)[0] = 99
	require.Equal(t, 1, orig["nested"].(map[string]any)["v"].([]any)[0])
}

func TestRunPatchDryRunAppliesNothing(t *testing.T) {
	cm := seedRestoreCM("p", map[string]string{
		"db.json": `{"target":"db","executor":"rds","version":1,"isLive":true,"state":{"instance:i-1":{"id":"i-1"}}}`,
	})
	c := useFakeClient(t, seedPlan("p"), cm)
	ctx, buf := testCtx()

	opts := &patchOptions{
		root:       testRoot(),
		target:     "db",
		resourceID: "instance:i-1",
		sets:       []string{"state=available"},
		dryRun:     true,
	}
	require.NoError(t, runPatch(ctx, opts, "p"))
	require.Contains(t, buf.String(), "DRY RUN")

	// ConfigMap untouched.
	var got corev1.ConfigMap
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "hibernator-restore-p"}, &got))
	require.NotContains(t, got.Data["db.json"], "available")
}

func TestRunPatchRejectsBadSetOp(t *testing.T) {
	cm := seedRestoreCM("p", map[string]string{
		"db.json": `{"target":"db","state":{"instance:i-1":{"id":"i-1"}}}`,
	})
	useFakeClient(t, seedPlan("p"), cm)
	ctx, _ := testCtx()

	opts := &patchOptions{
		root:       testRoot(),
		target:     "db",
		resourceID: "instance:i-1",
		sets:       []string{"noseparator"},
		dryRun:     true,
	}
	require.ErrorContains(t, runPatch(ctx, opts, "p"), "expected format key=value")
}

func TestRunPatchYesAppliesWithoutPrompt(t *testing.T) {
	cm := seedRestoreCM("p", map[string]string{
		"db.json": `{"target":"db","state":{"instance:i-1":{"id":"i-1"}}}`,
	})
	c := useFakeClient(t, seedPlan("p"), cm)
	ctx, _ := testCtx()

	// --yes skips the stdin prompt, so the full apply path runs in tests.
	opts := &patchOptions{
		root:       testRoot(),
		target:     "db",
		resourceID: "instance:i-1",
		sets:       []string{"state=available"},
		yes:        true,
	}
	require.NoError(t, runPatch(ctx, opts, "p"))

	var got corev1.ConfigMap
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "hibernator-restore-p"}, &got))
	require.Contains(t, got.Data["db.json"], `"state":"available"`)
}
