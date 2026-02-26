/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package printers

import (
	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

// ScheduleOutput is a wrapper for printing schedule evaluation results
type ScheduleOutput struct {
	Plan       hibernatorv1alpha1.HibernatePlan
	Result     interface{} // EvaluationResult
	Exceptions []hibernatorv1alpha1.ExceptionReference
	Events     []ScheduleEvent
}

// StatusOutput is a wrapper for printing plan status
type StatusOutput struct {
	Plan hibernatorv1alpha1.HibernatePlan
}

// RestoreDetailOutput is a wrapper for printing restore resource details
type RestoreDetailOutput struct {
	Plan       string
	Namespace  string
	TargetData interface{} // restore.Data
	ResourceID string
	State      map[string]any
}

// RestoreResourcesOutput is a wrapper for listing resources in restore point
type RestoreResourcesOutput struct {
	ConfigMap corev1.ConfigMap
	Target    string
}
