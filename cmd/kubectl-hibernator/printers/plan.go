/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package printers

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/samber/lo"
)

func (p *HumanReadablePrinter) printPlanListOutput(out *PlanListOutput, w io.Writer) error {
	t := table.NewWriter()
	t.SetStyle(DefaultTableStyle)
	t.SetOutputMirror(w)
	t.AppendHeader(table.Row{"Name", "Namespace", "Phase", "Suspended", "Next Event", "Age"})

	for _, item := range out.Items {
		plan := item.Plan

		suspended := "no"
		if plan.Spec.Suspend {
			suspended = "yes"
		}

		age := FormatAge(time.Since(plan.CreationTimestamp.Time))
		nextEvent := formatNextEvent(item.NextEvent)

		t.AppendRow(table.Row{
			plan.Name,
			plan.Namespace,
			plan.Status.Phase,
			suspended,
			nextEvent,
			age,
		})
	}

	t.Render()
	return nil
}

func formatNextEvent(event *common.ScheduleEvent) string {
	if event == nil {
		return "-"
	}

	duration := time.Until(event.Time)
	return fmt.Sprintf("%s (%s)", event.Operation, HumanDuration(duration))
}

func (p *HumanReadablePrinter) printHibernatePlan(plan hibernatorv1alpha1.HibernatePlan, w io.Writer) error {
	fmt.Fprintf(w, "Name:       %s\n", plan.Name)
	fmt.Fprintf(w, "Namespace:  %s\n", plan.Namespace)
	fmt.Fprintf(w, "Created:    %s\n", plan.CreationTimestamp.Format("2006-01-02 15:04:05"))
	fmt.Fprintln(w)

	// Schedule
	fmt.Fprintln(w, "Schedule:")
	fmt.Fprintf(w, "  Timezone: %s\n", plan.Spec.Schedule.Timezone)
	fmt.Fprintln(w, "  Off-Hour Windows:")
	for _, window := range plan.Spec.Schedule.OffHours {
		fmt.Fprintf(w, "    %s - %s on %v\n", window.Start, window.End, window.DaysOfWeek)
	}
	fmt.Fprintln(w)

	// Behavior
	fmt.Fprintln(w, "Behavior:")
	fmt.Fprintf(w, "  Mode:     %s\n", plan.Spec.Behavior.Mode)
	fmt.Fprintf(w, "  Retries:  %d\n", plan.Spec.Behavior.Retries)
	fmt.Fprintln(w)

	// Execution strategy
	fmt.Fprintln(w, "Execution Strategy:")
	fmt.Fprintf(w, "  Type:  %s\n", plan.Spec.Execution.Strategy.Type)
	if plan.Spec.Execution.Strategy.MaxConcurrency != nil {
		fmt.Fprintf(w, "  Max Concurrency:  %d\n", *plan.Spec.Execution.Strategy.MaxConcurrency)
	}
	if len(plan.Spec.Execution.Strategy.Dependencies) > 0 {
		fmt.Fprintln(w, "  Dependencies:")
		for _, dep := range plan.Spec.Execution.Strategy.Dependencies {
			fmt.Fprintf(w, "    %s -> %s\n", dep.From, dep.To)
		}
	}
	fmt.Fprintln(w)

	// Targets
	fmt.Fprintln(w, "Targets:")
	if len(plan.Spec.Targets) == 0 {
		fmt.Fprintln(w, "  (none)")
	} else {
		for i, target := range plan.Spec.Targets {
			fmt.Fprintf(w, "  [%d] %s (%s)\n", i, target.Name, target.Type)
			fmt.Fprintf(w, "      Connector: %s/%s\n", target.ConnectorRef.Kind, target.ConnectorRef.Name)
			if target.Parameters != nil && len(target.Parameters.Raw) > 0 {
				fmt.Fprintln(w, "      Parameters:")

				str, err := json.MarshalIndent(json.RawMessage(target.Parameters.Raw), "        ", "  ")
				if err != nil {
					var params map[string]interface{}
					if err := json.Unmarshal(target.Parameters.Raw, &params); err == nil {
						for k, v := range params {
							fmt.Fprintf(w, "        %s: %v\n", k, v)
						}
					}

				} else {
					fmt.Fprintf(w, "        %s\n", string(str))
				}
			}
		}
	}
	fmt.Fprintln(w)

	return p.printStatus(&StatusOutput{Plan: plan}, w)
}

