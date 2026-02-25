/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/restore"
)

type restorePointOptions struct {
	root       *rootOptions
	target     string
	resourceID string
}

// newRestorePointCommand creates the "restore point" parent command group.
func newRestorePointCommand(opts *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Manage restore points for HibernatePlans",
		Long: `Commands for inspecting and managing restore points (captured resource state during hibernation).

Restore points store metadata about resources as they were when hibernation occurred,
enabling proper restoration during wakeup with correct configuration and state.

Examples:
  kubectl hibernator restore init my-plan --target eks-cluster --executor eks  # Initialize empty restore point for target
  kubectl hibernator restore show my-plan                                        # Show restore point summary
  kubectl hibernator restore list-resources my-plan                              # List all resources
  kubectl hibernator restore list-resources my-plan --target eks-cluster         # Filter by target
  kubectl hibernator restore describe my-plan --target eks-cluster --resource-id xyz  # Show resource details
  kubectl hibernator restore patch my-plan --target eks-cluster --resource-id xyz --set desiredCapacity=10  # Update field
  kubectl hibernator restore drop my-plan --target eks-cluster --resource-id xyz`,
	}

	cmd.AddCommand(newRestoreInitCommand(opts))
	cmd.AddCommand(newRestoreShowCommand(opts))
	cmd.AddCommand(newRestoreListResourcesCommand(opts))
	cmd.AddCommand(newRestoreDescribeCommand(opts))
	cmd.AddCommand(newRestorePatchCommand(opts))
	cmd.AddCommand(newRestoreDropCommand(opts))

	return cmd
}

// newRestoreInitCommand initializes an empty restore point for a target
func newRestoreInitCommand(opts *rootOptions) *cobra.Command {
	type initOptions struct {
		root     *rootOptions
		target   string
		executor string
		force    bool
	}
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
			return runRestoreInit(cmd.Context(), initOpts, args[0])
		},
	}

	cmd.Flags().StringVarP(&initOpts.target, "target", "t", "", "Target name (required)")
	cmd.Flags().StringVarP(&initOpts.executor, "executor", "x", "", "Executor type (required)")
	cmd.Flags().BoolVar(&initOpts.force, "force", false, "Overwrite existing restore point entry for the target")

	cmd.MarkFlagRequired("target")
	cmd.MarkFlagRequired("executor")

	return cmd
}

