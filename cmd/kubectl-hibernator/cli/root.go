/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package cli

import (
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli/describe"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli/list"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli/logs"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli/restore"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli/resume"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli/retry"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli/preview"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli/suspend"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli/version"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
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
		Long: "kubectl-hibernator is a CLI plugin for managing HibernatePlan resources.\n\n" +
			"It provides commands to inspect schedules, view plan status, suspend/resume\n" +
			"hibernation, trigger retries, and tail controller logs.\n\n" +
			"Install by copying the binary to your PATH:\n" +
			"  cp bin/kubectl-hibernator /usr/local/bin/kubectl-hibernator\n\n" +
			"Then use as:\n" +
			"  kubectl hibernator list\n" +
			"  kubectl hibernator describe my-plan\n" +
			"  kubectl hibernator preview my-plan\n" +
			"  kubectl hibernator suspend my-plan --hours 4 --reason \"deployment\"\n" +
			"  kubectl hibernator resume my-plan\n" +
			"  kubectl hibernator retry my-plan\n" +
			"  kubectl hibernator logs my-plan",
		SilenceUsage:  true,
		SilenceErrors: false,
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
	cmd.AddCommand(logs.NewCommand(opts))
	cmd.AddCommand(restore.NewCommand(opts))

	return cmd
}
