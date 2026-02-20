/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

type suspendOptions struct {
	root   *rootOptions
	hours  float64
	until  string
	reason string
}

// newSuspendCommand creates the "suspend" command.
func newSuspendCommand(opts *rootOptions) *cobra.Command {
	susOpts := &suspendOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "suspend <plan-name>",
		Short: "Suspend a HibernatePlan by setting annotations",
		Long: `Suspend a HibernatePlan by adding suspend-until and suspend-reason annotations.
The controller will prevent hibernation operations until the deadline expires.

You must specify either --hours or --until to set the suspension deadline.

Examples:
  kubectl hibernator suspend my-plan --hours 4 --reason "deploying new version"
  kubectl hibernator suspend my-plan --until "2026-01-15T06:00:00Z" --reason "maintenance window"
  kubectl hibernator suspend my-plan --hours 24 --reason "incident response"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSuspend(cmd.Context(), susOpts, args[0])
		},
	}

	cmd.Flags().Float64Var(&susOpts.hours, "hours", 0, "Duration in hours to suspend (e.g., 4, 0.5)")
	cmd.Flags().StringVar(&susOpts.until, "until", "", "Deadline for suspension in RFC3339 format (e.g., 2026-01-15T06:00:00Z)")
	cmd.Flags().StringVar(&susOpts.reason, "reason", "", "Reason for suspension (recommended)")

	return cmd
}

func runSuspend(ctx context.Context, opts *suspendOptions, planName string) error {
	// Validate: must have either --hours or --until
	if opts.hours <= 0 && opts.until == "" {
		return fmt.Errorf("either --hours or --until must be specified")
	}
	if opts.hours > 0 && opts.until != "" {
		return fmt.Errorf("only one of --hours or --until can be specified")
	}

	// Calculate deadline
	var deadline time.Time
	if opts.hours > 0 {
		deadline = time.Now().Add(time.Duration(opts.hours * float64(time.Hour)))
	} else {
		var err error
		deadline, err = time.Parse(time.RFC3339, opts.until)
		if err != nil {
			return fmt.Errorf("invalid --until format (expected RFC3339, e.g., 2026-01-15T06:00:00Z): %w", err)
		}
		if deadline.Before(time.Now()) {
			return fmt.Errorf("--until deadline %s is in the past", opts.until)
		}
	}

	c, err := newK8sClient(opts.root)
	if err != nil {
		return err
	}

	ns := resolveNamespace(opts.root)

	// Fetch current plan to verify it exists
	var plan hibernatorv1alpha1.HibernatePlan
	if err := c.Get(ctx, types.NamespacedName{Name: planName, Namespace: ns}, &plan); err != nil {
		return fmt.Errorf("failed to get HibernatePlan %q in namespace %q: %w", planName, ns, err)
	}

	// Patch annotations
	patch := client.MergeFrom(plan.DeepCopy())

	if plan.Annotations == nil {
		plan.Annotations = make(map[string]string)
	}
	plan.Annotations[wellknown.AnnotationSuspendUntil] = deadline.UTC().Format(time.RFC3339)
	if opts.reason != "" {
		plan.Annotations[wellknown.AnnotationSuspendReason] = opts.reason
	}

	if err := c.Patch(ctx, &plan, patch); err != nil {
		return fmt.Errorf("failed to patch HibernatePlan %q: %w", planName, err)
	}

	fmt.Printf("HibernatePlan %q suspended until %s\n", planName, deadline.UTC().Format(time.RFC3339))
	if opts.reason != "" {
		fmt.Printf("Reason: %s\n", opts.reason)
	}

	return nil
}
