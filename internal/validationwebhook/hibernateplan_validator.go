/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package validationwebhook

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/go-logr/logr"
)

// HibernatePlanValidator validates HibernatePlan resources.
type HibernatePlanValidator struct {
	log logr.Logger
}

// NewHibernatePlanValidator creates a new HibernatePlanValidator.
func NewHibernatePlanValidator(log logr.Logger) *HibernatePlanValidator {
	return &HibernatePlanValidator{
		log: log.WithName("hibernateplan"),
	}
}

var _ admission.CustomValidator = &HibernatePlanValidator{}

// ValidateCreate implements webhook.CustomValidator.
func (v *HibernatePlanValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	plan, ok := obj.(*hibernatorv1alpha1.HibernatePlan)
	if !ok {
		return nil, fmt.Errorf("expected HibernatePlan but got %T", obj)
	}
	v.log.V(1).Info("validate create", "name", plan.Name)
	return v.validate(plan)
}

// ValidateUpdate implements webhook.CustomValidator.
func (v *HibernatePlanValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	oldPlan, ok := oldObj.(*hibernatorv1alpha1.HibernatePlan)
	if !ok {
		return nil, fmt.Errorf("expected HibernatePlan but got %T", newObj)
	}

	newPlan, ok := newObj.(*hibernatorv1alpha1.HibernatePlan)
	if !ok {
		return nil, fmt.Errorf("expected HibernatePlan but got %T", newObj)
	}

	// Allow target edits only in Active, Suspended, or Error phases
	if oldPlan.Status.Phase != hibernatorv1alpha1.PhaseActive &&
		oldPlan.Status.Phase != hibernatorv1alpha1.PhaseSuspended &&
		oldPlan.Status.Phase != hibernatorv1alpha1.PhaseError {
		if !reflect.DeepEqual(oldPlan.Spec.Targets, newPlan.Spec.Targets) {
			return nil, field.Forbidden(
				field.NewPath("spec", "targets"),
				fmt.Sprintf("targets cannot be modified while plan is in %s phase; wait for Active, Suspended, or Error phase", oldPlan.Status.Phase),
			)
		}
	}

	v.log.V(1).Info("validate update", "name", newPlan.Name)
	return v.validate(newPlan)
}

