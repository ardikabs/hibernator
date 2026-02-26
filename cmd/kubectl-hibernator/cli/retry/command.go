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
	"github.com/ardikabs/hibernator/internal/wellknown"
)

type retryOptions struct {
	root  *common.RootOptions
	force bool
}

// NewCommand creates the "retry" command.
func NewCommand(opts *common.RootOptions) *cobra.Command {
	retryOpts := &retryOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "retry <plan-name>",
		Short: "Trigger a manual retry of a failed HibernatePlan",
		Long: `Trigger a manual retry by adding the retry-now annotation to a HibernatePlan.
The controller will detect this annotation and initiate a retry of the failed operation.

By default, this only works when the plan is in Error phase.
Use --force to annotate regardless of phase.

Examples:
  kubectl hibernator retry my-plan
  kubectl hibernator retry my-plan --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRetry(cmd.Context(), retryOpts, args[0])
		},
	}

	cmd.Flags().BoolVar(&retryOpts.force, "force", false, "Add retry annotation regardless of plan phase")

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

	// Check phase unless --force
	if !opts.force && plan.Status.Phase != hibernatorv1alpha1.PhaseError {
		return fmt.Errorf("HibernatePlan %q is in %q phase (not Error); use --force to retry anyway", planName, plan.Status.Phase)
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

	fmt.Printf("Retry triggered for HibernatePlan %q\n", planName)
	if plan.Status.RetryCount > 0 {
		fmt.Printf("Previous retries: %d\n", plan.Status.RetryCount)
	}

	return nil
}