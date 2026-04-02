/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

import (
	"github.com/spf13/cobra"

	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
)

// NewCommand creates the "notification" parent command group.
func NewCommand(opts *common.RootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "notification",
		Aliases: []string{"notif"},
		Short:   "Manage HibernateNotification resources",
		Long: `Commands for inspecting and testing HibernateNotification resources.

HibernateNotifications define how and when operators are alerted about
hibernation lifecycle events (Start, Success, Failure, Recovery, PhaseChange).

Examples:
  # List all notifications in the current namespace
  kubectl hibernator notification list

  # List notifications matching a specific plan
  kubectl hibernator notification list --plan my-plan

  # Describe a notification
  kubectl hibernator notification describe my-notification

  # Check if a notification matches a plan
  kubectl hibernator notification describe my-notification --plan my-plan

  # Send a test notification (dry-run)
  kubectl hibernator notification send my-notification --event Success --dry-run

  # Send a real test notification
  kubectl hibernator notification send my-notification --event Start --sink slack-alerts

  # Send with local config file (no cluster secrets needed)
  kubectl hibernator notification send my-notification --event Failure --config-file ./slack-config.json

  # Send with custom template file
  kubectl hibernator notification send my-notification --event Success --template-file ./custom.gotpl

  # Fully local mode (no cluster access needed)
  kubectl hibernator notification send --event Success --sink-type slack --config-file ./slack-config.json

  # Fully local with plan from file
  kubectl hibernator notification send --event Failure --sink-type telegram --config-file ./tg.json --plan-file ./plan.yaml`,
	}

	cmd.AddCommand(newListCommand(opts))
	cmd.AddCommand(newDescribeCommand(opts))
	cmd.AddCommand(newSendCommand(opts))

	return cmd
}
