/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package validationwebhook

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
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

var _ admission.CustomValidator = &HibernateNotificationValidator{}

// ValidateCreate implements webhook.CustomValidator.
func (v *HibernateNotificationValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	notif, ok := obj.(*hibernatorv1alpha1.HibernateNotification)
	if !ok {
		return nil, fmt.Errorf("expected HibernateNotification but got %T", obj)
	}
	v.log.V(1).Info("validate create", "name", notif.Name)
	return v.validate(notif)
}

// ValidateUpdate implements webhook.CustomValidator.
func (v *HibernateNotificationValidator) ValidateUpdate(_ context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	notif, ok := newObj.(*hibernatorv1alpha1.HibernateNotification)
	if !ok {
		return nil, fmt.Errorf("expected HibernateNotification but got %T", newObj)
	}
	v.log.V(1).Info("validate update", "name", notif.Name)
	return v.validate(notif)
}

// ValidateDelete implements webhook.CustomValidator.
func (v *HibernateNotificationValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// validate performs validation on the HibernateNotification.
func (v *HibernateNotificationValidator) validate(notif *hibernatorv1alpha1.HibernateNotification) (admission.Warnings, error) {
	var allErrs field.ErrorList

	allErrs = append(allErrs, v.validateSinkNameUniqueness(notif)...)

	if len(allErrs) > 0 {
		return nil, allErrs.ToAggregate()
	}
	return nil, nil
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
