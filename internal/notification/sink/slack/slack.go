/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	slackapi "github.com/slack-go/slack"

	"github.com/ardikabs/hibernator/internal/notification/sink"
)

const (
	// SinkType is the identifier for the Slack sink.
	SinkType = "slack"
)

// Option configures a Sink.
type Option func(*Sink)

// WithHTTPClient overrides the HTTP client used for Slack webhook requests.
// Use this in tests or to supply a custom transport/timeout.
func WithHTTPClient(client *http.Client) Option {
	return func(s *Sink) {
		s.client = client
	}
}

// Sink sends notifications to Slack via Incoming Webhook URL.
type Sink struct {
	renderer sink.Renderer
	client   *http.Client
}

// New creates a new Slack sink.
// renderer is a required first-class parameter — Slack always needs template
// rendering to produce formatted messages.
// By default it uses http.DefaultClient. In production the caller should supply a
// shared retryable client via WithHTTPClient (see notification.NewHTTPClient).
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

// Send renders the notification payload using the Slack template and delivers it
// via Incoming Webhook. If opts.CustomTemplateRef is set, that template is used
// instead of the built-in default.
func (s *Sink) Send(ctx context.Context, payload sink.Payload, opts sink.SendOptions) error {
	var cfg config
	if err := json.Unmarshal(opts.Config, &cfg); err != nil {
		return fmt.Errorf("parse slack sink config: %w", err)
	}
	if cfg.WebhookURL == "" {
		return fmt.Errorf("slack sink config: webhook_url is required")
	}

	var renderOpts []sink.RenderOption
	if opts.CustomTemplate != nil {
		renderOpts = append(renderOpts, sink.WithCustomTemplate(opts.CustomTemplate))
	}

	content := s.renderer.Render(ctx, payload, renderOpts...)
	msg := &slackapi.WebhookMessage{Text: content}

	if err := slackapi.PostWebhookCustomHTTPContext(ctx, cfg.WebhookURL, s.client, msg); err != nil {
		return fmt.Errorf("send slack notification: %w", err)
	}

	return nil
}
