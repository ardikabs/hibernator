/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package version

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
	"github.com/ardikabs/hibernator/internal/version"
)

// NewCommand creates the "version" command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version of kubectl-hibernator",
		Long:  "Print the version of kubectl-hibernator",
		RunE: output.WrapRunE(func(ctx context.Context, args []string) error {
			out := output.FromContext(ctx)
			out.Info("kubectl-hibernator: %s", version.GetVersion())
			return nil
		}),
	}

	return cmd
}
