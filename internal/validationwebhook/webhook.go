/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package validationwebhook

import (
	ctrl "sigs.k8s.io/controller-runtime"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/go-logr/logr"
)

// SetupWithManager registers all validation webhooks with the manager.
func SetupWithManager(mgr ctrl.Manager, log logr.Logger) error {
	// HibernatePlan webhook
	if err := ctrl.NewWebhookManagedBy(mgr).
		For(&hibernatorv1alpha1.HibernatePlan{}).
		WithValidator(NewHibernatePlanValidator(log)).
		Complete(); err != nil {
		return err
	}

	// ScheduleException webhook (requires client for cross-resource validation)
	if err := ctrl.NewWebhookManagedBy(mgr).
		For(&hibernatorv1alpha1.ScheduleException{}).
		WithValidator(NewScheduleExceptionValidator(log, mgr.GetClient())).
		Complete(); err != nil {
		return err
	}

	// CloudProvider webhook
	if err := ctrl.NewWebhookManagedBy(mgr).
		For(&hibernatorv1alpha1.CloudProvider{}).
		WithValidator(NewCloudProviderValidator(log)).
		Complete(); err != nil {
		return err
	}

	return nil
}