// ValidateDelete implements webhook.CustomValidator.
func (v *HibernatePlanValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// validate performs validation on the HibernatePlan.
func (v *HibernatePlanValidator) validate(plan *hibernatorv1alpha1.HibernatePlan) (admission.Warnings, error) {
	var allErrs field.ErrorList
	var warnings admission.Warnings

	scheduleErrs, scheduleWarnings := v.validateSchedule(plan)
	allErrs = append(allErrs, scheduleErrs...)
	warnings = append(warnings, scheduleWarnings...)

	targetErrs, targetWarnings := v.validateTargets(plan)
	allErrs = append(allErrs, targetErrs...)
	warnings = append(warnings, targetWarnings...)

	strategyErrs, strategyWarnings := v.validateStrategy(plan)
	allErrs = append(allErrs, strategyErrs...)
	warnings = append(warnings, strategyWarnings...)

	if len(allErrs) > 0 {
		return warnings, allErrs.ToAggregate()
	}
	return warnings, nil
}

// validateSchedule validates the schedule configuration.
func (v *HibernatePlanValidator) validateSchedule(plan *hibernatorv1alpha1.HibernatePlan) (field.ErrorList, admission.Warnings) {
	var errs field.ErrorList
	var warnings admission.Warnings
	schedulePath := field.NewPath("spec", "schedule")

	if plan.Spec.Schedule.Timezone == "" {
		errs = append(errs, field.Required(
			schedulePath.Child("timezone"),
			"timezone is required",
		))
	}

	if len(plan.Spec.Schedule.OffHours) == 0 {
		errs = append(errs, field.Required(
			schedulePath.Child("offHours"),
			"at least one off-hour window is required",
		))
	}

	timeRegex := regexp.MustCompile(`^([0-1]?[0-9]|2[0-3]):[0-5][0-9]$`)
	validDays := map[string]bool{
		"MON": true, "TUE": true, "WED": true, "THU": true,
		"FRI": true, "SAT": true, "SUN": true,
	}

	for i, window := range plan.Spec.Schedule.OffHours {
		windowPath := schedulePath.Child("offHours").Index(i)

		if window.Start == "" {
			errs = append(errs, field.Required(
				windowPath.Child("start"),
				"start time is required",
			))
		} else if !timeRegex.MatchString(window.Start) {
			errs = append(errs, field.Invalid(
				windowPath.Child("start"),
				window.Start,
				"must be in HH:MM format (e.g., 20:00)",
			))
		}

		if window.End == "" {
			errs = append(errs, field.Required(
				windowPath.Child("end"),
				"end time is required",
			))
		} else if !timeRegex.MatchString(window.End) {
			errs = append(errs, field.Invalid(
				windowPath.Child("end"),
				window.End,
				"must be in HH:MM format (e.g., 06:00)",
			))
		}

		if window.Start != "" && window.End != "" && window.Start == window.End {
			errs = append(errs, field.Invalid(
				windowPath.Child("start"),
				window.Start,
				"start and end times must be different; a hibernation window requires a clear shutdown (start) and wakeup (end) schedule",
			))
		}

		if window.Start != "" && window.End != "" && window.Start != window.End {
			if startHour, startMin, errStart := parseTimeValues(window.Start); errStart == nil {
				if endHour, endMin, errEnd := parseTimeValues(window.End); errEnd == nil {
					startMinutes := startHour*60 + startMin
					endMinutes := endHour*60 + endMin

					var gap int
					if endMinutes >= startMinutes {
						gap = endMinutes - startMinutes
					} else {
						gap = (24*60 - startMinutes) + endMinutes
					}

					if gap > 0 && gap <= 1 {
						guidance := fmt.Sprintf("offHours[%d]: detected wakeup window within 1 minute gap from hibernation (start=%s, end=%s). "+
							"If the intention is to apply full-day wakeup operation, consider using ScheduleException with type=Suspend, start=00:00, end=23:59",
							i, window.Start, window.End)
						warnings = append(warnings, guidance)
					}
				}
			}
		}

		if len(window.DaysOfWeek) == 0 {
			errs = append(errs, field.Required(
				windowPath.Child("daysOfWeek"),
				"at least one day is required",
			))
		}

		for j, day := range window.DaysOfWeek {
			dayUpper := strings.ToUpper(day)
			if !validDays[dayUpper] {
				errs = append(errs, field.NotSupported(
					windowPath.Child("daysOfWeek").Index(j),
					day,
					[]string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
				))
			}
		}
	}

	return errs, warnings
}

// validateTargets validates target configuration.
func (v *HibernatePlanValidator) validateTargets(plan *hibernatorv1alpha1.HibernatePlan) (field.ErrorList, admission.Warnings) {
	var errs field.ErrorList
	var warnings admission.Warnings
	targetsPath := field.NewPath("spec", "targets")

	seen := make(map[string]int)
	for i, target := range plan.Spec.Targets {
		if prevIdx, ok := seen[target.Name]; ok {
			errs = append(errs, field.Duplicate(
				targetsPath.Index(i).Child("name"),
				fmt.Sprintf("target name %q already defined at index %d", target.Name, prevIdx),
			))
		}
		seen[target.Name] = i

		if target.ConnectorRef.Kind == "" {
			errs = append(errs, field.Required(
				targetsPath.Index(i).Child("connectorRef", "kind"),
				"connector kind is required",
			))
		} else if target.ConnectorRef.Kind != "CloudProvider" && target.ConnectorRef.Kind != "K8SCluster" {
			errs = append(errs, field.NotSupported(
				targetsPath.Index(i).Child("connectorRef", "kind"),
				target.ConnectorRef.Kind,
				[]string{"CloudProvider", "K8SCluster"},
			))
		}

		if target.ConnectorRef.Name == "" {
			errs = append(errs, field.Required(
				targetsPath.Index(i).Child("connectorRef", "name"),
				"connector name is required",
			))
		}

		validTypes := []string{
			"ec2", "eks", "rds", "karpenter", "workloadscaler",
			"gke", "cloudsql", "noop",
		}
		isValidType := false
		for _, vt := range validTypes {
			if target.Type == vt {
				isValidType = true
				break
			}
		}
		if !isValidType {
			errs = append(errs, field.NotSupported(
				targetsPath.Index(i).Child("type"),
				target.Type,
				validTypes,
			))
		}

		var paramsRaw []byte
		if target.Parameters != nil {
			paramsRaw = target.Parameters.Raw
		}
		if result := executorparams.ValidateParams(target.Type, paramsRaw); result != nil {
			paramPath := targetsPath.Index(i).Child("parameters")

			for _, errMsg := range result.Errors {
				errs = append(errs, field.Invalid(paramPath, target.Parameters, errMsg))
			}

			for _, warnMsg := range result.Warnings {
				warnings = append(warnings, fmt.Sprintf("target %q: %s", target.Name, warnMsg))
			}
		}
	}

	return errs, warnings
}

// validateStrategy validates the execution strategy.
func (v *HibernatePlanValidator) validateStrategy(plan *hibernatorv1alpha1.HibernatePlan) (field.ErrorList, admission.Warnings) {
	var errs field.ErrorList
	var warnings admission.Warnings
	strategyPath := field.NewPath("spec", "execution", "strategy")

	strategy := plan.Spec.Execution.Strategy

	targetNames := make(map[string]bool)
	for _, t := range plan.Spec.Targets {
		targetNames[t.Name] = true
	}

	switch strategy.Type {
	case hibernatorv1alpha1.StrategyDAG:
		dagErrs, dagWarnings := v.validateDAG(plan, targetNames, strategyPath)
		errs = append(errs, dagErrs...)
		warnings = append(warnings, dagWarnings...)

	case hibernatorv1alpha1.StrategyStaged:
		stagedErrs := v.validateStaged(plan, targetNames, strategyPath)
		errs = append(errs, stagedErrs...)

	case hibernatorv1alpha1.StrategyParallel, hibernatorv1alpha1.StrategySequential:
		// No additional validation needed

	default:
		errs = append(errs, field.NotSupported(
			strategyPath.Child("type"),
			strategy.Type,
			[]string{
				string(hibernatorv1alpha1.StrategySequential),
				string(hibernatorv1alpha1.StrategyParallel),
				string(hibernatorv1alpha1.StrategyDAG),
				string(hibernatorv1alpha1.StrategyStaged),
			},
		))
	}

	return errs, warnings
}

// validateDAG validates DAG dependencies and checks for cycles.
func (v *HibernatePlanValidator) validateDAG(plan *hibernatorv1alpha1.HibernatePlan, targetNames map[string]bool, strategyPath *field.Path) (field.ErrorList, admission.Warnings) {
	var errs field.ErrorList
	var warnings admission.Warnings
	depsPath := strategyPath.Child("dependencies")

	graph := make(map[string][]string)
	inDegree := make(map[string]int)

	for name := range targetNames {
		graph[name] = []string{}
		inDegree[name] = 0
	}

	for i, dep := range plan.Spec.Execution.Strategy.Dependencies {
		if !targetNames[dep.From] {
			errs = append(errs, field.Invalid(
				depsPath.Index(i).Child("from"),
				dep.From,
				fmt.Sprintf("target %q not found in spec.targets", dep.From),
			))
			continue
		}

		if !targetNames[dep.To] {
			errs = append(errs, field.Invalid(
				depsPath.Index(i).Child("to"),
				dep.To,
				fmt.Sprintf("target %q not found in spec.targets", dep.To),
			))
			continue
		}

		if dep.From == dep.To {
			errs = append(errs, field.Invalid(
				depsPath.Index(i),
				dep,
				"target cannot depend on itself",
			))
			continue
		}

		graph[dep.To] = append(graph[dep.To], dep.From)
		inDegree[dep.From]++
	}

	if len(errs) == 0 {
		queue := []string{}
		for name, degree := range inDegree {
			if degree == 0 {
				queue = append(queue, name)
			}
		}

		visited := 0
		for len(queue) > 0 {
			node := queue[0]
			queue = queue[1:]
			visited++

			for _, neighbor := range graph[node] {
				inDegree[neighbor]--
				if inDegree[neighbor] == 0 {
					queue = append(queue, neighbor)
				}
			}
		}

		if visited != len(targetNames) {
			errs = append(errs, field.Invalid(
				depsPath,
				plan.Spec.Execution.Strategy.Dependencies,
				"dependency graph contains a cycle",
			))
		}
	}

	referencedTargets := make(map[string]bool)
	for _, dep := range plan.Spec.Execution.Strategy.Dependencies {
		referencedTargets[dep.From] = true
		referencedTargets[dep.To] = true
	}

	for name := range targetNames {
		if !referencedTargets[name] && len(plan.Spec.Execution.Strategy.Dependencies) > 0 {
			warnings = append(warnings, fmt.Sprintf(
				"target %q has no dependencies defined; it will run at the first stage", name,
			))
		}
	}

	return errs, warnings
}

// validateStaged validates staged execution configuration.
func (v *HibernatePlanValidator) validateStaged(plan *hibernatorv1alpha1.HibernatePlan, targetNames map[string]bool, strategyPath *field.Path) field.ErrorList {
	var errs field.ErrorList
	stagesPath := strategyPath.Child("stages")

	if len(plan.Spec.Execution.Strategy.Stages) == 0 {
		errs = append(errs, field.Required(
			stagesPath,
			"at least one stage is required for Staged strategy",
		))
		return errs
	}

	assignedTargets := make(map[string]string)
	stageNames := make(map[string]int)

	for i, stage := range plan.Spec.Execution.Strategy.Stages {
		if prevIdx, ok := stageNames[stage.Name]; ok {
			errs = append(errs, field.Duplicate(
				stagesPath.Index(i).Child("name"),
				fmt.Sprintf("stage name %q already defined at index %d", stage.Name, prevIdx),
			))
		}
		stageNames[stage.Name] = i

		if len(stage.Targets) == 0 {
			errs = append(errs, field.Required(
				stagesPath.Index(i).Child("targets"),
				"stage must have at least one target",
			))
		}

		for j, targetName := range stage.Targets {
			if !targetNames[targetName] {
				errs = append(errs, field.Invalid(
					stagesPath.Index(i).Child("targets").Index(j),
					targetName,
					fmt.Sprintf("target %q not found in spec.targets", targetName),
				))
			}

			if prevStage, ok := assignedTargets[targetName]; ok {
				errs = append(errs, field.Duplicate(
					stagesPath.Index(i).Child("targets").Index(j),
					fmt.Sprintf("target %q already assigned to stage %q", targetName, prevStage),
				))
			}
			assignedTargets[targetName] = stage.Name
		}
	}

	for name := range targetNames {
		if _, ok := assignedTargets[name]; !ok {
			errs = append(errs, field.Invalid(
				stagesPath,
				name,
				fmt.Sprintf("target %q is not assigned to any stage", name),
			))
		}
	}

	return errs
}

// parseTimeValues parses HH:MM format and returns (hour, minute, error).
func parseTimeValues(timeStr string) (int, int, error) {
	parts := strings.Split(timeStr, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid time format")
	}

	hour := 0
	min := 0
	if n, err := fmt.Sscanf(timeStr, "%d:%d", &hour, &min); err != nil || n != 2 {
		return 0, 0, fmt.Errorf("failed to parse time")
	}

	return hour, min, nil
}
