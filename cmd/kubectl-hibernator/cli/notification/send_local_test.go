/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
)

const localPlanYAML = `apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: local-plan
  namespace: test-ns
spec:
  schedule:
    timezone: UTC
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON"]
  execution:
    strategy:
      type: Parallel
  behavior:
    mode: Strict
  targets:
    - name: db
      type: noop
      connectorRef:
        kind: CloudProvider
        name: test
`

func TestRunSendLocalDryRun(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "sink.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{"url":"https://example.invalid/hook"}`), 0600))
	planPath := filepath.Join(dir, "plan.yaml")
	require.NoError(t, os.WriteFile(planPath, []byte(localPlanYAML), 0600))

	ctx, buf := testCtx()
	opts := &sendOptions{
		root:       &common.RootOptions{Namespace: "test-ns"},
		event:      "Success",
		sinkType:   "webhook",
		configFile: configPath,
		planFile:   planPath,
		dryRun:     true,
	}
	require.NoError(t, runSend(ctx, opts, ""))
	require.Contains(t, buf.String(), "Success")
}

func TestRunSendLocalDryRunNormalizesEventCase(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "sink.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{"url":"https://example.invalid/hook"}`), 0600))
	planPath := filepath.Join(dir, "plan.yaml")
	require.NoError(t, os.WriteFile(planPath, []byte(localPlanYAML), 0600))

	ctx, buf := testCtx()
	opts := &sendOptions{
		root:       &common.RootOptions{Namespace: "test-ns"},
		event:      "failure",
		sinkType:   "webhook",
		configFile: configPath,
		planFile:   planPath,
		dryRun:     true,
	}
	require.NoError(t, runSend(ctx, opts, ""))
	// Lowercase input renders with the canonical event name.
	require.Contains(t, buf.String(), "Failure")
}

func TestRunSendRejectsBadEvent(t *testing.T) {
	ctx, _ := testCtx()
	opts := &sendOptions{root: &common.RootOptions{Namespace: "test-ns"}, event: "bogus"}
	require.ErrorContains(t, runSend(ctx, opts, ""), "invalid event")
}

func TestRunSendLocalRejectsPlanFlag(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "sink.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{}`), 0600))

	ctx, _ := testCtx()
	opts := &sendOptions{
		root:       &common.RootOptions{Namespace: "test-ns"},
		event:      "Success",
		sinkType:   "webhook",
		configFile: configPath,
		planName:   "p",
		dryRun:     true,
	}
	require.ErrorContains(t, runSend(ctx, opts, ""), "--plan requires cluster access")
}
