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

const (
	// KeyContextKey is the context key for rate limit key
	KeyContextKey contextKey = "ratelimit.key"
)

// Key represents a rate limit key that identifies the rate limit bucket.
// This is an opaque identifier - it could be a credential, webhook URL,
// API token, or any other string that uniquely identifies a rate limit scope.
type Key string

// WithContext adds a rate limit key to the context.
// Callers should invoke this before making HTTP requests to enable per-key rate limiting.
func WithContext(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, KeyContextKey, Key(key))
}

// GetKey extracts the rate limit key from context.
// Returns empty string if not found.
func GetKey(ctx context.Context) Key {
	if v := ctx.Value(KeyContextKey); v != nil {
		if k, ok := v.(Key); ok {
			return k
		}
	}
	return ""
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
// It checks for a key in the context and applies rate limiting if present.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Use default transport if base not provided
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}

	log := t.Log
	if log.IsZero() {
		log = logr.Discard()
	}

	// Check if this request has a key for rate limiting
	key := GetKey(req.Context())
	if key == "" || t.Registry == nil {
		// No key or no registry - proceed without rate limiting
		return base.RoundTrip(req)
	}

	// Log that we're applying rate limiting (key is partially redacted)
	log.V(1).Info("applying rate limit",
		"key", redactKey(string(key)),
		"url", req.URL.Redacted(),
		"method", req.Method)

	// Wait for rate limit token
	start := time.Now()
	if err := t.Registry.Wait(req.Context(), string(key)); err != nil {
		log.V(1).Info("rate limit wait cancelled",
			"key", redactKey(string(key)),
			"error", err)
		return nil, err
	}
	waitDuration := time.Since(start)

	// Log if we actually had to wait (rate limiting was active)
	if waitDuration > 0 {
		log.V(1).Info("rate limit applied",
			"key", redactKey(string(key)),
			"wait", waitDuration,
			"url", req.URL.Redacted())
	}

	// Proceed with the request
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
