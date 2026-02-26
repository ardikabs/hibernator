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
	"k8s.io/apimachinery/pkg/types"

	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/internal/restore"
)

// newDropCommand removes specific resources from restore point
func newDropCommand(opts *common.RootOptions) *cobra.Command {
	restoreOpts := &restorePointOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "drop <plan-name>",
		Short: "Remove specific resource from restore point",
		Long: `Drop (remove) a specific resource from the restore point.
This prevents the executor from using stale or problematic restore metadata.

When a resource is dropped, its entire state is removed from the restore ConfigMap.
The next hibernation cycle will capture fresh restore data for that resource.

Flags:
  --target       (required) The target name containing the resource
  --resource-id  (required) The resource identifier to drop

Examples:
  kubectl hibernator restore drop my-plan --target eks-cluster --resource-id node-xyz
  kubectl hibernator restore drop my-plan --target rds --resource-id db-prod-01`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDrop(cmd.Context(), restoreOpts, args[0])
		},
	}

	cmd.Flags().StringVarP(&restoreOpts.target, "target", "t", "", "Target name (required)")
	cmd.Flags().StringVarP(&restoreOpts.resourceID, "resource-id", "r", "", "Resource ID to drop (required)")

	lo.Must0(cmd.MarkFlagRequired("target"))
	lo.Must0(cmd.MarkFlagRequired("resource-id"))

	return cmd
}

func runDrop(ctx context.Context, opts *restorePointOptions, planName string) error {
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

	// Find and update the target's restore data
	found := false
	for key, val := range cm.Data {
		var data restore.Data
		if err := json.Unmarshal([]byte(val), &data); err != nil {
			continue
		}

		if data.Target != opts.target {
			continue
		}

		// Found the target's restore data
		found = true

		if _, ok := data.State[opts.resourceID]; !ok {
			return fmt.Errorf("resource %q not found in target %q", opts.resourceID, opts.target)
		}

		// Remove the resource ID from state
		delete(data.State, opts.resourceID)

		// Update ConfigMap
		dataBytes, err := json.Marshal(&data)
		if err != nil {
			return fmt.Errorf("marshal restore data: %w", err)
		}
		cm.Data[key] = string(dataBytes)
		fmt.Printf("Dropped resource %q from target %q (%d resources remaining)\n", opts.resourceID, opts.target, len(data.State))
		break
	}

	if !found {
		return fmt.Errorf("target %q not found in restore point", opts.target)
	}

	// Update the ConfigMap
	if err := c.Update(ctx, &cm); err != nil {
		return fmt.Errorf("failed to update restore point: %w", err)
	}

	fmt.Printf("Successfully dropped resource from restore point\n")
	return nil
}