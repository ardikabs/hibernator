/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package validationwebhook

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/go-logr/logr"
)

// ScheduleExceptionValidator validates ScheduleException resources.
type ScheduleExceptionValidator struct {
	log    logr.Logger
	client client.Reader
}

// NewScheduleExceptionValidator creates a new ScheduleExceptionValidator with the given client.
func NewScheduleExceptionValidator(log logr.Logger, c client.Reader) *ScheduleExceptionValidator {
	return &ScheduleExceptionValidator{
		log:    log.WithName("scheduleexception"),
		client: c,
	}
}

var _ admission.CustomValidator = &ScheduleExceptionValidator{}

// ValidateCreate implements webhook.CustomValidator.
func (v *ScheduleExceptionValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	exception, ok := obj.(*hibernatorv1alpha1.ScheduleException)
	if !ok {
		return nil, fmt.Errorf("expected ScheduleException but got %T", obj)
	}
	v.log.V(1).Info("validate create", "name", exception.Name)
	return v.validate(ctx, exception)
}

// ValidateUpdate implements webhook.CustomValidator.
func (v *ScheduleExceptionValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	exception, ok := newObj.(*hibernatorv1alpha1.ScheduleException)
	if !ok {
		return nil, fmt.Errorf("expected ScheduleException but got %T", newObj)
	}
	v.log.V(1).Info("validate update", "name", exception.Name)

	// Check if the update modifies override fields while the plan is mid-cycle.
	oldExc, ok := oldObj.(*hibernatorv1alpha1.ScheduleException)
	if ok && v.overrideFieldsChanged(oldExc, exception) {
		if err := v.checkMidCycleBlock(ctx, exception); err != nil {
			return nil, err
		}
	}

	return v.validate(ctx, exception)
}

// ValidateDelete implements webhook.CustomValidator.
func (v *ScheduleExceptionValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	exception, ok := obj.(*hibernatorv1alpha1.ScheduleException)
	if !ok {
		return nil, fmt.Errorf("expected ScheduleException but got %T", obj)
	}
	if err := v.checkMidCycleBlock(ctx, exception); err != nil {
		return nil, err
	}
	return nil, nil
}

// overrideFieldsChanged returns true if targetOverrides or executionOverride changed.
func (v *ScheduleExceptionValidator) overrideFieldsChanged(old, new *hibernatorv1alpha1.ScheduleException) bool {
	if len(old.Spec.TargetOverrides) != len(new.Spec.TargetOverrides) {
		return true
	}
	for i := range old.Spec.TargetOverrides {
		if old.Spec.TargetOverrides[i].TargetName != new.Spec.TargetOverrides[i].TargetName ||
			old.Spec.TargetOverrides[i].Disabled != new.Spec.TargetOverrides[i].Disabled ||
			!parametersEqual(old.Spec.TargetOverrides[i].Parameters, new.Spec.TargetOverrides[i].Parameters) {
			return true
		}
	}
	if (old.Spec.ExecutionOverride == nil) != (new.Spec.ExecutionOverride == nil) {
		return true
	}
	if old.Spec.ExecutionOverride != nil && new.Spec.ExecutionOverride != nil {
		if !executionStrategyEqual(old.Spec.ExecutionOverride.Strategy, new.Spec.ExecutionOverride.Strategy) ||
			!behaviorEqual(old.Spec.ExecutionOverride.Behavior, new.Spec.ExecutionOverride.Behavior) {
			return true
		}
	}
	return false
}

// parametersEqual compares two Parameters pointers for equality.
func parametersEqual(a, b *hibernatorv1alpha1.Parameters) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return string(a.Raw) == string(b.Raw)
}

// executionStrategyEqual compares two ExecutionStrategy pointers for equality.
func executionStrategyEqual(a, b *hibernatorv1alpha1.ExecutionStrategy) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	if a.Type != b.Type || !ptrEqual(a.MaxConcurrency, b.MaxConcurrency) {
		return false
	}
	if !slices.EqualFunc(a.Dependencies, b.Dependencies, func(d1, d2 hibernatorv1alpha1.Dependency) bool {
		return d1.From == d2.From && d1.To == d2.To
	}) {
		return false
	}
	return slices.EqualFunc(a.Stages, b.Stages, func(s1, s2 hibernatorv1alpha1.Stage) bool {
		if s1.Name != s2.Name || s1.Parallel != s2.Parallel || !ptrEqual(s1.MaxConcurrency, s2.MaxConcurrency) {
			return false
		}
		return slices.Equal(s1.Targets, s2.Targets)
	})
}

