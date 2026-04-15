/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NotificationSinkType defines supported notification sink types.
// +kubebuilder:validation:Enum=slack;telegram;webhook
type NotificationSinkType string

const (
	// SinkSlack sends notifications via Slack Incoming Webhook URL.
	SinkSlack NotificationSinkType = "slack"
	// SinkTelegram sends notifications via Telegram Bot API.
	SinkTelegram NotificationSinkType = "telegram"
	// SinkWebhook sends notifications via generic HTTP POST webhook.
	SinkWebhook NotificationSinkType = "webhook"
)

// NotificationEvent defines the hook point that triggers a notification.
// +kubebuilder:validation:Enum=Start;Success;Failure;Recovery;PhaseChange;ExecutionProgress
type NotificationEvent string

const (
	// EventStart fires when execution begins after the transition status write succeeds
	// (PostHook on Hibernating/WakingUp transition).
	EventStart NotificationEvent = "Start"
	// EventSuccess fires after execution completes successfully (PostHook on Hibernated/Active).
	EventSuccess NotificationEvent = "Success"
	// EventFailure fires when retries exhausted and plan enters permanent Error state
	// (PostHook on Error transition, gated by retryCount >= behavior.retries).
	EventFailure NotificationEvent = "Failure"
	// EventRecovery fires each time the recovery system retries from Error (PreHook).
	EventRecovery NotificationEvent = "Recovery"
	// EventPhaseChange fires on every phase transition (PostHook). Noisy — for audit trails.
	EventPhaseChange NotificationEvent = "PhaseChange"
	// EventExecutionProgress fires when an individual target's execution state changes
	// (e.g., Pending→Running, Running→Completed/Failed). Only fires on actual state
	// transitions, not on every poll tick.
	EventExecutionProgress NotificationEvent = "ExecutionProgress"
)

// ObjectKeyReference is a reference to a specific key in a namespaced object.
type ObjectKeyReference struct {
	// Name is the name of the object.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Key is the key within the object primarily for Secret or ConfigMap data.
	// If omitted, the dispatcher uses a default key ("config" for SecretRef, "template.gotpl" for TemplateRef).
	// +optional
	Key *string `json:"key,omitempty"`
}

// NotificationSink defines a destination for notification delivery.
// All sink-specific configuration (endpoints, credentials, options) is delegated
// to the referenced Secret under a well-known key ("config"). This minimizes
// the CRD footprint and keeps sensitive data out of the resource spec.
type NotificationSink struct {
	// Name is a human-readable identifier for this sink (unique within spec.sinks).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Type is the sink provider type.
	// +kubebuilder:validation:Required
	Type NotificationSinkType `json:"type"`

	// SecretRef is the name of the Secret containing the sink configuration.
	// The Secret must contain a key named "config" whose value is a JSON object
	// with all sink-specific settings (endpoint URL, credentials, options).
	//
	// Slack config example:   {"webhook_url": "https://hooks.slack.com/services/..."}
	// Telegram config example: {"token": "bot123:ABC", "chat_id": "-100123", "parse_mode": "MarkdownV2"}
	// Webhook config example:  {"url": "https://example.com/hook", "headers": {"Authorization": "Bearer ..."}}
	// +kubebuilder:validation:Required
	SecretRef ObjectKeyReference `json:"secretRef"`

	// TemplateRef references a ConfigMap key containing a Go template for message formatting.
	// If omitted, a built-in default template is used for the sink type.
	// +optional
	TemplateRef *ObjectKeyReference `json:"templateRef,omitempty"`
}

// NotificationState defines the lifecycle state of a HibernateNotification.
// +kubebuilder:validation:Enum=Bound;Detached
type NotificationState string

const (
	// NotificationStateBound indicates the notification is attached to at least one HibernatePlan.
	// The notification has a finalizer to ensure graceful cleanup on deletion.
	NotificationStateBound NotificationState = "Bound"

	// NotificationStateDetached indicates no HibernatePlan references this notification.
	// The finalizer is removed so the notification can be freely deleted.
	NotificationStateDetached NotificationState = "Detached"
)

// HibernateNotificationSpec defines the desired state of HibernateNotification.
type HibernateNotificationSpec struct {
	// Selector selects HibernatePlans by label.
	// The notification applies to all plans in the same namespace matching this selector.
	// +kubebuilder:validation:Required
	Selector metav1.LabelSelector `json:"selector"`

	// OnEvents specifies which hook points trigger this notification.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	OnEvents []NotificationEvent `json:"onEvents"`

	// Sinks defines the notification destinations.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Sinks []NotificationSink `json:"sinks"`
}

