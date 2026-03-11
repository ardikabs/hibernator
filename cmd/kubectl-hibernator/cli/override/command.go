/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package override

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

type overrideOptions struct {
	root    *common.RootOptions
	to      string
	disable bool
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

⚠️  CAUTION: This is a persistent override, not a one-shot action.
Once activated, the plan stays locked in the target phase until the override is explicitly
deactivated using --off. Forgetting to deactivate means the plan will never follow its
schedule again until the annotations are removed.

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
  kubectl hibernator override my-plan --disable`,
		Args: cobra.ExactArgs(1),
		RunE: output.WrapRunE(func(ctx context.Context, args []string) error {
			return runOverride(ctx, overrideOpts, args[0])
		}),
	}

	cmd.Flags().StringVar(&overrideOpts.to, "to", "", `Phase to drive the plan toward. Required when activating. Valid values: "hibernate", "wakeup"`)
	cmd.Flags().BoolVar(&overrideOpts.disable, "disable", false, "Deactivate the override and restore normal schedule control")

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

	c, err := common.NewK8sClient(opts.root)
	if err != nil {
		return err
	}

	ns := common.ResolveNamespace(opts.root)

	var plan hibernatorv1alpha1.HibernatePlan
	if err := c.Get(ctx, types.NamespacedName{Name: planName, Namespace: ns}, &plan); err != nil {
		return fmt.Errorf("failed to get HibernatePlan %q in namespace %q: %w", planName, ns, err)
	}

	patch := client.MergeFrom(plan.DeepCopy())

	if plan.Annotations == nil {
		plan.Annotations = make(map[string]string)
	}

	if opts.disable {
		return deactivateOverride(ctx, c, &plan, patch, planName)
	}

	return activateOverride(ctx, c, &plan, patch, planName, opts.to)
}

func activateOverride(ctx context.Context, c client.Client, plan *hibernatorv1alpha1.HibernatePlan, patch client.Patch, planName, target string) error {
	out := output.FromContext(ctx)

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

	out.Warning("The plan will stay locked at the target phase until you run: kubectl hibernator override %s --disable", planName)
	out.Success("Override activated for HibernatePlan %q (target: %s)", planName, target)
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

	if err := c.Patch(ctx, plan, patch); err != nil {
		return fmt.Errorf("failed to patch HibernatePlan %q: %w", planName, err)
	}

	out.Success("Override deactivated for HibernatePlan %q — schedule control restored", planName)
	return nil
}
