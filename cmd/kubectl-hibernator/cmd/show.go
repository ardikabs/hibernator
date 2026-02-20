/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package cmd

import (
	"github.com/spf13/cobra"
)

// newShowCommand creates the "show" parent command.
func newShowCommand(opts *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Inspect HibernatePlan schedule and status",
		Long:  `Show commands display schedule details and current status of HibernatePlan resources.`,
	}

	cmd.AddCommand(newShowScheduleCommand(opts))
	cmd.AddCommand(newShowStatusCommand(opts))

	return cmd
}