// behaviorEqual compares two Behavior pointers for equality.
func behaviorEqual(a, b *hibernatorv1alpha1.Behavior) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return a.Mode == b.Mode && a.FailFast == b.FailFast && ptrEqual(a.Retries, b.Retries)
}

// ptrEqual compares two *int32 pointers for equality.
func ptrEqual(a, b *int32) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return *a == *b
}

// checkMidCycleBlock returns an error if the referenced plan is mid-cycle with this exception.
func (v *ScheduleExceptionValidator) checkMidCycleBlock(ctx context.Context, exception *hibernatorv1alpha1.ScheduleException) error {
	planKey := client.ObjectKey{
		Name:      exception.Spec.PlanRef.Name,
		Namespace: exception.Namespace,
	}
	plan := new(hibernatorv1alpha1.HibernatePlan)
	if err := v.client.Get(ctx, planKey, plan); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to fetch referenced plan %s for mid-cycle check: %w", planKey, err)
	}
	if plan.Status.Phase == hibernatorv1alpha1.PhaseHibernating || plan.Status.Phase == hibernatorv1alpha1.PhaseWakingUp {
		if plan.Status.AppliedExceptionOverride == exception.Name {
			return fmt.Errorf("cannot modify or delete ScheduleException %s/%s while plan %s is mid-cycle (%s) with this exception's override",
				exception.Namespace, exception.Name, planKey.Name, plan.Status.Phase)
		}
	}
	return nil
}

// validate performs validation on the ScheduleException.
func (v *ScheduleExceptionValidator) validate(ctx context.Context, exception *hibernatorv1alpha1.ScheduleException) (admission.Warnings, error) {
	if !exception.DeletionTimestamp.IsZero() {
		return nil, nil
	}

	var (
		allErrs  field.ErrorList
		warnings admission.Warnings
	)

	planWarnings, planErrs := v.validatePlanRef(ctx, exception)
	warnings = append(warnings, planWarnings...)
	allErrs = append(allErrs, planErrs...)

	timeErrs := v.validateTimeRange(exception)
	allErrs = append(allErrs, timeErrs...)

	typeErrs := v.validateTypeSpecificFields(exception)
	allErrs = append(allErrs, typeErrs...)

	windowErrs := v.validateWindows(exception)
	allErrs = append(allErrs, windowErrs...)

	activeErrs := v.validateNoOverlappingExceptions(ctx, exception)
	allErrs = append(allErrs, activeErrs...)

	overrideErrs := v.validateExecutionOverrides(ctx, exception)
	allErrs = append(allErrs, overrideErrs...)

	if len(allErrs) > 0 {
		return warnings, apierrors.NewInvalid(
			exception.GroupVersionKind().GroupKind(),
			exception.Name,
			allErrs,
		)
	}

	return warnings, nil
}

// validatePlanRef validates the planRef field.
// A missing HibernatePlan is reported as a warning (the exception is still
// created but won't be picked up until a matching plan exists).
func (v *ScheduleExceptionValidator) validatePlanRef(ctx context.Context, exception *hibernatorv1alpha1.ScheduleException) (admission.Warnings, field.ErrorList) {
	var allErrs field.ErrorList
	planRefPath := field.NewPath("spec", "planRef")

	if exception.Spec.PlanRef.Name == "" {
		allErrs = append(allErrs, field.Required(planRefPath.Child("name"), "planRef.name must be specified"))
		return nil, allErrs
	}

	targetNamespace := exception.Spec.PlanRef.Namespace
	if targetNamespace == "" {
		targetNamespace = exception.Namespace
	}

	if targetNamespace != exception.Namespace {
		allErrs = append(allErrs, field.Invalid(
			planRefPath.Child("namespace"),
			targetNamespace,
			fmt.Sprintf("planRef must reference a HibernatePlan in the same namespace (%s)", exception.Namespace),
		))
		return nil, allErrs
	}

	plan := &hibernatorv1alpha1.HibernatePlan{}
	planKey := client.ObjectKey{
		Namespace: targetNamespace,
		Name:      exception.Spec.PlanRef.Name,
	}
	if err := v.client.Get(ctx, planKey, plan); err != nil {
		if apierrors.IsNotFound(err) {
			return admission.Warnings{
				fmt.Sprintf("HibernatePlan %q not found in namespace %q; this exception will have no effect until the plan is created",
					exception.Spec.PlanRef.Name, targetNamespace),
			}, nil
		}

		allErrs = append(allErrs, field.InternalError(
			planRefPath.Child("name"),
			fmt.Errorf("failed to verify HibernatePlan existence: %w", err),
		))
	}

	return nil, allErrs
}

