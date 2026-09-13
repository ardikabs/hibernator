/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package output

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSimpleFormatterRoutesStreams(t *testing.T) {
	var stdout, stderr bytes.Buffer
	f := &SimpleFormatter{stdout: &stdout, stderr: &stderr}

	f.Info("hello %s", "world")
	f.Success("done")
	f.Warning("careful")
	f.Error("boom")
	f.Hint("try this")

	require.Equal(t, "hello world\ndone\n", stdout.String())
	require.Equal(t, "⚠ Warning: careful\nError: boom\nHint: try this\n", stderr.String())
}

func TestColorFormatterPrefixes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	f := &ColorFormatter{stdout: &stdout, stderr: &stderr, colors: true}

	f.Success("done")
	f.Error("boom")

	require.Contains(t, stdout.String(), "✓")
	require.Contains(t, stdout.String(), "done")
	require.Contains(t, stderr.String(), "❌")
	require.Contains(t, stderr.String(), "boom")
}

func TestNewFormatterPrefersSimpleOffTTY(t *testing.T) {
	var stdout, stderr bytes.Buffer
	f := NewFormatter(&stdout, &stderr)
	// Buffers are never terminals, so this is deterministic regardless of
	// NO_COLOR/TERM in the environment.
	require.IsType(t, &SimpleFormatter{}, f)
}

func TestFormatterContextRoundTrip(t *testing.T) {
	var stdout, stderr bytes.Buffer
	f := &SimpleFormatter{stdout: &stdout, stderr: &stderr}

	got := FromContext(WithFormatter(context.Background(), f))
	require.Same(t, f, got)

	// Missing formatter falls back to a usable default, never nil.
	require.NotNil(t, FromContext(context.Background()))
}
