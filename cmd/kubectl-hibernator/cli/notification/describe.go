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
	"k8s.io/apimachinery/pkg/types"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/printers"
)

type describeOptions struct {
	root     *common.RootOptions
	planName string
}

func newDescribeCommand(opts *common.RootOptions) *cobra.Command {
	describeOpts := &describeOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "describe <notification-name>",
		Short: "Display detailed information about a HibernateNotification",
		Long: `Show comprehensive details about a HibernateNotification including:
- Label selector configuration
- Subscribed events
- Sink configurations (name, type, secret/template references)
- Status: watched plans, delivery history, last delivery/failure times

Use --plan to check whether this notification's selector matches a specific plan's labels.

Examples:
  kubectl hibernator notification describe my-notification
  kubectl hibernator notification describe my-notification -n production
  kubectl hibernator notification describe my-notification --plan my-plan
  kubectl hibernator notification describe my-notification --json`,
		Args: cobra.ExactArgs(1),
		RunE: output.WrapRunE(func(ctx context.Context, args []string) error {
			return runDescribe(ctx, describeOpts, args[0])
		}),
	}

	cmd.Flags().StringVar(&describeOpts.planName, "plan", "", "Check if this notification matches the given HibernatePlan")

	return cmd
}

func runDescribe(ctx context.Context, opts *describeOptions, notifName string) error {
	c, err := common.NewK8sClient(opts.root)
	if err != nil {
		return err
	}

	ns := common.ResolveNamespace(opts.root)

	var notif hibernatorv1alpha1.HibernateNotification
	if err := c.Get(ctx, types.NamespacedName{Name: notifName, Namespace: ns}, &notif); err != nil {
		return fmt.Errorf("failed to get HibernateNotification %q in namespace %q: %w", notifName, ns, err)
	}

	out := &printers.NotifDescribeOutput{
		Notification: notif,
	}

	// If --plan is specified, check selector match
	if opts.planName != "" {
		var plan hibernatorv1alpha1.HibernatePlan
		if err := c.Get(ctx, types.NamespacedName{Name: opts.planName, Namespace: ns}, &plan); err != nil {
			return fmt.Errorf("failed to get HibernatePlan %q in namespace %q: %w", opts.planName, ns, err)
		}

		out.PlanMatch = &printers.NotifPlanMatch{
			PlanName: opts.planName,
			Matches:  selectorMatchesPlan(notif.Spec.Selector, plan.Labels),
		}
	}

	d := &printers.Dispatcher{JSON: opts.root.JsonOutput}
	return d.PrintObj(out, os.Stdout)
}
