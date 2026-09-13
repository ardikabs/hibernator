/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ardikabs/hibernator/internal/restore"
)

// TestWeekendWorkflowExportPruneImport is the Friday-to-Monday partial-run
// loop in miniature: back up the full entry, mark everything but the weekend
// runner excluded, then restore the backup and prove the markers are gone
// with content identical.
func TestWeekendWorkflowExportPruneImport(t *testing.T) {
	c := useFakeClient(t, seedPlan("p"), seedDBFixture(t))
	ctx, _ := testCtx()
	root := testRoot()

	// Friday: snapshot the full restore data.
	backupPath := filepath.Join(t.TempDir(), "db-full.json")
	require.NoError(t, runExport(ctx, &exportOptions{root: root, target: "db", file: backupPath}, "p"))
	backupBytes, err := os.ReadFile(backupPath)
	require.NoError(t, err)

	// Weekend: everything but the weekend runner is excluded (state kept).
	require.NoError(t, runPrune(ctx, &pruneOptions{root: root, target: "db", keep: []string{"instance:i-1"}, yes: true}, "p"))
	trimmed := readTargetData(t, ctx, c, "p", "db")
	require.Contains(t, trimmed.State, "instance:i-1")
	require.Contains(t, trimmed.State, "instance:i-2")
	require.True(t, trimmed.Status["instance:i-2"].Excluded)

	// Monday: restore the backup wholesale.
	require.NoError(t, runImport(ctx, &importOptions{root: root, target: "db", file: backupPath, yes: true}, "p"))
	restored := readTargetData(t, ctx, c, "p", "db")

	var want restore.Data
	require.NoError(t, json.Unmarshal(backupBytes, &want))
	require.Equal(t, want.Target, restored.Target)
	require.Equal(t, want.State, restored.State)
	require.False(t, restored.Status["instance:i-2"].Excluded, "imported backup predates the marker")
}
