/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ExceptionType defines the type of schedule exception.
// +kubebuilder:validation:Enum=extend;suspend;replace
type ExceptionType string

const (
	// ExceptionExtend adds hibernation windows to the base schedule.
	ExceptionExtend ExceptionType = "extend"
	// ExceptionSuspend prevents hibernation during specified windows (carve-out).
	ExceptionSuspend ExceptionType = "suspend"
	// ExceptionReplace completely replaces the base schedule during the exception period.
	ExceptionReplace ExceptionType = "replace"
)

// ExceptionState represents the lifecycle state of an exception.
// +kubebuilder:validation:Enum=Active;Expired
type ExceptionState string

const (
	// ExceptionStateActive indicates the exception is currently active.
	ExceptionStateActive ExceptionState = "Active"
	// ExceptionStateExpired indicates the exception has passed its validUntil time.
	ExceptionStateExpired ExceptionState = "Expired"
)

// PlanReference references a HibernatePlan.
type PlanReference struct {
	// Name of the HibernatePlan.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace of the HibernatePlan.
	// If empty, defaults to the exception's namespace.
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`
}

// ScheduleExceptionSpec defines the desired state of ScheduleException.
type ScheduleExceptionSpec struct {
	// PlanRef references the HibernatePlan this exception applies to.
	// +kubebuilder:validation:Required
	PlanRef PlanReference `json:"planRef"`

	// ValidFrom is the start time of the exception period (RFC3339 format).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=date-time
	ValidFrom metav1.Time `json:"validFrom"`

	// ValidUntil is the end time of the exception period (RFC3339 format).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=date-time
	ValidUntil metav1.Time `json:"validUntil"`

	// Type specifies the exception type: extend, suspend, or replace.
	// +kubebuilder:validation:Required
	Type ExceptionType `json:"type"`

	// LeadTime specifies buffer period before suspension window.
	// Only valid when Type is "suspend".
	// Format: duration string (e.g., "30m", "1h", "3600s").
	// Prevents NEW hibernation starts within this buffer before suspension.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$`
	LeadTime string `json:"leadTime,omitempty"`

	// Windows defines the time windows for this exception.
	// Meaning depends on Type:
	// - extend: Additional hibernation windows (union with base schedule)
	// - suspend: Windows to prevent hibernation (carve-out from schedule)
	// - replace: Complete replacement schedule (ignore base schedule)
	// +kubebuilder:validation:MinItems=1
	Windows []OffHourWindow `json:"windows"`
}

// ScheduleExceptionStatus defines the observed state of ScheduleException.
type ScheduleExceptionStatus struct {
	// State is the current lifecycle state of the exception.
	// +kubebuilder:validation:Enum=Active;Expired
	State ExceptionState `json:"state,omitempty"`

	// AppliedAt is when the exception was first applied.
	// +kubebuilder:validation:Optional
	AppliedAt *metav1.Time `json:"appliedAt,omitempty"`

	// ExpiredAt is when the exception transitioned to Expired state.
	// +kubebuilder:validation:Optional
	ExpiredAt *metav1.Time `json:"expiredAt,omitempty"`

	// Message provides diagnostic information about the exception state.
	// +kubebuilder:validation:Optional
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=schedex
// +kubebuilder:printcolumn:name="Plan",type=string,JSONPath=`.spec.planRef.name`
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
// +kubebuilder:printcolumn:name="ValidFrom",type=date,JSONPath=`.spec.validFrom`
// +kubebuilder:printcolumn:name="ValidUntil",type=date,JSONPath=`.spec.validUntil`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ScheduleException is the Schema for the scheduleexceptions API.
type ScheduleException struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ScheduleExceptionSpec   `json:"spec,omitempty"`
	Status ScheduleExceptionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ScheduleExceptionList contains a list of ScheduleException.
type ScheduleExceptionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ScheduleException `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ScheduleException{}, &ScheduleExceptionList{})
}
