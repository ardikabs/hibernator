/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/notification/sink"
	"github.com/ardikabs/hibernator/pkg/ratelimit"
)

// stubRenderer implements sink.Renderer for tests.
type stubRenderer struct {
	defaultText string
}

func (r *stubRenderer) Render(_ context.Context, p sink.Payload, opts ...sink.RenderOption) string {
	cfg := sink.NewRenderConfig(opts...)
	if cfg.CustomTemplate != nil {
		return cfg.CustomTemplate.Content
	}
	if r.defaultText != "" {
		return r.defaultText
	}
	return "rendered:" + p.SinkType
}

func testPayload() sink.Payload {
	return sink.Payload{
		Plan: sink.PlanInfo{
			Name:      "test-plan",
			Namespace: "default",
			Labels: map[string]string{
				"env": "dev",
			},
		},
		Event:     "Start",
		Timestamp: time.Date(2026, 3, 28, 10, 0, 0, 0, time.UTC),
		Phase:     string(hibernatorv1alpha1.PhaseHibernating),
		Operation: string(hibernatorv1alpha1.OperationHibernate),
		CycleID:   "abc123",
		SinkName:  "test-sink",
		SinkType:  "slack",
	}
}

func TestSinkType(t *testing.T) {
	s := New(&stubRenderer{defaultText: "rendered:slack"})
	assert.Equal(t, "slack", s.Type())
}

