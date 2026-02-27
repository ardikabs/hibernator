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
	"time"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/scheduler"
	corev1 "k8s.io/api/core/v1"
)

// HumanReadablePrinter handles table-like output for various resources
type HumanReadablePrinter struct{}

func (p *HumanReadablePrinter) PrintObj(obj interface{}, w io.Writer) error {
	switch v := obj.(type) {
	case hibernatorv1alpha1.HibernatePlan:
		return p.printPlan(v, w)
	case *hibernatorv1alpha1.HibernatePlan:
		return p.printPlan(*v, w)
	case corev1.ConfigMap:
		// Used for restore points
		return p.printRestorePoint(v, w)
	case *corev1.ConfigMap:
		return p.printRestorePoint(*v, w)
	case *ScheduleOutput:
		return p.printSchedule(v, w)
	case *PlanListOutput:
		return p.printPlanListOutput(v, w)
	case *RestoreDetailOutput:
		return p.printRestoreDetail(v, w)
	case *RestoreResourcesOutput:
		return p.printRestoreResources(v, w)
	default:
		return fmt.Errorf("no human-readable printer registered for %T", obj)
	}
}

// textWriter wraps tabwriter with convenient methods for building formatted text output.

func (p *HumanReadablePrinter) printPlanListOutput(out *PlanListOutput, w io.Writer) error {
	tw := newTextWriter(w)
	tw.header("Name", "Namespace", "Phase", "Suspended", "Next Event", "Age")

	for _, item := range out.Items {
		plan := item.Plan

		suspended := "no"
		if plan.Spec.Suspend {
			suspended = "yes"
		}

		age := FormatAge(time.Since(plan.CreationTimestamp.Time))
		nextEvent := FormatNextEvent(item.NextEvent)

		tw.row(plan.Name, plan.Namespace, plan.Status.Phase, suspended, nextEvent, age)
	}

	return tw.flush()
}

func (p *HumanReadablePrinter) printPlan(plan hibernatorv1alpha1.HibernatePlan, w io.Writer) error {
	tw := newTextWriter(w)

	tw.line("Name:       %s", plan.Name)
	tw.line("Namespace:  %s", plan.Namespace)
	tw.line("Created:    %s", plan.CreationTimestamp.Format("2006-01-02 15:04:05"))
	tw.newline()

	// Schedule
	tw.line("Schedule:")
	tw.line("  Timezone: %s", plan.Spec.Schedule.Timezone)
	tw.line("  Off-Hour Windows:")
	for _, window := range plan.Spec.Schedule.OffHours {
		tw.line("    %s - %s on %v", window.Start, window.End, window.DaysOfWeek)
	}
	tw.newline()

	// Behavior
	tw.line("Behavior:")
	tw.line("  Mode:     %s", plan.Spec.Behavior.Mode)
	tw.line("  Retries:  %d", plan.Spec.Behavior.Retries)
	tw.newline()

	// Execution strategy
	tw.line("Execution Strategy:")
	tw.line("  Type:  %s", plan.Spec.Execution.Strategy.Type)
	if plan.Spec.Execution.Strategy.MaxConcurrency != nil {
		tw.line("  Max Concurrency:  %d", *plan.Spec.Execution.Strategy.MaxConcurrency)
	}
	if len(plan.Spec.Execution.Strategy.Dependencies) > 0 {
		tw.line("  Dependencies:")
		for _, dep := range plan.Spec.Execution.Strategy.Dependencies {
			tw.line("    %s -> %s", dep.From, dep.To)
		}
	}
	tw.newline()

	// Targets
	tw.line("Targets:")
	if len(plan.Spec.Targets) == 0 {
		tw.line("  (none)")
	} else {
		for i, target := range plan.Spec.Targets {
			tw.line("  [%d] %s (%s)", i, target.Name, target.Type)
			tw.line("      Connector: %s/%s", target.ConnectorRef.Kind, target.ConnectorRef.Name)
			if target.Parameters != nil && len(target.Parameters.Raw) > 0 {
				tw.line("      Parameters:")

				str, err := json.MarshalIndent(json.RawMessage(target.Parameters.Raw), "        ", "  ")
				if err != nil {
					var params map[string]interface{}
					if err := json.Unmarshal(target.Parameters.Raw, &params); err == nil {
						for k, v := range params {
							tw.line("        %s: %v", k, v)
						}
					}
				} else {
					tw.line("        %s", string(str))
				}
			}
		}
	}
	tw.newline()

	if err := tw.flush(); err != nil {
		return err
	}

	return p.printStatus(&StatusOutput{Plan: plan}, w)
}

