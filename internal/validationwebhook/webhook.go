/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package validationwebhook

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/go-logr/logr"
)

// WebhookPath is the single admission endpoint for all Hibernator resources.
const WebhookPath = "/validate"

// SetupWithManager registers a single multiplexing validation webhook that
// handles all Hibernator CRD types on one path. This avoids per-resource
// webhook entries in the ValidatingWebhookConfiguration.
func SetupWithManager(mgr ctrl.Manager, log logr.Logger) error {
	s := mgr.GetScheme()

	mux := &muxHandler{
		log:      log.WithName("mux-validator"),
		handlers: make(map[schema.GroupVersionKind]admission.Handler),
	}

	mux.handlers[hibernatorv1alpha1.GroupVersion.WithKind("HibernatePlan")] =
		admission.WithCustomValidator(s, &hibernatorv1alpha1.HibernatePlan{}, NewHibernatePlanValidator(log))

	mux.handlers[hibernatorv1alpha1.GroupVersion.WithKind("ScheduleException")] =
		admission.WithCustomValidator(s, &hibernatorv1alpha1.ScheduleException{}, NewScheduleExceptionValidator(log, mgr.GetClient()))

	mux.handlers[hibernatorv1alpha1.GroupVersion.WithKind("CloudProvider")] =
		admission.WithCustomValidator(s, &hibernatorv1alpha1.CloudProvider{}, NewCloudProviderValidator(log))

	mux.handlers[hibernatorv1alpha1.GroupVersion.WithKind("HibernateNotification")] =
		admission.WithCustomValidator(s, &hibernatorv1alpha1.HibernateNotification{}, NewHibernateNotificationValidator(log))

	mgr.GetWebhookServer().Register(WebhookPath, &webhook.Admission{Handler: mux})
	return nil
}

// muxHandler dispatches admission requests to per-resource validators based on
// the request's GroupVersionKind. controller-runtime decodes the body once;
// this handler only inspects the already-parsed request.
type muxHandler struct {
	log      logr.Logger
	handlers map[schema.GroupVersionKind]admission.Handler
}

var _ admission.Handler = &muxHandler{}

// Handle implements admission.Handler by routing to the registered per-kind handler.
func (m *muxHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	gvk := schema.GroupVersionKind{
		Group:   req.Kind.Group,
		Version: req.Kind.Version,
		Kind:    req.Kind.Kind,
	}

	handler, ok := m.handlers[gvk]
	if !ok {
		m.log.V(1).Info("no handler registered for kind", "gvk", gvk)
		return admission.Denied("no validation handler for " + gvk.String())
	}

	return handler.Handle(ctx, req)
}