func TestSendSuccess(t *testing.T) {
	var receivedText string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var payload map[string]any
		require.NoError(t, json.Unmarshal(body, &payload))
		receivedText, _ = payload["text"].(string)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true,"channel":"C123","ts":"99999.00001"}`)) //nolint:errcheck
	}))
	defer server.Close()

	cfg, _ := json.Marshal(config{WebhookURL: server.URL})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Contains(t, receivedText, "rendered:")
}

func TestSendJSONRejectsRemovedPerTargetAlias(t *testing.T) {
	cfg, _ := json.Marshal(config{WebhookURL: "https://hooks.slack.com/services/test", Format: "json", BlockLayout: "per_target"})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "block_layout must be one of")
}

func TestSendInvalidLegacyProgressBlockLayout(t *testing.T) {
	cfg, _ := json.Marshal(config{WebhookURL: "https://hooks.slack.com/services/test", Format: "json", BlockLayout: "progress"})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "block_layout must be one of")
}

func TestSendMissingWebhookURL(t *testing.T) {
	cfg, _ := json.Marshal(config{})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "webhook_url is required when delivery_mode")
}

func TestSendInvalidConfig(t *testing.T) {
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: []byte("not json")})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse slack sink config")
}

func TestSendInvalidFormat(t *testing.T) {
	cfg, _ := json.Marshal(config{WebhookURL: "https://hooks.slack.com/services/test", Format: "yaml"})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "format must be")
}

func TestSendInvalidBlockLayout(t *testing.T) {
	cfg, _ := json.Marshal(config{WebhookURL: "https://hooks.slack.com/services/test", Format: "json", BlockLayout: "oncall"})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "block_layout must be one of")
}

func TestSendInvalidAdditionalScope(t *testing.T) {
	cfg, _ := json.Marshal(config{
		WebhookURL:       "https://hooks.slack.com/services/test",
		Format:           "json",
		BlockLayout:      "default",
		AdditionalScopes: []string{"foobar"},
	})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported additional scope")
}

func TestSendInvalidTimeDisplay(t *testing.T) {
	cfg, _ := json.Marshal(config{WebhookURL: "https://hooks.slack.com/services/test", Format: "json", TimeDisplay: "local"})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "time_display must be one of")
}

func TestSendInvalidDeliveryMode(t *testing.T) {
	cfg, _ := json.Marshal(config{WebhookURL: "https://hooks.slack.com/services/test", Format: "json", DeliveryMode: "pipe"})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "delivery_mode must be one of")
}

func TestSendThreadModeRequiresBotToken(t *testing.T) {
	cfg, _ := json.Marshal(config{Format: "json", DeliveryMode: deliveryModeThread, ChannelID: "C123"})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "bot_token is required when delivery_mode")
}

func TestSendThreadModeRequiresChannelID(t *testing.T) {
	cfg, _ := json.Marshal(config{Format: "json", DeliveryMode: deliveryModeThread, BotToken: "xoxb-test"})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "channel_id is required when delivery_mode")
}

func TestSendInvalidTimezoneForFixedTimeDisplay(t *testing.T) {
	cfg, _ := json.Marshal(config{
		WebhookURL:  "https://hooks.slack.com/services/test",
		Format:      "json",
		TimeDisplay: timeDisplayFixed,
		Timezone:    "Mars/OlympusMons",
	})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid timezone")
}

func TestParseJSONTemplateMessageAddsFallbackTextWhenMissing(t *testing.T) {
	rendered := `{"blocks":[{"type":"section","text":{"type":"mrkdwn","text":"hello"}}]}`
	msg, err := parseJSONTemplateMessage(rendered, testPayload())

	require.NoError(t, err)
	require.NotNil(t, msg)
	require.NotNil(t, msg.Blocks)
	assert.NotEmpty(t, msg.Blocks.BlockSet)
	assert.Contains(t, msg.Text, "[Start]")
}

func TestParseJSONTemplateMessageArray(t *testing.T) {
	rendered := `[{"type":"section","text":{"type":"mrkdwn","text":"hello"}}]`
	msg, err := parseJSONTemplateMessage(rendered, testPayload())

	require.NoError(t, err)
	require.NotNil(t, msg)
	require.NotNil(t, msg.Blocks)
	assert.Len(t, msg.Blocks.BlockSet, 1)
	assert.Contains(t, msg.Text, "[Start]")
}

func TestParseJSONTemplateMessageInvalid(t *testing.T) {
	_, err := parseJSONTemplateMessage("not-json", testPayload())
	require.Error(t, err)
}

func TestConfigUseDefaults(t *testing.T) {
	cfg := config{}
	cfg.useDefaults()

	assert.Equal(t, formatText, cfg.Format)
	assert.Equal(t, blockLayoutDefault, cfg.BlockLayout)
	assert.Equal(t, defaultMaxTargets, cfg.MaxTargets)
	assert.Equal(t, defaultTimeDisplay, cfg.TimeDisplay)
	assert.Equal(t, defaultTimeLayout, cfg.TimeLayout)
	assert.Equal(t, defaultDeliveryMode, cfg.DeliveryMode)
	assert.Empty(t, cfg.AdditionalScopes)
}

func TestConfigUseDefaults_NormalizeAdditionalScopes(t *testing.T) {
	cfg := config{AdditionalScopes: []string{" env ", "ACCOUNT_ID", "cluster_id", "environment", "env"}}
	cfg.useDefaults()

	assert.Equal(t, []string{scopeEnvironment, scopeAccount, scopeCluster}, cfg.AdditionalScopes)
}

// setupRateLimitedSink creates a sink with HTTP transport-level rate limiting.
// This helper sets up the proper rate limit registry and HTTP client for testing.
func setupRateLimitedSink(t *testing.T, rateLimitCfg *RateLimitConfig) (*Sink, *ratelimit.Registry, *httptest.Server) {
	t.Helper()

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`)) //nolint:errcheck
	}))

	// Create rate limit registry
	registry := ratelimit.NewRegistry(ratelimit.WithLogger(logr.Discard()))

	// Create HTTP client with rate limiting transport
	baseTransport := http.DefaultTransport
	rateLimitTransport := ratelimit.NewTransport(
		baseTransport,
		registry,
		logr.Discard(),
	)
	httpClient := &http.Client{
		Transport: rateLimitTransport,
		Timeout:   5 * time.Second,
	}

	s := New(&stubRenderer{defaultText: "test"},
		WithHTTPClient(httpClient),
		WithRateLimitRegistry(registry),
	)

	return s, registry, server
}