func (p *HumanReadablePrinter) printStatus(out *StatusOutput, w io.Writer) error {
	plan := out.Plan

	tw := newTextWriter(w)
	tw.line("Status")

	tw.line("  Phase:     %s", plan.Status.Phase)
	tw.line("  Suspended: %t", plan.Spec.Suspend)

	// Suspend annotations
	if plan.Spec.Suspend && plan.Annotations != nil {
		if until, ok := plan.Annotations["hibernator.ardikabs.com/suspend-until"]; ok {
			tw.line("  Suspend Until: %s", until)
		}
		if reason, ok := plan.Annotations["hibernator.ardikabs.com/suspend-reason"]; ok {
			tw.line("  Suspend Reason: %s", reason)
		}
	}

	tw.newline()

	if plan.Status.CurrentCycleID != "" {
		tw.line("  Current Cycle: %s", plan.Status.CurrentCycleID)
		tw.line("  Operation:     %s", plan.Status.CurrentOperation)
		tw.newline()
	}

	if plan.Status.Phase == hibernatorv1alpha1.PhaseError {
		tw.line("  Error:       %s", plan.Status.ErrorMessage)
		tw.line("  Retry Count: %d/%d", plan.Status.RetryCount, plan.Spec.Behavior.Retries)
		if plan.Status.LastRetryTime != nil {
			tw.line("  Last Retry:  %s (%s ago)", plan.Status.LastRetryTime.Format(time.RFC3339), HumanDuration(time.Since(plan.Status.LastRetryTime.Time)))
		}
		tw.newline()
	}

	if len(plan.Status.Executions) > 0 {
		tw.line("  Target Executions:")
		for _, exec := range plan.Status.Executions {
			tw.text("  %s %-30s  %s", StateIcon(exec.State), exec.Target, exec.State)
			if exec.Attempts > 0 {
				tw.text("  (attempts: %d)", exec.Attempts)
			}
			if exec.Message != "" {
				tw.text("  %s", exec.Message)
			}
			tw.newline()

			if exec.StartedAt != nil {
				tw.line("    Started:  %s", exec.StartedAt.Format(time.RFC3339))
			}
			if exec.FinishedAt != nil {
				tw.line("    Finished: %s", exec.FinishedAt.Format(time.RFC3339))
			}
		}
	}

	if len(plan.Status.ExecutionHistory) > 1 {
		last := plan.Status.ExecutionHistory[len(plan.Status.ExecutionHistory)-2]
		tw.line("\n  Last Cycle: %s", last.CycleID)

		if last.ShutdownExecution != nil {
			p.printOperationSummary(tw, "    Shutdown", last.ShutdownExecution)
		}
		if last.WakeupExecution != nil {
			p.printOperationSummary(tw, "    Wakeup", last.WakeupExecution)
		}
	}

	if len(plan.Status.ActiveExceptions) > 0 {
		tw.line("\n  Active Exceptions:")
		for _, exc := range plan.Status.ActiveExceptions {
			tw.line("    - %s (type: %s, until: %s, state: %s)",
				exc.Name,
				exc.Type,
				exc.ValidUntil.Format("2006-01-02 15:04:05"),
				exc.State,
			)
		}
	}

	return tw.flush()
}

func (p *HumanReadablePrinter) printOperationSummary(tw *textWriter, prefix string, op *hibernatorv1alpha1.ExecutionOperationSummary) {
	successStr := "failed"
	if op.Success {
		successStr = "success"
	}
	tw.text("%s: %s (started: %s", prefix, successStr, op.StartTime.Format(time.RFC3339))
	if op.EndTime != nil {
		tw.text(", ended: %s", op.EndTime.Format(time.RFC3339))
	}
	tw.line(")")

	if op.ErrorMessage != "" {
		tw.line("%s  Error: %s", prefix, op.ErrorMessage)
	}
}

