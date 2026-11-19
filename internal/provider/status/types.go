/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package status

import hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"

// ControllerStatuses is the status bus between reconcilers, processors and the async status layer.
// Producers (Workers, state handlers) depend only on Updater[T] — they never
// see UpdateManager or any pool internals. Swap fields with any Updater[T]
// implementation (e.g. a call-recording test double) without touching producers.
type ControllerStatuses struct {
	// PlanStatuses accepts status mutations for HibernatePlan objects.
	PlanStatuses Updater[*hibernatorv1alpha1.HibernatePlan]

	// ExceptionStatuses accepts status mutations for ScheduleException objects.
	ExceptionStatuses Updater[*hibernatorv1alpha1.ScheduleException]

	// NotificationStatuses accepts status mutations for HibernateNotification objects.
	NotificationStatuses Updater[*hibernatorv1alpha1.HibernateNotification]

	// CloudProviderStatuses accepts status mutations for CloudProvider objects.
	CloudProviderStatuses Updater[*hibernatorv1alpha1.CloudProvider]

	// K8SClusterStatuses accepts status mutations for K8SCluster objects.
	K8SClusterStatuses Updater[*hibernatorv1alpha1.K8SCluster]
}
