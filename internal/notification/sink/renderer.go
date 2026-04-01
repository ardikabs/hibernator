/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package sink

import (
	"context"
	"text/template"
)

// RenderOption configures how a single Render call behaves. Sinks use options
// to inject context-specific template functions without coupling the Renderer
// to any particular sink's requirements.
type RenderOption func(*RenderConfig)

// RenderConfig collects option values for a single Render call.
type RenderConfig struct {
	// ExtraFuncs are additional template functions merged into the base func
	// map for this render call only. They do not persist across calls.
	ExtraFuncs template.FuncMap
}

// NewRenderConfig applies the given options and returns the resulting config.
func NewRenderConfig(opts ...RenderOption) RenderConfig {
	cfg := RenderConfig{}
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// WithExtraFuncs returns a RenderOption that merges the given template functions
// into the func map for a single render call. This lets sinks inject
// context-specific helpers (e.g., Telegram injects htmlSafe only when
// parse_mode is HTML) without polluting the global function namespace.
func WithExtraFuncs(funcs template.FuncMap) RenderOption {
	return func(c *RenderConfig) {
		if c.ExtraFuncs == nil {
			c.ExtraFuncs = make(template.FuncMap, len(funcs))
		}
		for k, v := range funcs {
			c.ExtraFuncs[k] = v
		}
	}
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
