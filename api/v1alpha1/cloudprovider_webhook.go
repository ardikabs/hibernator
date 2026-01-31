/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package v1alpha1

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// Ensure CloudProvider implements CustomValidator interface.
var _ admission.CustomValidator = &CloudProvider{}

// ValidateCreate implements webhook.CustomValidator.
func (r *CloudProvider) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	cloudProvider, ok := obj.(*CloudProvider)
	if !ok {
		return nil, fmt.Errorf("expected CloudProvider but got %T", obj)
	}

	return nil, r.validateCloudProvider(cloudProvider)
}

// ValidateUpdate implements webhook.CustomValidator.
func (r *CloudProvider) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	cloudProvider, ok := newObj.(*CloudProvider)
	if !ok {
		return nil, fmt.Errorf("expected CloudProvider but got %T", newObj)
	}

	return nil, r.validateCloudProvider(cloudProvider)
}

// ValidateDelete implements webhook.CustomValidator.
func (r *CloudProvider) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	// No validation needed for delete
	return nil, nil
}

// validateCloudProvider performs validation logic for CloudProvider.
func (r *CloudProvider) validateCloudProvider(cp *CloudProvider) error {
	if cp.Spec.Type == CloudProviderAWS {
		if cp.Spec.AWS == nil {
			return fmt.Errorf("spec.aws is required when type is 'aws'")
		}

		// Validate that at least one authentication method is specified
		if cp.Spec.AWS.Auth.ServiceAccount == nil && cp.Spec.AWS.Auth.Static == nil {
			return fmt.Errorf("at least one authentication method must be specified: spec.aws.auth.serviceAccount or spec.aws.auth.static")
		}
	}

	return nil
}
