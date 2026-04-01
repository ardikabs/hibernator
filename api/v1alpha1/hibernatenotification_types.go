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
// +kubebuilder:validation:Enum=Start;Success;Failure;Recovery;PhaseChange
type NotificationEvent string

const (
	// EventStart fires right before execution begins (PreHook on Hibernating/WakingUp).
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
	// MatchedPlans is the count of HibernatePlans currently matching the selector.
	// +optional
	MatchedPlans int32 `json:"matchedPlans,omitempty"`

	// Message provides diagnostic information.
	// +optional
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=hnotif
// +kubebuilder:printcolumn:name="Events",type=string,JSONPath=`.spec.onEvents`
// +kubebuilder:printcolumn:name="Sinks",type=integer,JSONPath=`.spec.sinks`
// +kubebuilder:printcolumn:name="Matched",type=integer,JSONPath=`.status.matchedPlans`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

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
