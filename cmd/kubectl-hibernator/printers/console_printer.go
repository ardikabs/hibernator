/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package printers

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/samber/lo"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

// ConsolePrinter handles table-like output for various resources
type ConsolePrinter struct{}

func (p *ConsolePrinter) PrintObj(obj interface{}, w io.Writer) error {
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
	case *NotifListOutput:
		return p.printNotifList(v, w)
	case *NotifDescribeOutput:
		return p.printNotifDescribe(v, w)
	case *NotifSendDryRunOutput:
		return p.printNotifSendDryRun(v, w)
	default:
		return fmt.Errorf("no human-readable printer registered for %T", obj)
	}
}

// printPlanListOutput renders the tabular plan list for `kubectl-hibernator list`.
func (p *ConsolePrinter) printPlanListOutput(out *PlanListOutput, w io.Writer) error {
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

// printPlan renders full plan details (schedule, behavior, execution, targets, status) for `kubectl-hibernator describe`.
func (p *ConsolePrinter) printPlan(plan hibernatorv1alpha1.HibernatePlan, w io.Writer) error {
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
	tw.line("  Retries:  %d", ptr.Deref(plan.Spec.Behavior.Retries, wellknown.DefaultRecoveryMaxRetryAttempts))
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

// printStatus renders the live status block (phase, executions, history, exceptions); called internally by printPlan.
func (p *ConsolePrinter) printStatus(out *StatusOutput, w io.Writer) error {
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
		tw.line("  Retry Count: %d/%d", plan.Status.RetryCount, ptr.Deref(plan.Spec.Behavior.Retries, wellknown.DefaultRecoveryMaxRetryAttempts))
		if plan.Status.LastRetryTime != nil {
			tw.line("  Last Retry:  %s (%s ago)", plan.Status.LastRetryTime.Format(time.RFC3339), HumanDuration(time.Since(plan.Status.LastRetryTime.Time)))
		}
		tw.newline()
	}

	if len(plan.Status.Executions) > 0 {
		tw.line("  Target Executions:")
		for _, exec := range plan.Status.Executions {
			tw.row("  ",
				StateIcon(exec.State),
				exec.Target,
				exec.State,
				lo.Ternary(exec.Attempts > 0, fmt.Sprintf("(attempts: %d)", exec.Attempts), ""),
			)

			if exec.Message != "" {
				tw.row("  ", "  ", "Message:", exec.Message)
			}
			if exec.StartedAt != nil {
				tw.row("  ", "  ", "Started:", exec.StartedAt.Format(time.RFC3339))
			}
			if exec.FinishedAt != nil {
				tw.row("  ", "  ", "Finished:", exec.FinishedAt.Format(time.RFC3339))
			}
		}
	}

	tw.newline()

	if len(plan.Status.ExecutionHistory) > 1 {
		last := plan.Status.ExecutionHistory[len(plan.Status.ExecutionHistory)-2]
		// tw.line("\n  Last Cycle: %s", last.CycleID)
		tw.row("", "Last Cycle:", last.CycleID)

		if last.ShutdownExecution != nil {
			p.printOperationSummary(tw, last.ShutdownExecution)
		}
		if last.WakeupExecution != nil {
			p.printOperationSummary(tw, last.WakeupExecution)
		}
	}

	if len(plan.Status.ExceptionReferences) > 0 {
		tw.line("\n Exceptions:")
		for _, exc := range plan.Status.ExceptionReferences {
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

// printOperationSummary renders a single shutdown/wakeup cycle summary line; called internally by printStatus.
func (p *ConsolePrinter) printOperationSummary(tw *textWriter, op *hibernatorv1alpha1.ExecutionOperationSummary) {
	successStr := "Failed"
	if op.Success {
		successStr = "Succeeded"
	}

	toTitle := cases.Title(language.English, cases.Compact)

	tw.text("    %s: %s (StartedAt: %s", toTitle.String(string(op.Operation)), successStr, op.StartTime.Format(time.RFC3339))
	if op.EndTime != nil {
		tw.text(", FinishedAt: %s", op.EndTime.Format(time.RFC3339))
	}
	if op.ErrorMessage != "" {
		tw.text(", Error: %s", op.ErrorMessage)
	}
	tw.line(")")

	for _, target := range op.TargetResults {
		tw.row("  ", "  ", StateIcon(target.State), fmt.Sprintf("%s:", target.Target), lo.Ternary(target.Message != "", target.Message, "N/A"))
	}
}

// printSchedule renders schedule evaluation, upcoming events, and active exceptions for `kubectl-hibernator preview`.
func (p *ConsolePrinter) printSchedule(out *ScheduleOutput, w io.Writer) error {
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
			in := ev.In
			if in == 0 {
				in = time.Until(ev.Time)
			}
			evtw.row("", ev.Operation, ev.Time.Format(time.RFC3339), HumanDuration(in))
		}

		evtw.newline()
	}

	if len(exceptions) > 0 {
		tw.line("Active Exceptions:")
		for _, ex := range exceptions {
			tw.line("  - %s (type=%s, until=%s, state=%s)",
				ex.Name, ex.Type, ex.ValidUntil.Format(time.RFC3339), ex.State)
		}
		tw.newline()
	}

	if err := tw.flush(); err != nil {
		return err
	}

	return nil
}

// printRestorePoint renders a summary table of all restore-point targets for `kubectl-hibernator restore show`.
func (p *ConsolePrinter) printRestorePoint(cm corev1.ConfigMap, w io.Writer) error {
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

// printRestoreResources renders the flat resource list within a restore point for `kubectl-hibernator restore list`.
func (p *ConsolePrinter) printRestoreResources(out *RestoreResourcesOutput, w io.Writer) error {
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

// printRestoreDetail renders the full metadata and raw state of a single restore resource for `kubectl-hibernator restore inspect`.
func (p *ConsolePrinter) printRestoreDetail(out *RestoreDetailOutput, w io.Writer) error {
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

// printNotifList renders the tabular notification list for `kubectl-hibernator notification list`.
func (p *ConsolePrinter) printNotifList(out *NotifListOutput, w io.Writer) error {
	tw := newTextWriter(w)
	tw.header("Name", "Namespace", "Events", "Sinks", "Watched Plans", "Last Delivery", "Age")

	for _, item := range out.Items {
		notif := item.Notification

		events := make([]string, len(notif.Spec.OnEvents))
		for i, e := range notif.Spec.OnEvents {
			events[i] = string(e)
		}

		sinkCount := fmt.Sprintf("%d", len(notif.Spec.Sinks))
		planCount := fmt.Sprintf("%d", len(notif.Status.WatchedPlans))

		lastDelivery := "-"
		if notif.Status.LastDeliveryTime != nil {
			lastDelivery = HumanDuration(time.Since(notif.Status.LastDeliveryTime.Time)) + " ago"
		}

		age := FormatAge(time.Since(notif.CreationTimestamp.Time))

		tw.row(notif.Name, notif.Namespace, strings.Join(events, ","), sinkCount, planCount, lastDelivery, age)
	}

	return tw.flush()
}

// printNotifDescribe renders detailed notification information for `kubectl-hibernator notification describe`.
func (p *ConsolePrinter) printNotifDescribe(out *NotifDescribeOutput, w io.Writer) error {
	notif := out.Notification
	tw := newTextWriter(w)

	tw.line("Name:       %s", notif.Name)
	tw.line("Namespace:  %s", notif.Namespace)
	tw.line("Created:    %s", notif.CreationTimestamp.Format("2006-01-02 15:04:05"))

	if len(notif.Labels) > 0 {
		tw.line("Labels:")
		for k, v := range notif.Labels {
			tw.line("  %s: %s", k, v)
		}
	}
	tw.newline()

	// Selector
	tw.line("Selector:")
	if len(notif.Spec.Selector.MatchLabels) > 0 {
		for k, v := range notif.Spec.Selector.MatchLabels {
			tw.line("  %s: %s", k, v)
		}
	}
	if len(notif.Spec.Selector.MatchExpressions) > 0 {
		for _, expr := range notif.Spec.Selector.MatchExpressions {
			tw.line("  %s %s %v", expr.Key, expr.Operator, expr.Values)
		}
	}
	tw.newline()

	// Events
	tw.line("Events:")
	for _, e := range notif.Spec.OnEvents {
		tw.line("  - %s", e)
	}
	tw.newline()

	// Sinks
	tw.line("Sinks:")
	for i, s := range notif.Spec.Sinks {
		tw.line("  [%d] %s (%s)", i, s.Name, s.Type)
		tw.line("      Secret: %s", formatObjectKeyRef(s.SecretRef))
		if s.TemplateRef != nil {
			tw.line("      Template: %s", formatObjectKeyRef(*s.TemplateRef))
		}
	}
	tw.newline()

	// Plan match result
	if out.PlanMatch != nil {
		icon := "[NO]"
		if out.PlanMatch.Matches {
			icon = "[OK]"
		}
		tw.line("Plan Match: %s %s", icon, out.PlanMatch.PlanName)
		tw.newline()
	}

	// Status
	tw.line("Status:")
	if len(notif.Status.WatchedPlans) > 0 {
		tw.line("  Watched Plans:")
		for _, p := range notif.Status.WatchedPlans {
			ns := p.Namespace
			if ns == "" {
				ns = notif.Namespace
			}
			tw.line("    - %s/%s", ns, p.Name)
		}
	} else {
		tw.line("  Watched Plans: (none)")
	}

	if notif.Status.LastDeliveryTime != nil {
		tw.line("  Last Delivery: %s (%s ago)", notif.Status.LastDeliveryTime.Format(time.RFC3339), HumanDuration(time.Since(notif.Status.LastDeliveryTime.Time)))
	}
	if notif.Status.LastFailureTime != nil {
		tw.line("  Last Failure:  %s (%s ago)", notif.Status.LastFailureTime.Format(time.RFC3339), HumanDuration(time.Since(notif.Status.LastFailureTime.Time)))
	}

	if len(notif.Status.SinkStatuses) > 0 {
		tw.line("  Recent Deliveries:")
		sinkStatuses := lo.Values(notif.Status.SinkStatuses)
		sort.Slice(sinkStatuses, func(i, j int) bool {
			return sinkStatuses[i].TransitionTimestamp.After(sinkStatuses[j].TransitionTimestamp.Time)
		})
		for _, ss := range sinkStatuses {
			status := "[FAIL]"
			if ss.Success {
				status = "[OK]"
			}
			tw.line("    %s %s (%s/%s %s %s) at %s", status, ss.SinkName, ss.PlanRef.Namespace, ss.PlanRef.Name, ss.Operation, ss.CycleID, ss.TransitionTimestamp.Format(time.RFC3339))
			if ss.Message != "" {
				tw.line("        %s", ss.Message)
			}
		}
	}

	return tw.flush()
}

// printNotifSendDryRun renders the dry-run send output for `kubectl-hibernator notification send --dry-run`.
func (p *ConsolePrinter) printNotifSendDryRun(out *NotifSendDryRunOutput, w io.Writer) error {
	tw := newTextWriter(w)

	tw.line("Dry Run — notification NOT sent")
	tw.newline()
	tw.line("Sink:    %s (%s)", out.SinkName, out.SinkType)
	tw.line("Event:   %s", out.Event)
	tw.newline()
	tw.line("Rendered Message:")
	tw.line("---")
	tw.line("%s", out.Rendered)
	tw.line("---")

	return tw.flush()
}

// formatObjectKeyRef formats an ObjectKeyReference as "name[key]" or just "name".
func formatObjectKeyRef(ref hibernatorv1alpha1.ObjectKeyReference) string {
	if ref.Key != nil {
		return fmt.Sprintf("%s[%s]", ref.Name, *ref.Key)
	}
	return ref.Name
}
