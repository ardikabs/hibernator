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

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/printers"
	"github.com/ardikabs/hibernator/internal/restore"
)

// newListCommand displays restore point information (summary by default, detailed with -o wide)
func newListCommand(opts *common.RootOptions) *cobra.Command {
	restoreOpts := &restorePointOptions{root: opts}
	var outputFormat string

	cmd := &cobra.Command{
		Use:   "list <plan-name>",
		Short: "Display restore point information",
		Long: `Show restore point information for a HibernatePlan.

By default, shows a summary of the restore point:
- Overall live status (isLive=true means high-quality recent capture)
- Total number of resources in the restore point
- Summary by target

Output formats:
  -o wide   Show detailed list of all individual resources
  -o json   Show detailed list in JSON format

Filter by specific target using --target flag.

Examples:
  kubectl hibernator restore list my-plan              # Show summary
  kubectl hibernator restore list my-plan -o wide      # Show detailed list
  kubectl hibernator restore list my-plan --target eks-cluster -o wide
  kubectl hibernator restore list my-plan -o json      # Detailed list in JSON`,
		Args: cobra.ExactArgs(1),
		RunE: output.WrapRunE(func(ctx context.Context, args []string) error {
			switch outputFormat {
			case "wide":
				return runListResources(ctx, restoreOpts, args[0])
			case "json":
				restoreOpts.root.JsonOutput = true
				return runListResources(ctx, restoreOpts, args[0])
			case "":
				return runList(ctx, restoreOpts, args[0])
			default:
				return fmt.Errorf("invalid output format %q; supported formats: wide, json", outputFormat)
			}
		}),
	}

	cmd.Flags().StringVarP(&outputFormat, "output", "o", "", "Output format (json or wide)")
	cmd.Flags().StringVarP(&restoreOpts.target, "target", "t", "", "Filter by specific target name")

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
		return fmt.Errorf("no restore point found for plan %q", planName)
	}

	d := &printers.Dispatcher{JSON: opts.root.JsonOutput}
	return d.PrintObj(&printers.RestoreResourcesOutput{
		ConfigMap: cm,
		Target:    opts.target,
	}, os.Stdout)
}

// runList displays a summary of the restore point
func runList(ctx context.Context, opts *restorePointOptions, planName string) error {
	c, err := common.NewK8sClient(opts.root)
	if err != nil {
		return err
	}

	ns := common.ResolveNamespace(opts.root)

	// Get the plan to verify it exists
	var plan hibernatorv1alpha1.HibernatePlan
	if err := c.Get(ctx, types.NamespacedName{Name: planName, Namespace: ns}, &plan); err != nil {
		return fmt.Errorf("failed to get HibernatePlan %q in namespace %q: %w", planName, ns, err)
	}

	// Load restore ConfigMap
	cmName := restore.GetRestoreConfigMap(planName)
	var cm corev1.ConfigMap
	if err := c.Get(ctx, types.NamespacedName{Name: cmName, Namespace: ns}, &cm); err != nil {
		out := output.FromContext(ctx)
		out.Hint("No restore point found for plan %q", planName)
		return nil
	}

	d := &printers.Dispatcher{JSON: opts.root.JsonOutput}
	return d.PrintObj(cm, os.Stdout)
}
