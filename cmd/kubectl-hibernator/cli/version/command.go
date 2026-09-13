/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package version

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
	"github.com/ardikabs/hibernator/internal/version"
)

// versionJSON is the machine-readable version output for --json.
type versionJSON struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// NewCommand creates the "version" command.
func NewCommand(opts *common.RootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version of kubectl-hibernator",
		Long:  "Print the version of kubectl-hibernator",
		RunE: output.WrapRunE(func(ctx context.Context, args []string) error {
			out := output.FromContext(ctx)
			if opts.JsonOutput {
				raw, err := json.Marshal(versionJSON{Version: version.Version, Commit: version.CommitHash})
				if err != nil {
					return fmt.Errorf("marshal version: %w", err)
				}
				out.Info(string(raw))
				return nil
			}
			out.Info("kubectl-hibernator: %s", version.GetVersion())

			// Check for updates (silently ignore errors)
			checker := NewChecker()
			if newVersion, hasUpdate := checker.CheckForUpdate(ctx); hasUpdate {
				out.Info("")
				out.Info(FormatUpdateMessage(newVersion))
			}

			return nil
		}),
	}

	return cmd
}
