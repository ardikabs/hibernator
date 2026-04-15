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

	"github.com/go-logr/logr"
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
	renderer  sink.Renderer
	client    *http.Client
	serverURL string
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

func withServerURL(url string) Option {
	return func(s *Sink) {
		s.serverURL = url
	}
}

func newWithServerURL(renderer sink.Renderer, client *http.Client, serverURL string) *Sink {
	return New(renderer, WithHTTPClient(client), withServerURL(serverURL))
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
//
// Behavior by config.delivery_mode:
//   - channel: custom templates are honored when provided.
//   - thread: custom templates are ignored intentionally, and the sink uses
//     opinionated built-in thread layouts to keep root context/status updates
//     consistent throughout the notification lifecycle.
func (s *Sink) Send(ctx context.Context, payload sink.Payload, opts sink.SendOptions) (sink.SendResult, error) {
	opts.Log.Info("dispatching Slack notification", "event", payload.Event, "plan", payload.Plan.String(), "cycle_id", payload.CycleID, "sink", payload.SinkName)
	opts.Log.V(1).Info("starting Slack sink send", "has_custom_template", opts.CustomTemplate != nil, "has_sink_state", len(opts.SinkState) > 0)

	var cfg config
	if err := json.Unmarshal(opts.Config, &cfg); err != nil {
		return sink.SendResult{}, fmt.Errorf("parse slack sink config: %w", err)
	}
	cfg.useDefaults()
	if err := cfg.validate(); err != nil {
		return sink.SendResult{}, err
	}
	opts.Log.Info("Slack sink config resolved", "delivery_mode", cfg.DeliveryMode, "format", cfg.Format, "block_layout", cfg.BlockLayout)

	customTemplate := opts.CustomTemplate
	if cfg.DeliveryMode == deliveryModeThread && customTemplate != nil {
		opts.Log.Info("ignored custom template for Slack thread delivery mode; using built-in opinionated thread layout for consistent context")
		customTemplate = nil
	}

	handler, err := newDeliveryHandler(s, cfg, deliveryRuntime{
		log:            opts.Log,
		sinkState:      opts.SinkState,
		customTemplate: customTemplate,
	})
	if err != nil {
		return sink.SendResult{}, err
	}

	states, err := handler.deliver(ctx, payload)
	if err != nil {
		return sink.SendResult{}, err
	}

	opts.Log.Info("Slack notification dispatched", "delivery_mode", cfg.DeliveryMode, "event", payload.Event, "plan", payload.Plan.String(), "cycle_id", payload.CycleID)
	if len(states) > 0 {
		opts.Log.V(1).Info("Slack sink emitted state metadata", "state_keys", len(states), "has_root_ts", states["slack.thread.root_ts"] != "")
	}

	return sink.SendResult{States: states}, nil
}

type deliveryRuntime struct {
	log            logr.Logger
	sinkState      map[string]string
	customTemplate *sink.CustomTemplate
}

// buildMessage constructs the Slack message based on the payload and config.
func (s *Sink) buildMessage(ctx context.Context, payload sink.Payload, cfg config, customTemplate *sink.CustomTemplate) *slackapi.WebhookMessage {
	switch cfg.Format {
	case formatJSON:
		if customTemplate != nil {
			rendered := s.renderer.Render(ctx, payload, sink.WithCustomTemplate(customTemplate))
			if msg, err := parseJSONTemplateMessage(rendered, payload); err == nil {
				return msg
			}
		}
		return presetJSONMessage(payload, cfg)

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

func (s *Sink) buildRootMessage(ctx context.Context, payload sink.Payload, cfg config, customTemplate *sink.CustomTemplate) *slackapi.WebhookMessage {
	switch cfg.Format {
	case formatJSON:
		if customTemplate != nil {
			rendered := s.renderer.Render(ctx, payload, sink.WithCustomTemplate(customTemplate))
			if msg, err := parseJSONTemplateMessage(rendered, payload); err == nil {
				return msg
			}
		}
		return presetJSONRootMessage(payload, cfg)

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

// presetJSONMessage builds a Slack message using the configured preset layout.
func presetJSONMessage(payload sink.Payload, cfg config) *slackapi.WebhookMessage {
	factory := newLayoutFactory()
	composer := newLayoutComposer(payload, cfg)
	return &slackapi.WebhookMessage{
		Text:   fallbackText(payload),
		Blocks: &slackapi.Blocks{BlockSet: factory.build(cfg.BlockLayout, composer)},
	}
}

func presetJSONRootMessage(payload sink.Payload, cfg config) *slackapi.WebhookMessage {
	composer := newLayoutComposer(payload, cfg)
	return &slackapi.WebhookMessage{
		Text:   fallbackText(payload),
		Blocks: &slackapi.Blocks{BlockSet: composer.buildThreadRoot()},
	}
}

// parseJSONTemplateMessage attempts to parse the rendered template output as a Slack WebhookMessage.
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
