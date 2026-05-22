/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package suspend

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
	"github.com/ardikabs/hibernator/cmd/runner/timeparse"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

type suspendOptions struct {
	root    *common.RootOptions
	seconds float64
	until   string
	reason  string
	dryRun  bool
}

// NewCommand creates the "suspend" command.
func NewCommand(opts *common.RootOptions) *cobra.Command {
	susOpts := &suspendOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "suspend <plan-name>",
		Short: "Suspend a HibernatePlan by setting annotations",
		Long: `Suspend a HibernatePlan by setting spec.suspend=true and optional suspend-until annotation.
While suspended, the controller prevents all hibernation operations for this plan.

By default, suspension is indefinite — the plan stays suspended until explicitly resumed
using "kubectl hibernator resume". To set a bounded suspension period, specify either
--seconds or --until. When the deadline is reached, the controller automatically
removes the suspension and resumes normal schedule control.

The --until flag supports multiple user-friendly formats:
  Relative:      "in 30 minutes", "in 2 hours", "tomorrow at 6am", "next Monday"
  Date:          "2026-01-15", "Jan 15, 2026"
  Date+Time:     "2026-01-15 14:30", "Jan 15, 2026 2:30pm"
  RFC3339:       "2026-01-15T14:30:00Z" (for scripts)

All times are interpreted in your local timezone and stored as UTC internally.

Examples:
  # Suspend indefinitely (must manually resume)
  kubectl hibernator suspend my-plan --reason "manual hold"

  # Suspend for 4 hours
  kubectl hibernator suspend my-plan --seconds 14400 --reason "deploying new version"

  # Suspend using natural language
  kubectl hibernator suspend my-plan --until "in 2 hours" --reason "maintenance window"

  # Suspend until tomorrow morning
  kubectl hibernator suspend my-plan --until "tomorrow at 8am" --reason "scheduled maintenance"

  # Suspend until a specific date/time
  kubectl hibernator suspend my-plan --until "2026-01-15 14:30" --reason "holiday freeze"

  # Preview what would happen without actually suspending
  kubectl hibernator suspend my-plan --until "in 2 hours" --reason "test" --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: output.WrapRunE(func(ctx context.Context, args []string) error {
			return runSuspend(ctx, susOpts, args[0])
		}),
	}

	cmd.Flags().Float64Var(&susOpts.seconds, "seconds", 0, "Duration in seconds to suspend (e.g., 3600, 1800)")
	cmd.Flags().StringVar(&susOpts.until, "until", "", `Deadline for suspension. Supports formats: "in 30 minutes", "tomorrow at 6am", "2026-01-15 14:30", "2026-01-15T14:30:00Z"`)
	cmd.Flags().StringVar(&susOpts.reason, "reason", "User initiated", "Reason for suspension (recommended)")
	cmd.Flags().BoolVar(&susOpts.dryRun, "dry-run", false, "Preview what would happen without making changes")

	return cmd
}

func runSuspend(ctx context.Context, opts *suspendOptions, planName string) error {
	out := output.FromContext(ctx)

	if opts.seconds < 0 {
		return fmt.Errorf("--seconds must be positive")
	}
	if opts.seconds > 0 && opts.until != "" {
		return fmt.Errorf("only one of --seconds or --until can be specified")
	}

	// Calculate deadline
	var deadline time.Time
	if opts.seconds > 0 && opts.until != "" {
		return fmt.Errorf("only one of --seconds or --until can be specified")
	}

	if opts.seconds > 0 {
		deadline = time.Now().Add(time.Duration(opts.seconds * float64(time.Second)))
	} else if opts.until != "" {
		var err error
		deadline, err = timeparse.ParseDeadline(opts.until, time.Now())
		if err != nil {
			return fmt.Errorf("invalid --until value: %w", err)
		}
	}

	c, err := common.NewK8sClient(opts.root)
	if err != nil {
		return err
	}

	ns := common.ResolveNamespace(opts.root)

	// Fetch current plan to verify it exists
	var plan hibernatorv1alpha1.HibernatePlan
	if err := c.Get(ctx, types.NamespacedName{Name: planName, Namespace: ns}, &plan); err != nil {
		return fmt.Errorf("failed to get HibernatePlan %q in namespace %q: %w", planName, ns, err)
	}

	// Display dry-run information
	if opts.dryRun {
		out.Info("[DRY-RUN] Would suspend HibernatePlan %q", planName)
		out.Info("  Current Phase: %s", plan.Status.Phase)
		out.Info("  Current State: suspended=%v", plan.Spec.Suspend)
		if !deadline.IsZero() {
			out.Hint("Would set %s: %s", wellknown.AnnotationSuspendUntil, timeparse.FormatDeadline(deadline))
		} else {
			out.Warning("Would suspend indefinitely (no deadline)")
		}
		out.Hint("Would set %s: %s", wellknown.AnnotationSuspendReason, opts.reason)
		out.Hint("Would set spec.suspend: true")
		out.Success("Dry-run complete. No changes were made.")
		return nil
	}

	// Patch: set annotations for suspend-until and reason
	patch := client.MergeFrom(plan.DeepCopy())

	plan.Spec.Suspend = true

	if plan.Annotations == nil {
		plan.Annotations = make(map[string]string)
	}

	if !deadline.IsZero() {
		plan.Annotations[wellknown.AnnotationSuspendUntil] = deadline.Format(time.RFC3339)
	}

	if opts.reason != "" {
		plan.Annotations[wellknown.AnnotationSuspendReason] = opts.reason
	}

	if err := c.Patch(ctx, &plan, patch); err != nil {
		return fmt.Errorf("failed to patch HibernatePlan %q: %w", planName, err)
	}

	out.Success("HibernatePlan %q suspended", planName)
	if !deadline.IsZero() {
		out.Hint("Suspended Until: %s (%s)",
			timeparse.FormatDeadline(deadline),
			timeparse.FormatDuration(time.Now(), deadline))
	} else {
		out.Warning("Suspended Indefinitely: the plan will stay suspended until you run: kubectl hibernator resume %s", planName)
	}
	if opts.reason != "" {
		out.Info("Reason: %s", opts.reason)
	}

	return nil
}
