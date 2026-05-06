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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/notification/sink"
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

func TestSendRateLimiting_SameSinkName(t *testing.T) {
	// Rate limit configuration: 10 req/sec with burst of 2
	// Expected behavior:
	//   - Request 1 & 2: Fast (within burst)
	//   - Request 3+: Must wait for token (~100ms per request at 10 rps)

	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`)) //nolint:errcheck
	}))
	defer server.Close()

	cfg, _ := json.Marshal(config{
		WebhookURL: server.URL,
		RateLimit: &RateLimitConfig{
			RequestsPerSecond: 10.0,
			Burst:             2,
		},
	})

	s := New(&stubRenderer{defaultText: "test"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))

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

	// === Evidence: First two should be significantly faster than last two ===
	t.Logf("\n=== RATE LIMIT EVIDENCE ===")
	t.Logf("First 2 requests avg: %v", (requestDurations[0]+requestDurations[1])/2)
	t.Logf("Last 2 requests avg: %v", (requestDurations[2]+requestDurations[3])/2)

	// First two should be much faster (burst)
	assert.Less(t, requestDurations[0].Milliseconds(), int64(200), "Request 1 should be fast")
	// Request 2 may be slightly delayed depending on timing relative to token refill
	assert.Less(t, requestDurations[1].Milliseconds(), int64(150), "Request 2 should be relatively fast")

	// Last two should be slower (waiting for token)
	// At 10 rps, each token takes ~100ms
	assert.GreaterOrEqual(t, requestDurations[2].Milliseconds(), int64(80),
		"Request 3 should wait for rate limiter")
	assert.GreaterOrEqual(t, requestDurations[3].Milliseconds(), int64(80),
		"Request 4 should wait for rate limiter")

	// The later requests should be significantly slower than early ones
	assert.Less(t, requestDurations[0]+requestDurations[1], requestDurations[2]+requestDurations[3],
		"First two should be faster than last two combined")

	// Verify all requests were sent
	assert.Equal(t, 4, requestCount, "All 4 requests should have been sent")
}

func TestSendRateLimiting_DifferentSinkNames(t *testing.T) {
	// Each sink name has its own independent rate limiter
	// So "sink-a", "sink-b", "sink-c" should run IN PARALLEL

	var requestCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`)) //nolint:errcheck
	}))
	defer server.Close()

	// Use 1 req/sec to make timing obvious
	cfg, _ := json.Marshal(config{
		WebhookURL: server.URL,
		RateLimit: &RateLimitConfig{
			RequestsPerSecond: 1.0,
			Burst:             1,
		},
	})

	s := New(&stubRenderer{defaultText: "test"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))

	t.Logf("\n=== PARALLEL TEST: 3 DIFFERENT SINK NAMES CONCURRENTLY ===")

	// Launch all 3 requests concurrently (in parallel)
	payload1 := testPayload()
	payload1.SinkName = "sink-a"

	payload2 := testPayload()
	payload2.SinkName = "sink-b"

	payload3 := testPayload()
	payload3.SinkName = "sink-c"

	start := time.Now()

	errCh := make(chan error, 3)
	go func() { _, err := s.Send(context.Background(), payload1, sink.SendOptions{Config: cfg}); errCh <- err }()
	go func() { _, err := s.Send(context.Background(), payload2, sink.SendOptions{Config: cfg}); errCh <- err }()
	go func() { _, err := s.Send(context.Background(), payload3, sink.SendOptions{Config: cfg}); errCh <- err }()

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
	t.Logf("If shared limiter: ~3 seconds (sequential)")
	t.Logf("If independent limiters: ~1 second (parallel)")

	// === Evidence: INDEPENDENT ===
	// If they shared the same rate limiter, total would be ~3 seconds (sequential)
	// If they have independent limiters, total should be ~1 second (parallel)
	assert.Less(t, totalDuration.Milliseconds(), int64(2000),
		"3 independent sinks should complete in < 2s (proves parallelism)")

	// They should NOT take 3 seconds (which would prove they're blocking)
	assert.GreaterOrEqual(t, totalDuration.Milliseconds(), int64(900),
		"They should take at least ~1 second each (1 rps rate limit)")

	t.Logf("\n=== INDEPENDENCE PROVED ===")
	t.Logf("All 3 different sink names ran in PARALLEL!")
	t.Logf("Each waited ~1 second for their own rate limiter")

	assert.Equal(t, int32(3), requestCount.Load(), "All 3 requests should have been sent")
}

