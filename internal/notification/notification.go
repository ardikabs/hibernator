/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

import (
	"net/http"
	"time"

	"github.com/go-logr/logr"
	retryhttp "github.com/hashicorp/go-retryablehttp"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/ardikabs/hibernator/internal/notification/sink"
	slacksink "github.com/ardikabs/hibernator/internal/notification/sink/slack"
	telegramsink "github.com/ardikabs/hibernator/internal/notification/sink/telegram"
	webhooksink "github.com/ardikabs/hibernator/internal/notification/sink/webhook"
	"github.com/ardikabs/hibernator/pkg/ratelimit"
)

// Instance represents a notification subsystem instance
// with its Notifier interface and Runnable dispatcher.
type Instance struct {
	// Notifier is the submit-only interface distributed to plan processors and
	// state handlers — analogous to status.Updater.
	Notifier Notifier

	// Runnable is the controller-runtime Runnable that must be registered via
	// mgr.Add(). It owns the dispatch goroutine pool and channel lifecycle.
	Runnable manager.Runnable
}

// Notifier is the write-facing interface exposed to consumers (state handlers,
// processors). It intentionally hides all dispatcher and pool internals, mirroring
// the pattern used by status.Updater.
type Notifier interface {
	Submit(req Request)
}

// config holds subsystem-level configuration resolved from Option funcs.
type config struct {
	// extraSinks are additional sink implementations injected by the caller
	// (e.g., an in-memory sink for E2E tests).
	extraSinks []sink.Sink

	// disableDefaultSinks disables registration of the default Slack/Telegram/fake
	// sinks. Useful in tests that need full control over the sink registry.
	disableDefaultSinks bool

	// dispatcherConfig is forwarded to NewDispatcher.
	dispatcherConfig DispatcherConfig

	// deliveryCallback is invoked after each dispatch attempt for status tracking.
	deliveryCallback DeliveryCallback
}

// Option configures the notification subsystem constructed by New.
type Option func(*config)

// WithSink registers an additional sink implementation into the registry.
// This is the primary hook for E2E tests to inject a custom in-memory or
// recording sink without touching production code paths.
func WithSink(s sink.Sink) Option {
	return func(cfg *config) {
		cfg.extraSinks = append(cfg.extraSinks, s)
	}
}

// DisableDefaultSinks disables the default built-in sink registrations
// (Slack, Telegram, fake). Combine with WithSink to construct a
// fully-controlled registry for testing.
func DisableDefaultSinks() Option {
	return func(cfg *config) {
		cfg.disableDefaultSinks = true
	}
}

// WithDispatcherConfig overrides the default dispatcher configuration.
func WithDispatcherConfig(cfg DispatcherConfig) Option {
	return func(c *config) {
		c.dispatcherConfig = cfg
	}
}

// WithDeliveryCallback registers a callback invoked after each dispatch attempt.
// Used by the notification lifecycle processor to track per-sink delivery status.
func WithDeliveryCallback(cb DeliveryCallback) Option {
	return func(c *config) {
		c.deliveryCallback = cb
	}
}

// New constructs the notification subsystem instance: sink registry, template engine,
// and dispatcher. It registers all built-in sink implementations (Slack, Telegram,
// fake) using a shared retryable HTTP client unless DisableDefaultSinks is specified,
// builds a TemplateEngine backed by the controller-runtime client, and returns an
// Instance whose Notifier can be distributed to processors.
//
// This is the single public entry point that hides all notification internals
// from the setup/wiring layer.
func New(log logr.Logger, cl client.Reader, opts ...Option) Instance {
	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}

	registry := sink.NewRegistry()

	dispatcherOpts := []DispatcherOption{}

	if !cfg.disableDefaultSinks {
		tmplEngine := NewTemplateEngine(log.WithName("template"))

		// Create shared rate limiter registry for all sinks
		// This ensures rate limiting is keyed by a unique identifier (token/webhook)
		// Rate limiting happens at the HTTP transport level, so every API call
		// (including thread mode's multiple calls per notification) is properly throttled.
		rateLimitRegistry := ratelimit.NewRegistry(ratelimit.WithLogger(log.WithName("ratelimit")))

		// Create HTTP client with rate limiting transport at the bottom of the chain.
		// The transport reads the rate limit key from request context and applies
		// per-key rate limiting based on configs registered by each sink.
		httpClient := newHTTPClient(log.WithName("http-client"), rateLimitRegistry)

		registry.Register(slacksink.New(tmplEngine,
			slacksink.WithHTTPClient(httpClient)))
		registry.Register(telegramsink.New(tmplEngine,
			telegramsink.WithHTTPClient(httpClient)))
		registry.Register(webhooksink.New(tmplEngine,
			webhooksink.WithHTTPClient(httpClient)))

		dispatcherOpts = append(dispatcherOpts, withRateLimitRegistry(rateLimitRegistry))
	}

	if cfg.deliveryCallback != nil {
		dispatcherOpts = append(dispatcherOpts, withDeliveryCallback(cfg.deliveryCallback))
	}

	for _, s := range cfg.extraSinks {
		registry.Register(s)
	}

	dispatcher := NewDispatcher(
		log.WithName("dispatcher"),
		cl,
		registry,
		cfg.dispatcherConfig,
		dispatcherOpts...,
	)

	return Instance{
		Notifier: dispatcher.Notifier(),
		Runnable: dispatcher,
	}
}

// newHTTPClient builds a retryable http.Client suitable for notification sinks:
// up to 5 retries, exponential back-off 1 s – 30 s with Retry-After support, stdlib logger suppressed.
//
// Rate limit handling: Slack may return 429 with Retry-After headers (typically 1-60s).
// The default backoff respects Retry-After when present, and uses exponential backoff otherwise.
// With 5 retries and 30s max wait, we can handle most rate limit scenarios.
//
// The rateLimitRegistry is used to apply per-key rate limiting at the HTTP transport level.
// Sinks must inject the rate limit key into the request context using ratelimit.WithKey()
// for rate limiting to be applied.
func newHTTPClient(log logr.Logger, rateLimitRegistry *ratelimit.Registry) *http.Client {
	rc := retryhttp.NewClient()
	rc.RetryMax = 10
	rc.RetryWaitMin = 1 * time.Second
	rc.RetryWaitMax = 30 * time.Second
	rc.Logger = nil
	// Per-call HTTP timeout: bounds the actual network request independently
	// of the dispatcher's umbrella timeout. Rate limit waits happen in the
	// transport *before* this timer starts, so slow throttling does not
	// starve the call budget.
	rc.HTTPClient.Timeout = 30 * time.Second

	// Add response hook to log rate limit events for observability
	rc.ResponseLogHook = func(_ retryhttp.Logger, resp *http.Response) {
		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfter := resp.Header.Get("Retry-After")
			log.V(1).Info("notification sink rate limited (429)",
				"retry_after", retryAfter,
				"url", resp.Request.URL.Redacted())
		}
	}

	// Wrap the transport with per-key rate limiting.
	// This ensures every HTTP request (including thread mode's multiple API calls)
	// is properly rate limited based on the key in the request context.
	baseTransport := rc.HTTPClient.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	rc.HTTPClient.Transport = ratelimit.NewTransport(
		baseTransport,
		rateLimitRegistry,
		log.WithName("transport"),
	)

	return rc.StandardClient()
}
