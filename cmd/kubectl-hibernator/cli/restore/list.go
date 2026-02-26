/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/printers"
	"github.com/ardikabs/hibernator/internal/restore"
)

// newListResourcesCommand lists individual resources in the restore point
func newListResourcesCommand(opts *common.RootOptions) *cobra.Command {
	restoreOpts := &restorePointOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "list-resources <plan-name>",
		Short: "List all resources in the restore point",
		Long: `Show all resources stored in the restore point for a HibernatePlan.
Resources are organized by target. Display shows:
- Resource ID (identifier within the target's state)
- Target name
- Executor type
- Quality indicator (whether this is live/fresh data)

Filter by specific target using --target flag.

Examples:
  kubectl hibernator restore list-resources my-plan
  kubectl hibernator restore list-resources my-plan --target eks-cluster
  kubectl hibernator restore list-resources my-plan --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runListResources(cmd.Context(), restoreOpts, args[0])
		},
	}

	cmd.Flags().StringVar(&restoreOpts.target, "target", "", "Filter by specific target name")

	return cmd
}

func runListResources(ctx context.Context, opts *restorePointOptions, planName string) error {
	c, err := common.NewK8sClient(opts.root)
	if err != nil {
		return err
	}

	ns := common.ResolveNamespace(opts.root)

	// Load restore ConfigMap
	cmName := restore.GetRestoreConfigMap(planName)
	var cm corev1.ConfigMap
	if err := c.Get(ctx, types.NamespacedName{Name: cmName, Namespace: ns}, &cm); err != nil {
		fmt.Fprintf(os.Stderr, "No restore point found for plan %q\n", planName)
		return nil
	}

	d := &printers.Dispatcher{JSON: opts.root.JsonOutput}
	return d.PrintObj(&printers.RestoreResourcesOutput{
		ConfigMap: cm,
		Target:    opts.target,
	}, os.Stdout)
}
