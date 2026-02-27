/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package resume

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

type resumeOptions struct {
	root *common.RootOptions
}

// NewCommand creates the "resume" command.
func NewCommand(opts *common.RootOptions) *cobra.Command {
	resOpts := &resumeOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "resume <plan-name>",
		Short: "Resume a suspended HibernatePlan",
		Long: `Resume a suspended HibernatePlan by removing suspend annotations
and setting spec.suspend=false.

This clears the suspend-until deadline and suspend-reason annotations,
allowing the controller to resume schedule evaluation.

Examples:
  kubectl hibernator resume my-plan
  kubectl hibernator resume my-plan -n production`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runResume(cmd.Context(), resOpts, args[0])
		},
	}

	return cmd
}

func runResume(ctx context.Context, opts *resumeOptions, planName string) error {
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

	// Check if actually suspended
	isSuspended := plan.Spec.Suspend
	hasSuspendAnnotations := false
	if plan.Annotations != nil {
		_, hasUntil := plan.Annotations[wellknown.AnnotationSuspendUntil]
		hasSuspendAnnotations = hasUntil
	}

	if !isSuspended && !hasSuspendAnnotations {
		fmt.Printf("HibernatePlan %q is not suspended\n", planName)
		return nil
	}

	// Patch: remove annotations and set spec.suspend=false
	patch := client.MergeFrom(plan.DeepCopy())

	plan.Spec.Suspend = false
	if plan.Annotations != nil {
		delete(plan.Annotations, wellknown.AnnotationSuspendUntil)
	}

	if err := c.Patch(ctx, &plan, patch); err != nil {
		return fmt.Errorf("failed to patch HibernatePlan %q: %w", planName, err)
	}

	fmt.Printf("HibernatePlan %q resumed\n", planName)

	return nil
}