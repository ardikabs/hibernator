/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package sink

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/types"
)

// RenderOption configures how a single Render call behaves. Sinks use options
// to inject context-specific template functions without coupling the Renderer
// to any particular sink's requirements.
type RenderOption func(*RenderConfig)

// CustomTemplate carries a user-provided Go template string loaded from a
// ConfigMap. The optional Key field enables cache lookups keyed by the
// ConfigMap's NamespacedName.
type CustomTemplate struct {
	// Content is the raw Go template string.
	Content string

	// Key uniquely identifies this custom template in the cache.
	// It is typically the NamespacedName of the source ConfigMap.
	// Zero value means the template is not cacheable.
	Key types.NamespacedName
}

// RenderConfig collects option values for a single Render call.
type RenderConfig struct {
	// Timeout overrides the default per-call render timeout. Zero means use
	// the engine default.
	Timeout time.Duration

	// Fallback, when non-nil, is returned verbatim whenever template parsing
	// or execution fails, instead of the engine's plain-text fallback.
	Fallback func(p Payload) string

	// MissingKey controls the template engine's behaviour when a key is
	// missing from the data context. Valid values: "default", "zero",
	// "error". Empty string leaves the engine default ("default") in place.
	MissingKey string

	// CustomTemplate, when non-nil, overrides the default template for the
	// sink type specified in the Payload.
	CustomTemplate *CustomTemplate
}

// NewRenderConfig applies the given options and returns the resulting config.
func NewRenderConfig(opts ...RenderOption) RenderConfig {
	cfg := RenderConfig{}
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// WithTimeout overrides the per-call render timeout. Use this when a sink
// needs more (or less) time than the engine's default budget.
func WithTimeout(d time.Duration) RenderOption {
	return func(c *RenderConfig) { c.Timeout = d }
}

// WithFallback sets a fixed string that is returned when template parsing or
// execution fails, replacing the engine's default plain-text fallback.
func WithFallback(fn func(p Payload) string) RenderOption {
	return func(c *RenderConfig) { c.Fallback = fn }
}

// WithMissingKeyError configures the template engine to return an error
// (and therefore fall back to the fallback message) when the template
// references a key that is absent from the data context.
func WithMissingKeyError() RenderOption {
	return func(c *RenderConfig) { c.MissingKey = "error" }
}

// WithCustomTemplate overrides the default template for this Render call.
// The engine uses the CustomTemplate's Content as the template source and
// may cache the parsed result keyed by CustomTemplate.Key.
func WithCustomTemplate(ct *CustomTemplate) RenderOption {
	return func(c *RenderConfig) { c.CustomTemplate = ct }
}

// Renderer abstracts the template rendering capability provided by the notification
// package. Sinks that need formatted output (Slack, Telegram) call Render with
// a Payload and receive the rendered message. The engine resolves the correct
// template (default for the sink type, or a custom override via RenderOption).
// This avoids a direct dependency on the notification.TemplateEngine type.
type Renderer interface {
	// Render renders the payload using the default template for payload.SinkType.
	// Callers may pass RenderOption values to override the template or adjust
	// rendering behaviour. If an error occurs, a plain-text fallback is returned.
	Render(ctx context.Context, payload Payload, opts ...RenderOption) string
}
