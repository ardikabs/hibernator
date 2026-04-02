/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/printers"
)

type listOptions struct {
	root          *common.RootOptions
	allNamespaces bool
	planName      string
}

func newListCommand(opts *common.RootOptions) *cobra.Command {
	listOpts := &listOptions{root: opts}

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List HibernateNotifications in the cluster",
		Long: `List all HibernateNotification resources with useful information such as
name, namespace, subscribed events, sink count, watched plans, and last delivery.

Use --plan to filter notifications whose selector matches a specific HibernatePlan's labels.

Examples:
  kubectl hibernator notification list
  kubectl hibernator notification list -n production
  kubectl hibernator notification list --all-namespaces
  kubectl hibernator notification list --plan my-plan
  kubectl hibernator notification list --json`,
		Args: cobra.NoArgs,
		RunE: output.WrapRunE(func(ctx context.Context, args []string) error {
			return runList(ctx, listOpts)
		}),
	}

	cmd.Flags().BoolVarP(&listOpts.allNamespaces, "all-namespaces", "A", false, "List notifications from all namespaces")
	cmd.Flags().StringVar(&listOpts.planName, "plan", "", "Filter notifications matching this HibernatePlan's labels")

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

	var notifications hibernatorv1alpha1.HibernateNotificationList
	if err := c.List(ctx, &notifications, client.InNamespace(ns)); err != nil {
		return fmt.Errorf("failed to list HibernateNotifications: %w", err)
	}

	items := make([]printers.NotifListItem, 0, len(notifications.Items))

	// If --plan is specified, fetch the plan and filter by label selector
	if opts.planName != "" {
		planNS := common.ResolveNamespace(opts.root)
		var plan hibernatorv1alpha1.HibernatePlan
		if err := c.Get(ctx, types.NamespacedName{Name: opts.planName, Namespace: planNS}, &plan); err != nil {
			return fmt.Errorf("failed to get HibernatePlan %q in namespace %q: %w", opts.planName, planNS, err)
		}

		for _, notif := range notifications.Items {
			if selectorMatchesPlan(notif.Spec.Selector, plan.Labels) {
				items = append(items, printers.NotifListItem{Notification: notif})
			}
		}
	} else {
		for _, notif := range notifications.Items {
			items = append(items, printers.NotifListItem{Notification: notif})
		}
	}

	out := &printers.NotifListOutput{Items: items}
	d := &printers.Dispatcher{JSON: opts.root.JsonOutput}
	return d.PrintObj(out, os.Stdout)
}

// selectorMatchesPlan evaluates whether a notification's label selector matches
// the given plan labels. This mirrors the controller's runtime matching logic.
func selectorMatchesPlan(selector metav1.LabelSelector, planLabels map[string]string) bool {
	sel, err := metav1.LabelSelectorAsSelector(&selector)
	if err != nil {
		return false
	}
	return sel.Matches(labels.Set(planLabels))
}