// validateTimeRange validates validFrom and validUntil.
func (v *ScheduleExceptionValidator) validateTimeRange(exception *hibernatorv1alpha1.ScheduleException) field.ErrorList {
	var allErrs field.ErrorList
	specPath := field.NewPath("spec")

	if !exception.Spec.ValidFrom.Before(&exception.Spec.ValidUntil) {
		allErrs = append(allErrs, field.Invalid(
			specPath.Child("validUntil"),
			exception.Spec.ValidUntil.Format(time.RFC3339),
			fmt.Sprintf("validUntil must be after validFrom (%s)", exception.Spec.ValidFrom.Format(time.RFC3339)),
		))
	}

	duration := exception.Spec.ValidUntil.Sub(exception.Spec.ValidFrom.Time)
	maxDuration := 90 * 24 * time.Hour
	if duration > maxDuration {
		allErrs = append(allErrs, field.Invalid(
			specPath.Child("validUntil"),
			exception.Spec.ValidUntil.Format(time.RFC3339),
			fmt.Sprintf("exception duration (%v) exceeds maximum of 90 days", duration),
		))
	}

	return allErrs
}

// validateTypeSpecificFields validates fields specific to exception type.
func (v *ScheduleExceptionValidator) validateTypeSpecificFields(exception *hibernatorv1alpha1.ScheduleException) field.ErrorList {
	var allErrs field.ErrorList
	specPath := field.NewPath("spec")

	if exception.Spec.LeadTime != "" && exception.Spec.Type != hibernatorv1alpha1.ExceptionSuspend {
		allErrs = append(allErrs, field.Invalid(
			specPath.Child("leadTime"),
			exception.Spec.LeadTime,
			fmt.Sprintf("leadTime is only valid when type is 'suspend' (current type: %s)", exception.Spec.Type),
		))
	}

	if exception.Spec.LeadTime != "" {
		if _, err := time.ParseDuration(exception.Spec.LeadTime); err != nil {
			allErrs = append(allErrs, field.Invalid(
				specPath.Child("leadTime"),
				exception.Spec.LeadTime,
				fmt.Sprintf("invalid duration format: %v", err),
			))
		}
	}

	return allErrs
}

// validateWindows validates the time windows.
func (v *ScheduleExceptionValidator) validateWindows(exception *hibernatorv1alpha1.ScheduleException) field.ErrorList {
	var allErrs field.ErrorList
	windowsPath := field.NewPath("spec", "windows")

	if len(exception.Spec.Windows) == 0 {
		allErrs = append(allErrs, field.Required(windowsPath, "at least one window must be specified"))
		return allErrs
	}

	timePattern := regexp.MustCompile(`^([0-1]?[0-9]|2[0-3]):[0-5][0-9]$`)
	validDays := map[string]bool{
		"MON": true, "TUE": true, "WED": true, "THU": true,
		"FRI": true, "SAT": true, "SUN": true,
	}

	for i, window := range exception.Spec.Windows {
		windowPath := windowsPath.Index(i)

		if !timePattern.MatchString(window.Start) {
			allErrs = append(allErrs, field.Invalid(
				windowPath.Child("start"),
				window.Start,
				"must be in HH:MM format (e.g., '20:00')",
			))
		}

		if !timePattern.MatchString(window.End) {
			allErrs = append(allErrs, field.Invalid(
				windowPath.Child("end"),
				window.End,
				"must be in HH:MM format (e.g., '06:00')",
			))
		}

		if len(window.DaysOfWeek) == 0 {
			allErrs = append(allErrs, field.Required(
				windowPath.Child("daysOfWeek"),
				"at least one day must be specified",
			))
		}

		for j, day := range window.DaysOfWeek {
			if !validDays[day] {
				allErrs = append(allErrs, field.Invalid(
					windowPath.Child("daysOfWeek").Index(j),
					day,
					"must be one of: MON, TUE, WED, THU, FRI, SAT, SUN",
				))
			}
		}
	}

	return allErrs
}

