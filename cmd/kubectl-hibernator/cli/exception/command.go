/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package exception

import (
	"github.com/spf13/cobra"

	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
)

// NewCommand creates the ScheduleException command group.
func NewCommand(opts *common.RootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exception",
		Short: "Manage ScheduleException resources",
		Long: `Create, list, inspect, and delete ScheduleException resources
that temporarily deviate HibernatePlans from their base schedules.

Subcommands:
  create  Create exceptions for one plan (--plan) or a plan selector (-l)
  list    Compact kubectl-get style listing with plan, label, and type filters
  describe  Operational detail with state, plan, and label filters
  delete  Delete by name or qualified filters with confirmation`,
	}

	cmd.AddCommand(newCreateCommand(opts))
	cmd.AddCommand(newListCommand(opts))
	cmd.AddCommand(newDescribeCommand(opts))
	cmd.AddCommand(newDeleteCommand(opts))
	return cmd
}
