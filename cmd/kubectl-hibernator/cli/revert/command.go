/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package revert

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

type revertOptions struct {
	root   *common.RootOptions
	dryRun bool
	wait   bool
}

// NewCommand creates the "revert" command.
func NewCommand(opts *common.RootOptions) *cobra.Command {
	revertOpts := &revertOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "revert <plan-name>",
		Short: "Revert a failed hibernation by waking up successfully hibernated targets",
		Long: `Trigger a revert operation on a HibernatePlan in Error phase.

This command sets the revert annotation, causing the controller to transition
the plan to WakingUp and attempt to restore targets that were successfully
hibernated. Targets that failed to hibernate are skipped (treated as no-op).

After wakeup completes:
  - During hibernation window: plan transitions to Suspended
  - During active window: plan transitions to Active

Use --dry-run to preview which targets would be reverted without making changes.

Examples:
  kubectl hibernator revert my-plan
  kubectl hibernator revert my-plan --dry-run
  kubectl hibernator revert my-plan --wait`,
		Args: cobra.ExactArgs(1),
		RunE: output.WrapRunE(func(ctx context.Context, args []string) error {
			return runRevert(ctx, revertOpts, args[0])
		}),
	}

	cmd.Flags().BoolVar(&revertOpts.dryRun, "dry-run", false, "Preview which targets would be reverted without making changes")
	cmd.Flags().BoolVar(&revertOpts.wait, "wait", false, "Wait for the revert operation to complete")

	return cmd
}

func runRevert(ctx context.Context, opts *revertOptions, planName string) error {
	c, err := common.NewK8sClient(opts.root)
	if err != nil {
		return err
	}

	ns := common.ResolveNamespace(opts.root)
	out := output.FromContext(ctx)

	// Fetch current plan
	var plan hibernatorv1alpha1.HibernatePlan
	if err := c.Get(ctx, types.NamespacedName{Name: planName, Namespace: ns}, &plan); err != nil {
		return fmt.Errorf("failed to get HibernatePlan %q in namespace %q: %w", planName, ns, err)
	}

	// revert is only for Error phase plans
	if plan.Status.Phase != hibernatorv1alpha1.PhaseError {
		return revertPhaseError(planName, plan.Status.Phase)
	}

	// Dry-run: preview which targets would be reverted
	if opts.dryRun {
		return runDryRun(ctx, c, out, &plan)
	}

	// Set revert annotation
	patch := client.MergeFrom(plan.DeepCopy())

	if plan.Annotations == nil {
		plan.Annotations = make(map[string]string)
	}
	plan.Annotations[wellknown.AnnotationRevert] = "true"

	if err := c.Patch(ctx, &plan, patch); err != nil {
		return fmt.Errorf("failed to patch HibernatePlan %q: %w", planName, err)
	}

	out.Success("Revert triggered for HibernatePlan %q", planName)
	out.Info("Targets with live restore data will be restored. Failed targets will be skipped.")

	// Wait for completion if requested
	if opts.wait {
		return waitForRevert(ctx, c, out, planName, ns)
	}

	return nil
}

func runDryRun(ctx context.Context, c client.Client, out output.Formatter, plan *hibernatorv1alpha1.HibernatePlan) error {
	restoreMgr := restore.NewManager(c, logr.Discard()) // no logger needed for dry-run

	out.Info("Dry-run: Previewing revert for HibernatePlan %q", plan.Name)
	out.Info("")
	out.Info("%-20s %-15s %s", "TARGET", "STATUS", "ACTION")
	out.Info("%-20s %-15s %s", "------", "------", "------")

	revertCount := 0
	skipCount := 0

	for _, target := range plan.Spec.Targets {
		data, err := restoreMgr.Load(ctx, plan.Namespace, plan.Name, target.Name)
		if err != nil {
			out.Info("%-20s %-15s %s", target.Name, "ERROR", fmt.Sprintf("Failed to check: %v", err))
			skipCount++
			continue
		}

		if data != nil && data.IsLive {
			out.Info("%-20s %-15s %s", target.Name, "LIVE", "Will be reverted")
			revertCount++
		} else {
			out.Info("%-20s %-15s %s", target.Name, "NO DATA", "Will be skipped (no-op)")
			skipCount++
		}
	}

	out.Info("")
	out.Info("Summary: %d target(s) will be reverted, %d target(s) will be skipped", revertCount, skipCount)

	if revertCount == 0 {
		out.Warning("No targets have live restore data. Revert would be a no-op.")
	}

	return nil
}

func waitForRevert(ctx context.Context, c client.Client, out output.Formatter, planName, ns string) error {
	out.Info("Waiting for revert to complete...")

	pollInterval := 5 * time.Second
	maxWait := 10 * time.Minute
	deadline := time.Now().Add(maxWait)

	for time.Now().Before(deadline) {
		var plan hibernatorv1alpha1.HibernatePlan
		if err := c.Get(ctx, types.NamespacedName{Name: planName, Namespace: ns}, &plan); err != nil {
			return fmt.Errorf("failed to poll plan status: %w", err)
		}

		switch plan.Status.Phase {
		case hibernatorv1alpha1.PhaseActive:
			out.Success("Revert completed. Plan is now Active.")
			return nil
		case hibernatorv1alpha1.PhaseSuspended:
			out.Success("Revert completed. Plan is now Suspended (revert during hibernation window).")
			out.Info("Use 'kubectl hibernator resume %s' to resume normal schedule.", planName)
			return nil
		case hibernatorv1alpha1.PhaseError:
			// Still in Error - check if revert annotation is gone (meaning revert failed or no-op)
			if plan.Annotations[wellknown.AnnotationRevert] != "true" {
				out.Warning("Revert annotation was removed but plan is still in Error phase.")
				out.Info("This may indicate no targets had live restore data or the wakeup failed.")
				return nil
			}
			// Revert annotation still present - revert is in progress
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
			// Continue polling
		}
	}

	return fmt.Errorf("timeout waiting for revert to complete (waited %s)", maxWait)
}

func revertPhaseError(planName string, phase hibernatorv1alpha1.PlanPhase) error {
	return fmt.Errorf(`HibernatePlan %q is in %q phase, not Error — cannot revert

Revert is only available for plans stuck in Error phase after a failed hibernation.
It selectively wakes up targets that were successfully hibernated and skips failed ones.

To check the plan status and available actions:
  kubectl hibernator describe %s`, planName, phase, planName)
}
