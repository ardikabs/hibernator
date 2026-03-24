/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package list

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/printers"
	"github.com/ardikabs/hibernator/internal/scheduler"
)

type listOptions struct {
	root          *common.RootOptions
	allNamespaces bool
}

// NewCommand creates the "list" or "ls" command.
func NewCommand(opts *common.RootOptions) *cobra.Command {
	listOpts := &listOptions{root: opts}

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List HibernatePlans in the cluster",
		Long: `List all HibernatePlan resources with useful information such as
name, namespace, phase, suspension status, and next scheduled event.

Examples:
  kubectl hibernator list
  kubectl hibernator list -n production
  kubectl hibernator list --all-namespaces
  kubectl hibernator list --json
  kubectl hibernator ls`,
		RunE: output.WrapRunE(func(ctx context.Context, args []string) error {
			return runList(ctx, listOpts)
		}),
	}

	cmd.Flags().BoolVarP(&listOpts.allNamespaces, "all-namespaces", "A", false, "List plans from all namespaces")

	return cmd
}

func runList(ctx context.Context, opts *listOptions) error {
	c, err := common.NewK8sClient(opts.root)
	if err != nil {
		return err
	}

	ns := common.ResolveNamespace(opts.root)
	if opts.allNamespaces {
		ns = ""
	}

	var plans hibernatorv1alpha1.HibernatePlanList
	if err := c.List(ctx, &plans, client.InNamespace(ns)); err != nil {
		return fmt.Errorf("failed to list HibernatePlans: %w", err)
	}

	items := make([]printers.PlanListItem, len(plans.Items))
	for i, plan := range plans.Items {
		items[i].Plan = plan

		if !plan.Spec.Suspend {
			var exceptions []*scheduler.Exception
			if excs, err := common.FetchActiveExceptions(ctx, c, plan); err == nil && len(excs) > 0 {
				exceptions = excs
			}

			if event, err := common.ComputeNextEvent(plan.Spec.Schedule, exceptions); err == nil {
				items[i].NextEvent = event
			}
		}
	}

	output := &printers.PlanListOutput{Items: items}
	d := &printers.Dispatcher{JSON: opts.root.JsonOutput}
	return d.PrintObj(output, os.Stdout)
}
