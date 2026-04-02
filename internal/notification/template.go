/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"strings"
	"text/template"
	"time"

	"github.com/Masterminds/sprig/v3"
	"github.com/go-logr/logr"
	"github.com/go-telegram/bot"

	"github.com/ardikabs/hibernator/internal/notification/sink"
)

const (
	// defaultRenderTimeout is the maximum time allowed for template rendering.
	defaultRenderTimeout = 1 * time.Second
)

// TemplateEngine renders notification messages from Go templates.
// It implements the sink.Renderer interface so that sinks can request
// on-demand rendering of their built-in or custom templates.
type TemplateEngine struct {
	log logr.Logger
}

// NewTemplateEngine creates a new TemplateEngine.
func NewTemplateEngine(log logr.Logger) *TemplateEngine {
	return &TemplateEngine{
		log: log,
	}
}

// Render implements sink.Renderer. It parses and executes the given Go template
// string against the Payload directly. On any error, a plain-text fallback is
// returned.
//
// Callers may pass RenderOption values to customise rendering behaviour (e.g.,
// WithTimeout, WithFallback, WithMissingKeyError).
func (e *TemplateEngine) Render(ctx context.Context, tmplStr string, payload Payload, opts ...sink.RenderOption) string {
	cfg := sink.NewRenderConfig(opts...)

	fallback := e.plainFallback
	if cfg.Fallback != nil {
		fallback = cfg.Fallback
	}

	fm := safeFuncMap()

	// TODO(perf): consider caching parsed templates keyed by template string hash
	// to avoid re-parsing the same template on every call.
	tmpl := template.New("render").Funcs(fm)
	if cfg.MissingKey != "" {
		tmpl = tmpl.Option("missingkey=" + cfg.MissingKey)
	}

	parsed, err := tmpl.Parse(tmplStr)
	if err != nil {
		e.log.Error(err, "template parse failed, using plain fallback",
			"sinkType", payload.SinkType)
		return fallback(payload)
	}

	timeout := defaultRenderTimeout
	if cfg.Timeout > 0 {
		timeout = cfg.Timeout
	}

	msg, err := e.executeWithTimeout(parsed, payload, timeout)
	if err != nil {
		e.log.Error(err, "template execution failed, using plain fallback",
			"sinkType", payload.SinkType)
		return fallback(payload)
	}

	return msg
}

// executeWithTimeout renders a template with a render timeout to prevent infinite loops.
func (e *TemplateEngine) executeWithTimeout(tmpl *template.Template, payload Payload, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	type result struct {
		msg string
		err error
	}

	ch := make(chan result, 1)
	go func() {
		var buf bytes.Buffer
		err := tmpl.Execute(&buf, payload)

		select {
		case ch <- result{msg: buf.String(), err: err}:
		case <-ctx.Done():
		}
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			return "", fmt.Errorf("execute template: %w", r.err)
		}
		return strings.TrimSpace(r.msg), nil
	case <-ctx.Done():
		return "", fmt.Errorf("template rendering timed out after %s", timeout)
	}
}

// plainFallback returns a minimal plain-text message when all template rendering fails.
func (e *TemplateEngine) plainFallback(p Payload) string {
	msg := fmt.Sprintf("[%s] %s — %s/%s", p.Event, p.Operation, p.Plan.Namespace, p.Plan.Name)
	if p.Phase != "" {
		msg += fmt.Sprintf(" | Phase: %s", p.Phase)
	}
	if p.ErrorMessage != "" {
		msg += fmt.Sprintf(" | Error: %s", p.ErrorMessage)
	}
	return msg
}

// safeFuncMap returns a template.FuncMap with potentially unsafe functions removed
// and additional safe helpers added.
func safeFuncMap() template.FuncMap {
	fm := sprig.TxtFuncMap()
	for _, name := range []string{"env", "expandenv"} {
		delete(fm, name)
	}

	fm["escapeHTML"] = html.EscapeString
	fm["escapeMarkdown"] = bot.EscapeMarkdown
	return fm
}