// HibernateNotificationStatus defines the observed state of HibernateNotification.
type HibernateNotificationStatus struct {
	// State is the lifecycle state of this notification: Bound or Detached.
	// Bound means at least one HibernatePlan matches the selector.
	// Detached means no HibernatePlan matches; the notification can be freely deleted.
	// +optional
	// +kubebuilder:default=Detached
	State NotificationState `json:"state,omitempty"`

	// WatchedPlans is the list of HibernatePlan references currently matching the selector.
	// +optional
	WatchedPlans []PlanReference `json:"watchedPlans,omitempty"`

	// LastDeliveryTime is the timestamp of the most recent successful notification delivery
	// across all sinks. Nil if no successful delivery has occurred.
	// +optional
	LastDeliveryTime *metav1.Time `json:"lastDeliveryTime,omitempty"`

	// LastFailureTime is the timestamp of the most recent failed notification delivery
	// across all sinks. Nil if no failure has occurred.
	// +optional
	LastFailureTime *metav1.Time `json:"lastFailureTime,omitempty"`

	// SinkStatuses stores tracked per-sink delivery state scoped by
	// sink + plan + cycle + operation.
	// The key format is implementation-defined by the controller.
	// Retention prioritizes relevance: for each sink+plan pair, only the most
	// recent 2 cycle entries are retained.
	// +optional
	SinkStatuses map[string]NotificationSinkStatus `json:"sinkStatuses,omitempty"`

	// ObservedGeneration is the most recent .metadata.generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// MaxSinkStatusCyclesPerPlan is the number of most-recent cycle entries kept
// per sink+plan pair in SinkStatuses.
const MaxSinkStatusCyclesPerPlan = 2

// MaxSinkStatusEntries is a safety cap across all sink status entries.
const MaxSinkStatusEntries = 500

// NotificationSinkStatus records tracked delivery state for a sink key.
type NotificationSinkStatus struct {
	// SinkName is the sink name as defined in spec.sinks[].name.
	SinkName string `json:"sinkName"`

	// PlanRef identifies the plan this sink status belongs to.
	PlanRef PlanReference `json:"planRef"`

	// CycleID is the execution cycle identifier.
	// +optional
	CycleID string `json:"cycleId,omitempty"`

	// Operation is the operation associated with this sink status (shutdown/wakeup).
	// +optional
	Operation string `json:"operation,omitempty"`

	// Success indicates whether the most recent delivery attempt succeeded.
	Success bool `json:"success"`

	// TransitionTimestamp is when the most recent delivery attempt completed.
	TransitionTimestamp metav1.Time `json:"transitionTimestamp"`

	// SuccessCount is the number of successful deliveries for this sink key.
	SuccessCount int32 `json:"successCount,omitempty"`

	// FailureCount is the number of failed deliveries for this sink key.
	FailureCount int32 `json:"failureCount,omitempty"`

	// LastSuccessTime is when the most recent successful delivery completed.
	// +optional
	LastSuccessTime *metav1.Time `json:"lastSuccessTime,omitempty"`

	// LastFailureTime is when the most recent failed delivery completed.
	// +optional
	LastFailureTime *metav1.Time `json:"lastFailureTime,omitempty"`

	// Message is a human-readable description of the most recent delivery outcome.
	// +optional
	Message string `json:"message,omitempty"`

	// States carries sink-specific arbitrary key/value context for this sink key.
	// Keys should be namespaced by sink type (for example,
	// "slack.thread.root_ts") to avoid collisions.
	// +optional
	States map[string]string `json:"states,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=hnotif
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`,description="Lifecycle state (Bound/Detached)"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
//
// HibernateNotification is the Schema for the hibernatenotifications API.
type HibernateNotification struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired state of HibernateNotification.
	Spec HibernateNotificationSpec `json:"spec,omitempty"`

	// Status defines the observed state of HibernateNotification.
	Status HibernateNotificationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HibernateNotificationList contains a list of HibernateNotification.
type HibernateNotificationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	// Items is the list of HibernateNotification resources.
	Items []HibernateNotification `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HibernateNotification{}, &HibernateNotificationList{})
}
