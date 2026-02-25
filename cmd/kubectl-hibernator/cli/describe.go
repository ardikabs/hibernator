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

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

type describeOptions struct {
	root *rootOptions
}

// newDescribeCommand creates the "describe" command.
func newDescribeCommand(opts *rootOptions) *cobra.Command {
	describeOpts := &describeOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "describe <plan-name>",
		Short: "Display detailed information about a HibernatePlan",
		Long: `Show comprehensive details about a HibernatePlan including:
- Schedule configuration (timezone, off-hour windows)
- Execution strategy and behavior mode
- List of targets with executor-specific parameters
- Current status and execution history
- Active exceptions and suspend state

Examples:
  kubectl hibernator describe my-plan
  kubectl hibernator describe my-plan -n production
  kubectl hibernator describe my-plan --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDescribe(cmd.Context(), describeOpts, args[0])
		},
	}

	return cmd
}

func runDescribe(ctx context.Context, opts *describeOptions, planName string) error {
	c, err := newK8sClient(opts.root)
	if err != nil {
		return err
	}

	ns := resolveNamespace(opts.root)

	var plan hibernatorv1alpha1.HibernatePlan
	if err := c.Get(ctx, types.NamespacedName{Name: planName, Namespace: ns}, &plan); err != nil {
		return fmt.Errorf("failed to get HibernatePlan %q in namespace %q: %w", planName, ns, err)
	}

	if opts.root.jsonOutput {
		return printDescribeJSON(plan)
	}

	return printDescribeTable(plan)
}

func printDescribeJSON(plan hibernatorv1alpha1.HibernatePlan) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(plan)
}

func printDescribeTable(plan hibernatorv1alpha1.HibernatePlan) error {
	// Basic information
	fmt.Printf("Name:       %s\n", plan.Name)
	fmt.Printf("Namespace:  %s\n", plan.Namespace)
	fmt.Printf("Created:    %s\n", plan.CreationTimestamp.Format("2006-01-02 15:04:05"))
	fmt.Println()

	// Schedule
	fmt.Println("Schedule:")
	fmt.Printf("  Timezone: %s\n", plan.Spec.Schedule.Timezone)
	fmt.Println("  Off-Hour Windows:")
	for _, window := range plan.Spec.Schedule.OffHours {
		fmt.Printf("    %s - %s on %v\n", window.Start, window.End, window.DaysOfWeek)
	}
	fmt.Println()

	// Behavior
	fmt.Println("Behavior:")
	fmt.Printf("  Mode:     %s\n", plan.Spec.Behavior.Mode)
	fmt.Printf("  Retries:  %d\n", plan.Spec.Behavior.Retries)
	fmt.Println()

	// Execution strategy
	fmt.Println("Execution Strategy:")
	fmt.Printf("  Type:             %s\n", plan.Spec.Execution.Strategy.Type)
	if plan.Spec.Execution.Strategy.MaxConcurrency != nil {
		fmt.Printf("  Max Concurrency:  %d\n", *plan.Spec.Execution.Strategy.MaxConcurrency)
	}
	if len(plan.Spec.Execution.Strategy.Dependencies) > 0 {
		fmt.Println("  Dependencies:")
		for _, dep := range plan.Spec.Execution.Strategy.Dependencies {
			fmt.Printf("    %s -> %s\n", dep.From, dep.To)
		}
	}
	fmt.Println()

	// Targets
	fmt.Println("Targets:")
	if len(plan.Spec.Targets) == 0 {
		fmt.Println("  (none)")
	} else {
		for i, target := range plan.Spec.Targets {
			fmt.Printf("  [%d] %s (%s)\n", i, target.Name, target.Type)
			fmt.Printf("      Connector: %s/%s\n", target.ConnectorRef.Kind, target.ConnectorRef.Name)
			if target.Parameters != nil && len(target.Parameters.Raw) > 0 {
				fmt.Println("      Parameters:")
				var params map[string]interface{}
				if err := json.Unmarshal(target.Parameters.Raw, &params); err == nil {
					for k, v := range params {
						fmt.Printf("        %s: %v\n", k, v)
					}
				}
			}
		}
	}
	fmt.Println()

	// Status
	fmt.Println("Status:")
	fmt.Printf("  Phase:         %s\n", plan.Status.Phase)
	fmt.Printf("  Suspended:     %t\n", plan.Spec.Suspend)
	if plan.Spec.Suspend && plan.Annotations != nil {
		if reason, ok := plan.Annotations["hibernator.ardikabs.com/suspend-reason"]; ok {
			fmt.Printf("  Suspend Reason: %s\n", reason)
		}
	}
	fmt.Println()

	// Current operation
	if plan.Status.CurrentCycleID != "" {
		fmt.Printf("  Current Cycle:     %s\n", plan.Status.CurrentCycleID)
		fmt.Printf("  Current Operation: %s\n", plan.Status.CurrentOperation)
	}

	// Error status
	if plan.Status.Phase == hibernatorv1alpha1.PhaseError {
		fmt.Printf("  Error:      %s\n", plan.Status.ErrorMessage)
		fmt.Printf("  Retry Count: %d/%d\n", plan.Status.RetryCount, plan.Spec.Behavior.Retries)
	}

	// Target executions
	if len(plan.Status.Executions) > 0 {
		fmt.Println("\n  Target Executions:")
		for _, exec := range plan.Status.Executions {
			icon := stateIcon(exec.State)
			fmt.Printf("    %s %s: %s\n", icon, exec.Target, exec.State)
			if exec.Message != "" {
				fmt.Printf("      Message: %s\n", exec.Message)
			}
			if exec.Attempts > 0 {
				fmt.Printf("      Attempts: %d\n", exec.Attempts)
			}
		}
	}

	// Active exceptions
	if len(plan.Status.ActiveExceptions) > 0 {
		fmt.Println("\nActive Exceptions:")
		for _, exc := range plan.Status.ActiveExceptions {
			fmt.Printf("  %s (until: %s)\n", exc.Name, exc.ValidUntil.Format("2006-01-02 15:04:05"))
		}
	}

	return nil
}