// validateExecutionOverrides validates the execution override fields.
// It rejects overrides on suspend exceptions, validates target existence,
// and validates parameters using executor-specific validators.
func (v *ScheduleExceptionValidator) validateExecutionOverrides(ctx context.Context, exception *hibernatorv1alpha1.ScheduleException) field.ErrorList {
	var allErrs field.ErrorList
	specPath := field.NewPath("spec")

	// Rule 1: Reject overrides on suspend exceptions
	if exception.Spec.Type == hibernatorv1alpha1.ExceptionSuspend {
		if len(exception.Spec.TargetOverrides) > 0 {
			allErrs = append(allErrs, field.Forbidden(
				specPath.Child("targetOverrides"),
				"targetOverrides are not allowed for 'suspend' type exceptions",
			))
		}
		if exception.Spec.ExecutionOverride != nil {
			allErrs = append(allErrs, field.Forbidden(
				specPath.Child("executionOverride"),
				"executionOverride is not allowed for 'suspend' type exceptions",
			))
		}
		return allErrs
	}

	// Rule 2: Validate targetOverrides exist in the referenced plan
	if len(exception.Spec.TargetOverrides) > 0 {
		// Fetch the referenced plan
		targetNamespace := exception.Spec.PlanRef.Namespace
		if targetNamespace == "" {
			targetNamespace = exception.Namespace
		}

		plan := &hibernatorv1alpha1.HibernatePlan{}
		planKey := client.ObjectKey{
			Namespace: targetNamespace,
			Name:      exception.Spec.PlanRef.Name,
		}

		if err := v.client.Get(ctx, planKey, plan); err != nil {
			if !apierrors.IsNotFound(err) {
				allErrs = append(allErrs, field.InternalError(
					specPath.Child("planRef"),
					fmt.Errorf("failed to verify HibernatePlan for targetOverrides: %w", err),
				))
			}
			// If plan is not found, we can't validate targetOverrides - let validatePlanRef handle the warning
		} else {
			// Build target name map
			targetMap := make(map[string]*hibernatorv1alpha1.Target)
			for i := range plan.Spec.Targets {
				targetMap[plan.Spec.Targets[i].Name] = &plan.Spec.Targets[i]
			}

			for i, override := range exception.Spec.TargetOverrides {
				overridePath := specPath.Child("targetOverrides").Index(i)

				// Validate targetName exists
				target, ok := targetMap[override.TargetName]
				if !ok {
					allErrs = append(allErrs, field.NotFound(
						overridePath.Child("targetName"),
						override.TargetName,
					))
					continue
				}

				// Validate parameters using executor-specific validators
				if override.Parameters != nil && len(override.Parameters.Raw) > 0 {
					result := executorparams.ValidateParams(target.Type, override.Parameters.Raw)
					if result != nil && result.HasErrors() {
						for _, err := range result.Errors {
							allErrs = append(allErrs, field.Invalid(
								overridePath.Child("parameters"),
								string(override.Parameters.Raw),
								err,
							))
						}
					}
				}

				// Validate disabled doesn't break DAG dependencies
				if override.Disabled {
					if hasDependencyOn(plan, override.TargetName) {
						allErrs = append(allErrs, field.Invalid(
							overridePath.Child("disabled"),
							true,
							fmt.Sprintf("cannot disable target %q: other targets have dependencies on it", override.TargetName),
						))
					}
				}
			}
		}
	}

	// Rule 3: Validate executionOverride fields
	if exception.Spec.ExecutionOverride != nil {
		override := exception.Spec.ExecutionOverride

		// Validate strategy type
		if override.Strategy != nil {
			strategyPath := specPath.Child("executionOverride", "strategy")
			validStrategyTypes := map[string]bool{
				string(hibernatorv1alpha1.StrategySequential): true,
				string(hibernatorv1alpha1.StrategyParallel):    true,
				string(hibernatorv1alpha1.StrategyDAG):        true,
				string(hibernatorv1alpha1.StrategyStaged):     true,
			}
			if !validStrategyTypes[string(override.Strategy.Type)] {
				allErrs = append(allErrs, field.NotSupported(
					strategyPath.Child("type"),
					override.Strategy.Type,
					[]string{"Sequential", "Parallel", "DAG", "Staged"},
				))
			}
		}
	}

	// Rule 4: Only one active exception may have execution overrides per plan
	if len(exception.Spec.TargetOverrides) > 0 || exception.Spec.ExecutionOverride != nil {
		targetNamespace := exception.Spec.PlanRef.Namespace
		if targetNamespace == "" {
			targetNamespace = exception.Namespace
		}

		exceptionList := &hibernatorv1alpha1.ScheduleExceptionList{}
		listOpts := []client.ListOption{
			client.InNamespace(targetNamespace),
			client.MatchingLabels{
				wellknown.LabelPlan: exception.Spec.PlanRef.Name,
			},
		}

		if err := v.client.List(ctx, exceptionList, listOpts...); err == nil {
			for _, existing := range exceptionList.Items {
				if existing.Namespace == exception.Namespace && existing.Name == exception.Name {
					continue
				}
				if existing.Status.State == hibernatorv1alpha1.ExceptionStateExpired ||
					existing.Status.State == hibernatorv1alpha1.ExceptionStateDetached {
					continue
				}
				if !existing.Spec.ValidFrom.Time.Before(exception.Spec.ValidUntil.Time) ||
					!exception.Spec.ValidFrom.Time.Before(existing.Spec.ValidUntil.Time) {
					continue // Disjoint validity periods
				}
				if len(existing.Spec.TargetOverrides) > 0 || existing.Spec.ExecutionOverride != nil {
					allErrs = append(allErrs, field.Forbidden(
						specPath,
						fmt.Sprintf(
							"only one active exception may have execution overrides per plan; existing exception %q already has overrides",
							existing.Name,
						),
					))
					break
				}
			}
		}
	}

	return allErrs
}

