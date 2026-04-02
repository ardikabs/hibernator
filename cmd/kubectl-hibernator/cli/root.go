/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package cli

import (
	"os"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli/describe"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli/list"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli/logs"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli/notification"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli/override"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli/preview"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli/restart"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli/restore"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli/resume"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli/retry"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli/suspend"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli/version"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
)

var scheme = runtime.NewScheme()

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = hibernatorv1alpha1.AddToScheme(scheme)
}

// NewRootCommand creates the root command for kubectl-hibernator.
func NewRootCommand() *cobra.Command {
	opts := &common.RootOptions{}

	cmd := &cobra.Command{
		Use:   "kubectl-hibernator",
		Short: "Manage Hibernator plans from the command line",
		Long: `kubectl-hibernator is a CLI plugin for managing HibernatePlan resources.

It provides commands to inspect schedules, view plan status, suspend/resume
hibernation, trigger retries, and tail controller logs.

Install by copying the binary to your PATH:
  cp bin/kubectl-hibernator /usr/local/bin/kubectl-hibernator

Then use as:
  kubectl hibernator list
  kubectl hibernator describe my-plan
  kubectl hibernator preview my-plan
  kubectl hibernator suspend my-plan --hours 4 --reason "deployment"
  kubectl hibernator resume my-plan
  kubectl hibernator retry my-plan
  kubectl hibernator override my-plan --to hibernate
  kubectl hibernator restart my-plan
  kubectl hibernator logs my-plan`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			formatter := output.NewFormatter(os.Stdout, os.Stderr)
			cmd.SetContext(output.WithFormatter(cmd.Context(), formatter))
			return nil
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Global flags
	cmd.PersistentFlags().StringVar(&opts.Kubeconfig, "kubeconfig", "", "Path to kubeconfig file (defaults to $KUBECONFIG or ~/.kube/config)")
	cmd.PersistentFlags().StringVarP(&opts.Namespace, "namespace", "n", "", "Kubernetes namespace (defaults to current context namespace)")
	cmd.PersistentFlags().BoolVar(&opts.JsonOutput, "json", false, "Output in JSON format")

	// Register subcommands
	cmd.AddCommand(version.NewCommand())
	cmd.AddCommand(list.NewCommand(opts))
	cmd.AddCommand(describe.NewCommand(opts))
	cmd.AddCommand(preview.NewCommand(opts))
	cmd.AddCommand(suspend.NewCommand(opts))
	cmd.AddCommand(resume.NewCommand(opts))
	cmd.AddCommand(retry.NewCommand(opts))
	cmd.AddCommand(override.NewCommand(opts))
	cmd.AddCommand(restart.NewCommand(opts))
	cmd.AddCommand(restore.NewCommand(opts))
	cmd.AddCommand(notification.NewCommand(opts))
	cmd.AddCommand(logs.NewCommand(opts))

	return cmd
}
