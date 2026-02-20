/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

type showStatusOptions struct {
	root *rootOptions
}

// newShowStatusCommand creates the "show status" command.
func newShowStatusCommand(opts *rootOptions) *cobra.Command {
	statusOpts := &showStatusOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "status <plan-name>",
		Short: "Display current status of a HibernatePlan",
		Long: `Show the current phase, target execution progress, retry state,
and last execution results for a HibernatePlan.

Examples:
  kubectl hibernator show status my-plan
  kubectl hibernator show status my-plan -n production
  kubectl hibernator show status my-plan --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShowStatus(cmd.Context(), statusOpts, args[0])
		},
	}

	return cmd
}

func runShowStatus(ctx context.Context, opts *showStatusOptions, planName string) error {
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
		return printStatusJSON(plan)
	}

	return printStatusTable(plan)
}

type statusJSONOutput struct {
	Plan           string                                     `json:"plan"`
	Namespace      string                                     `json:"namespace"`
	Phase          hibernatorv1alpha1.PlanPhase                `json:"phase"`
	Suspended      bool                                       `json:"suspended"`
	CycleID        string                                     `json:"currentCycleID,omitempty"`
	Operation      string                                     `json:"currentOperation,omitempty"`
	RetryCount     int32                                      `json:"retryCount"`
	ErrorMessage   string                                     `json:"errorMessage,omitempty"`
	Targets        []hibernatorv1alpha1.ExecutionStatus        `json:"targets,omitempty"`
	LastExecution  *hibernatorv1alpha1.ExecutionCycle           `json:"lastExecution,omitempty"`
}

func printStatusJSON(plan hibernatorv1alpha1.HibernatePlan) error {
	output := statusJSONOutput{
		Plan:         plan.Name,
		Namespace:    plan.Namespace,
		Phase:        plan.Status.Phase,
		Suspended:    plan.Spec.Suspend,
		CycleID:      plan.Status.CurrentCycleID,
		Operation:    plan.Status.CurrentOperation,
		RetryCount:   plan.Status.RetryCount,
		ErrorMessage: plan.Status.ErrorMessage,
		Targets:      plan.Status.Executions,
	}

	if len(plan.Status.ExecutionHistory) > 0 {
		last := plan.Status.ExecutionHistory[len(plan.Status.ExecutionHistory)-1]
		output.LastExecution = &last
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

func printStatusTable(plan hibernatorv1alpha1.HibernatePlan) error {
	fmt.Printf("Plan:      %s\n", plan.Name)
	fmt.Printf("Namespace: %s\n", plan.Namespace)
	fmt.Printf("Phase:     %s\n", plan.Status.Phase)
	fmt.Printf("Suspended: %t\n", plan.Spec.Suspend)
	fmt.Println()

	if plan.Status.CurrentCycleID != "" {
		fmt.Printf("Cycle ID:  %s\n", plan.Status.CurrentCycleID)
	}

	if plan.Status.CurrentOperation != "" {
		fmt.Printf("Operation: %s\n", plan.Status.CurrentOperation)
	}

	if plan.Status.Phase == hibernatorv1alpha1.PhaseError {
		fmt.Printf("Error:     %s\n", plan.Status.ErrorMessage)
		fmt.Printf("Retries:   %d/%d\n", plan.Status.RetryCount, plan.Spec.Behavior.Retries)
		if plan.Status.LastRetryTime != nil {
			fmt.Printf("Last Retry: %s (%s ago)\n",
				plan.Status.LastRetryTime.Format(time.RFC3339),
				humanDuration(time.Since(plan.Status.LastRetryTime.Time)))
		}
	}
	fmt.Println()

	// Target execution status
	if len(plan.Status.Executions) > 0 {
		fmt.Println("Targets:")
		for _, exec := range plan.Status.Executions {
			stateIcon := stateIcon(exec.State)
			fmt.Printf("  %s %-30s  %s", stateIcon, exec.Target, exec.State)
			if exec.Attempts > 0 {
				fmt.Printf("  (attempts: %d)", exec.Attempts)
			}
			if exec.Message != "" {
				fmt.Printf("  %s", exec.Message)
			}
			fmt.Println()

			if exec.StartedAt != nil {
				fmt.Printf("    Started:  %s\n", exec.StartedAt.Format(time.RFC3339))
			}
			if exec.FinishedAt != nil {
				fmt.Printf("    Finished: %s\n", exec.FinishedAt.Format(time.RFC3339))
			}
		}
		fmt.Println()
	}

	// Last execution cycle
	if len(plan.Status.ExecutionHistory) > 0 {
		last := plan.Status.ExecutionHistory[len(plan.Status.ExecutionHistory)-1]
		fmt.Printf("Last Cycle: %s\n", last.CycleID)

		if last.ShutdownExecution != nil {
			printOperationSummary("  Shutdown", last.ShutdownExecution)
		}
		if last.WakeupExecution != nil {
			printOperationSummary("  Wakeup", last.WakeupExecution)
		}
		fmt.Println()
	}

	// Suspend annotations
	if ann := plan.Annotations; ann != nil {
		if until, ok := ann["hibernator.ardikabs.com/suspend-until"]; ok {
			fmt.Printf("Suspend Until: %s\n", until)
		}
		if reason, ok := ann["hibernator.ardikabs.com/suspend-reason"]; ok {
			fmt.Printf("Suspend Reason: %s\n", reason)
		}
	}

	return nil
}

func printOperationSummary(prefix string, op *hibernatorv1alpha1.ExecutionOperationSummary) {
	successStr := "failed"
	if op.Success {
		successStr = "success"
	}
	fmt.Printf("%s: %s (started: %s", prefix, successStr, op.StartTime.Format(time.RFC3339))
	if op.EndTime != nil {
		fmt.Printf(", ended: %s", op.EndTime.Format(time.RFC3339))
	}
	fmt.Println(")")

	if op.ErrorMessage != "" {
		fmt.Printf("%s  Error: %s\n", prefix, op.ErrorMessage)
	}
}

func stateIcon(state hibernatorv1alpha1.ExecutionState) string {
	switch state {
	case hibernatorv1alpha1.StateCompleted:
		return "[OK]"
	case hibernatorv1alpha1.StateFailed:
		return "[FAIL]"
	case hibernatorv1alpha1.StateRunning:
		return "[..]"
	case hibernatorv1alpha1.StatePending:
		return "[--]"
	default:
		return "[??]"
	}
}