func runRestoreInit(ctx context.Context, opts interface{}, planName string) error {
	// Type assert the options
	initOpts := opts.(*struct {
		root     *rootOptions
		target   string
		executor string
		force    bool
	})

	c, err := newK8sClient(initOpts.root)
	if err != nil {
		return err
	}

	ns := resolveNamespace(initOpts.root)

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
	targetKey := fmt.Sprintf("%s.json", initOpts.target)
	if _, exists := cm.Data[targetKey]; exists && !initOpts.force {
		return fmt.Errorf("restore point entry already exists for target %q (use --force to overwrite)", initOpts.target)
	}

	// Create empty restore data
	restoreData := &restore.Data{
		Target:    initOpts.target,
		Executor:  initOpts.executor,
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

	fmt.Printf("✓ Initialized empty restore point for target %q (executor: %s)\n", initOpts.target, initOpts.executor)
	return nil
}

// newRestoreShowCommand shows summary of restore point(s)
func newRestoreShowCommand(opts *rootOptions) *cobra.Command {
	restoreOpts := &restorePointOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "show <plan-name>",
		Short: "Display restore point summary",
		Long: `Show a summary of the restore point for a HibernatePlan.
Displays:
- Overall live status (isLive=true means high-quality recent capture)
- Total number of resources in the restore point
- Summary by target

Examples:
  kubectl hibernator restore show my-plan -n production
  kubectl hibernator restore show my-plan --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRestoreShow(cmd.Context(), restoreOpts, args[0])
		},
	}

	return cmd
}

func runRestoreShow(ctx context.Context, opts *restorePointOptions, planName string) error {
	c, err := newK8sClient(opts.root)
	if err != nil {
		return err
	}

	ns := resolveNamespace(opts.root)

	// Get the plan to verify it exists
	var plan hibernatorv1alpha1.HibernatePlan
	if err := c.Get(ctx, types.NamespacedName{Name: planName, Namespace: ns}, &plan); err != nil {
		return fmt.Errorf("failed to get HibernatePlan %q in namespace %q: %w", planName, ns, err)
	}

	// Load restore ConfigMap
	cmName := restore.GetRestoreConfigMap(planName)
	var cm corev1.ConfigMap
	if err := c.Get(ctx, types.NamespacedName{Name: cmName, Namespace: ns}, &cm); err != nil {
		fmt.Fprintf(os.Stderr, "No restore point found for plan %q\n", planName)
		return nil
	}

	if opts.root.jsonOutput {
		return printRestoreShowJSON(cm)
	}
	return printRestoreShowTable(cm)
}

type restoreShowJSONOutput struct {
	Plan           string             `json:"plan"`
	Namespace      string             `json:"namespace"`
	RestorePoints  []restorePointData `json:"restorePoints,omitempty"`
	TotalResources int                `json:"totalResources"`
}

type restorePointData struct {
	Target        string `json:"target"`
	Executor      string `json:"executor"`
	IsLive        bool   `json:"isLive"`
	CapturedAt    string `json:"capturedAt,omitempty"`
	ResourceCount int    `json:"resourceCount"`
	CreatedAt     string `json:"createdAt,omitempty"`
}

func printRestoreShowJSON(cm corev1.ConfigMap) error {
	output := restoreShowJSONOutput{
		Plan:      cm.Labels["hibernator.ardikabs.com/plan"],
		Namespace: cm.Namespace,
	}

	for _, val := range cm.Data {
		var data restore.Data
		if err := json.Unmarshal([]byte(val), &data); err != nil {
			continue
		}

		resourceCount := len(data.State)
		output.RestorePoints = append(output.RestorePoints, restorePointData{
			Target:        data.Target,
			Executor:      data.Executor,
			IsLive:        data.IsLive,
			CapturedAt:    data.CapturedAt,
			ResourceCount: resourceCount,
			CreatedAt:     data.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
		output.TotalResources += resourceCount
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

func printRestoreShowTable(cm corev1.ConfigMap) error {
	var totalResources int
	var points []restorePointData

	for _, val := range cm.Data {
		var data restore.Data
		if err := json.Unmarshal([]byte(val), &data); err != nil {
			continue
		}

		resourceCount := len(data.State)
		points = append(points, restorePointData{
			Target:        data.Target,
			Executor:      data.Executor,
			IsLive:        data.IsLive,
			CapturedAt:    data.CapturedAt,
			ResourceCount: resourceCount,
			CreatedAt:     data.CreatedAt.Format("2006-01-02 15:04:05"),
		})
		totalResources += resourceCount
	}

	if len(points) == 0 {
		fmt.Printf("Plan: %s (Namespace: %s)\n", cm.Labels["hibernator.ardikabs.com/plan"], cm.Namespace)
		fmt.Println("No restore point data found")
		return nil
	}

	// Summary header
	fmt.Printf("Plan: %s (Namespace: %s)\n", cm.Labels["hibernator.ardikabs.com/plan"], cm.Namespace)
	fmt.Printf("Total Resources: %d\n\n", totalResources)

	// Table of restore points by target
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TARGET\tEXECUTOR\tLIVE\tRESOURCES\tCAPTURED AT")

	for _, p := range points {
		live := "no"
		if p.IsLive {
			live = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", p.Target, p.Executor, live, p.ResourceCount, p.CapturedAt)
	}

	return w.Flush()
}

// newRestoreListResourcesCommand lists individual resources in the restore point
func newRestoreListResourcesCommand(opts *rootOptions) *cobra.Command {
	restoreOpts := &restorePointOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "list-resources <plan-name>",
		Short: "List all resources in the restore point",
		Long: `Show all resources stored in the restore point for a HibernatePlan.
Resources are organized by target. Display shows:
- Resource ID (identifier within the target's state)
- Target name
- Executor type
- Quality indicator (whether this is live/fresh data)

Filter by specific target using --target flag.

Examples:
  kubectl hibernator restore list-resources my-plan
  kubectl hibernator restore list-resources my-plan --target eks-cluster
  kubectl hibernator restore list-resources my-plan --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRestoreListResources(cmd.Context(), restoreOpts, args[0])
		},
	}

	cmd.Flags().StringVar(&restoreOpts.target, "target", "", "Filter by specific target name")

	return cmd
}

