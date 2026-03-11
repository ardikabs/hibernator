/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package retry

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

type retryOptions struct {
	root *common.RootOptions
}

// NewCommand creates the "retry" command.
func NewCommand(opts *common.RootOptions) *cobra.Command {
	retryOpts := &retryOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "retry <plan-name>",
		Short: "Trigger a manual retry of a failed HibernatePlan",
		Long: `Trigger a manual retry by adding the retry-now annotation to a HibernatePlan.
The controller will detect this annotation and initiate a retry of the failed operation.

This command only applies to plans in Error phase. To re-run the last executor
operation on an Active or Hibernated plan, use the 'restart' subcommand instead.

Examples:
  kubectl hibernator retry my-plan`,
		Args: cobra.ExactArgs(1),
		RunE: output.WrapRunE(func(ctx context.Context, args []string) error {
			return runRetry(ctx, retryOpts, args[0])
		}),
	}

	// --force is deprecated: retry is now strictly limited to Error phase plans.
	// Use 'restart' to re-run the last operation on Active or Hibernated plans.
	var deprecatedForce bool
	cmd.Flags().BoolVar(&deprecatedForce, "force", false, "[DEPRECATED] has no effect; retry is now restricted to Error phase plans")
	_ = cmd.Flags().MarkDeprecated("force", "retry is now restricted to plans in Error phase. Use 'restart' to re-trigger an operation on Active or Hibernated plans")

	return cmd
}

func runRetry(ctx context.Context, opts *retryOptions, planName string) error {
	c, err := common.NewK8sClient(opts.root)
	if err != nil {
		return err
	}

	ns := common.ResolveNamespace(opts.root)

	// Fetch current plan
	var plan hibernatorv1alpha1.HibernatePlan
	if err := c.Get(ctx, types.NamespacedName{Name: planName, Namespace: ns}, &plan); err != nil {
		return fmt.Errorf("failed to get HibernatePlan %q in namespace %q: %w", planName, ns, err)
	}

	// retry is strictly for Error phase plans
	if plan.Status.Phase != hibernatorv1alpha1.PhaseError {
		return retryPhaseError(planName, plan.Status.Phase)
	}

	// Patch annotation
	patch := client.MergeFrom(plan.DeepCopy())

	if plan.Annotations == nil {
		plan.Annotations = make(map[string]string)
	}
	plan.Annotations[wellknown.AnnotationRetryNow] = "true"

	if err := c.Patch(ctx, &plan, patch); err != nil {
		return fmt.Errorf("failed to patch HibernatePlan %q: %w", planName, err)
	}

	out := output.FromContext(ctx)
	out.Success("Retry triggered for HibernatePlan %q", planName)
	if plan.Status.RetryCount > 0 {
		out.Info("Previous retries: %d", plan.Status.RetryCount)
	}

	return nil
}

// retryPhaseError returns an informative error when retry is attempted on a plan that is not in Error phase.
// It guides the user toward the correct subcommand based on the plan's current phase.
func retryPhaseError(planName string, phase hibernatorv1alpha1.PlanPhase) error {
	const hint = `
Hint: 'retry' is reserved for plans stuck in Error phase — it clears the error and
re-attempts the failed operation using the controller's recovery mechanism.

If the plan is Active or Hibernated and you want to re-run the last executor operation
(e.g., re-apply a partial hibernation or wakeup), use:

  kubectl hibernator restart %s

'restart' is a one-shot, voluntary re-trigger that honours the plan's current state
without interfering with the schedule or phase transitions.`

	return fmt.Errorf("HibernatePlan %q is in %q phase, not Error — cannot retry\n%s",
		planName, phase, fmt.Sprintf(hint, planName))
}
