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
	"strings"

	slackapi "github.com/slack-go/slack"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
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
// via Incoming Webhook.
//
// Behavior by config.format:
//   - text (default): render text and send as plain Slack text message.
//   - json: if custom template is provided, render and parse JSON payload;
//     otherwise build a preset JSON blocks payload.
func (s *Sink) Send(ctx context.Context, payload sink.Payload, opts sink.SendOptions) error {
	var cfg config
	if err := json.Unmarshal(opts.Config, &cfg); err != nil {
		return fmt.Errorf("parse slack sink config: %w", err)
	}
	cfg.useDefaults()
	if err := cfg.validate(); err != nil {
		return err
	}

	if cfg.WebhookURL == "" {
		return fmt.Errorf("slack sink config: webhook_url is required")
	}

	if shouldSuppressExecutionProgress(payload, cfg) {
		return nil
	}

	msg := s.buildMessage(ctx, payload, cfg, opts.CustomTemplate)

	if err := slackapi.PostWebhookCustomHTTPContext(ctx, cfg.WebhookURL, s.client, msg); err != nil {
		return fmt.Errorf("send slack notification: %w", err)
	}

	return nil
}

func shouldSuppressExecutionProgress(payload sink.Payload, cfg config) bool {
	if cfg.Format != formatJSON {
		return false
	}
	if payload.Event != "ExecutionProgress" {
		return false
	}
	if cfg.BlockLayout == blockLayoutAuto {
		return false
	}
	if cfg.BlockLayout != blockLayoutDefault && cfg.BlockLayout != blockLayoutCompact {
		return false
	}
	if payload.TargetExecution == nil {
		return false
	}

	switch hibernatorv1alpha1.ExecutionState(payload.TargetExecution.State) {
	case hibernatorv1alpha1.StateCompleted,
		hibernatorv1alpha1.StateFailed,
		hibernatorv1alpha1.StateAborted:
		return false
	default:
		return true
	}
}

func (s *Sink) buildMessage(ctx context.Context, payload sink.Payload, cfg config, customTemplate *sink.CustomTemplate) *slackapi.WebhookMessage {
	switch cfg.Format {
	case formatJSON:
		if customTemplate != nil {
			rendered := s.renderer.Render(ctx, payload, sink.WithCustomTemplate(customTemplate))
			if msg, err := parseJSONTemplateMessage(rendered, payload); err == nil {
				return msg
			}
		}
		return presetJSONMessage(payload, cfg.BlockLayout, cfg.MaxTargets, cfg.AdditionalScopes)

	case formatText:
		fallthrough
	default:
		var renderOpts []sink.RenderOption
		if customTemplate != nil {
			renderOpts = append(renderOpts, sink.WithCustomTemplate(customTemplate))
		}
		content := s.renderer.Render(ctx, payload, renderOpts...)
		return &slackapi.WebhookMessage{Text: content}
	}
}

func parseJSONTemplateMessage(rendered string, payload sink.Payload) (*slackapi.WebhookMessage, error) {
	rendered = strings.TrimSpace(rendered)
	if rendered == "" {
		return nil, fmt.Errorf("empty template output")
	}

	var msg slackapi.WebhookMessage
	if err := json.Unmarshal([]byte(rendered), &msg); err == nil {
		if msg.Blocks != nil && len(msg.Blocks.BlockSet) > 0 {
			if strings.TrimSpace(msg.Text) == "" {
				msg.Text = fallbackText(payload)
			}
			return &msg, nil
		}
	}

	var blocks slackapi.Blocks
	if err := json.Unmarshal([]byte(rendered), &blocks); err == nil && len(blocks.BlockSet) > 0 {
		return &slackapi.WebhookMessage{
			Text:   fallbackText(payload),
			Blocks: &blocks,
		}, nil
	}

	return nil, fmt.Errorf("template output is not a valid Slack JSON payload")
}

func (c config) validate() error {
	switch c.Format {
	case formatText, formatJSON:
		// ok
	default:
		return fmt.Errorf("slack sink config: format must be %q or %q", formatText, formatJSON)
	}

	if c.Format != formatJSON {
		return nil
	}

	switch c.BlockLayout {
	case blockLayoutDefault, blockLayoutCompact, blockLayoutAuto:
		// ok
	default:
		return fmt.Errorf("slack sink config: block_layout must be one of %q, %q, %q", blockLayoutDefault, blockLayoutCompact, blockLayoutAuto)
	}

	for _, scope := range c.AdditionalScopes {
		switch scope {
		case scopeAccount, scopeCluster, scopeEnvironment, scopeRegion, scopeProject, scopeProvider, scopeConnector:
			// ok
		default:
			return fmt.Errorf("slack sink config: unsupported additional scope %q", scope)
		}
	}

	return nil
}
