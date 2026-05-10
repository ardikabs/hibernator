/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package ratelimit

import (
	"context"
	"net/http"
	"time"

	"github.com/go-logr/logr"
)

// contextKey is the type for context keys to avoid collisions
type contextKey string

const rateLimitContextKey contextKey = "ratelimit.entry"

// rateLimitEntry carries rate limit configuration in the request context.
// The transport reads this entry, registers configs with the registry,
// and waits for tokens before allowing the request to proceed.
type rateLimitEntry struct {
	key    string
	cfg    Config
	opName string
	opCfg  Config
}

// RateLimitOption configures a rate limit entry.
type RateLimitOption func(*rateLimitEntry)

// WithOperation adds a per-operation rate limit to the entry.
// The transport will register key#opName with the given config and
// wait on the operation-scoped path (global then operation).
func WithOperation(opName string, cfg Config) RateLimitOption {
	return func(e *rateLimitEntry) {
		e.opName = opName
		e.opCfg = cfg
	}
}

// WithRateLimit adds a rate limit key and configuration to the context.
// The transport will register the config with its registry and wait for
// a token before allowing the HTTP request to proceed.
//
// For operation-scoped rate limiting (e.g. per-API-method limits within
// a shared parent bucket), use WithOperation option.
func WithRateLimit(ctx context.Context, key string, cfg Config, opts ...RateLimitOption) context.Context {
	entry := &rateLimitEntry{
		key: key,
		cfg: cfg,
	}
	for _, opt := range opts {
		opt(entry)
	}
	return context.WithValue(ctx, rateLimitContextKey, entry)
}

// getRateLimitEntry extracts the rate limit entry from context.
// Returns nil if not found or wrong type.
func getRateLimitEntry(ctx context.Context) *rateLimitEntry {
	if v := ctx.Value(rateLimitContextKey); v != nil {
		if e, ok := v.(*rateLimitEntry); ok {
			return e
		}
	}
	return nil
}

// Transport is an HTTP RoundTripper that applies rate limiting per key.
// It wraps an underlying transport and waits for rate limit tokens before
// allowing requests to proceed.
type Transport struct {
	// Base is the underlying RoundTripper. If nil, uses http.DefaultTransport.
	Base http.RoundTripper

	// Registry holds the rate limiters per key.
	Registry *Registry

	// Log is used for logging rate limit events.
	Log logr.Logger
}

// redactKey masks the middle portion of a key, showing only first 3 and last 3 characters.
// Examples:
//   - "xoxb-1234567890" -> "xox...890"
//   - "abc" -> "abc" (too short to redact)
//   - "abcd" -> "a...d"
//   - "abcde" -> "ab...de"
func redactKey(key string) string {
	if len(key) == 2 {
		return key
	}

	if len(key) <= 6 {
		return key[:2] + "..." + key[len(key)-2:]
	}

	return key[:3] + "..." + key[len(key)-3:]
}

// RoundTrip implements http.RoundTripper.
// It reads rate limit configuration from the request context, registers it
// with the registry, and waits for a token before forwarding the request.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Use default transport if base not provided
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}

	log := t.Log.WithValues(
		"url", req.URL.Redacted(),
		"method", req.Method,
	)
	if log.IsZero() {
		log = logr.Discard()
	}

	entry := getRateLimitEntry(req.Context())
	if entry == nil || t.Registry == nil {
		// No rate limit config or no registry - proceed without rate limiting
		return base.RoundTrip(req)
	}

	waitKey := entry.key
	if entry.opName != "" {
		// Register parent + operation in one coordinated step.
		// This ensures the parent is created with the correct config
		// rather than falling back to defaults.
		t.Registry.registerEntry(entry)
		waitKey = entry.key + "#" + entry.opName
	} else {
		// Register parent only.
		t.Registry.Register(entry.key, entry.cfg)
	}

	log = log.WithValues("mode", "registry", "key", redactKey(waitKey))
	log.V(1).Info("applying rate limit")

	start := time.Now()
	if err := t.Registry.Wait(req.Context(), waitKey); err != nil {
		log.V(1).Info("rate limit wait cancelled", "error", err)
		return nil, err
	}
	waitDuration := time.Since(start)

	if waitDuration > 0 {
		log.V(1).Info("rate limit applied", "wait", waitDuration)
	}

	return base.RoundTrip(req)
}

// NewTransport creates a new rate-limiting HTTP transport.
// It wraps the provided base transport (or http.DefaultTransport if nil) with
// per-key rate limiting using the provided registry.
func NewTransport(base http.RoundTripper, registry *Registry, log logr.Logger) *Transport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &Transport{
		Base:     base,
		Registry: registry,
		Log:      log,
	}
}

// TransportOption configures the rate limiting transport.
type TransportOption func(*Transport)

// WithTransportLogger sets the logger for the transport.
func WithTransportLogger(log logr.Logger) TransportOption {
	return func(t *Transport) {
		t.Log = log
	}
}

// WithTransportBase sets the base transport.
func WithTransportBase(base http.RoundTripper) TransportOption {
	return func(t *Transport) {
		t.Base = base
	}
}
