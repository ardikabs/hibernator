/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package validationwebhook

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/go-logr/logr"
)

// HibernateNotificationValidator validates HibernateNotification resources.
type HibernateNotificationValidator struct {
	log logr.Logger
}

// NewHibernateNotificationValidator creates a new HibernateNotificationValidator.
func NewHibernateNotificationValidator(log logr.Logger) *HibernateNotificationValidator {
	return &HibernateNotificationValidator{
		log: log.WithName("hibernatenotification"),
	}
}

var _ admission.Validator[*hibernatorv1alpha1.HibernateNotification] = &HibernateNotificationValidator{}

// ValidateCreate implements admission.Validator.
func (v *HibernateNotificationValidator) ValidateCreate(_ context.Context, notif *hibernatorv1alpha1.HibernateNotification) (admission.Warnings, error) {
	v.log.V(1).Info("validate create", "name", notif.Name)
	return v.validate(notif)
}

// ValidateUpdate implements admission.Validator.
func (v *HibernateNotificationValidator) ValidateUpdate(_ context.Context, _, notif *hibernatorv1alpha1.HibernateNotification) (admission.Warnings, error) {
	v.log.V(1).Info("validate update", "name", notif.Name)
	return v.validate(notif)
}

// ValidateDelete implements admission.Validator.
func (v *HibernateNotificationValidator) ValidateDelete(_ context.Context, _ *hibernatorv1alpha1.HibernateNotification) (admission.Warnings, error) {
	return nil, nil
}

// validate performs validation on the HibernateNotification.
func (v *HibernateNotificationValidator) validate(notif *hibernatorv1alpha1.HibernateNotification) (admission.Warnings, error) {
	var allErrs field.ErrorList

	allErrs = append(allErrs, v.validateSelector(notif)...)
	allErrs = append(allErrs, v.validateSinkNameUniqueness(notif)...)

	if len(allErrs) > 0 {
		return nil, allErrs.ToAggregate()
	}
	return nil, nil
}

// validateSelector ensures spec.selector is a valid Kubernetes label selector.
func (v *HibernateNotificationValidator) validateSelector(notif *hibernatorv1alpha1.HibernateNotification) field.ErrorList {
	var errs field.ErrorList
	selectorPath := field.NewPath("spec", "selector")

	if _, err := metav1.LabelSelectorAsSelector(&notif.Spec.Selector); err != nil {
		errs = append(errs, field.Invalid(selectorPath, notif.Spec.Selector, err.Error()))
	}

	return errs
}

// validateSinkNameUniqueness ensures that all sink names within a notification are unique.
func (v *HibernateNotificationValidator) validateSinkNameUniqueness(notif *hibernatorv1alpha1.HibernateNotification) field.ErrorList {
	var errs field.ErrorList
	sinksPath := field.NewPath("spec", "sinks")

	seen := make(map[string]int, len(notif.Spec.Sinks))
	for i, s := range notif.Spec.Sinks {
		if prev, exists := seen[s.Name]; exists {
			errs = append(errs, field.Invalid(
				sinksPath.Index(i).Child("name"),
				s.Name,
				fmt.Sprintf("duplicate sink name (first defined at sinks[%d])", prev),
			))
		} else {
			seen[s.Name] = i
		}
	}

	return errs
}
