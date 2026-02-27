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
)

// JSONPrinter handles JSON output for various resources with context-relevant information.
type JSONPrinter struct{}

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

func (p *JSONPrinter) planToJSON(plan hibernatorv1alpha1.HibernatePlan) PlanJSON {
	out := PlanJSON{
		Name:      plan.Name,
		Namespace: plan.Namespace,
		Created:   plan.CreationTimestamp.Format(time.RFC3339),
		Schedule: PlanScheduleJSON{
			Timezone: plan.Spec.Schedule.Timezone,
			OffHours: make([]OffHourWindowJSON, len(plan.Spec.Schedule.OffHours)),
		},
		Behavior: PlanBehaviorJSON{
			Mode:    string(plan.Spec.Behavior.Mode),
			Retries: plan.Spec.Behavior.Retries,
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
		output.RestorePoints = append(output.RestorePoints, RestorePointData{
			Target:        data.Target,
			Executor:      data.Executor,
			IsLive:        data.IsLive,
			CapturedAt:    data.CapturedAt,
			ResourceCount: resourceCount,
			CreatedAt:     data.CreatedAt.Format("2006-01-02T15:04:05Z"),
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
		status.LastRetryTime = plan.Status.LastRetryTime.Format(time.RFC3339)
	}

	for _, exec := range plan.Status.Executions {
		e := ExecutionStatusJSON{
			Target:   exec.Target,
			State:    string(exec.State),
			Attempts: exec.Attempts,
			Message:  exec.Message,
		}
		if exec.StartedAt != nil {
			e.StartedAt = exec.StartedAt.Format(time.RFC3339)
		}
		if exec.FinishedAt != nil {
			e.FinishedAt = exec.FinishedAt.Format(time.RFC3339)
		}
		status.Executions = append(status.Executions, e)
	}

	for _, exc := range plan.Status.ActiveExceptions {
		status.ActiveExceptions = append(status.ActiveExceptions, ExceptionReferenceJSON{
			Name:       exc.Name,
			ValidUntil: exc.ValidUntil.Format(time.RFC3339),
		})
	}

	return status
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
			NextHibernate: evalResult.NextHibernateTime.Format(time.RFC3339),
			NextWakeUp:    evalResult.NextWakeUpTime.Format(time.RFC3339),
		}
	}

	for _, exc := range out.Exceptions {
		result.Exceptions = append(result.Exceptions, ExceptionReferenceJSON{
			Name:       exc.Name,
			ValidUntil: exc.ValidUntil.Format(time.RFC3339),
		})
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
	result.CreatedAt = data.CreatedAt.Format(time.RFC3339)
	result.CapturedAt = data.CapturedAt

	return result
}

func (p *JSONPrinter) restoreResourcesToJSON(out *RestoreResourcesOutput) RestoreResourcesJSON {
	result := RestoreResourcesJSON{
		Resources: []RestoreResourceJSON{},
	}

	for _, val := range out.ConfigMap.Data {
		var data struct {
			Target     string                 `json:"target"`
			Executor   string                 `json:"executor"`
			IsLive     bool                   `json:"isLive"`
			CapturedAt string                 `json:"capturedAt"`
			State      map[string]interface{} `json:"state"`
		}
		if err := json.Unmarshal([]byte(val), &data); err != nil {
			continue
		}

		if out.Target != "" && data.Target != out.Target {
			continue
		}

		for resourceID := range data.State {
			result.Resources = append(result.Resources, RestoreResourceJSON{
				ResourceID: resourceID,
				Target:     data.Target,
				Executor:   data.Executor,
				IsLive:     data.IsLive,
				CapturedAt: data.CapturedAt,
			})
		}
	}

	return result
}
