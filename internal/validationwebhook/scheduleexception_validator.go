/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package validationwebhook

import (
	"context"
	"fmt"
	"regexp"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
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
	return v.validate(ctx, exception)
}

// ValidateDelete implements webhook.CustomValidator.
func (v *ScheduleExceptionValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
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