func (p *HumanReadablePrinter) printStatus(out *StatusOutput, w io.Writer) error {
	plan := out.Plan

	t := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	lo.Must1(fmt.Fprintln(t, "Status"))

	lo.Must1(fmt.Fprintf(t, "  Phase:     %s\n", plan.Status.Phase))
	lo.Must1(fmt.Fprintf(t, "  Suspended: %t\n", plan.Spec.Suspend))

	// Suspend annotations
	if plan.Spec.Suspend && plan.Annotations != nil {
		if until, ok := plan.Annotations["hibernator.ardikabs.com/suspend-until"]; ok {
			lo.Must1(fmt.Fprintf(t, "  Suspend Until: %s\n", until))
		}
		if reason, ok := plan.Annotations["hibernator.ardikabs.com/suspend-reason"]; ok {
			lo.Must1(fmt.Fprintf(t, "  Suspend Reason: %s\n", reason))
		}
	}

	lo.Must1(fmt.Fprintln(t))

	if plan.Status.CurrentCycleID != "" {
		lo.Must1(fmt.Fprintf(t, "  Current Cycle: %s\n", plan.Status.CurrentCycleID))
		lo.Must1(fmt.Fprintf(t, "  Operation:     %s\n", plan.Status.CurrentOperation))
		lo.Must1(fmt.Fprintln(t))
	}

	if plan.Status.Phase == hibernatorv1alpha1.PhaseError {
		lo.Must1(fmt.Fprintf(t, "  Error:       %s\n", plan.Status.ErrorMessage))
		lo.Must1(fmt.Fprintf(t, "  Retry Count: %d/%d\n", plan.Status.RetryCount, plan.Spec.Behavior.Retries))
		if plan.Status.LastRetryTime != nil {
			lo.Must1(fmt.Fprintf(t, "  Last Retry:  %s (%s ago)\n", plan.Status.LastRetryTime.Format(time.RFC3339), HumanDuration(time.Since(plan.Status.LastRetryTime.Time))))
		}
		lo.Must1(fmt.Fprintln(t))
	}

	if len(plan.Status.Executions) > 0 {
		lo.Must1(fmt.Fprintln(t, "  Target Executions:"))
		for _, exec := range plan.Status.Executions {
			lo.Must1(fmt.Fprintf(t, "  %s %-30s  %s", StateIcon(exec.State), exec.Target, exec.State))
			if exec.Attempts > 0 {
				lo.Must1(fmt.Fprintf(t, "  (attempts: %d)", exec.Attempts))
			}
			if exec.Message != "" {
				lo.Must1(fmt.Fprintf(t, "  %s", exec.Message))
			}
			lo.Must1(fmt.Fprintln(t, ""))

			if exec.StartedAt != nil {
				lo.Must1(fmt.Fprintf(t, "    Started:  %s\n", exec.StartedAt.Format(time.RFC3339)))
			}
			if exec.FinishedAt != nil {
				lo.Must1(fmt.Fprintf(t, "    Finished: %s\n", exec.FinishedAt.Format(time.RFC3339)))
			}
		}
	}

	if len(plan.Status.ExecutionHistory) > 1 {
		last := plan.Status.ExecutionHistory[len(plan.Status.ExecutionHistory)-2]
		lo.Must1(fmt.Fprintf(t, "\n  Last Cycle: %s\n", last.CycleID))

		if last.ShutdownExecution != nil {
			p.printOperationSummary(w, "    Shutdown", last.ShutdownExecution)
		}
		if last.WakeupExecution != nil {
			p.printOperationSummary(w, "    Wakeup", last.WakeupExecution)
		}
	}

	if len(plan.Status.ActiveExceptions) > 0 {
		lo.Must1(fmt.Fprintln(t, "\n  Active Exceptions:"))
		for _, exc := range plan.Status.ActiveExceptions {
			lo.Must1(fmt.Fprintf(t, "    %s (until: %s)\n", exc.Name, exc.ValidUntil.Format("2006-01-02 15:04:05")))
		}
	}

	return t.Flush()
}

func (p *HumanReadablePrinter) printOperationSummary(w io.Writer, prefix string, op *hibernatorv1alpha1.ExecutionOperationSummary) {
	successStr := "failed"
	if op.Success {
		successStr = "success"
	}
	fmt.Fprintf(w, "%s: %s (started: %s", prefix, successStr, op.StartTime.Format(time.RFC3339))
	if op.EndTime != nil {
		fmt.Fprintf(w, ", ended: %s", op.EndTime.Format(time.RFC3339))
	}
	fmt.Fprintln(w, ")")

	if op.ErrorMessage != "" {
		fmt.Fprintf(w, "%s  Error: %s\n", prefix, op.ErrorMessage)
	}
}

func (p *HumanReadablePrinter) printSchedule(out *ScheduleOutput, w io.Writer) error {
	plan := out.Plan
	result := out.Result.(*scheduler.EvaluationResult)
	exceptions := out.Exceptions
	events := out.Events

	fmt.Fprintf(w, "Plan:      %s\n", plan.Name)
	if plan.Namespace != "" {
		fmt.Fprintf(w, "Namespace: %s\n", plan.Namespace)
	}
	fmt.Fprintf(w, "Timezone:  %s\n", plan.Spec.Schedule.Timezone)
	fmt.Fprintln(w)

	fmt.Fprintln(w, "Off-Hour Windows:")
	for _, window := range plan.Spec.Schedule.OffHours {
		fmt.Fprintf(w, "  %s - %s  [%s]\n", window.Start, window.End, strings.Join(window.DaysOfWeek, ", "))
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "Current State:")
	fmt.Fprintf(w, "  State:              %s\n", result.CurrentState)
	fmt.Fprintf(w, "  Next Hibernate:     %s (%s)\n", result.NextHibernateTime.Format(time.RFC3339), HumanDuration(time.Until(result.NextHibernateTime)))
	fmt.Fprintf(w, "  Next WakeUp:        %s (%s)\n", result.NextWakeUpTime.Format(time.RFC3339), HumanDuration(time.Until(result.NextWakeUpTime)))
	fmt.Fprintln(w)

	if len(events) > 0 {
		fmt.Fprintf(w, "Upcoming Events (next %d):\n", len(events))
		t := table.NewWriter()
		t.SetOutputMirror(w)
		t.SetStyle(DefaultTableStyle)
		t.AppendHeader(table.Row{"#", "Operation", "Time", "In"})

		for i, ev := range events {
			t.AppendRow(table.Row{
				i + 1,
				ev.Operation,
				ev.Time.Format(time.RFC3339),
				HumanDuration(time.Until(ev.Time)),
			})
		}
		t.Render()
		fmt.Fprintln(w)
	}

	if len(exceptions) > 0 {
		fmt.Fprintln(w, "Active Exceptions:")
		for _, ex := range exceptions {
			fmt.Fprintf(w, "  - %s (type=%s, until=%s, state=%s)\n",
				ex.Name, ex.Type, ex.ValidUntil.Format(time.RFC3339), ex.State)
		}
		fmt.Fprintln(w)
	}

	return nil
}