// hasDependencyOn checks if any target in the plan depends on the given target.
func hasDependencyOn(plan *hibernatorv1alpha1.HibernatePlan, targetName string) bool {
	if plan.Spec.Execution.Strategy.Type != hibernatorv1alpha1.StrategyDAG {
		return false
	}
	for _, dep := range plan.Spec.Execution.Strategy.Dependencies {
		if dep.From == targetName {
			return true
		}
	}
	return false
}

// validateNoOverlappingExceptions checks that the incoming exception does not
// conflict with existing Active or Pending exceptions for the same plan.
//
// Validation follows a two-tier approach:
//  1. Window collision — if validity periods overlap, check whether any pair of
//     schedule windows across the two exceptions collide (shared day + overlapping
//     time range). Non-colliding exceptions can always coexist.
//  2. Type pairing — when windows DO collide, only certain cross-type combinations
//     are allowed (extend+suspend, replace+extend, replace+suspend). Same-type
//     collisions are always rejected.
func (v *ScheduleExceptionValidator) validateNoOverlappingExceptions(ctx context.Context, exception *hibernatorv1alpha1.ScheduleException) field.ErrorList {
	var allErrs field.ErrorList

	targetNamespace := exception.Spec.PlanRef.Namespace
	if targetNamespace == "" {
		targetNamespace = exception.Namespace
	}

	exceptionList := &hibernatorv1alpha1.ScheduleExceptionList{}
	listOpts := []client.ListOption{
		client.InNamespace(targetNamespace),
		client.MatchingLabels{
			wellknown.LabelPlan: exception.Spec.PlanRef.Name,
		},
	}

	if err := v.client.List(ctx, exceptionList, listOpts...); err != nil {
		allErrs = append(allErrs, field.InternalError(
			field.NewPath("spec", "planRef"),
			fmt.Errorf("failed to query existing exceptions: %w", err),
		))
		return allErrs
	}

	for _, existing := range exceptionList.Items {
		if existing.Namespace == exception.Namespace && existing.Name == exception.Name {
			continue
		}

		if existing.Status.State == hibernatorv1alpha1.ExceptionStateExpired ||
			existing.Status.State == hibernatorv1alpha1.ExceptionStateDetached {
			continue
		}

		// Tier 0: validity period overlap check.
		s1 := exception.Spec.ValidFrom.Time
		e1 := exception.Spec.ValidUntil.Time
		s2 := existing.Spec.ValidFrom.Time
		e2 := existing.Spec.ValidUntil.Time

		if !s1.Before(e2) || !s2.Before(e1) {
			continue // Disjoint validity periods — always safe.
		}

		// Tier 1: window collision check.
		if !windowsCollide(exception.Spec.Windows, existing.Spec.Windows) {
			continue // Non-colliding windows — allowed regardless of type.
		}

		// Tier 2: windows DO collide — check type pairing.
		if exception.Spec.Type == existing.Spec.Type {
			allErrs = append(allErrs, field.Forbidden(
				field.NewPath("spec", "planRef", "name"),
				fmt.Sprintf(
					"colliding same-type %q exceptions cannot coexist; merge windows into a single exception (conflicts with %s exception %q)",
					exception.Spec.Type,
					existing.Status.State,
					existing.Name,
				),
			))
			break
		}

		if !isAllowedTypePair(exception.Spec.Type, existing.Spec.Type) {
			allErrs = append(allErrs, field.Forbidden(
				field.NewPath("spec", "planRef", "name"),
				fmt.Sprintf(
					"unsupported colliding exception type combination %q + %q (conflicts with %s exception %q)",
					exception.Spec.Type,
					existing.Spec.Type,
					existing.Status.State,
					existing.Name,
				),
			))
			break
		}

		// Allowed cross-type collision (e.g., extend+suspend) — intentional composition.
	}

	return allErrs
}
