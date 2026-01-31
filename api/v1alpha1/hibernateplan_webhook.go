/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package v1alpha1

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/ardikabs/hibernator/pkg/executorparams"
)

var hibernateplanlog = logf.Log.WithName("hibernateplan-resource")

// SetupWebhookWithManager sets up the webhook with the manager.
func (r *HibernatePlan) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		WithValidator(r).
		Complete()
}

// +kubebuilder:webhook:path=/validate-hibernator-ardikabs-com-v1alpha1-hibernateplan,mutating=false,failurePolicy=fail,sideEffects=None,groups=hibernator.ardikabs.com,resources=hibernateplans,verbs=create;update,versions=v1alpha1,name=vhibernateplan.kb.io,admissionReviewVersions=v1

var _ webhook.CustomValidator = &HibernatePlan{}

// ValidateCreate implements webhook.CustomValidator.
func (r *HibernatePlan) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	plan, ok := obj.(*HibernatePlan)
	if !ok {
		return nil, fmt.Errorf("expected HibernatePlan but got %T", obj)
	}
	hibernateplanlog.Info("validate create", "name", plan.Name)
	return r.validate(plan)
}

// ValidateUpdate implements webhook.CustomValidator.
func (r *HibernatePlan) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	plan, ok := newObj.(*HibernatePlan)
	if !ok {
		return nil, fmt.Errorf("expected HibernatePlan but got %T", newObj)
	}
	hibernateplanlog.Info("validate update", "name", plan.Name)
	return r.validate(plan)
}

// ValidateDelete implements webhook.CustomValidator.
func (r *HibernatePlan) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	// No validation on delete
	return nil, nil
}

// validate performs validation on the HibernatePlan.
func (r *HibernatePlan) validate(plan *HibernatePlan) (admission.Warnings, error) {
	var allErrs field.ErrorList
	var warnings admission.Warnings

	// Validate schedule
	scheduleErrs := r.validateSchedule(plan)
	allErrs = append(allErrs, scheduleErrs...)

	// Validate targets
	targetErrs, targetWarnings := r.validateTargets(plan)
	allErrs = append(allErrs, targetErrs...)
	warnings = append(warnings, targetWarnings...)

	// Validate execution strategy
	strategyErrs, strategyWarnings := r.validateStrategy(plan)
	allErrs = append(allErrs, strategyErrs...)
	warnings = append(warnings, strategyWarnings...)

	if len(allErrs) > 0 {
		return warnings, allErrs.ToAggregate()
	}
	return warnings, nil
}

