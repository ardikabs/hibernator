/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package v1alpha1

import (
	"context"
	"fmt"
	"regexp"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var scheduleexceptionlog = logf.Log.WithName("scheduleexception-resource")

// scheduleExceptionValidator is a package-level variable that holds the client for validation.
// It is initialized by SetupWebhookWithManager.
var scheduleExceptionValidator client.Reader

// SetupWebhookWithManager sets up the webhook with the manager.
func (r *ScheduleException) SetupWebhookWithManager(mgr ctrl.Manager) error {
	scheduleExceptionValidator = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		WithValidator(r).
		Complete()
}

// +kubebuilder:webhook:path=/validate-hibernator-ardikabs-com-v1alpha1-scheduleexception,mutating=false,failurePolicy=fail,sideEffects=None,groups=hibernator.ardikabs.com,resources=scheduleexceptions,verbs=create;update,versions=v1alpha1,name=vscheduleexception.kb.io,admissionReviewVersions=v1

var _ webhook.CustomValidator = &ScheduleException{}

// ValidateCreate implements webhook.CustomValidator.
func (r *ScheduleException) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	exception, ok := obj.(*ScheduleException)
	if !ok {
		return nil, fmt.Errorf("expected ScheduleException but got %T", obj)
	}
	scheduleexceptionlog.Info("validate create", "name", exception.Name)
	return r.validate(ctx, exception)
}

// ValidateUpdate implements webhook.CustomValidator.
func (r *ScheduleException) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	exception, ok := newObj.(*ScheduleException)
	if !ok {
		return nil, fmt.Errorf("expected ScheduleException but got %T", newObj)
	}
	scheduleexceptionlog.Info("validate update", "name", exception.Name)
	return r.validate(ctx, exception)
}

// ValidateDelete implements webhook.CustomValidator.
func (r *ScheduleException) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	// No validation on delete
	return nil, nil
}

// validate performs validation on the ScheduleException.
func (r *ScheduleException) validate(ctx context.Context, exception *ScheduleException) (admission.Warnings, error) {
	var allErrs field.ErrorList

	// Validate planRef
	planErrs := r.validatePlanRef(ctx, exception)
	allErrs = append(allErrs, planErrs...)

	// Validate time range
	timeErrs := r.validateTimeRange(exception)
	allErrs = append(allErrs, timeErrs...)

	// Validate type-specific fields
	typeErrs := r.validateTypeSpecificFields(exception)
	allErrs = append(allErrs, typeErrs...)

	// Validate windows
	windowErrs := r.validateWindows(exception)
	allErrs = append(allErrs, windowErrs...)

	// Check for single active exception constraint
	activeErrs := r.validateSingleActiveException(ctx, exception)
	allErrs = append(allErrs, activeErrs...)

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			exception.GroupVersionKind().GroupKind(),
			exception.Name,
			allErrs,
		)
	}

	return nil, nil
}

// validatePlanRef validates the planRef field.
func (r *ScheduleException) validatePlanRef(ctx context.Context, exception *ScheduleException) field.ErrorList {
	var allErrs field.ErrorList
	planRefPath := field.NewPath("spec", "planRef")

	// Ensure planRef.name is not empty
	if exception.Spec.PlanRef.Name == "" {
		allErrs = append(allErrs, field.Required(planRefPath.Child("name"), "planRef.name must be specified"))
		return allErrs
	}

	// Determine target namespace (default to exception's namespace if not specified)
	targetNamespace := exception.Spec.PlanRef.Namespace
	if targetNamespace == "" {
		targetNamespace = exception.Namespace
	}

	// Enforce same-namespace constraint
	if targetNamespace != exception.Namespace {
		allErrs = append(allErrs, field.Invalid(
			planRefPath.Child("namespace"),
			targetNamespace,
			fmt.Sprintf("planRef must reference a HibernatePlan in the same namespace (%s)", exception.Namespace),
		))
		return allErrs
	}

	// Verify HibernatePlan exists
	plan := &HibernatePlan{}
	planKey := client.ObjectKey{
		Namespace: targetNamespace,
		Name:      exception.Spec.PlanRef.Name,
	}
	if err := scheduleExceptionValidator.Get(ctx, planKey, plan); err != nil {
		if apierrors.IsNotFound(err) {
			allErrs = append(allErrs, field.NotFound(
				planRefPath.Child("name"),
				exception.Spec.PlanRef.Name,
			))
		} else {
			allErrs = append(allErrs, field.InternalError(
				planRefPath.Child("name"),
				fmt.Errorf("failed to verify HibernatePlan existence: %w", err),
			))
		}
	}

	return allErrs
}

