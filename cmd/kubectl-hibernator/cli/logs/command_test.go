/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package logs

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLogFilterMatches(t *testing.T) {
	f := &logFilter{planName: "my-plan", executionIDs: []string{"abc123"}}

	runnerLine := `{"ts":"2026-09-12T10:00:00Z","level":"info","msg":"progress","execution-service.runner-logs":"x","plan":"my-plan"}`
	require.True(t, f.matches(runnerLine))
	// Execution ID matches even without the plan name.
	idLine := `{"ts":"2026-09-12T10:00:00Z","level":"info","msg":"progress","execution-service.runner-logs":"x","executionId":"abc123"}`
	require.True(t, f.matches(idLine))
	// Non-runner lines never match.
	require.False(t, f.matches(`{"ts":"2026-09-12T10:00:00Z","level":"info","msg":"reconciling","plan":"my-plan"}`))
	// Other plans never match.
	require.False(t, f.matches(`{"ts":"2026-09-12T10:00:00Z","level":"info","msg":"progress","execution-service.runner-logs":"x","plan":"other-plan"}`))

	targeted := &logFilter{planName: "my-plan"}
	line := `{"ts":"2026-09-12T10:00:00Z","level":"info","msg":"progress","execution-service.runner-logs":"x","plan":"my-plan","target":"db"}`
	targeted.target = "db"
	require.True(t, targeted.matches(line))
	targeted.target = "eks"
	require.False(t, targeted.matches(line))

	errOnly := &logFilter{planName: "my-plan", level: "error"}
	require.True(t, errOnly.matches(`{"level":"info","msg":"x","execution-service.runner-logs":"x","plan":"my-plan","error":"boom"}`))
	require.False(t, errOnly.matches(runnerLine))
}

func TestFormatLogLine(t *testing.T) {
	// Non-JSON passes through untouched.
	require.Equal(t, "plain line", formatLogLine("plain line"))

	// Structured error context renders the error message.
	got := formatLogLine(`{"ts":"2026-09-12T10:00:00Z","level":"error","msg":"error context","error":"boom","target":"db","executionId":"e1"}`)
	require.Contains(t, got, "[ERROR]")
	require.Contains(t, got, "boom")
	require.Contains(t, got, "exec=e1, target=db")

	// Progress renders phase context.
	got = formatLogLine(`{"ts":"2026-09-12T10:00:00Z","level":"info","msg":"progress","message":"scaling","phase":"hibernating"}`)
	require.Contains(t, got, "scaling")
	require.Contains(t, got, "hibernating")
}

func TestConvertEpochToTimePrecision(t *testing.T) {
	sec := convertEpochToTime(1_700_000_000)
	require.Equal(t, int64(1_700_000_000), sec.Unix())

	ms := convertEpochToTime(1_700_000_000_123)
	require.Equal(t, int64(1_700_000_000), ms.Unix())
	require.Equal(t, int64(123_000_000), int64(ms.Nanosecond()))

	ns := convertEpochToTime(1_700_000_000_123_456_789)
	require.Equal(t, int64(1_700_000_000), ns.Unix())
}

func TestParseTimestampField(t *testing.T) {
	now := time.Now()
	entry := map[string]interface{}{"ts": "2026-09-12T10:00:00Z"}
	got, ok := parseTimestampField(entry, "ts", now)
	require.True(t, ok)
	require.Equal(t, 2026, got.Year())

	// Missing field falls back to default.
	got, ok = parseTimestampField(map[string]interface{}{}, "ts", now)
	require.False(t, ok)
	require.Equal(t, now, got)
}

func TestExtractString(t *testing.T) {
	entry := map[string]interface{}{"s": "x", "f": float64(10), "b": true}
	require.Equal(t, "x", extractString(entry, "s"))
	require.Equal(t, "10", extractString(entry, "f"))
	require.Equal(t, "true", extractString(entry, "b"))
	require.Equal(t, "", extractString(entry, "missing"))
}

func TestDiscoverControllerNamespace(t *testing.T) {
	t.Setenv("HIBERNATOR_CONTROLLER_NAMESPACE", "custom-ns")
	require.Equal(t, "custom-ns", discoverControllerNamespace())

	prev, had := os.LookupEnv("HIBERNATOR_CONTROLLER_NAMESPACE")
	require.NoError(t, os.Unsetenv("HIBERNATOR_CONTROLLER_NAMESPACE"))
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("HIBERNATOR_CONTROLLER_NAMESPACE", prev)
		}
	})
	require.Equal(t, "hibernator-system", discoverControllerNamespace())
}
