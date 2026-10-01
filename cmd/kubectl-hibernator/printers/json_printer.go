/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package printers

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/scheduler"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

// JSONPrinter handles JSON output for various resources with context-relevant information.
type JSONPrinter struct{}

// formatUnixTime returns the Unix timestamp (seconds since epoch) for JSON output
func formatUnixTime(t time.Time) int64 {
	return t.Unix()
}

func (p *JSONPrinter) PrintObj(obj interface{}, w io.Writer) error {
	var output interface{}
	var err error

	switch v := obj.(type) {
	case hibernatorv1alpha1.HibernatePlan:
		output = p.planToJSON(v)
	case *hibernatorv1alpha1.HibernatePlan:
		output = p.planToJSON(*v)
	case *PlanListOutput:
		output = p.planListToJSON(v)
	case *ExceptionListOutput:
		output = p.exceptionListToJSON(v)
	case *ExceptionStatusOutput:
		output = p.exceptionStatusToJSON(v)
	case *ExceptionOperationOutput:
		output = p.exceptionOperationToJSON(v)
	case *ScheduleOutput:
		output, err = p.scheduleToJSON(v)
	case *StatusOutput:
		output = p.statusToJSON(v)
	case corev1.ConfigMap:
		output, err = p.printRestoreShowJSON(v)
	case *corev1.ConfigMap:
		output, err = p.printRestoreShowJSON(*v)
	case *RestoreDetailOutput:
		output = p.restoreDetailToJSON(v)
	case *RestoreResourcesOutput:
		output = p.restoreResourcesToJSON(v)
	case *NotifListOutput:
		output = p.notifListToJSON(v)
	case *NotifDescribeOutput:
		output = p.notifDescribeToJSON(v)
	case *NotifSendDryRunOutput:
		output = p.notifSendDryRunToJSON(v)
	default:
		output = obj
	}

	if err != nil {
		return err
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

func (p *JSONPrinter) exceptionListToJSON(out *ExceptionListOutput) ExceptionListJSON {
	result := ExceptionListJSON{Items: make([]ExceptionListItemJSON, len(out.Items))}
	for i, exception := range out.Items {
		result.Items[i] = exceptionListItemToJSON(exception)
	}
	return result
}

func (p *JSONPrinter) exceptionStatusToJSON(out *ExceptionStatusOutput) ExceptionStatusJSON {
	result := ExceptionStatusJSON{Items: make([]ExceptionStatusItemJSON, len(out.Items))}
	for i, item := range out.Items {
		exception := item.Exception
		status := ExceptionStatusItemJSON{
			Name:       exception.Name,
			Namespace:  exception.Namespace,
			Labels:     exception.Labels,
			Plan:       exception.Spec.PlanRef.Name,
			PlanPhase:  item.PlanPhase,
			PlanExists: item.PlanExists,
			Type:       exception.Spec.Type,
			State:      exception.Status.State,
			ValidFrom:  formatRFC3339UTC(exception.Spec.ValidFrom.Time),
			ValidUntil: formatRFC3339UTC(exception.Spec.ValidUntil.Time),
			Timing:     item.Timing,
			Windows:    exceptionWindowsToJSON(exception.Spec.Windows),
			CreatedAt:  formatRFC3339UTC(exception.CreationTimestamp.Time),
			Message:    exception.Status.Message,
		}
		if exception.Status.AppliedAt != nil {
			status.AppliedAt = formatRFC3339UTC(exception.Status.AppliedAt.Time)
		}
		if exception.Status.ExpiredAt != nil {
			status.ExpiredAt = formatRFC3339UTC(exception.Status.ExpiredAt.Time)
		}
		if exception.Status.DetachedAt != nil {
			status.DetachedAt = formatRFC3339UTC(exception.Status.DetachedAt.Time)
		}
		result.Items[i] = status
	}
	return result
}

func (p *JSONPrinter) exceptionOperationToJSON(out *ExceptionOperationOutput) ExceptionOperationJSON {
	result := ExceptionOperationJSON{
		Action: out.Action,
		Items:  make([]ExceptionOperationResultJSON, len(out.Items)),
	}
	for i, item := range out.Items {
		result.Items[i] = ExceptionOperationResultJSON{
			Plan:    item.Plan,
			Name:    item.Name,
			Result:  item.Result,
			Message: item.Message,
		}
	}
	return result
}

func exceptionListItemToJSON(exception hibernatorv1alpha1.ScheduleException) ExceptionListItemJSON {
	return ExceptionListItemJSON{
		Name:       exception.Name,
		Namespace:  exception.Namespace,
		Labels:     exception.Labels,
		Plan:       exception.Spec.PlanRef.Name,
		Type:       exception.Spec.Type,
		State:      exception.Status.State,
		ValidFrom:  formatRFC3339UTC(exception.Spec.ValidFrom.Time),
		ValidUntil: formatRFC3339UTC(exception.Spec.ValidUntil.Time),
		Windows:    exceptionWindowsToJSON(exception.Spec.Windows),
		CreatedAt:  formatRFC3339UTC(exception.CreationTimestamp.Time),
	}
}

func exceptionWindowsToJSON(windows []hibernatorv1alpha1.OffHourWindow) []OffHourWindowJSON {
	result := make([]OffHourWindowJSON, len(windows))
	for i, window := range windows {
		days := make([]string, len(window.DaysOfWeek))
		copy(days, window.DaysOfWeek)
		result[i] = OffHourWindowJSON{
			Start:      window.Start,
			End:        window.End,
			DaysOfWeek: days,
		}
	}
	return result
}

func formatRFC3339UTC(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func (p *JSONPrinter) planToJSON(plan hibernatorv1alpha1.HibernatePlan) PlanJSON {
	out := PlanJSON{
		Name:      plan.Name,
		Namespace: plan.Namespace,
		Created:   formatUnixTime(plan.CreationTimestamp.Time),
		Schedule: PlanScheduleJSON{
			Timezone: plan.Spec.Schedule.Timezone,
			OffHours: make([]OffHourWindowJSON, len(plan.Spec.Schedule.OffHours)),
		},
		Behavior: PlanBehaviorJSON{
			Mode:    string(plan.Spec.Behavior.Mode),
			Retries: ptr.Deref(plan.Spec.Behavior.Retries, 3),
		},
		Execution: PlanExecutionJSON{
			StrategyType:   string(plan.Spec.Execution.Strategy.Type),
			MaxConcurrency: plan.Spec.Execution.Strategy.MaxConcurrency,
		},
		Targets: make([]PlanTargetJSON, len(plan.Spec.Targets)),
	}

	for i, w := range plan.Spec.Schedule.OffHours {
		out.Schedule.OffHours[i] = OffHourWindowJSON{
			Start:      w.Start,
			End:        w.End,
			DaysOfWeek: w.DaysOfWeek,
		}
	}

	for i, dep := range plan.Spec.Execution.Strategy.Dependencies {
		out.Execution.Dependencies = append(out.Execution.Dependencies, PlanDependencyJSON{
			From: dep.From,
			To:   dep.To,
		})
		_ = i
	}

	for i, t := range plan.Spec.Targets {
		target := PlanTargetJSON{
			Name:         t.Name,
			Type:         string(t.Type),
			ConnectorRef: fmt.Sprintf("%s/%s", t.ConnectorRef.Kind, t.ConnectorRef.Name),
		}
		if t.Parameters != nil && len(t.Parameters.Raw) > 0 {
			var params map[string]interface{}
			if err := json.Unmarshal(t.Parameters.Raw, &params); err == nil {
				target.Parameters = params
			}
		}
		out.Targets[i] = target
	}

	out.Status = p.buildStatusJSON(plan)

	return out
}

func (p *JSONPrinter) printRestoreShowJSON(cm corev1.ConfigMap) (any, error) {
	output := RestoreShowJSONOutput{
		Plan:      cm.Labels["hibernator.ardikabs.com/plan"],
		Namespace: cm.Namespace,
	}

	for _, val := range cm.Data {
		var data restore.Data
		if err := json.Unmarshal([]byte(val), &data); err != nil {
			continue
		}

		resourceCount := len(data.State)
		staleCount := 0
		for _, status := range data.Status {
			if status.StaleCount > 0 {
				staleCount++
			}
		}

		var capturedAt int64
		if data.CapturedAt != nil {
			capturedAt = formatUnixTime(data.CapturedAt.Time)
		}

		output.RestorePoints = append(output.RestorePoints, RestorePointData{
			Target:         data.Target,
			Executor:       data.Executor,
			IsLive:         data.IsLive,
			ResourceCount:  resourceCount,
			StaleResources: staleCount,
			CreatedAt:      formatUnixTime(data.CreatedAt.Time),
			CapturedAt:     capturedAt,
		})
		output.TotalResources += resourceCount
	}

	return output, nil
}

func (p *JSONPrinter) buildStatusJSON(plan hibernatorv1alpha1.HibernatePlan) PlanStatusJSON {
	status := PlanStatusJSON{
		Phase:            string(plan.Status.Phase),
		Suspended:        plan.Spec.Suspend,
		CurrentCycleID:   plan.Status.CurrentCycleID,
		CurrentOperation: string(plan.Status.CurrentOperation),
		ErrorMessage:     plan.Status.ErrorMessage,
		RetryCount:       plan.Status.RetryCount,
	}

	if plan.Spec.Suspend && plan.Annotations != nil {
		if until, ok := plan.Annotations["hibernator.ardikabs.com/suspend-until"]; ok {
			status.SuspendUntil = until
		}
		if reason, ok := plan.Annotations["hibernator.ardikabs.com/suspend-reason"]; ok {
			status.SuspendReason = reason
		}
	}

	if plan.Status.LastRetryTime != nil {
		status.LastRetryTime = formatUnixTime(plan.Status.LastRetryTime.Time)
	}

	for _, exec := range plan.Status.Executions {
		e := ExecutionStatusJSON{
			Target:   exec.Target,
			State:    string(exec.State),
			Attempts: exec.Attempts,
			Message:  exec.Message,
		}
		if exec.StartedAt != nil {
			e.StartedAt = formatUnixTime(exec.StartedAt.Time)
		}
		if exec.FinishedAt != nil {
			e.FinishedAt = formatUnixTime(exec.FinishedAt.Time)
		}
		status.Executions = append(status.Executions, e)
	}

	for _, exc := range plan.Status.ExceptionReferences {
		ref := ExceptionReferenceJSON{
			Name:       exc.Name,
			Type:       string(exc.Type),
			ValidFrom:  formatUnixTime(exc.ValidFrom.Time),
			ValidUntil: formatUnixTime(exc.ValidUntil.Time),
			State:      string(exc.State),
		}
		if exc.AppliedAt != nil {
			ref.AppliedAt = formatUnixTime(exc.AppliedAt.Time)
		}
		status.ExceptionReferences = append(status.ExceptionReferences, ref)
	}

	for _, cycle := range plan.Status.ExecutionHistory {
		c := ExecutionCycleJSON{CycleID: cycle.CycleID}
		if cycle.ShutdownExecution != nil {
			c.ShutdownExecution = p.operationSummaryToJSON(cycle.ShutdownExecution)
		}
		if cycle.WakeupExecution != nil {
			c.WakeupExecution = p.operationSummaryToJSON(cycle.WakeupExecution)
		}
		status.ExecutionHistory = append(status.ExecutionHistory, c)
	}

	return status
}

func (p *JSONPrinter) operationSummaryToJSON(op *hibernatorv1alpha1.ExecutionOperationSummary) *ExecutionOperationSummaryJSON {
	s := &ExecutionOperationSummaryJSON{
		Operation:    string(op.Operation),
		StartTime:    formatUnixTime(op.StartTime.Time),
		Success:      op.Success,
		ErrorMessage: op.ErrorMessage,
	}
	if op.EndTime != nil {
		s.EndTime = formatUnixTime(op.EndTime.Time)
	}
	for _, tr := range op.TargetResults {
		r := TargetExecutionResultJSON{
			Target:      tr.Target,
			State:       string(tr.State),
			Attempts:    tr.Attempts,
			ExecutionID: tr.ExecutionID,
			Message:     tr.Message,
		}
		if tr.StartedAt != nil {
			r.StartedAt = formatUnixTime(tr.StartedAt.Time)
		}
		if tr.FinishedAt != nil {
			r.FinishedAt = formatUnixTime(tr.FinishedAt.Time)
		}
		s.TargetResults = append(s.TargetResults, r)
	}
	return s
}

func (p *JSONPrinter) planListToJSON(out *PlanListOutput) PlanListJSON {
	result := PlanListJSON{
		Items: make([]PlanListItemJSON, len(out.Items)),
	}

	for i, item := range out.Items {
		result.Items[i] = PlanListItemJSON{
			Name:      item.Plan.Name,
			Namespace: item.Plan.Namespace,
			Phase:     string(item.Plan.Status.Phase),
			Suspended: item.Plan.Spec.Suspend,
			NextEvent: item.NextEvent,
			Age:       FormatAge(time.Since(item.Plan.CreationTimestamp.Time)),
		}
	}

	return result
}

func (p *JSONPrinter) scheduleToJSON(out *ScheduleOutput) (ScheduleJSON, error) {
	result := ScheduleJSON{
		Plan:      out.Plan.Name,
		Namespace: out.Plan.Namespace,
		Timezone:  out.Plan.Spec.Schedule.Timezone,
		OffHours:  make([]OffHourWindowJSON, len(out.Plan.Spec.Schedule.OffHours)),
		Events:    out.Events,
	}

	for i, w := range out.Plan.Spec.Schedule.OffHours {
		result.OffHours[i] = OffHourWindowJSON{
			Start:      w.Start,
			End:        w.End,
			DaysOfWeek: w.DaysOfWeek,
		}
	}

	if evalResult, ok := out.Result.(*scheduler.EvaluationResult); ok {
		result.State = ScheduleStateJSON{
			Current:       string(evalResult.CurrentState),
			NextHibernate: formatUnixTime(evalResult.NextHibernateTime),
			NextWakeUp:    formatUnixTime(evalResult.NextWakeUpTime),
		}
	}

	for _, exc := range out.Exceptions {
		ref := ExceptionReferenceJSON{
			Name:       exc.Name,
			Type:       string(exc.Type),
			ValidFrom:  formatUnixTime(exc.ValidFrom.Time),
			ValidUntil: formatUnixTime(exc.ValidUntil.Time),
			State:      string(exc.State),
		}
		if exc.AppliedAt != nil {
			ref.AppliedAt = formatUnixTime(exc.AppliedAt.Time)
		}
		result.Exceptions = append(result.Exceptions, ref)
	}

	return result, nil
}

func (p *JSONPrinter) statusToJSON(out *StatusOutput) PlanStatusJSON {
	return p.buildStatusJSON(out.Plan)
}

func (p *JSONPrinter) restoreDetailToJSON(out *RestoreDetailOutput) RestoreDetailJSON {
	result := RestoreDetailJSON{
		Plan:       out.Plan,
		Namespace:  out.Namespace,
		ResourceID: out.ResourceID,
		State:      out.State,
	}

	// Extract additional fields from TargetData if available
	data := out.TargetData.(restore.Data)

	result.Target = data.Target
	result.Executor = data.Executor
	result.IsLive = data.IsLive
	if status, ok := data.Status[out.ResourceID]; ok {
		result.Excluded = status.Excluded
	}
	result.CreatedAt = formatUnixTime(data.CreatedAt.Time)
	if data.CapturedAt != nil {
		result.CapturedAt = formatUnixTime(data.CapturedAt.Time)
	}

	return result
}

func (p *JSONPrinter) restoreResourcesToJSON(out *RestoreResourcesOutput) RestoreResourcesJSON {
	result := RestoreResourcesJSON{
		Resources: []RestoreResourceJSON{},
	}

	for _, val := range out.ConfigMap.Data {
		var data restore.Data
		if err := json.Unmarshal([]byte(val), &data); err != nil {
			continue
		}

		if out.Target != "" && data.Target != out.Target {
			continue
		}

		var capturedAtUnix int64 = 0
		if data.CapturedAt != nil {
			capturedAtUnix = formatUnixTime(data.CapturedAt.Time)
		}

		for resourceID, state := range data.State {
			staleCount := 0
			excluded := false
			if data.Status != nil {
				staleCount = data.Status[resourceID].StaleCount
				excluded = data.Status[resourceID].Excluded
			}

			// Non-object states cannot be rendered structurally; keep the
			// row with a null state instead of panicking on the assertion.
			stateMap, _ := state.(map[string]any)
			result.Resources = append(result.Resources, RestoreResourceJSON{
				ResourceID: resourceID,
				Target:     data.Target,
				Executor:   data.Executor,
				IsLive:     data.IsLive,
				CapturedAt: capturedAtUnix,
				StaleCount: staleCount,
				Excluded:   excluded,
				CycleID:    data.CycleID,
				State:      stateMap,
			})
		}
	}

	return result
}

func (p *JSONPrinter) notifListToJSON(out *NotifListOutput) NotifListJSON {
	result := NotifListJSON{
		Items: make([]NotifListItemJSON, len(out.Items)),
	}

	for i, item := range out.Items {
		notif := item.Notification

		events := make([]string, len(notif.Spec.OnEvents))
		for j, e := range notif.Spec.OnEvents {
			events[j] = string(e)
		}

		sinks := make([]string, len(notif.Spec.Sinks))
		for j, s := range notif.Spec.Sinks {
			sinks[j] = s.Name
		}

		entry := NotifListItemJSON{
			Name:         notif.Name,
			Namespace:    notif.Namespace,
			Events:       events,
			Sinks:        sinks,
			WatchedPlans: len(notif.Status.WatchedPlans),
			Age:          FormatAge(time.Since(notif.CreationTimestamp.Time)),
		}

		if notif.Status.LastDeliveryTime != nil {
			entry.LastDelivery = formatUnixTime(notif.Status.LastDeliveryTime.Time)
		}
		if notif.Status.LastFailureTime != nil {
			entry.LastFailure = formatUnixTime(notif.Status.LastFailureTime.Time)
		}

		result.Items[i] = entry
	}

	return result
}

func (p *JSONPrinter) notifDescribeToJSON(out *NotifDescribeOutput) NotifDescribeJSON {
	notif := out.Notification

	events := make([]string, len(notif.Spec.OnEvents))
	for i, e := range notif.Spec.OnEvents {
		events[i] = string(e)
	}

	sinks := make([]NotifSinkJSON, len(notif.Spec.Sinks))
	for i, s := range notif.Spec.Sinks {
		sj := NotifSinkJSON{
			Name:      s.Name,
			Type:      string(s.Type),
			SecretRef: s.SecretRef.Name,
		}
		if s.TemplateRef != nil {
			sj.TemplateRef = &s.TemplateRef.Name
		}
		sinks[i] = sj
	}

	watchedPlans := make([]NotifWatchedPlanJSON, len(notif.Status.WatchedPlans))
	for i, pr := range notif.Status.WatchedPlans {
		watchedPlans[i] = NotifWatchedPlanJSON{
			Name:      pr.Name,
			Namespace: pr.Namespace,
		}
	}

	sinkStatuses := make([]NotifSinkStatusJSON, 0, len(notif.Status.SinkStatuses))
	for _, ss := range notif.Status.SinkStatuses {
		sinkStatuses = append(sinkStatuses, NotifSinkStatusJSON{
			Name:      ss.SinkName,
			Success:   ss.Success,
			Timestamp: formatUnixTime(ss.TransitionTimestamp.Time),
			Message:   ss.Message,
		})
	}

	result := NotifDescribeJSON{
		Name:      notif.Name,
		Namespace: notif.Namespace,
		Created:   formatUnixTime(notif.CreationTimestamp.Time),
		Labels:    notif.Labels,
		Selector:  notif.Spec.Selector.MatchLabels,
		Events:    events,
		Sinks:     sinks,
		Status: NotifStatusJSON{
			WatchedPlans: watchedPlans,
			SinkStatuses: sinkStatuses,
		},
	}

	if notif.Status.LastDeliveryTime != nil {
		result.Status.LastDelivery = formatUnixTime(notif.Status.LastDeliveryTime.Time)
	}
	if notif.Status.LastFailureTime != nil {
		result.Status.LastFailure = formatUnixTime(notif.Status.LastFailureTime.Time)
	}

	if out.PlanMatch != nil {
		result.PlanMatch = &NotifPlanMatchJSON{
			PlanName: out.PlanMatch.PlanName,
			Matches:  out.PlanMatch.Matches,
		}
	}

	return result
}

func (p *JSONPrinter) notifSendDryRunToJSON(out *NotifSendDryRunOutput) NotifSendDryRunJSON {
	return NotifSendDryRunJSON{
		SinkName: out.SinkName,
		SinkType: out.SinkType,
		Event:    out.Event,
		Rendered: out.Rendered,
	}
}
