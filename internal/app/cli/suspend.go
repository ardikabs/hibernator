/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package cli

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
	root    *rootOptions
	seconds float64
	until   string
	reason  string
}

// newSuspendCommand creates the "suspend" command.
func newSuspendCommand(opts *rootOptions) *cobra.Command {
	susOpts := &suspendOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "suspend <plan-name>",
		Short: "Suspend a HibernatePlan by setting annotations",
		Long: `Suspend a HibernatePlan by adding suspend-until and suspend-reason annotations.
The controller will prevent hibernation operations until the deadline expires.

You must specify either --seconds or --until to set the suspension deadline.

Examples:
  kubectl hibernator suspend my-plan --seconds 4 --reason "deploying new version"
  kubectl hibernator suspend my-plan --until "2026-01-15T06:00:00Z" --reason "maintenance window"
  kubectl hibernator suspend my-plan --seconds 24 --reason "incident response"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSuspend(cmd.Context(), susOpts, args[0])
		},
	}

	cmd.Flags().Float64Var(&susOpts.seconds, "seconds", 0, "Duration in seconds to suspend (e.g., 3600, 1800)")
	cmd.Flags().StringVar(&susOpts.until, "until", "", "Deadline for suspension in RFC3339 format in UTC (e.g., 2026-01-15T06:00:00Z)")
	cmd.Flags().StringVar(&susOpts.reason, "reason", "User initiated", "Reason for suspension (recommended)")

	return cmd
}

func runSuspend(ctx context.Context, opts *suspendOptions, planName string) error {
	// Validate: must have either --seconds or --until
	if opts.seconds <= 0 && opts.until == "" {
		return fmt.Errorf("either --seconds or --until must be specified")
	}
	if opts.seconds > 0 && opts.until != "" {
		return fmt.Errorf("only one of --seconds or --until can be specified")
	}

	// Calculate deadline
	var deadline time.Time
	if opts.seconds > 0 {
		deadline = time.Now().Add(time.Duration(opts.seconds * float64(time.Second)))
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

	// Patch: set annotations for suspend-until and reason
	patch := client.MergeFrom(plan.DeepCopy())

	plan.Spec.Suspend = true

	if plan.Annotations == nil {
		plan.Annotations = make(map[string]string)
	}
	plan.Annotations[wellknown.AnnotationSuspendUntil] = deadline.Format(time.RFC3339)
	if opts.reason != "" {
		plan.Annotations[wellknown.AnnotationSuspendReason] = opts.reason
	}

	if err := c.Patch(ctx, &plan, patch); err != nil {
		return fmt.Errorf("failed to patch HibernatePlan %q: %w", planName, err)
	}

	fmt.Printf("HibernatePlan %q suspended\n", planName)
	fmt.Printf("Suspended Until: %s\n", deadline.Format(time.RFC3339))
	if opts.reason != "" {
		fmt.Printf("Reason: %s\n", opts.reason)
	}

	return nil
}