func (p *HumanReadablePrinter) printSchedule(out *ScheduleOutput, w io.Writer) error {
	plan := out.Plan
	result := out.Result.(*scheduler.EvaluationResult)
	exceptions := out.Exceptions
	events := out.Events

	tw := newTextWriter(w)

	tw.line("Plan:      %s", plan.Name)
	if plan.Namespace != "" {
		tw.line("Namespace: %s", plan.Namespace)
	}
	tw.line("Timezone:  %s", plan.Spec.Schedule.Timezone)
	tw.newline()

	tw.line("Off-Hour Windows:")
	for _, window := range plan.Spec.Schedule.OffHours {
		tw.line("  %s - %s  [%s]", window.Start, window.End, strings.Join(window.DaysOfWeek, ", "))
	}
	tw.newline()

	tw.line("Current State:")
	tw.line("  State:              %s", result.CurrentState)
	tw.line("  Next Hibernate:     %s (%s)", result.NextHibernateTime.Format(time.RFC3339), HumanDuration(time.Until(result.NextHibernateTime)))
	tw.line("  Next WakeUp:        %s (%s)", result.NextWakeUpTime.Format(time.RFC3339), HumanDuration(time.Until(result.NextWakeUpTime)))
	tw.newline()

	if len(events) > 0 {
		tw.line("Upcoming Events (next %d):", len(events))

		evtw := newTextWriter(tw.w)
		evtw.newline()

		evtw.header("", "Operation", "Time", "In")

		for _, ev := range events {
			evtw.row("", ev.Operation, ev.Time.Format(time.RFC3339), HumanDuration(time.Until(ev.Time)))
		}

		evtw.newline()
	}

	if len(exceptions) > 0 {
		tw.line("Active Exceptions:")
		for _, ex := range exceptions {
			tw.line("  - %s (type=%s, until=%s, state=%s)\n",
				ex.Name, ex.Type, ex.ValidUntil.Format(time.RFC3339), ex.State)
		}
		tw.newline()
	}

	if err := tw.flush(); err != nil {
		return err
	}

	return nil
}

func (p *HumanReadablePrinter) printRestorePoint(cm corev1.ConfigMap, w io.Writer) error {
	var totalResources int
	var points []RestorePointData

	for _, val := range cm.Data {
		var data restore.Data
		if err := json.Unmarshal([]byte(val), &data); err != nil {
			continue
		}

		resourceCount := len(data.State)
		points = append(points, RestorePointData{
			Target:        data.Target,
			Executor:      data.Executor,
			IsLive:        data.IsLive,
			CapturedAt:    data.CapturedAt,
			ResourceCount: resourceCount,
			CreatedAt:     data.CreatedAt.Format("2006-01-02 15:04:05"),
		})
		totalResources += resourceCount
	}

	tw := newTextWriter(w)
	tw.row("Plan: ", fmt.Sprintf("%s/%s", cm.Namespace, cm.Labels["hibernator.ardikabs.com/plan"]))
	if len(points) == 0 {
		tw.newline()
		tw.line("No restore point data found")
		return tw.flush()
	}

	// Summary header
	tw.row("Total Resources:", totalResources)
	tw.newline()

	// Table of restore points by target
	tw.header("Target", "Executor", "Live", "Resources", "Captured At")

	for _, pt := range points {
		live := "no"
		if pt.IsLive {
			live = "yes"
		}
		tw.row(pt.Target, pt.Executor, live, pt.ResourceCount, pt.CapturedAt)
	}

	return tw.flush()
}

func (p *HumanReadablePrinter) printRestoreResources(out *RestoreResourcesOutput, w io.Writer) error {
	cm := out.ConfigMap
	filterTarget := out.Target

	var resources []RestoreResource
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
			resources = append(resources, RestoreResource{
				ResourceID: resourceID,
				Target:     data.Target,
				Executor:   data.Executor,
				IsLive:     data.IsLive,
				CapturedAt: data.CapturedAt,
			})
		}
	}

	tw := newTextWriter(w)
	tw.row("Plan: ", fmt.Sprintf("%s/%s", cm.Namespace, cm.Labels["hibernator.ardikabs.com/plan"]))

	if len(resources) == 0 {
		tw.newline()
		tw.line("No resources found in restore point")
		return tw.flush()
	}

	tw.newline()
	tw.header("Resource ID", "Target", "Executor", "Live", "Captured At")

	for _, r := range resources {
		live := "no"
		if r.IsLive {
			live = "yes"
		}
		tw.row(r.ResourceID, r.Target, r.Executor, live, r.CapturedAt)
	}

	return tw.flush()
}

func (p *HumanReadablePrinter) printRestoreDetail(out *RestoreDetailOutput, w io.Writer) error {
	data := out.TargetData.(restore.Data)
	tw := newTextWriter(w)

	tw.row("Plan:", out.Plan)
	tw.row("Namespace:", out.Namespace)

	tw.newline()
	tw.row("Target:", data.Target)
	tw.row("Resource ID:", out.ResourceID)
	tw.row("Executor:", data.Executor)
	tw.newline()

	tw.line("Metadata:")
	tw.row("  Live:", data.IsLive)
	tw.row("  Created At:", data.CreatedAt.Format(time.RFC3339))
	if data.CapturedAt != "" {
		tw.row("  Captured At:", data.CapturedAt)
	}
	tw.newline()

	tw.line("Resource State:")
	stateJSON, err := json.MarshalIndent(out.State, "  ", "  ")
	if err != nil {
		tw.line("  (unable to format state: %v)", err)
		return tw.flush()
	}

	tw.line("  %s", string(stateJSON))
	return tw.flush()
}