// validateTimeRange validates validFrom and validUntil.
func (r *ScheduleException) validateTimeRange(exception *ScheduleException) field.ErrorList {
	var allErrs field.ErrorList
	specPath := field.NewPath("spec")

	// Validate validFrom <= validUntil
	if !exception.Spec.ValidFrom.Before(&exception.Spec.ValidUntil) {
		allErrs = append(allErrs, field.Invalid(
			specPath.Child("validUntil"),
			exception.Spec.ValidUntil.Format(time.RFC3339),
			fmt.Sprintf("validUntil must be after validFrom (%s)", exception.Spec.ValidFrom.Format(time.RFC3339)),
		))
	}

	// Validate duration <= 90 days
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
func (r *ScheduleException) validateTypeSpecificFields(exception *ScheduleException) field.ErrorList {
	var allErrs field.ErrorList
	specPath := field.NewPath("spec")

	// LeadTime is only valid for suspend type
	if exception.Spec.LeadTime != "" && exception.Spec.Type != ExceptionSuspend {
		allErrs = append(allErrs, field.Invalid(
			specPath.Child("leadTime"),
			exception.Spec.LeadTime,
			fmt.Sprintf("leadTime is only valid when type is 'suspend' (current type: %s)", exception.Spec.Type),
		))
	}

	// Validate leadTime format if present
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
func (r *ScheduleException) validateWindows(exception *ScheduleException) field.ErrorList {
	var allErrs field.ErrorList
	windowsPath := field.NewPath("spec", "windows")

	// Ensure at least one window
	if len(exception.Spec.Windows) == 0 {
		allErrs = append(allErrs, field.Required(windowsPath, "at least one window must be specified"))
		return allErrs
	}

	// Validate each window
	timePattern := regexp.MustCompile(`^([0-1]?[0-9]|2[0-3]):[0-5][0-9]$`)
	validDays := map[string]bool{
		"MON": true, "TUE": true, "WED": true, "THU": true,
		"FRI": true, "SAT": true, "SUN": true,
	}

	for i, window := range exception.Spec.Windows {
		windowPath := windowsPath.Index(i)

		// Validate start time format
		if !timePattern.MatchString(window.Start) {
			allErrs = append(allErrs, field.Invalid(
				windowPath.Child("start"),
				window.Start,
				"must be in HH:MM format (e.g., '20:00')",
			))
		}

		// Validate end time format
		if !timePattern.MatchString(window.End) {
			allErrs = append(allErrs, field.Invalid(
				windowPath.Child("end"),
				window.End,
				"must be in HH:MM format (e.g., '06:00')",
			))
		}

		// Validate daysOfWeek
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

// validateSingleActiveException ensures only one active exception per plan.
func (r *ScheduleException) validateSingleActiveException(ctx context.Context, exception *ScheduleException) field.ErrorList {
	var allErrs field.ErrorList

	// Determine target namespace
	targetNamespace := exception.Spec.PlanRef.Namespace
	if targetNamespace == "" {
		targetNamespace = exception.Namespace
	}

	// Query for existing active exceptions for this plan
	exceptionList := &ScheduleExceptionList{}
	listOpts := []client.ListOption{
		client.InNamespace(targetNamespace),
		client.MatchingLabels{
			"hibernator.ardikabs.com/plan": exception.Spec.PlanRef.Name,
		},
	}

	if err := scheduleExceptionValidator.List(ctx, exceptionList, listOpts...); err != nil {
		// If we can't query, fail open with internal error
		allErrs = append(allErrs, field.InternalError(
			field.NewPath("spec", "planRef"),
			fmt.Errorf("failed to query existing exceptions: %w", err),
		))
		return allErrs
	}

	// Check for active exceptions (excluding this one if it's an update)
	for _, existing := range exceptionList.Items {
		// Skip self
		if existing.Namespace == exception.Namespace && existing.Name == exception.Name {
			continue
		}

		// Check if existing exception is active
		if existing.Status.State == ExceptionStateActive {
			allErrs = append(allErrs, field.Forbidden(
				field.NewPath("spec", "planRef", "name"),
				fmt.Sprintf("plan %s already has active exception %s (expires at %s)",
					exception.Spec.PlanRef.Name,
					existing.Name,
					existing.Spec.ValidUntil.Format(time.RFC3339),
				),
			))
			break // Only report first conflict
		}
	}

	return allErrs
}