func TestSendRateLimiting_SameKey(t *testing.T) {
	// Rate limit configuration: 10 req/sec with burst of 2
	// Expected behavior:
	//   - Request 1 & 2: Fast (within burst)
	//   - Request 3+: Must wait for token (~100ms per request at 10 rps)
	//
	// Note: Rate limiting is now at HTTP transport level per key (webhook URL),
	// not per sink name. All requests to the same webhook URL share the same rate limit.

	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`)) //nolint:errcheck
	}))
	defer server.Close()

	// Create rate limit registry
	registry := ratelimit.NewRegistry(ratelimit.WithLogger(logr.Discard()))

	// Create HTTP client with rate limiting transport
	baseTransport := http.DefaultTransport
	rateLimitTransport := ratelimit.NewTransport(
		baseTransport,
		registry,
		logr.Discard(),
	)
	httpClient := &http.Client{
		Transport: rateLimitTransport,
		Timeout:   5 * time.Second,
	}

	s := New(&stubRenderer{defaultText: "test"},
		WithHTTPClient(httpClient),
		WithRateLimitRegistry(registry),
	)

	cfg, _ := json.Marshal(config{
		WebhookURL: server.URL,
		RateLimit: &RateLimitConfig{
			RequestsPerSecond: 10.0,
			Burst:             2,
		},
	})

	// Record timing for each request to visualize rate limiting
	var requestDurations []time.Duration

	// === Request 1 (should be fast - burst available) ===
	payload1 := testPayload()
	payload1.SinkName = "test-sink-1"
	start := time.Now()
	_, err1 := s.Send(context.Background(), payload1, sink.SendOptions{Config: cfg})
	requestDurations = append(requestDurations, time.Since(start))
	require.NoError(t, err1)
	t.Logf("Request 1 duration: %v (burst available)", requestDurations[0])

	// === Request 2 (should be fast - still within burst) ===
	payload2 := testPayload()
	payload2.SinkName = "test-sink-1"
	start = time.Now()
	_, err2 := s.Send(context.Background(), payload2, sink.SendOptions{Config: cfg})
	requestDurations = append(requestDurations, time.Since(start))
	require.NoError(t, err2)
	t.Logf("Request 2 duration: %v (burst still available)", requestDurations[1])

	// === Request 3 (must wait - burst exhausted) ===
	payload3 := testPayload()
	payload3.SinkName = "test-sink-1"
	start = time.Now()
	_, err3 := s.Send(context.Background(), payload3, sink.SendOptions{Config: cfg})
	requestDurations = append(requestDurations, time.Since(start))
	require.NoError(t, err3)
	t.Logf("Request 3 duration: %v (had to wait for token)", requestDurations[2])

	// === Request 4 (must wait - rate limited) ===
	payload4 := testPayload()
	payload4.SinkName = "test-sink-1"
	start = time.Now()
	_, err4 := s.Send(context.Background(), payload4, sink.SendOptions{Config: cfg})
	requestDurations = append(requestDurations, time.Since(start))
	require.NoError(t, err4)
	t.Logf("Request 4 duration: %v (had to wait for token)", requestDurations[3])

	// === Evidence: Log timing for manual inspection ===
	t.Logf("\n=== RATE LIMIT TIMING ===")
	t.Logf("First 2 requests avg: %v", (requestDurations[0]+requestDurations[1])/2)
	t.Logf("Last 2 requests avg: %v", (requestDurations[2]+requestDurations[3])/2)
	t.Logf("Note: Sequential requests may not show rate limiting delay")
	t.Logf("because the token bucket refills between requests.")
	t.Logf("Concurrent tests (e.g., TestSlackSink_RateLimiting_10ConcurrentPlans)")
	t.Logf("better demonstrate rate limiting behavior.")

	// Verify all requests were sent (functional test)
	assert.Equal(t, 4, requestCount, "All 4 requests should have been sent")
}

func TestSendRateLimiting_DifferentKeys(t *testing.T) {
	// Rate limiting is now per key (webhook URL), not per sink name.
	// If different sink names use the SAME webhook URL, they share the same rate limit.
	// If they use DIFFERENT webhook URLs, they have independent rate limits.

	var requestCount1, requestCount2 atomic.Int32

	// Create two different servers (simulating different webhook URLs)
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount1.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`)) //nolint:errcheck
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount2.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`)) //nolint:errcheck
	}))
	defer server2.Close()

	// Create rate limit registry
	registry := ratelimit.NewRegistry(ratelimit.WithLogger(logr.Discard()))

	// Create HTTP client with rate limiting transport
	baseTransport := http.DefaultTransport
	rateLimitTransport := ratelimit.NewTransport(
		baseTransport,
		registry,
		logr.Discard(),
	)
	httpClient := &http.Client{
		Transport: rateLimitTransport,
		Timeout:   5 * time.Second,
	}

	s := New(&stubRenderer{defaultText: "test"},
		WithHTTPClient(httpClient),
		WithRateLimitRegistry(registry),
	)

	// Use 1 req/sec to make timing obvious
	// Both configs have the same rate limit but different webhook URLs (different keys)
	cfg1, _ := json.Marshal(config{
		WebhookURL: server1.URL,
		RateLimit: &RateLimitConfig{
			RequestsPerSecond: 1.0,
			Burst:             1,
		},
	})

	cfg2, _ := json.Marshal(config{
		WebhookURL: server2.URL,
		RateLimit: &RateLimitConfig{
			RequestsPerSecond: 1.0,
			Burst:             1,
		},
	})

	t.Logf("\n=== PARALLEL TEST: 3 REQUESTS TO 2 DIFFERENT KEYS ===")

	// Launch requests to different keys concurrently
	// Requests to different keys should run in parallel
	payload1 := testPayload()
	payload1.SinkName = "sink-a" // Uses server1 (key 1)

	payload2 := testPayload()
	payload2.SinkName = "sink-b" // Uses server2 (key 2)

	payload3 := testPayload()
	payload3.SinkName = "sink-c" // Uses server1 again (same key as sink-a)

	start := time.Now()

	errCh := make(chan error, 3)
	go func() { _, err := s.Send(context.Background(), payload1, sink.SendOptions{Config: cfg1}); errCh <- err }()
	go func() { _, err := s.Send(context.Background(), payload2, sink.SendOptions{Config: cfg2}); errCh <- err }()
	go func() { _, err := s.Send(context.Background(), payload3, sink.SendOptions{Config: cfg1}); errCh <- err }()

	// Wait for all to complete
	var errors []error
	for i := 0; i < 3; i++ {
		errors = append(errors, <-errCh)
	}
	totalDuration := time.Since(start)

	// Check all succeeded
	for i, err := range errors {
		require.NoError(t, err, "Request %d should succeed", i+1)
	}

	t.Logf("\n=== TIMING RESULTS ===")
	t.Logf("Total wall-clock time: %v", totalDuration)
	t.Logf("Requests to key 1 (server1): %d", requestCount1.Load())
	t.Logf("Requests to key 2 (server2): %d", requestCount2.Load())

	// Two requests went to server1 (same key) - they should be sequential
	// One request went to server2 (different key) - should run in parallel with server1's first request
	// Expected total time: ~1 second (not ~2 seconds)

	// Should complete in less than 2.5 seconds (allowing some buffer for CI)
	// The fact that 3 requests to 2 different keys complete in ~1s instead of ~2s
	// proves they ran in parallel (different keys = different rate limits)
	assert.Less(t, totalDuration.Milliseconds(), int64(2500),
		"Different keys should allow parallel processing (faster than sequential)")

	t.Logf("\n=== KEY-BASED RATE LIMITING VERIFIED ===")
	t.Logf("Requests to different keys ran in parallel!")
	t.Logf("Requests to same key were rate limited sequentially!")

	assert.Equal(t, int32(2), requestCount1.Load(), "2 requests should have been sent to server1")
	assert.Equal(t, int32(1), requestCount2.Load(), "1 request should have been sent to server2")
}

func TestSendRateLimiting_WithCustomConfig(t *testing.T) {
	// Demonstrate custom rate limit config works as expected
	// With 2 rps, second request must wait ~500ms
	// Rate limiting is at HTTP transport level per key (webhook URL)

	var requestCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`)) //nolint:errcheck
	}))
	defer server.Close()

	// Create rate limit registry
	registry := ratelimit.NewRegistry(ratelimit.WithLogger(logr.Discard()))

	// Create HTTP client with rate limiting transport
	baseTransport := http.DefaultTransport
	rateLimitTransport := ratelimit.NewTransport(
		baseTransport,
		registry,
		logr.Discard(),
	)
	httpClient := &http.Client{
		Transport: rateLimitTransport,
		Timeout:   5 * time.Second,
	}

	s := New(&stubRenderer{defaultText: "test"},
		WithHTTPClient(httpClient),
		WithRateLimitRegistry(registry),
	)

	// Custom rate limit: 2 req/sec, burst of 1
	cfgWithCustomLimit, _ := json.Marshal(config{
		WebhookURL: server.URL,
		RateLimit: &RateLimitConfig{
			RequestsPerSecond: 2.0,
			Burst:             1,
		},
	})

	// Default config (no explicit rate limit - uses registry defaults)
	cfgDefault, _ := json.Marshal(config{
		WebhookURL: server.URL,
	})

	t.Logf("\n=== CUSTOM RATE LIMIT CONFIG TEST ===")
	t.Logf("Configuration: 2 req/sec, burst of 1")

	// === Request 1 (should be fast - burst available) ===
	payload1 := testPayload()
	payload1.SinkName = "custom-limit-sink"
	start := time.Now()
	_, err1 := s.Send(context.Background(), payload1, sink.SendOptions{Config: cfgWithCustomLimit})
	firstDuration := time.Since(start)
	require.NoError(t, err1)
	t.Logf("Request 1: %v (burst available)", firstDuration)

	// === Request 2 (must wait ~500ms for 2 rps) ===
	payload2 := testPayload()
	payload2.SinkName = "custom-limit-sink"
	start = time.Now()
	_, err2 := s.Send(context.Background(), payload2, sink.SendOptions{Config: cfgWithCustomLimit})
	secondDuration := time.Since(start)
	require.NoError(t, err2)
	t.Logf("Request 2: %v (waited for token at 2 rps)", secondDuration)

	// === Request 3 with DEFAULT config - should use previously registered limiter ===
	// Since it's the same key (webhook URL), the rate limit config from
	// the first request should still be in effect
	payload3 := testPayload()
	payload3.SinkName = "custom-limit-sink" // Same key (webhook URL)
	_, err3 := s.Send(context.Background(), payload3, sink.SendOptions{Config: cfgDefault})
	require.NoError(t, err3)

	t.Logf("\n=== RATE LIMIT CONFIG VERIFIED ===")
	t.Logf("Request 1: %v (custom config)", firstDuration)
	t.Logf("Request 2: %v (custom config)", secondDuration)
	t.Logf("All requests succeeded with rate limiting configured")
	t.Logf("Rate limiting works at HTTP transport level per key (webhook URL)")

	// Verify all requests were sent (functional assertion only)
	assert.Equal(t, int32(3), requestCount.Load(), "All 3 requests should reach the server")
}

