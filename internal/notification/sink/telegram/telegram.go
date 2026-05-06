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
	"github.com/ardikabs/hibernator/pkg/ratelimit"
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

// WithRateLimitRegistry sets the rate limit registry for per-key rate limiting.
// The registry is used to register rate limit configs keyed by a unique identifier (bot token).
// Rate limits in Telegram are per bot token, so the key is the token itself.
// If not provided, rate limiting is disabled.
func WithRateLimitRegistry(registry *ratelimit.Registry) Option {
	return func(s *Sink) {
		s.rateLimitRegistry = registry
	}
}

// Sink sends notifications to Telegram via the go-telegram/bot SDK.
type Sink struct {
	renderer          sink.Renderer
	client            *http.Client
	serverURL         string
	rateLimitRegistry *ratelimit.Registry
}

// New creates a new Telegram sink.
// renderer is a required first-class parameter — Telegram always needs template
// rendering to produce formatted messages.
// By default it uses http.DefaultClient. In production the caller should supply a
// shared retryable client via WithHTTPClient (see notification.NewHTTPClient).
func New(renderer sink.Renderer, opts ...Option) *Sink {
	s := &Sink{
		renderer: renderer,
		client:   http.DefaultClient,
		// rateLimitRegistry is nil by default; rate limiting is applied at HTTP transport level
		// when a registry is provided via WithRateLimitRegistry.
	}
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
//
// Rate limiting is applied at the HTTP transport level per key (bot token).
// The rate limit configuration is read from the Secret config and registered
// with the rate limit registry on the first send for each token.
func (s *Sink) Send(ctx context.Context, payload sink.Payload, opts sink.SendOptions) (sink.SendResult, error) {
	var cfg config
	if err := json.Unmarshal(opts.Config, &cfg); err != nil {
		return sink.SendResult{}, fmt.Errorf("parse telegram sink config: %w", err)
	}
	if cfg.Token == "" {
		return sink.SendResult{}, fmt.Errorf("telegram sink config: token is required")
	}
	if cfg.ChatID == "" {
		return sink.SendResult{}, fmt.Errorf("telegram sink config: chat_id is required")
	}

	// Register rate limit config for this key (bot token) if a registry is configured.
	// Rate limiting is enforced at the HTTP transport level for every API call.
	// Telegram rate limits are per bot token.
	if cfg.RateLimit != nil && s.rateLimitRegistry != nil {
		key := cfg.Token
		s.registerRateLimitConfig(key, cfg.RateLimit)
		// Inject key into context so the HTTP transport can apply rate limiting
		ctx = ratelimit.WithContext(ctx, key)
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
		return sink.SendResult{}, fmt.Errorf("create telegram bot client: %w", err)
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
		return sink.SendResult{}, fmt.Errorf("send telegram notification: %w", err)
	}

	return sink.SendResult{}, nil
}

// registerRateLimitConfig registers the rate limit configuration for the given key.
// This allows the HTTP transport to apply per-key rate limiting.
func (s *Sink) registerRateLimitConfig(key string, rateLimitCfg *RateLimitConfig) {
	if rateLimitCfg == nil {
		return
	}

	rlCfg := ratelimit.Config{
		RequestsPerSecond: rateLimitCfg.RequestsPerSecond,
		Burst:             rateLimitCfg.Burst,
		RequestsPerMinute: rateLimitCfg.RequestsPerMinute,
	}

	// Register the config with the registry.
	// If the key already exists, this updates its config.
	s.rateLimitRegistry.Register(key, rlCfg)
}
