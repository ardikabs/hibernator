/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

import (
	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/notification/sink"
)

// Request represents a single notification request submitted
// by a hook closure. It contains all data needed by the dispatcher to resolve
// credentials, render the message, and send it to the sink.
type Request struct {
	// Payload carries the notification event data.
	Payload Payload

	// SinkName is the human-readable sink identifier (for logging).
	SinkName string

	// SinkType is the sink provider type (e.g., "slack", "telegram", "webhook").
	SinkType string

	// SecretRef references the Secret containing sink config.
	// If Key is empty, the dispatcher uses a default key.
	SecretRef hibernatorv1alpha1.ObjectKeyReference

	// TemplateRef optionally references a ConfigMap key for custom templates.
	// Nil means use the default template.
	TemplateRef *hibernatorv1alpha1.ObjectKeyReference
}

// Payload carries the notification event data passed to sinks for dispatch.
type Payload = sink.Payload

// TargetInfo holds execution state for a single target.
type TargetInfo = sink.TargetInfo

// PlanInfo carries plan metadata for the template context.
type PlanInfo = sink.PlanInfo
