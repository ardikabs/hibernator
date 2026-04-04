/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"k8s.io/utils/ptr"

	"github.com/ardikabs/hibernator/internal/notification/sink"
)

const (
	// SinkType is the identifier for the Telegram sink.
	SinkType = "telegram"
)

// Option configures a Sink.
type Option func(*Sink)

// WithHTTPClient overrides the HTTP client used for Telegram Bot API requests.
// Use this in tests or to supply a custom transport/timeout.
func WithHTTPClient(client *http.Client) Option {
	return func(s *Sink) {
		s.client = client
	}
}

// withServerURL sets a custom Telegram Bot API base URL (for testing only).
func withServerURL(url string) Option {
	return func(s *Sink) {
		s.serverURL = url
	}
}

// Sink sends notifications to Telegram via the go-telegram/bot SDK.
type Sink struct {
	renderer  sink.Renderer
	client    *http.Client
	serverURL string
}

// New creates a new Telegram sink.
// renderer is a required first-class parameter — Telegram always needs template
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

// newWithServerURL creates a Telegram sink pointing at a custom server URL.
// This is an internal helper used exclusively by tests.
func newWithServerURL(renderer sink.Renderer, client *http.Client, serverURL string) *Sink {
	return New(renderer, WithHTTPClient(client), withServerURL(serverURL))
}

// Type returns the sink type identifier.
func (s *Sink) Type() string {
	return SinkType
}

// Send renders the notification payload using the Telegram template and delivers it
// via the Bot API SDK. If opts.CustomTemplateRef is set, that template is used
// instead of the built-in default.
func (s *Sink) Send(ctx context.Context, payload sink.Payload, opts sink.SendOptions) error {
	var cfg config
	if err := json.Unmarshal(opts.Config, &cfg); err != nil {
		return fmt.Errorf("parse telegram sink config: %w", err)
	}
	if cfg.Token == "" {
		return fmt.Errorf("telegram sink config: token is required")
	}
	if cfg.ChatID == "" {
		return fmt.Errorf("telegram sink config: chat_id is required")
	}

	var renderOpts []sink.RenderOption
	if opts.CustomTemplate != nil {
		renderOpts = append(renderOpts, sink.WithCustomTemplate(opts.CustomTemplate))
	}

	parseMode := ptr.Deref(cfg.ParseMode, string(models.ParseModeHTML))
	content := s.renderer.Render(ctx, payload, renderOpts...)
	botOpts := []bot.Option{
		bot.WithHTTPClient(0, s.client),
		bot.WithSkipGetMe(),
	}
	if s.serverURL != "" {
		botOpts = append(botOpts, bot.WithServerURL(s.serverURL))
	}

	b, err := bot.New(cfg.Token, botOpts...)
	if err != nil {
		return fmt.Errorf("create telegram bot client: %w", err)
	}

	// Resolve ChatID: numeric int64 or string channel username (e.g., "@mychannel").
	var chatID any
	if id, err := strconv.ParseInt(cfg.ChatID, 10, 64); err == nil {
		chatID = id
	} else {
		chatID = cfg.ChatID
	}

	params := &bot.SendMessageParams{
		ChatID:    chatID,
		Text:      content,
		ParseMode: models.ParseMode(parseMode),
	}

	if _, err := b.SendMessage(ctx, params); err != nil {
		return fmt.Errorf("send telegram notification: %w", err)
	}

	return nil
}
