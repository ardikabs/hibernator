/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restart

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

type restartOptions struct {
	root *common.RootOptions
}

// NewCommand creates the "restart" subcommand.
func NewCommand(opts *common.RootOptions) *cobra.Command {
	restartOpts := &restartOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "restart <plan-name>",
		Short: "Re-trigger the last executor operation on a HibernatePlan",
		Long: `Re-trigger the last executor operation by adding the restart annotation to a HibernatePlan.

The controller detects the annotation and re-runs the executor that was last recorded in
.status.currentOperation, without changing the schedule or overriding any phase logic.

This is useful when you want to:
  - Re-apply a partial hibernation or wakeup that succeeded only for some targets.
  - Test idempotency of an executor on a live plan.

Unlike override, restart is a voluntary, one-shot action — the annotation is consumed
(deleted) by the controller before re-execution, so it cannot loop.

The plan must be in PhaseActive (to re-run wakeup) or PhaseHibernated (to re-run hibernation).
Use retry instead when the plan is stuck in PhaseError.

Examples:
  kubectl hibernator restart my-plan
  kubectl hibernator restart my-plan -n production`,
		Args: cobra.ExactArgs(1),
		RunE: output.WrapRunE(func(ctx context.Context, args []string) error {
			return runRestart(ctx, restartOpts, args[0])
		}),
	}

	return cmd
}

func runRestart(ctx context.Context, opts *restartOptions, planName string) error {
	out := output.FromContext(ctx)
	c, err := common.NewK8sClient(opts.root)
	if err != nil {
		return err
	}

	ns := common.ResolveNamespace(opts.root)

	var plan hibernatorv1alpha1.HibernatePlan
	if err := c.Get(ctx, types.NamespacedName{Name: planName, Namespace: ns}, &plan); err != nil {
		return fmt.Errorf("failed to get HibernatePlan %q in namespace %q: %w", planName, ns, err)
	}

	switch plan.Status.Phase {
	case hibernatorv1alpha1.PhaseActive, hibernatorv1alpha1.PhaseHibernated:
		if plan.Status.CurrentOperation == "" {
			return fmt.Errorf("HibernatePlan %q has no recorded operation (.status.currentOperation is empty); the plan must have completed at least one hibernation cycle before restart can be used", planName)
		}
	default:
		return fmt.Errorf("HibernatePlan %q is in %q phase; restart only applies to Active or Hibernated plans", planName, plan.Status.Phase)
	}

	patch := client.MergeFrom(plan.DeepCopy())

	if plan.Annotations == nil {
		plan.Annotations = make(map[string]string)
	}
	common.MarkTrue(plan.Annotations, wellknown.AnnotationRestart)

	if err := c.Patch(ctx, &plan, patch); err != nil {
		return fmt.Errorf("failed to patch HibernatePlan %q: %w", planName, err)
	}

	out.Success("Restart triggered for HibernatePlan %q (last operation: %s)", planName, plan.Status.CurrentOperation)
	return nil
}