func runRestoreListResources(ctx context.Context, opts *restorePointOptions, planName string) error {
	c, err := newK8sClient(opts.root)
	if err != nil {
		return err
	}

	ns := resolveNamespace(opts.root)

	// Load restore ConfigMap
	cmName := restore.GetRestoreConfigMap(planName)
	var cm corev1.ConfigMap
	if err := c.Get(ctx, types.NamespacedName{Name: cmName, Namespace: ns}, &cm); err != nil {
		fmt.Fprintf(os.Stderr, "No restore point found for plan %q\n", planName)
		return nil
	}

	if opts.root.jsonOutput {
		return printRestoreResourcesJSON(cm, opts.target)
	}
	return printRestoreResourcesTable(cm, opts.target)
}

type restoreResource struct {
	ResourceID string `json:"resourceId"`
	Target     string `json:"target"`
	Executor   string `json:"executor"`
	IsLive     bool   `json:"isLive"`
	CapturedAt string `json:"capturedAt,omitempty"`
}

func printRestoreResourcesJSON(cm corev1.ConfigMap, filterTarget string) error {
	var resources []restoreResource

	for _, val := range cm.Data {
		var data restore.Data
		if err := json.Unmarshal([]byte(val), &data); err != nil {
			continue
		}

		if filterTarget != "" && data.Target != filterTarget {
			continue
		}

		// Extract resource IDs from state
		for resourceID := range data.State {
			resources = append(resources, restoreResource{
				ResourceID: resourceID,
				Target:     data.Target,
				Executor:   data.Executor,
				IsLive:     data.IsLive,
				CapturedAt: data.CapturedAt,
			})
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(resources)
}

func printRestoreResourcesTable(cm corev1.ConfigMap, filterTarget string) error {
	var resources []restoreResource

	for _, val := range cm.Data {
		var data restore.Data
		if err := json.Unmarshal([]byte(val), &data); err != nil {
			continue
		}

		if filterTarget != "" && data.Target != filterTarget {
			continue
		}

		// Extract resource IDs from state
		for resourceID := range data.State {
			resources = append(resources, restoreResource{
				ResourceID: resourceID,
				Target:     data.Target,
				Executor:   data.Executor,
				IsLive:     data.IsLive,
				CapturedAt: data.CapturedAt,
			})
		}
	}

	if len(resources) == 0 {
		fmt.Println("No resources found in restore point")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "RESOURCE ID\tTARGET\tEXECUTOR\tLIVE\tCAPTURED AT")

	for _, r := range resources {
		live := "no"
		if r.IsLive {
			live = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.ResourceID, r.Target, r.Executor, live, r.CapturedAt)
	}

	return w.Flush()
}

// newRestoreDescribeCommand shows details of a specific resource in the restore point
func newRestoreDescribeCommand(opts *rootOptions) *cobra.Command {
	restoreOpts := &restorePointOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "describe <plan-name>",
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
  kubectl hibernator restore describe my-plan --target eks-cluster --resource-id node-123
  kubectl hibernator restore describe my-plan --target rds --resource-id db-prod-01
  kubectl hibernator restore describe my-plan -t eks-cluster -r node-123 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRestoreDescribe(cmd.Context(), restoreOpts, args[0])
		},
	}

	cmd.Flags().StringVarP(&restoreOpts.target, "target", "t", "", "Target name (required)")
	cmd.Flags().StringVarP(&restoreOpts.resourceID, "resource-id", "r", "", "Resource ID (required)")
	cmd.MarkFlagRequired("target")
	cmd.MarkFlagRequired("resource-id")

	return cmd
}

