/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"github.com/spf13/cobra"

	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
)

type restorePointOptions struct {
	root       *common.RootOptions
	target     string
	resourceID string
}

// NewCommand creates the "restore" parent command group.
func NewCommand(opts *common.RootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Manage restore points for HibernatePlans",
		Long: `Commands for inspecting and managing restore points (captured resource state during hibernation).

Restore points store metadata about resources as they were when hibernation occurred,
enabling proper restoration during wakeup with correct configuration and state.

Examples:
  # Initialize empty restore point for target
  kubectl hibernator restore init my-plan --target eks-cluster --executor eks

  # Show restore point summary
  kubectl hibernator restore show my-plan

  # List all resources
  kubectl hibernator restore list my-plan

  # List all resources for a specific target
  kubectl hibernator restore list my-plan --target eks-cluster

  # Inspect of a specific resource in the restore point
  kubectl hibernator restore inspect my-plan --target eks-cluster --resource-id xyz

  # Update
  kubectl hibernator restore patch my-plan --target eks-cluster --resource-id xyz --set desiredCapacity=10

  # Drop a resource from the restore point (use with caution)
  kubectl hibernator restore drop my-plan --target eks-cluster --resource-id xyz`,
	}

	cmd.AddCommand(newInitCommand(opts))
	cmd.AddCommand(newShowCommand(opts))
	cmd.AddCommand(newListResourcesCommand(opts))
	cmd.AddCommand(newInspectCommand(opts))
	cmd.AddCommand(newPatchCommand(opts))
	cmd.AddCommand(newDropCommand(opts))

	return cmd
}
