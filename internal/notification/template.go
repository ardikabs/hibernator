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
	"sync"
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

	// defaultTemplateFile is the base path for default templates in the embedded filesystem.
	defaultTemplateFile = "default.gotmpl"
)

// TemplateEngine renders notification messages from Go templates.
// It implements the sink.Renderer interface so that sinks can request
// on-demand rendering of their built-in or custom templates.
//
// Default (embedded) templates are parsed once and cached by sink type —
// they never change at runtime. Custom (user-provided) templates are
// re-parsed on every call so content changes take effect immediately.
type TemplateEngine struct {
	log   logr.Logger
	cache sync.Map // map[string]*template.Template
}

// NewTemplateEngine creates a new TemplateEngine. Default templates are lazily
// cached on first Render call for each sink type.
func NewTemplateEngine(log logr.Logger) *TemplateEngine {
	return &TemplateEngine{
		log: log,
	}
}

// Render implements sink.Renderer. It resolves the template for the given
// payload (default for the sink type, or a custom override via RenderOption),
// executes it against the Payload, and returns the rendered message.
// On any error, a plain-text fallback is returned.
func (e *TemplateEngine) Render(ctx context.Context, payload Payload, opts ...sink.RenderOption) string {
	cfg := sink.NewRenderConfig(opts...)

	fallback := e.plainFallback
	if cfg.Fallback != nil {
		fallback = cfg.Fallback
	}

	parsed, err := e.resolveTemplate(payload.SinkType, cfg)
	if err != nil {
		e.log.Error(err, "template resolution failed, using plain fallback",
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

// resolveTemplate returns a parsed *template.Template. Custom templates are
// always re-parsed on every call so that content changes take effect
// immediately. Default templates are parsed once and cached by sink type,
// since the embedded files never change at runtime.
func (e *TemplateEngine) resolveTemplate(sinkType string, cfg sink.RenderConfig) (*template.Template, error) {
	fm := safeFuncMap()

	// Custom template takes precedence — always re-parse to pick up changes.
	if ct := cfg.CustomTemplate; ct != nil {
		if ct.Key.Name == "" || ct.Key.Namespace == "" {
			return nil, fmt.Errorf("custom template Key.Name or Key.Namespace must not be empty")
		}

		tmpl := template.New("custom").Funcs(fm)
		if cfg.MissingKey != "" {
			tmpl = tmpl.Option("missingkey=" + cfg.MissingKey)
		}

		parsed, err := tmpl.Parse(ct.Content)
		if err != nil {
			return nil, fmt.Errorf("parse custom template: %w", err)
		}

		return parsed, nil
	}

	// Default template — try cache first.
	cacheKey := "default:" + sinkType
	if cached, ok := e.cache.Load(cacheKey); ok {
		return e.cloneWithOptions(cached.(*template.Template), cfg)
	}

	// Cache miss — load and parse the default template.
	tmpl := template.New(defaultTemplateFile).Funcs(fm)
	if cfg.MissingKey != "" {
		tmpl = tmpl.Option("missingkey=" + cfg.MissingKey)
	}

	parsed, err := tmpl.ParseFS(sink.TemplateFS, sinkType+"/"+defaultTemplateFile)
	if err != nil {
		return nil, fmt.Errorf("parse default template for %q: %w", sinkType, err)
	}

	e.cache.Store(cacheKey, parsed)
	return parsed, nil
}

// cloneWithOptions clones a cached template and applies per-call options
// (e.g., missingkey). Cloning is necessary because template.Option mutates
// the template and we must not modify cached entries.
func (e *TemplateEngine) cloneWithOptions(tmpl *template.Template, cfg sink.RenderConfig) (*template.Template, error) {
	clone, err := tmpl.Clone()
	if err != nil {
		return nil, fmt.Errorf("clone cached template: %w", err)
	}
	if cfg.MissingKey != "" {
		clone = clone.Option("missingkey=" + cfg.MissingKey)
	}
	return clone, nil
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
