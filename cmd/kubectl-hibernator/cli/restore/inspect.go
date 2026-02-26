/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/samber/lo"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/printers"
	"github.com/ardikabs/hibernator/internal/restore"
)

// newInspectCommand shows details of a specific resource in the restore point
func newInspectCommand(opts *common.RootOptions) *cobra.Command {
	restoreOpts := &restorePointOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "inspect <plan-name>",
		Short: "Display restore point resource details",
		Long: `Show the actual content and metadata of a specific resource in the restore point.
Displays:
- Target and Executor information
- Resource quality indicator (isLive status)
- Full resource state data (executor-specific configuration and metadata)

Requires:
  --target      The target name containing the resource
  --resource-id The resource identifier

Examples:
  kubectl hibernator restore inspect my-plan --target eks-cluster --resource-id node-123
  kubectl hibernator restore inspect my-plan --target rds --resource-id db-prod-01
  kubectl hibernator restore inspect my-plan -t eks-cluster -r node-123 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInspect(cmd.Context(), restoreOpts, args[0])
		},
	}

	cmd.Flags().StringVarP(&restoreOpts.target, "target", "t", "", "Target name (required)")
	cmd.Flags().StringVarP(&restoreOpts.resourceID, "resource-id", "r", "", "Resource ID (required)")

	lo.Must0(cmd.MarkFlagRequired("target"))
	lo.Must0(cmd.MarkFlagRequired("resource-id"))

	return cmd
}

func runInspect(ctx context.Context, opts *restorePointOptions, planName string) error {
	c, err := common.NewK8sClient(opts.root)
	if err != nil {
		return err
	}

	ns := common.ResolveNamespace(opts.root)

	// Load restore ConfigMap
	cmName := restore.GetRestoreConfigMap(planName)
	var cm corev1.ConfigMap
	if err := c.Get(ctx, types.NamespacedName{Name: cmName, Namespace: ns}, &cm); err != nil {
		return fmt.Errorf("no restore point found for plan %q: %w", planName, err)
	}

	// Find the target's restore data
	var targetData *restore.Data
	var resourceState map[string]any

	for _, val := range cm.Data {
		var data restore.Data
		if err := json.Unmarshal([]byte(val), &data); err != nil {
			continue
		}

		if data.Target != opts.target {
			continue
		}

		// Found the target
		targetData = &data

		// Check if resource exists
		if state, ok := data.State[opts.resourceID]; ok {
			resourceState = state.(map[string]any)
			break
		}

		// Resource not found in this target
		return fmt.Errorf("resource %q not found in target %q", opts.resourceID, opts.target)
	}

	if targetData == nil {
		return fmt.Errorf("target %q not found in restore point", opts.target)
	}

	d := &printers.Dispatcher{JSON: opts.root.JsonOutput}
	return d.PrintObj(&printers.RestoreDetailOutput{
		Plan:       planName,
		Namespace:  ns,
		TargetData: *targetData,
		ResourceID: opts.resourceID,
		State:      resourceState,
	}, os.Stdout)
}
