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
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/printers"
	"github.com/ardikabs/hibernator/internal/restore"
)

// newShowCommand shows summary of restore point(s)
func newShowCommand(opts *common.RootOptions) *cobra.Command {
	restoreOpts := &restorePointOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "show <plan-name>",
		Short: "Display restore point summary",
		Long: `Show a summary of the restore point for a HibernatePlan.
Displays:
- Overall live status (isLive=true means high-quality recent capture)
- Total number of resources in the restore point
- Summary by target

Examples:
  kubectl hibernator restore show my-plan -n production
  kubectl hibernator restore show my-plan --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShow(cmd.Context(), restoreOpts, args[0])
		},
	}

	return cmd
}

func runShow(ctx context.Context, opts *restorePointOptions, planName string) error {
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
		fmt.Fprintf(os.Stderr, "No restore point found for plan %q\n", planName)
		return nil
	}

	d := &printers.Dispatcher{JSON: opts.root.JsonOutput}
	return d.PrintObj(cm, os.Stdout)
}