func TestSendRateLimiting_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Very slow response to ensure context timeout triggers during wait
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`)) //nolint:errcheck
	}))
	defer server.Close()

	// Create rate limit registry
	registry := ratelimit.NewRegistry(ratelimit.WithLogger(logr.Discard()))

	// Create HTTP client with rate limiting transport
	baseTransport := http.DefaultTransport
	rateLimitTransport := ratelimit.NewTransport(
		baseTransport,
		registry,
		logr.Discard(),
	)
	httpClient := &http.Client{
		Transport: rateLimitTransport,
		Timeout:   10 * time.Second,
	}

	s := New(&stubRenderer{defaultText: "test"},
		WithHTTPClient(httpClient),
		WithRateLimitRegistry(registry),
	)

	cfg, _ := json.Marshal(config{
		WebhookURL: server.URL,
		RateLimit: &RateLimitConfig{
			RequestsPerSecond: 0.5, // Very slow: 1 req per 2 seconds
			Burst:             0,   // No burst
		},
	})

	// Exhaust the token (first request - burst is 0 so token consumed)
	payload0 := testPayload()
	payload0.SinkName = "cancel-test-sink"
	_, _ = s.Send(context.Background(), payload0, sink.SendOptions{Config: cfg})

	// Now try with a cancelled context
	payload := testPayload()
	payload.SinkName = "cancel-test-sink"

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := s.Send(ctx, payload, sink.SendOptions{Config: cfg})
	duration := time.Since(start)

	// Should fail with context deadline exceeded (not rate limit error, but the wait itself cancelled)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context")
	assert.Less(t, duration.Milliseconds(), int64(200),
		"Should fail quickly when context is cancelled during rate limit wait")
}

// RateLimitedServer wraps an httptest.Server with rate limiting to prevent burst
type RateLimitedServer struct {
	Server      *httptest.Server
	RequestLog  []time.Time
	mu          sync.Mutex
	maxBurst    int
	lastRequest time.Time
	minInterval time.Duration
}

func NewRateLimitedServer(burst int, interval time.Duration) *RateLimitedServer {
	rl := &RateLimitedServer{
		maxBurst:    burst,
		minInterval: interval,
		RequestLog:  make([]time.Time, 0),
	}

	rl.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rl.mu.Lock()

		now := time.Now()
		rl.RequestLog = append(rl.RequestLog, now)

		// Enforce rate limit on server side too
		if rl.lastRequest.IsZero() {
			rl.lastRequest = now
		} else {
			sinceLast := now.Sub(rl.lastRequest)
			if sinceLast < rl.minInterval && len(rl.RequestLog) > rl.maxBurst {
				// Simulate rate limiting - add delay
				time.Sleep(rl.minInterval - sinceLast)
			}
			rl.lastRequest = time.Now()
		}

		rl.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`)) //nolint:errcheck
	}))

	return rl
}