func TestSendRateLimiting_WithCustomConfig(t *testing.T) {
	// Demonstrate custom rate limit config works as expected
	// With 2 rps, second request must wait ~500ms

	var requestCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`)) //nolint:errcheck
	}))
	defer server.Close()

	// Custom rate limit: 2 req/sec, burst of 1
	cfgWithCustomLimit, _ := json.Marshal(config{
		WebhookURL: server.URL,
		RateLimit: &RateLimitConfig{
			RequestsPerSecond: 2.0,
			Burst:             1,
		},
	})

	// Default config (no explicit rate limit)
	cfgDefault, _ := json.Marshal(config{
		WebhookURL: server.URL,
	})

	s := New(&stubRenderer{defaultText: "test"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))

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

	// At 2 rps = 500ms per request
	// With burst of 1, request 2 must wait ~500ms
	assert.GreaterOrEqual(t, secondDuration.Milliseconds(), int64(400),
		"Request 2 should wait ~500ms for 2 rps rate limit")

	// === Request 3 with DEFAULT config - should still use cached limiter ===
	payload3 := testPayload()
	payload3.SinkName = "custom-limit-sink" // Same sink name = same cached limiter!
	start = time.Now()
	_, err3 := s.Send(context.Background(), payload3, sink.SendOptions{Config: cfgDefault})
	thirdDuration := time.Since(start)
	require.NoError(t, err3)
	t.Logf("Request 3: %v (uses cached limiter from request 1)", thirdDuration)

	// Should also wait because it reuses the cached limiter (2 rps)
	assert.GreaterOrEqual(t, thirdDuration.Milliseconds(), int64(400),
		"Request 3 should wait (reuses cached limiter at 2 rps)")

	t.Logf("\n=== CACHING EVIDENCE ===")
	t.Logf("Request 2 (custom config): %v", secondDuration)
	t.Logf("Request 3 (default config): %v", thirdDuration)
	t.Logf("Both waited ~500ms because they share the same limiter by sink name!")

	assert.Equal(t, int32(3), requestCount.Load())
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

	cfg, _ := json.Marshal(config{
		WebhookURL: server.URL,
		RateLimit: &RateLimitConfig{
			RequestsPerSecond: 0.5, // Very slow: 1 req per 2 seconds
			Burst:             0,   // No burst
		},
	})

	s := New(&stubRenderer{defaultText: "test"}, WithHTTPClient(&http.Client{Timeout: 10 * time.Second}))

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
	// Verify they're processed respecting rate limit
	//
	// Uses faster rate (10 rps) to complete in ~500ms instead of 15 seconds
	// while still validating burst and rate limiting behavior
	//
	// Expected behavior:
	// - First 3 requests complete quickly (burst)
	// - Remaining 7 wait in queue, processing at 10/sec
	// - Total time should be ~700ms (7/10 = 0.7s + overhead)

	numRequests := 10
	rateLimitRPS := 10.0
	burstSize := 3

	t.Logf("\n=== BURST LOAD TEST ===")
	t.Logf("Requests: %d", numRequests)
	t.Logf("Rate limit: %.1f req/sec", rateLimitRPS)
	t.Logf("Burst: %d", burstSize)

	// Create test server - no server-side rate limiting needed
	// since we're testing client-side rate limiting
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`)) //nolint:errcheck
	}))
	defer server.Close()

	cfg, _ := json.Marshal(config{
		WebhookURL: server.URL,
		RateLimit: &RateLimitConfig{
			RequestsPerSecond: rateLimitRPS,
			Burst:             burstSize,
		},
	})

	s := New(&stubRenderer{defaultText: "test"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))

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

	// Verify rate limiting is working (not completing instantly)
	assert.GreaterOrEqual(t, totalDuration.Seconds(), minExpectedDuration*0.7,
		"Should take at least ~%.0fms (proves rate limiting is active)", minExpectedDuration*0.7*1000)

	// Should complete within reasonable time for CI
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
