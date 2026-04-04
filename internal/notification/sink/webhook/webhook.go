/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/ardikabs/hibernator/internal/notification/sink"
)

const (
	// SinkType is the identifier for the webhook sink.
	SinkType = "webhook"
)

// Option configures a Sink.
type Option func(*Sink)

// WithHTTPClient overrides the HTTP client used for webhook requests.
func WithHTTPClient(client *http.Client) Option {
	return func(s *Sink) {
		s.client = client
	}
}

// Sink sends notifications via HTTP POST to a user-configured URL.
type Sink struct {
	renderer sink.Renderer
	client   *http.Client
}

// New creates a new webhook sink.
// renderer may be nil — it is only used when the Secret config sets
// enable_renderer=true. When nil and enable_renderer is requested, the
// "rendered" field is silently omitted.
func New(renderer sink.Renderer, opts ...Option) *Sink {
	s := &Sink{renderer: renderer, client: http.DefaultClient}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Type returns the sink type identifier.
func (s *Sink) Type() string {
	return SinkType
}

// Send delivers a notification payload as a JSON POST to the configured URL.
// The body always includes a "context" field with the raw payload. When
// enable_renderer is true in the config, a "rendered" field is added with
// the template-rendered message.
func (s *Sink) Send(ctx context.Context, payload sink.Payload, opts sink.SendOptions) error {
	var cfg config
	if err := json.Unmarshal(opts.Config, &cfg); err != nil {
		return fmt.Errorf("parse webhook sink config: %w", err)
	}
	if cfg.URL == "" {
		return fmt.Errorf("webhook sink config: url is required")
	}

	body := webhookBody{
		Context: toWebhookContext(payload),
	}

	if cfg.EnableRenderer && s.renderer != nil {
		var renderOpts []sink.RenderOption
		if opts.CustomTemplate != nil {
			renderOpts = append(renderOpts, sink.WithCustomTemplate(opts.CustomTemplate))
		}
		body.Rendered = s.renderer.Render(ctx, payload, renderOpts...)
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal webhook body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("send webhook notification: %w", err)
	}
	// nolint:errcheck
	defer resp.Body.Close()

	// nolint:errcheck
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned non-2xx status: %d", resp.StatusCode)
	}

	return nil
}
