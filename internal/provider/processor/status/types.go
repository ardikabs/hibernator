/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package status

import hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"

// ControllerStatuses is the status bus between processors and the async status layer.
// Producers (Workers, state handlers) depend only on Updater[T] — they never
// see UpdateProcessor or any pool internals. Swap fields with any Updater[T]
// implementation (e.g. a call-recording test double) without touching producers.
type ControllerStatuses struct {
	// PlanStatuses accepts status mutations for HibernatePlan objects.
	PlanStatuses Updater[*hibernatorv1alpha1.HibernatePlan]

	// ExceptionStatuses accepts status mutations for ScheduleException objects.
	ExceptionStatuses Updater[*hibernatorv1alpha1.ScheduleException]

	// NotificationStatuses accepts status mutations for HibernateNotification objects.
	NotificationStatuses Updater[*hibernatorv1alpha1.HibernateNotification]
}
