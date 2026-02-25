/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

type listOptions struct {
	root          *rootOptions
	allNamespaces bool
}

// newListCommand creates the "list" or "ls" command.
func newListCommand(opts *rootOptions) *cobra.Command {
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
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(cmd.Context(), listOpts)
		},
	}

	cmd.Flags().BoolVarP(&listOpts.allNamespaces, "all-namespaces", "A", false, "List plans from all namespaces")

	return cmd
}

func runList(ctx context.Context, opts *listOptions) error {
	c, err := newK8sClient(opts.root)
	if err != nil {
		return err
	}

	ns := resolveNamespace(opts.root)
	if opts.allNamespaces {
		ns = ""
	}

	var plans hibernatorv1alpha1.HibernatePlanList
	if err := c.List(ctx, &plans, client.InNamespace(ns)); err != nil {
		return fmt.Errorf("failed to list HibernatePlans: %w", err)
	}

	if opts.root.jsonOutput {
		return printListJSON(plans)
	}

	return printListTable(plans)
}

type listJSONItem struct {
	Name          string                       `json:"name"`
	Namespace     string                       `json:"namespace"`
	Phase         hibernatorv1alpha1.PlanPhase `json:"phase"`
	Suspended     bool                         `json:"suspended"`
	SuspendReason string                       `json:"suspendReason,omitempty"`
	NextEvent     string                       `json:"nextEvent,omitempty"`
	CreatedAt     string                       `json:"createdAt"`
}

func printListJSON(plans hibernatorv1alpha1.HibernatePlanList) error {
	items := []listJSONItem{}
	for _, plan := range plans.Items {
		item := listJSONItem{
			Name:      plan.Name,
			Namespace: plan.Namespace,
			Phase:     plan.Status.Phase,
			Suspended: plan.Spec.Suspend,
			CreatedAt: plan.CreationTimestamp.Format(time.RFC3339),
		}

		if plan.Annotations != nil {
			if reason, ok := plan.Annotations["hibernator.ardikabs.com/suspend-reason"]; ok {
				item.SuspendReason = reason
			}
		}

		// Try to calculate next event (next scheduled hibernation)
		if nextEvent := getNextEvent(plan); nextEvent != "" {
			item.NextEvent = nextEvent
		}

		items = append(items, item)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(items)
}

func printListTable(plans hibernatorv1alpha1.HibernatePlanList) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tNAMESPACE\tPHASE\tSUSPENDED\tNEXT EVENT\tAGE")

	for _, plan := range plans.Items {
		suspended := "no"
		if plan.Spec.Suspend {
			suspended = "yes"
		}

		age := formatAge(time.Since(plan.CreationTimestamp.Time))
		nextEvent := getNextEvent(plan)

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			plan.Name,
			plan.Namespace,
			plan.Status.Phase,
			suspended,
			nextEvent,
			age,
		)
	}

	return w.Flush()
}

// getNextEvent tries to calculate the next scheduled event (hibernation or wakeup)
func getNextEvent(plan hibernatorv1alpha1.HibernatePlan) string {
	if plan.Spec.Suspend {
		return "suspended"
	}

	// TODO: Implement proper next event calculation using the schedule evaluator
	// For now, just return empty string
	return ""
}

func formatAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dmo", int(d.Hours()/24/30))
	}
}
