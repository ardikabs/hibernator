/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/samber/lo"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/internal/restore"
)

type initOptions struct {
	root     *common.RootOptions
	target   string
	executor string
	force    bool
}

// newInitCommand initializes an empty restore point for a target
func newInitCommand(opts *common.RootOptions) *cobra.Command {
	initOpts := &initOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "init <plan-name>",
		Short: "Initialize empty restore point for a target",
		Long: `Create an empty restore point for a specific target and executor.

This is useful for handling edge cases where no restore point exists for a target
but the target is part of the plan. Initializing creates a clean entry that can later
be populated with actual restore data during hibernation.

Flags:
  --target   (required) Target name to initialize
  --executor (required) Executor type (e.g., eks, rds, ec2, karpenter)
  --force    Overwrite existing restore point entry for the target

Examples:
  kubectl hibernator restore init my-plan --target eks-cluster --executor eks
  kubectl hibernator restore init my-plan --target db-prod --executor rds --force
  kubectl hibernator restore init my-plan -t karpenter-target -x karpenter`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd.Context(), initOpts, args[0])
		},
	}

	cmd.Flags().StringVarP(&initOpts.target, "target", "t", "", "Target name (required)")
	cmd.Flags().StringVarP(&initOpts.executor, "executor", "x", "", "Executor type (required)")
	cmd.Flags().BoolVar(&initOpts.force, "force", false, "Overwrite existing restore point entry for the target")

	lo.Must0(cmd.MarkFlagRequired("target"))
	lo.Must0(cmd.MarkFlagRequired("executor"))

	return cmd
}

func runInit(ctx context.Context, opts *initOptions, planName string) error {
	c, err := common.NewK8sClient(opts.root)
	if err != nil {
		return err
	}

	ns := common.ResolveNamespace(opts.root)

	// Verify the plan exists
	var plan hibernatorv1alpha1.HibernatePlan
	if err := c.Get(ctx, types.NamespacedName{Name: planName, Namespace: ns}, &plan); err != nil {
		return fmt.Errorf("failed to get HibernatePlan %q in namespace %q: %w", planName, ns, err)
	}

	// Get or create the restore ConfigMap
	cmName := restore.GetRestoreConfigMap(planName)
	var cm corev1.ConfigMap
	err = c.Get(ctx, types.NamespacedName{Name: cmName, Namespace: ns}, &cm)

	// Create new ConfigMap if it doesn't exist
	if apierrors.IsNotFound(err) {
		cm = corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cmName,
				Namespace: ns,
				Labels: map[string]string{
					"hibernator.ardikabs.com/plan": planName,
				},
			},
			Data: make(map[string]string),
		}
		if err := c.Create(ctx, &cm); err != nil {
			return fmt.Errorf("failed to create restore ConfigMap: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to get restore ConfigMap: %w", err)
	}

	// Check if target already exists
	targetKey := fmt.Sprintf("%s.json", opts.target)
	if _, exists := cm.Data[targetKey]; exists && !opts.force {
		return fmt.Errorf("restore point entry already exists for target %q (use --force to overwrite)", opts.target)
	}

	// Create empty restore data
	restoreData := &restore.Data{
		Target:    opts.target,
		Executor:  opts.executor,
		Version:   1,
		CreatedAt: metav1.Now(),
		IsLive:    false,
		State:     make(map[string]any),
	}

	// Marshal to JSON
	dataBytes, err := json.Marshal(restoreData)
	if err != nil {
		return fmt.Errorf("failed to marshal restore data: %w", err)
	}

	// Update ConfigMap
	cm.Data[targetKey] = string(dataBytes)

	if err := c.Update(ctx, &cm); err != nil {
		return fmt.Errorf("failed to update restore ConfigMap: %w", err)
	}

	fmt.Printf("✓ Initialized empty restore point for target %q (executor: %s)\n", opts.target, opts.executor)
	return nil
}