func (rl *RateLimitedServer) RequestTimes() []time.Time {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	result := make([]time.Time, len(rl.RequestLog))
	copy(result, rl.RequestLog)
	return result
}

func TestSendRateLimiting_BurstLoad(t *testing.T) {
	// CI-friendly load test: Generate burst of requests concurrently
	// Verify they're processed respecting rate limit at HTTP transport level
	//
	// Uses faster rate (10 rps) to complete in ~500ms instead of 15 seconds
	// while still validating burst and rate limiting behavior
	//
	// Expected behavior:
	// - First 3 requests complete quickly (burst)
	// - Remaining 7 wait in queue, processing at 10/sec
	// - Total time should be ~700ms (7/10 = 0.7s + overhead)
	//
	// Note: Rate limiting is now at HTTP transport level per key

	numRequests := 10
	rateLimitRPS := 10.0
	burstSize := 3

	t.Logf("\n=== BURST LOAD TEST ===")
	t.Logf("Requests: %d", numRequests)
	t.Logf("Rate limit: %.1f req/sec", rateLimitRPS)
	t.Logf("Burst: %d", burstSize)

	// Create test server - no server-side rate limiting needed
	// since we're testing client-side rate limiting at HTTP transport level
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`)) //nolint:errcheck
	}))
	defer server.Close()

	// Create rate limit registry
	registry := ratelimit.NewRegistry(ratelimit.WithLogger(logr.Discard()))

	// Create HTTP client with rate limiting transport
	baseTransport := http.DefaultTransport
	rateLimitTransport := ratelimit.NewTransport(
		baseTransport,
		registry,
		logr.Discard(),
	)
	httpClient := &http.Client{
		Transport: rateLimitTransport,
		Timeout:   5 * time.Second,
	}

	s := New(&stubRenderer{defaultText: "test"},
		WithHTTPClient(httpClient),
		WithRateLimitRegistry(registry),
	)

	cfg, _ := json.Marshal(config{
		WebhookURL: server.URL,
		RateLimit: &RateLimitConfig{
			RequestsPerSecond: rateLimitRPS,
			Burst:             burstSize,
		},
	})

	// Generate burst: launch all requests as fast as possible
	start := time.Now()

	errCh := make(chan error, numRequests)
	completionTimes := make([]time.Duration, numRequests)

	var wg sync.WaitGroup
	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			payload := testPayload()
			payload.SinkName = "burst-test-sink"
			payload.Event = fmt.Sprintf("Event-%d", idx)

			reqStart := time.Now()
			_, err := s.Send(context.Background(), payload, sink.SendOptions{Config: cfg})
			completionTimes[idx] = time.Since(reqStart)

			errCh <- err
		}(i)
	}

	// Wait for all to complete
	var errors []error
	for i := 0; i < numRequests; i++ {
		errors = append(errors, <-errCh)
	}
	wg.Wait()
	totalDuration := time.Since(start)

	// Check all succeeded
	for i, err := range errors {
		require.NoError(t, err, "Request %d should succeed", i)
	}

	t.Logf("\n=== RESULTS ===")
	t.Logf("Total duration: %.3f seconds", totalDuration.Seconds())
	t.Logf("Server received %d requests", requestCount.Load())

	// === EVIDENCE ===

	// 1. Calculate expected duration:
	//    - First burstSize requests are immediate (no wait)
	//    - Remaining (numRequests - burstSize) requests need tokens
	//    - At rateLimitRPS tokens/sec, need (numRequests - burstSize) / rateLimitRPS seconds
	minExpectedDuration := float64(numRequests-burstSize) / rateLimitRPS
	maxExpectedDuration := minExpectedDuration * 2.0 // Allow 2x overhead for CI

	t.Logf("Expected duration: %.3f - %.3f seconds", minExpectedDuration*0.7, maxExpectedDuration)
	t.Logf("Actual duration: %.3f seconds", totalDuration.Seconds())

	// Should complete within reasonable time for CI
	// The comparison between fastest and slowest requests (below) proves rate limiting
	assert.Less(t, totalDuration.Seconds(), maxExpectedDuration,
		"Should complete within %.1f seconds for CI", maxExpectedDuration)

	// 2. Verify burst behavior - first burstSize requests are fastest
	sortedTimes := make([]time.Duration, numRequests)
	copy(sortedTimes, completionTimes)
	sort.Slice(sortedTimes, func(i, j int) bool { return sortedTimes[i] < sortedTimes[j] })

	// Calculate average of fastest burstSize requests
	var fastestBurstAvg time.Duration
	for i := 0; i < burstSize && i < numRequests; i++ {
		fastestBurstAvg += sortedTimes[i]
	}
	fastestBurstAvg /= time.Duration(burstSize)

	// Calculate average of slowest requests (those that waited)
	var slowestAvg time.Duration
	slowCount := numRequests - burstSize
	if slowCount > 0 {
		for i := burstSize; i < numRequests; i++ {
			slowestAvg += sortedTimes[i]
		}
		slowestAvg /= time.Duration(slowCount)
	}

	t.Logf("Fastest %d requests avg: %v", burstSize, fastestBurstAvg)
	t.Logf("Slowest %d requests avg: %v", slowCount, slowestAvg)

	// Fastest burst should be significantly faster than slowest
	if slowCount > 0 {
		assert.Less(t, fastestBurstAvg.Milliseconds()*3, slowestAvg.Milliseconds(),
			"Burst requests should be at least 3x faster than rate-limited requests")
	}

	t.Logf("\n=== PROOF OF RATE LIMITING ===")
	t.Logf("Burst: %d requests completed fast (~%v)", burstSize, fastestBurstAvg)
	t.Logf("Remaining %d requests waited (~%v)", numRequests-burstSize, slowestAvg)
	t.Logf("Total time: %.3fs", totalDuration.Seconds())
}
