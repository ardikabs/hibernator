/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package version

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
	"github.com/ardikabs/hibernator/internal/version"
)

func TestVersionJSONOutput(t *testing.T) {
	var buf bytes.Buffer
	ctx := output.WithFormatter(context.Background(), output.NewFormatter(&buf, &buf))
	ctx = output.WithWriter(ctx, &buf)

	// The --json path returns before the network update check, so this
	// test is hermetic.
	cmd := NewCommand(&common.RootOptions{JsonOutput: true})
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())
	require.Contains(t, buf.String(), `"version"`)
	require.Contains(t, buf.String(), version.Version)
	require.Contains(t, buf.String(), version.CommitHash)
}
