/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package sink

import (
	"context"
	"time"
)

// RenderOption configures how a single Render call behaves. Sinks use options
// to inject context-specific template functions without coupling the Renderer
// to any particular sink's requirements.
type RenderOption func(*RenderConfig)

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

// Renderer abstracts the template rendering capability provided by the notification
// package. Sinks that need formatted output (Slack, Telegram) call Render with a
// Go-template string and receive the rendered message. This avoids a direct
// dependency on the notification.TemplateEngine type.
type Renderer interface {
	// Render executes the given Go template string against the payload data
	// and returns the rendered output. Callers may pass RenderOption values
	// to inject additional template functions for this call only. If an error
	// occurs, a plain-text fallback is returned instead.
	Render(ctx context.Context, tmplStr string, payload Payload, opts ...RenderOption) string
}
