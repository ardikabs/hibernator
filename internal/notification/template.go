/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"strings"
	"text/template"
	"time"

	"github.com/Masterminds/sprig/v3"
	"github.com/go-logr/logr"

	"github.com/ardikabs/hibernator/internal/notification/sink"
)

const (
	// renderTimeout is the maximum time allowed for template rendering.
	renderTimeout = 1 * time.Second
)

// NotificationContext is the data context passed to Go templates for rendering.
// It combines event metadata, plan details, execution state, and sink info.
type NotificationContext struct {
	// Event is the hook point that triggered this notification.
	Event string

	// Timestamp is when the event occurred.
	Timestamp time.Time

	// Phase is the plan phase after the transition.
	Phase string

	// PreviousPhase is the plan phase before the transition (empty on Start).
	PreviousPhase string

	// Operation is the current operation: "Hibernate" or "WakeUp".
	Operation string

	// Plan holds the plan name, namespace, and labels.
	Plan PlanInfo

	// CycleID is the current execution cycle identifier.
	CycleID string

	// Targets holds per-target execution state (available on Success/Failure).
	Targets []TargetInfo

	// ErrorMessage provides error details (Failure/Recovery only).
	ErrorMessage string

	// RetryCount is the current retry attempt number (Recovery/Failure only).
	RetryCount int32

	// SinkName is the human-readable name of the sink being dispatched to.
	SinkName string

	// SinkType is the sink provider type (e.g., "slack", "telegram", "webhook").
	SinkType string
}

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
// string against a NotificationContext derived from the payload. On any error,
// a plain-text fallback is returned.
//
// Callers may pass RenderOption values to supply extra template functions for
// this call only. For example, the Telegram sink injects an htmlSafe helper
// when parse_mode is HTML.
func (e *TemplateEngine) Render(ctx context.Context, tmplStr string, payload Payload, opts ...sink.RenderOption) string {
	nc := payloadToContext(payload)

	fm := safeFuncMap()
	if len(opts) > 0 {
		cfg := sink.NewRenderConfig(opts...)
		maps.Copy(fm, cfg.ExtraFuncs)
	}

	// TODO(perf): consider caching parsed templates keyed by template string hash
	// to avoid re-parsing the same template on every call.
	tmpl, err := template.New("render").Funcs(fm).Parse(tmplStr)
	if err != nil {
		e.log.Error(err, "template parse failed, using plain fallback",
			"sinkType", payload.SinkType)
		return e.plainFallback(nc)
	}

	msg, err := e.executeWithTimeout(tmpl, nc)
	if err != nil {
		e.log.Error(err, "template execution failed, using plain fallback",
			"sinkType", payload.SinkType)
		return e.plainFallback(nc)
	}

	return msg
}

// payloadToContext converts a sink.Payload into a NotificationContext suitable
// for Go template execution. This keeps template data access consistent
// (e.g., .Plan.Name, .Plan.Namespace) regardless of the payload shape.
func payloadToContext(p Payload) NotificationContext {
	nc := NotificationContext{
		Event:         p.Event,
		Timestamp:     p.Timestamp,
		Phase:         p.Phase,
		PreviousPhase: p.PreviousPhase,
		Operation:     p.Operation,
		Plan: PlanInfo{
			Name:      p.ID.Name,
			Namespace: p.ID.Namespace,
			Labels:    p.Labels,
		},
		CycleID:      p.CycleID,
		ErrorMessage: p.ErrorMessage,
		RetryCount:   p.RetryCount,
		SinkName:     p.SinkName,
		SinkType:     p.SinkType,
		Targets:      p.Targets,
	}

	return nc
}

// executeWithTimeout renders a template with a render timeout to prevent infinite loops.
func (e *TemplateEngine) executeWithTimeout(tmpl *template.Template, nc NotificationContext) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), renderTimeout)
	defer cancel()

	type result struct {
		msg string
		err error
	}

	ch := make(chan result, 1)
	go func() {
		var buf bytes.Buffer
		err := tmpl.Execute(&buf, nc)

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
		return "", fmt.Errorf("template rendering timed out after %s", renderTimeout)
	}
}

// plainFallback returns a minimal plain-text message when all template rendering fails.
func (e *TemplateEngine) plainFallback(nc NotificationContext) string {
	msg := fmt.Sprintf("[%s] %s — %s/%s", nc.Event, nc.Operation, nc.Plan.Namespace, nc.Plan.Name)
	if nc.Phase != "" {
		msg += fmt.Sprintf(" | Phase: %s", nc.Phase)
	}
	if nc.ErrorMessage != "" {
		msg += fmt.Sprintf(" | Error: %s", nc.ErrorMessage)
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

	return fm
}
