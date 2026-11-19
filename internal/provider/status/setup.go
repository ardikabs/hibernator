/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package status

import (
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

// kindedRunnable is an UpdateManager of any resource type.
type kindedRunnable interface {
	manager.Runnable
	Kind() string
}

// SetupWithManager creates one UpdateManager per resource that records status,
// adds them to mgr, and returns the writers that reconcilers and processors use
// to send updates.
func SetupWithManager(mgr ctrl.Manager, log logr.Logger) (*ControllerStatuses, error) {
	log = log.WithName("status")

	plans := newManager[*hibernatorv1alpha1.HibernatePlan](mgr, log)
	exceptions := newManager[*hibernatorv1alpha1.ScheduleException](mgr, log)
	notifications := newManager[*hibernatorv1alpha1.HibernateNotification](mgr, log)
	cloudProviders := newManager[*hibernatorv1alpha1.CloudProvider](mgr, log)
	k8sClusters := newManager[*hibernatorv1alpha1.K8SCluster](mgr, log)

	for _, m := range []kindedRunnable{plans, exceptions, notifications, cloudProviders, k8sClusters} {
		if err := mgr.Add(m); err != nil {
			return nil, fmt.Errorf("unable to add %s status manager: %w", m.Kind(), err)
		}
		log.Info("registered status manager", "kind", m.Kind())
	}

	return &ControllerStatuses{
		PlanStatuses:          plans.Writer(),
		ExceptionStatuses:     exceptions.Writer(),
		NotificationStatuses:  notifications.Writer(),
		CloudProviderStatuses: cloudProviders.Writer(),
		K8SClusterStatuses:    k8sClusters.Writer(),
	}, nil
}

// newManager creates the UpdateManager for T, with its logger named after T's kind.
func newManager[T client.Object](mgr ctrl.Manager, log logr.Logger) *UpdateManager[T] {
	var zero T
	name := strings.ToLower(hibernatorv1alpha1.KindOf(zero))
	return NewUpdateManager[T](log.WithName(name), mgr.GetClient(), mgr.GetAPIReader())
}
