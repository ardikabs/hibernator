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
// +kubebuilder:validation:Enum=Pending;Active;Expired;Detached
type ExceptionState string

const (
	// ExceptionStatePending indicates the exception is not yet active.
	ExceptionStatePending ExceptionState = "Pending"
	// ExceptionStateActive indicates the exception is currently active.
	ExceptionStateActive ExceptionState = "Active"
	// ExceptionStateExpired indicates the exception has passed its validUntil time.
	ExceptionStateExpired ExceptionState = "Expired"
	// ExceptionStateDetached indicates the referenced plan no longer exists.
	// The exception is still a valid resource but is not bound to any plan.
	// If a plan with the same name is re-created, the exception may transition
	// back to a time-based state (Pending, Active, or Expired).
	ExceptionStateDetached ExceptionState = "Detached"
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

// TargetOverride defines a per-target override for the exception window.
// The base target's parameters and execution strategy are fully replaced (not merged).
type TargetOverride struct {
	// TargetName is the name of the target in the referenced HibernatePlan.
	// +kubebuilder:validation:Required
	TargetName string `json:"targetName"`

	// Parameters is a full replacement of the target's base parameters.
	// When set, the executor receives these parameters instead of the base target's parameters.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +optional
	Parameters *Parameters `json:"parameters,omitempty"`

	// Disabled, when true, excludes the target from both shutdown and wakeup
	// for the entire exception window.
	// +kubebuilder:default=false
	// +optional
	Disabled bool `json:"disabled,omitempty"`
}

// ExecutionOverride defines a full replacement of the execution strategy
// and behavior for the exception window.
type ExecutionOverride struct {
	// Strategy is a full replacement of the plan's execution strategy.
	// If omitted, the base plan's strategy is used.
	// +optional
	Strategy *ExecutionStrategy `json:"strategy,omitempty"`

	// Behavior is a full replacement of the plan's execution behavior.
	// If omitted, the base plan's behavior is used.
	// +optional
	Behavior *Behavior `json:"behavior,omitempty"`
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

	// TargetOverrides defines per-target overrides for the exception window.
	// Only valid when Type is "extend" or "replace".
	// +kubebuilder:validation:Optional
	// +optional
	TargetOverrides []TargetOverride `json:"targetOverrides,omitempty"`

	// ExecutionOverride defines a full replacement of the execution strategy
	// and behavior for the exception window.
	// Only valid when Type is "extend" or "replace".
	// +kubebuilder:validation:Optional
	// +optional
	ExecutionOverride *ExecutionOverride `json:"executionOverride,omitempty"`
}

// ScheduleExceptionStatus defines the observed state of ScheduleException.
type ScheduleExceptionStatus struct {
	// State is the current lifecycle state of the exception.
	// +kubebuilder:validation:Enum=Pending;Active;Expired;Detached
	State ExceptionState `json:"state,omitempty"`

	// AppliedAt is when the exception was first applied.
	// +kubebuilder:validation:Optional
	AppliedAt *metav1.Time `json:"appliedAt,omitempty"`

	// ExpiredAt is when the exception transitioned to Expired state.
	// +kubebuilder:validation:Optional
	ExpiredAt *metav1.Time `json:"expiredAt,omitempty"`

	// DetachedAt is when the exception transitioned to Detached state (plan was deleted).
	// +kubebuilder:validation:Optional
	DetachedAt *metav1.Time `json:"detachedAt,omitempty"`

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
// +kubebuilder:printcolumn:name="ValidFrom",type=string,JSONPath=`.spec.validFrom`
// +kubebuilder:printcolumn:name="ValidUntil",type=string,JSONPath=`.spec.validUntil`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ScheduleException is the Schema for the scheduleexceptions API.
type ScheduleException struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired state of ScheduleException.
	Spec ScheduleExceptionSpec `json:"spec,omitempty"`

	// Status defines the observed state of ScheduleException.
	Status ScheduleExceptionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ScheduleExceptionList contains a list of ScheduleException.
type ScheduleExceptionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	// Items is the list of ScheduleException resources.
	Items []ScheduleException `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ScheduleException{}, &ScheduleExceptionList{})
}
