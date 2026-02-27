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

// CloudProviderValidator validates CloudProvider resources.
type CloudProviderValidator struct {
	log logr.Logger
}

// NewCloudProviderValidator creates a new CloudProviderValidator.
func NewCloudProviderValidator(log logr.Logger) *CloudProviderValidator {
	return &CloudProviderValidator{
		log: log.WithName("cloudprovider"),
	}
}

var _ admission.CustomValidator = &CloudProviderValidator{}

// ValidateCreate implements webhook.CustomValidator.
func (v *CloudProviderValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	cloudProvider, ok := obj.(*hibernatorv1alpha1.CloudProvider)
	if !ok {
		return nil, fmt.Errorf("expected CloudProvider but got %T", obj)
	}
	v.log.V(1).Info("validate create", "name", cloudProvider.Name)
	return v.validate(cloudProvider)
}

// ValidateUpdate implements webhook.CustomValidator.
func (v *CloudProviderValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	cloudProvider, ok := newObj.(*hibernatorv1alpha1.CloudProvider)
	if !ok {
		return nil, fmt.Errorf("expected CloudProvider but got %T", newObj)
	}
	v.log.V(1).Info("validate update", "name", cloudProvider.Name)
	return v.validate(cloudProvider)
}

// ValidateDelete implements webhook.CustomValidator.
func (v *CloudProviderValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// validate performs validation on the CloudProvider.
func (v *CloudProviderValidator) validate(cp *hibernatorv1alpha1.CloudProvider) (admission.Warnings, error) {
	var allErrs field.ErrorList

	if cp.Spec.Type == hibernatorv1alpha1.CloudProviderAWS {
		if cp.Spec.AWS == nil {
			allErrs = append(allErrs, field.Required(
				field.NewPath("spec", "aws"),
				"spec.aws is required when type is 'aws'",
			))
		} else {
			if cp.Spec.AWS.Auth.ServiceAccount == nil && cp.Spec.AWS.Auth.Static == nil {
				allErrs = append(allErrs, field.Required(
					field.NewPath("spec", "aws", "auth"),
					"at least one authentication method must be specified: spec.aws.auth.serviceAccount or spec.aws.auth.static",
				))
			}
		}
	}

	if len(allErrs) > 0 {
		return nil, allErrs.ToAggregate()
	}
	return nil, nil
}
