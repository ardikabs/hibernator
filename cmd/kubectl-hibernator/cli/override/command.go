/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package override

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

type overrideOptions struct {
	root    *common.RootOptions
	to      string
	disable bool
	seconds float64
	until   string
	dryRun  bool
}

// NewCommand creates the "override" subcommand.
func NewCommand(opts *common.RootOptions) *cobra.Command {
	overrideOpts := &overrideOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "override <plan-name>",
		Short: "Override the schedule of a HibernatePlan",
		Long: `Manually override the schedule of a HibernatePlan by setting the override-action
and override-phase-target annotations.

While override is active, the controller ignores the configured schedule for this plan.
The plan will be driven toward the specified target phase (hibernate or wakeup) and will
stay there on every reconcile tick — it will NOT automatically transition to the next
phase based on the schedule.

⚠️  CAUTION: By default, this is a persistent override.
Once activated, the plan stays locked in the target phase until the override is explicitly
deactivated using --disable. Forgetting to deactivate means the plan will never follow its
schedule again until the annotations are removed.

Use --until or --seconds to set an automatic expiration time for the override.
When the deadline is reached, the override is automatically disabled and schedule
control is restored.

The --until flag supports multiple user-friendly formats:
  Relative:      "in 30 minutes", "in 2 hours", "tomorrow at 6am", "next Monday"
  Date:          "2026-01-15", "Jan 15, 2026"
  Date+Time:     "2026-01-15 14:30", "Jan 15, 2026 2:30pm"
  RFC3339:       "2026-01-15T14:30:00Z" (for scripts)

All times are interpreted in your local timezone and stored as UTC internally.

Use restart instead for a voluntary one-shot re-trigger of the last operation.
Use retry instead when the plan is stuck in PhaseError.

Activating the override requires --to (hibernate or wakeup).
Deactivating the override requires --disable.

Examples:
  # Force the plan to hibernate immediately, ignoring the schedule
  kubectl hibernator override my-plan --to hibernate

  # Force the plan to wake up immediately, ignoring the schedule
  kubectl hibernator override my-plan --to wakeup

  # Deactivate the override and restore normal schedule control
  kubectl hibernator override my-plan --disable

  # Force wakeup for 2 hours, then auto-restore schedule control
  kubectl hibernator override my-plan --to wakeup --seconds 7200

  # Force hibernation until tomorrow morning
  kubectl hibernator override my-plan --to hibernate --until "tomorrow at 8am"

  # Force wakeup until a specific date/time
  kubectl hibernator override my-plan --to wakeup --until "2026-01-15 14:30"

  # Preview what would happen without actually overriding
  kubectl hibernator override my-plan --to hibernate --until "in 2 hours" --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: output.WrapRunE(func(ctx context.Context, args []string) error {
			return runOverride(ctx, overrideOpts, args[0])
		}),
	}

	cmd.Flags().StringVar(&overrideOpts.to, "to", "", `Phase to drive the plan toward. Required when activating. Valid values: "hibernate", "wakeup"`)
	cmd.Flags().BoolVar(&overrideOpts.disable, "disable", false, "Deactivate the override and restore normal schedule control")
	cmd.Flags().StringVar(&overrideOpts.until, "until", "", `Deadline for override. Supports formats: "in 30 minutes", "tomorrow at 6am", "2026-01-15 14:30", "2026-01-15T14:30:00Z"`)
	cmd.Flags().Float64Var(&overrideOpts.seconds, "seconds", 0, "Duration in seconds for override to remain active (e.g., 3600, 1800)")
	cmd.Flags().BoolVar(&overrideOpts.dryRun, "dry-run", false, "Preview what would happen without making changes")

	return cmd
}

func runOverride(ctx context.Context, opts *overrideOptions, planName string) error {
	if opts.disable && opts.to != "" {
		return fmt.Errorf("--disable and --to are mutually exclusive")
	}
	if !opts.disable && opts.to == "" {
		return fmt.Errorf("--to is required when activating an override; use --disable to deactivate")
	}
	if opts.to != "" && opts.to != wellknown.OverridePhaseTargetHibernate && opts.to != wellknown.OverridePhaseTargetWakeup {
		return fmt.Errorf("invalid --to %q; valid values are %q and %q", opts.to, wellknown.OverridePhaseTargetHibernate, wellknown.OverridePhaseTargetWakeup)
	}
	if opts.seconds < 0 {
		return fmt.Errorf("--seconds must be positive")
	}
	if opts.seconds > 0 && opts.until != "" {
		return fmt.Errorf("only one of --seconds or --until can be specified")
	}

	out := output.FromContext(ctx)

	var deadline time.Time
	switch {
	case opts.seconds > 0:
		deadline = time.Now().Add(time.Duration(opts.seconds * float64(time.Second)))
	case opts.until != "":
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

	var plan hibernatorv1alpha1.HibernatePlan
	if err := c.Get(ctx, types.NamespacedName{Name: planName, Namespace: ns}, &plan); err != nil {
		return fmt.Errorf("failed to get HibernatePlan %q in namespace %q: %w", planName, ns, err)
	}

	// Display dry-run information
	if opts.dryRun {
		out.Info("[DRY-RUN] Previewing override for HibernatePlan %q", planName)
		out.Info("  Current Phase: %s", plan.Status.Phase)
		out.Info("  Current Override State: active=%v", common.IsMarkedTrue(plan.Annotations, wellknown.AnnotationOverrideAction))
		if opts.disable {
			out.Hint("Action: Would deactivate override")
			out.Hint("Would remove annotations:")
			out.Hint("  - %s", wellknown.AnnotationOverrideAction)
			out.Hint("  - %s", wellknown.AnnotationOverridePhaseTarget)
			out.Hint("  - %s", wellknown.AnnotationOverrideUntil)
		} else {
			out.Hint("Action: Would activate override")
			out.Hint("Target Phase: %s", opts.to)
			out.Hint("Would set %s: true", wellknown.AnnotationOverrideAction)
			out.Hint("Would set %s: %s", wellknown.AnnotationOverridePhaseTarget, opts.to)
			if !deadline.IsZero() {
				out.Hint("Would set %s: %s", wellknown.AnnotationOverrideUntil, timeparse.FormatDeadline(deadline))
			} else {
				out.Warning("Override would be indefinite (no deadline)")
			}
		}
		out.Success("Dry-run complete. No changes were made.")
		return nil
	}

	patch := client.MergeFrom(plan.DeepCopy())

	if plan.Annotations == nil {
		plan.Annotations = make(map[string]string)
	}

	if opts.disable {
		return deactivateOverride(ctx, c, &plan, patch, planName)
	}

	if !deadline.IsZero() {
		plan.Annotations[wellknown.AnnotationOverrideUntil] = deadline.Format(time.RFC3339)
	}

	if err := activateOverride(ctx, c, &plan, patch, planName, opts.to); err != nil {
		return err
	}

	out.Success("Override activated for HibernatePlan %q (target: %s)", planName, opts.to)
	if !deadline.IsZero() {
		out.Hint("Override Until: %s (%s)",
			timeparse.FormatDeadline(deadline),
			timeparse.FormatDuration(deadline))
	} else {
		out.Warning("Override Indefinitely: the plan will stay locked at the target phase until you run: kubectl hibernator override %s --disable", planName)
	}

	return nil
}

func activateOverride(ctx context.Context, c client.Client, plan *hibernatorv1alpha1.HibernatePlan, patch client.Patch, planName, target string) error {
	switch plan.Status.Phase {
	case hibernatorv1alpha1.PhaseActive, hibernatorv1alpha1.PhaseHibernated:
		// valid — proceed
	default:
		return fmt.Errorf("HibernatePlan %q is in %q phase; override only applies to Active or Hibernated plans (execution phases run to completion naturally)", planName, plan.Status.Phase)
	}

	common.MarkTrue(plan.Annotations, wellknown.AnnotationOverrideAction)
	plan.Annotations[wellknown.AnnotationOverridePhaseTarget] = target

	if err := c.Patch(ctx, plan, patch); err != nil {
		return fmt.Errorf("failed to patch HibernatePlan %q: %w", planName, err)
	}

	return nil
}

func deactivateOverride(ctx context.Context, c client.Client, plan *hibernatorv1alpha1.HibernatePlan, patch client.Patch, planName string) error {
	out := output.FromContext(ctx)
	if !common.IsMarkedTrue(plan.Annotations, wellknown.AnnotationOverrideAction) {
		out.Success("HibernatePlan %q does not have an active override, already in its normal schedule control state", planName)
		return nil
	}

	delete(plan.Annotations, wellknown.AnnotationOverrideAction)
	delete(plan.Annotations, wellknown.AnnotationOverridePhaseTarget)
	delete(plan.Annotations, wellknown.AnnotationOverrideUntil)
	if err := c.Patch(ctx, plan, patch); err != nil {
		return fmt.Errorf("failed to patch HibernatePlan %q: %w", planName, err)
	}

	out.Success("Override deactivated for HibernatePlan %q — schedule control restored", planName)
	return nil
}
