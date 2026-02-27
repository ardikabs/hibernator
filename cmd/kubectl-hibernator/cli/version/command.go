/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package version

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ardikabs/hibernator/internal/version"
)

// NewCommand creates the "version" command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version of kubectl-hibernator",
		Long:  "Print the version of kubectl-hibernator",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("kubectl-hibernator", version.GetVersion())
			return nil
		},
	}

	return cmd
}