func runRestoreDescribe(ctx context.Context, opts *restorePointOptions, planName string) error {
	c, err := newK8sClient(opts.root)
	if err != nil {
		return err
	}

	ns := resolveNamespace(opts.root)

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

	if opts.root.jsonOutput {
		return printRestoreDescribeJSON(planName, ns, *targetData, opts.resourceID, resourceState)
	}
	return printRestoreDescribeTable(planName, ns, *targetData, opts.resourceID, resourceState)
}

type restoreDescribeJSONOutput struct {
	Plan       string         `json:"plan"`
	Namespace  string         `json:"namespace"`
	Target     string         `json:"target"`
	ResourceID string         `json:"resourceId"`
	Executor   string         `json:"executor"`
	IsLive     bool           `json:"isLive"`
	CreatedAt  string         `json:"createdAt"`
	CapturedAt string         `json:"capturedAt,omitempty"`
	State      map[string]any `json:"state,omitempty"`
}

func printRestoreDescribeJSON(plan, namespace string, data restore.Data, resourceID string, state map[string]any) error {
	output := restoreDescribeJSONOutput{
		Plan:       plan,
		Namespace:  namespace,
		Target:     data.Target,
		ResourceID: resourceID,
		Executor:   data.Executor,
		IsLive:     data.IsLive,
		CreatedAt:  data.CreatedAt.Format(time.RFC3339),
		CapturedAt: data.CapturedAt,
		State:      state,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

func printRestoreDescribeTable(plan, namespace string, data restore.Data, resourceID string, state map[string]any) error {
	fmt.Printf("Plan:       %s\n", plan)
	fmt.Printf("Namespace:  %s\n", namespace)
	fmt.Printf("Target:     %s\n", data.Target)
	fmt.Printf("Resource ID: %s\n", resourceID)
	fmt.Printf("Executor:   %s\n", data.Executor)
	fmt.Println()

	fmt.Println("Metadata:")
	fmt.Printf("  Live:       %v\n", data.IsLive)
	fmt.Printf("  Created At: %s\n", data.CreatedAt.Format(time.RFC3339))
	if data.CapturedAt != "" {
		fmt.Printf("  Captured At: %s\n", data.CapturedAt)
	}
	fmt.Println()

	fmt.Println("Resource State:")
	stateJSON, err := json.MarshalIndent(state, "  ", "  ")
	if err != nil {
		fmt.Printf("  (unable to format state: %v)\n", err)
		return nil
	}
	fmt.Printf("  %s\n", string(stateJSON))

	return nil
}

// newRestoreDropCommand removes specific resources from restore point
func newRestoreDropCommand(opts *rootOptions) *cobra.Command {
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
			return runRestoreDrop(cmd.Context(), restoreOpts, args[0])
		},
	}

	cmd.Flags().StringVarP(&restoreOpts.target, "target", "t", "", "Target name (required)")
	cmd.Flags().StringVarP(&restoreOpts.resourceID, "resource-id", "r", "", "Resource ID to drop (required)")
	cmd.MarkFlagRequired("target")
	cmd.MarkFlagRequired("resource-id")

	return cmd
}

func runRestoreDrop(ctx context.Context, opts *restorePointOptions, planName string) error {
	c, err := newK8sClient(opts.root)
	if err != nil {
		return err
	}

	ns := resolveNamespace(opts.root)

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