// validateSchedule validates the schedule configuration.
func (r *HibernatePlan) validateSchedule(plan *HibernatePlan) field.ErrorList {
	var errs field.ErrorList
	schedulePath := field.NewPath("spec", "schedule")

	// Validate timezone
	if plan.Spec.Schedule.Timezone == "" {
		errs = append(errs, field.Required(
			schedulePath.Child("timezone"),
			"timezone is required",
		))
	}

	// Validate offHours
	if len(plan.Spec.Schedule.OffHours) == 0 {
		errs = append(errs, field.Required(
			schedulePath.Child("offHours"),
			"at least one off-hour window is required",
		))
	}

	// HH:MM time format regex
	timeRegex := regexp.MustCompile(`^([0-1]?[0-9]|2[0-3]):[0-5][0-9]$`)
	validDays := map[string]bool{
		"MON": true, "TUE": true, "WED": true, "THU": true,
		"FRI": true, "SAT": true, "SUN": true,
	}

	for i, window := range plan.Spec.Schedule.OffHours {
		windowPath := schedulePath.Child("offHours").Index(i)

		// Validate start time
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

		// Validate end time
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

		// Validate daysOfWeek
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

	return errs
}

// validateTargets validates target configuration.
func (r *HibernatePlan) validateTargets(plan *HibernatePlan) (field.ErrorList, admission.Warnings) {
	var errs field.ErrorList
	var warnings admission.Warnings
	targetsPath := field.NewPath("spec", "targets")

	// Check for duplicate target names
	seen := make(map[string]int)
	for i, target := range plan.Spec.Targets {
		if prevIdx, ok := seen[target.Name]; ok {
			errs = append(errs, field.Duplicate(
				targetsPath.Index(i).Child("name"),
				fmt.Sprintf("target name %q already defined at index %d", target.Name, prevIdx),
			))
		}
		seen[target.Name] = i

		// Validate connector reference
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

		// Validate target type
		validTypes := []string{"eks", "rds", "ec2", "asg", "karpenter", "gke", "cloudsql", "workloadscaler"}
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

		// Validate target parameters using executor-specific validators
		var paramsRaw []byte
		if target.Parameters != nil {
			paramsRaw = target.Parameters.Raw
		}
		if result := executorparams.ValidateParams(target.Type, paramsRaw); result != nil {
			paramPath := targetsPath.Index(i).Child("parameters")

			// Add errors
			for _, errMsg := range result.Errors {
				errs = append(errs, field.Invalid(paramPath, target.Parameters, errMsg))
			}

			// Add warnings (unknown fields, etc.)
			for _, warnMsg := range result.Warnings {
				warnings = append(warnings, fmt.Sprintf("target %q: %s", target.Name, warnMsg))
			}
		}
	}

	return errs, warnings
}

// validateStrategy validates the execution strategy.
func (r *HibernatePlan) validateStrategy(plan *HibernatePlan) (field.ErrorList, admission.Warnings) {
	var errs field.ErrorList
	var warnings admission.Warnings
	strategyPath := field.NewPath("spec", "execution", "strategy")

	strategy := plan.Spec.Execution.Strategy

	// Build target name set for reference validation
	targetNames := make(map[string]bool)
	for _, t := range plan.Spec.Targets {
		targetNames[t.Name] = true
	}

	switch strategy.Type {
	case StrategyDAG:
		// Validate DAG dependencies
		dagErrs, dagWarnings := r.validateDAG(plan, targetNames, strategyPath)
		errs = append(errs, dagErrs...)
		warnings = append(warnings, dagWarnings...)

	case StrategyStaged:
		// Validate staged configuration
		stagedErrs := r.validateStaged(plan, targetNames, strategyPath)
		errs = append(errs, stagedErrs...)

	case StrategyParallel, StrategySequential:
		// No additional validation needed

	default:
		errs = append(errs, field.NotSupported(
			strategyPath.Child("type"),
			strategy.Type,
			[]string{string(StrategySequential), string(StrategyParallel), string(StrategyDAG), string(StrategyStaged)},
		))
	}

	return errs, warnings
}

// validateDAG validates DAG dependencies and checks for cycles.
func (r *HibernatePlan) validateDAG(plan *HibernatePlan, targetNames map[string]bool, strategyPath *field.Path) (field.ErrorList, admission.Warnings) {
	var errs field.ErrorList
	var warnings admission.Warnings
	depsPath := strategyPath.Child("dependencies")

	// Build adjacency list
	graph := make(map[string][]string)
	inDegree := make(map[string]int)

	// Initialize all targets
	for name := range targetNames {
		graph[name] = []string{}
		inDegree[name] = 0
	}

	// Process dependencies
	for i, dep := range plan.Spec.Execution.Strategy.Dependencies {
		// Validate 'from' exists
		if !targetNames[dep.From] {
			errs = append(errs, field.Invalid(
				depsPath.Index(i).Child("from"),
				dep.From,
				fmt.Sprintf("target %q not found in spec.targets", dep.From),
			))
			continue
		}

		// Validate 'to' exists
		if !targetNames[dep.To] {
			errs = append(errs, field.Invalid(
				depsPath.Index(i).Child("to"),
				dep.To,
				fmt.Sprintf("target %q not found in spec.targets", dep.To),
			))
			continue
		}

		// Self-dependency check
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

	// Cycle detection using Kahn's algorithm
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

	// Check for orphan targets (not in any dependency)
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
func (r *HibernatePlan) validateStaged(plan *HibernatePlan, targetNames map[string]bool, strategyPath *field.Path) field.ErrorList {
	var errs field.ErrorList
	stagesPath := strategyPath.Child("stages")

	if len(plan.Spec.Execution.Strategy.Stages) == 0 {
		errs = append(errs, field.Required(
			stagesPath,
			"at least one stage is required for Staged strategy",
		))
		return errs
	}

	// Track which targets are assigned to stages
	assignedTargets := make(map[string]string) // target -> stage name
	stageNames := make(map[string]int)         // stage name -> index

	for i, stage := range plan.Spec.Execution.Strategy.Stages {
		// Check duplicate stage names
		if prevIdx, ok := stageNames[stage.Name]; ok {
			errs = append(errs, field.Duplicate(
				stagesPath.Index(i).Child("name"),
				fmt.Sprintf("stage name %q already defined at index %d", stage.Name, prevIdx),
			))
		}
		stageNames[stage.Name] = i

		// Check stage has targets
		if len(stage.Targets) == 0 {
			errs = append(errs, field.Required(
				stagesPath.Index(i).Child("targets"),
				"stage must have at least one target",
			))
		}

		// Validate targets in stage
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

	// Check for unassigned targets
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
