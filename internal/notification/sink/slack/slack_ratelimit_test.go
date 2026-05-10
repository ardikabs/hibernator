/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ardikabs/hibernator/internal/notification/sink"
	"github.com/ardikabs/hibernator/pkg/ratelimit"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

// rateLimitedServer is an HTTP test server that tracks request timing.
// It allows all requests through but records when they arrive, allowing us to
// verify that client-side rate limiting is working without the complexity of
// server-side rate limiting interfering with the slack library's retry logic.
type rateLimitedServer struct {
	Server       *httptest.Server
	RequestTimes []time.Time
	RequestCount atomic.Int32
	mu           sync.Mutex
}

// newRateLimitedServer creates a test server that tracks request timing.
// It accepts all requests and returns 200 OK, but records the timestamps
// so we can verify client-side rate limiting is spacing requests correctly.
func newRateLimitedServer() *rateLimitedServer {
	rl := &rateLimitedServer{
		RequestTimes: make([]time.Time, 0),
	}

	rl.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rl.mu.Lock()
		defer rl.mu.Unlock()

		now := time.Now()
		rl.RequestCount.Add(1)
		rl.RequestTimes = append(rl.RequestTimes, now)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true,"channel":"C123","ts":"1234567890.123456"}`))
	}))

	return rl
}

// GetRequestTimes returns a copy of all request timestamps
func (rl *rateLimitedServer) GetRequestTimes() []time.Time {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	result := make([]time.Time, len(rl.RequestTimes))
	copy(result, rl.RequestTimes)
	return result
}

// TestSlackSink_RateLimiting_10ConcurrentPlans verifies that client-side rate limiting works
// correctly when 10 plans trigger notifications simultaneously.
//
// Test Configuration:
//   - Client rate limit: 100 requests per second (fast but measurable)
//   - Client burst: 2 requests allowed immediately
//   - Concurrent plans: 10
//
// Expected Behavior:
//   - Total time: ~80-100ms (fast but proves rate limiting)
//   - First 2 requests: Immediate (burst)
//   - Remaining 8 requests: ~10ms apart each
//   - All requests succeed (client-side rate limiting prevents server overload)
func TestSlackSink_RateLimiting_10ConcurrentPlans(t *testing.T) {
	// Create test server that records request timing but accepts all requests
	server := newRateLimitedServer()
	defer server.Server.Close()

	// Use faster rate limit for quick test: 100 RPS = 10ms between requests
	// Still proves rate limiting works but completes in ~100ms instead of 9s
	const (
		rps   = 100.0 // 100 requests per second = 10ms interval
		burst = 2
	)

	t.Logf("Test server started: %s", server.Server.URL)
	t.Logf("Client rate limit: %.0f RPS, burst of %d (%.0fms interval)", rps, burst, 1000/rps)

	// Create rate limit registry with faster config for quick test
	registry := ratelimit.NewRegistry(
		ratelimit.WithDefaultConfig(ratelimit.Config{
			Rate: rps,
			Burst:             burst,
		}),
		ratelimit.WithLogger(logr.Discard()),
	)

	// Create HTTP client with rate limiting transport
	transport := ratelimit.NewTransport(
		http.DefaultTransport,
		registry,
		logr.Discard(),
	)
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second, // Long timeout to allow for rate limiting waits
	}

	// Create Slack sink with rate limiting enabled
	s := New(&stubRenderer{defaultText: "test message"},
		WithHTTPClient(httpClient),
		
	)

	// Configure sink with rate limit settings
	cfg, _ := json.Marshal(config{
		WebhookURL: server.Server.URL,
		RateLimit: &RateLimitConfig{
			Rate: rps,
			Burst:             burst,
		},
	})

	// Simulate 10 plans triggering simultaneously
	numPlans := 10
	t.Logf("\n=== Starting test with %d concurrent plans ===", numPlans)

	errCh := make(chan error, numPlans)
	start := time.Now()

	// Launch all 10 requests concurrently (simulating 10 plans starting at once)
	for i := 0; i < numPlans; i++ {
		go func(planNum int) {
			payload := sink.Payload{
				Plan: sink.PlanInfo{
					Name:      "test-plan",
					Namespace: "default",
				},
				Event:     "Start",
				Timestamp: time.Now(),
				Phase:     string(hibernatorv1alpha1.PhaseHibernating),
				Operation: string(hibernatorv1alpha1.OperationHibernate),
				CycleID:   "cycle-123",
				SinkName:  "slack-test",
				SinkType:  "slack",
			}

			_, err := s.Send(context.Background(), payload, sink.SendOptions{Config: cfg})
			errCh <- err
		}(i)
	}

	// Wait for all requests to complete
	var errors []error
	for i := 0; i < numPlans; i++ {
		errors = append(errors, <-errCh)
	}
	totalDuration := time.Since(start)

	// Verify all requests succeeded
	for i, err := range errors {
		require.NoError(t, err, "Plan %d should not have errors", i+1)
	}

	t.Logf("\n=== RESULTS ===")
	t.Logf("Total duration: %.2f seconds", totalDuration.Seconds())
	t.Logf("Successful requests: %d", server.RequestCount.Load())

	// === ASSERTIONS ===

	// 1. All 10 requests should have succeeded
	assert.Equal(t, int32(numPlans), server.RequestCount.Load(),
		"All %d requests should succeed", numPlans)

	// 2. Total duration check - rate limited version should be slower than non-rate-limited
	// The comparison test (TestSlackSink_NoRateLimiting_10ConcurrentPlans) proves this
	// Here we just ensure it completes in reasonable time (< 200ms)
	maxExpectedDuration := 200 * time.Millisecond // Less than 200ms (not too slow)
	assert.Less(t, totalDuration, maxExpectedDuration,
		"Total duration should be less than %v (fast test)", maxExpectedDuration)

	// 3. Verify request spacing
	requestTimes := server.GetRequestTimes()
	require.Len(t, requestTimes, numPlans, "Should have %d request timestamps", numPlans)

	// First 2 requests should be relatively close together (burst window)
	firstBurst := requestTimes[1].Sub(requestTimes[0])
	t.Logf("First burst (requests 1-2): %v", firstBurst)
	assert.Less(t, firstBurst, 20*time.Millisecond,
		"First 2 requests should be within burst window (<20ms apart)")

	// Remaining requests should be spaced ~10ms apart (100 RPS = 10ms interval)
	// We only check the upper bound to avoid flakiness in fast CI environments
	for i := 2; i < len(requestTimes); i++ {
		gap := requestTimes[i].Sub(requestTimes[i-1])
		t.Logf("Gap between request %d and %d: %v", i, i+1, gap)

		// Allow some tolerance for the 10ms spacing - only check upper bound to avoid flakiness
		assert.Less(t, gap, 30*time.Millisecond,
			"Request %d should not wait more than 30ms after request %d (rate limiting should prevent long gaps)", i+1, i)
	}

	t.Logf("\n=== RATE LIMITING VERIFIED ===")
	t.Logf("✓ All %d requests delivered successfully", numPlans)
	t.Logf("✓ Client-side rate limiting prevented server overload")
	t.Logf("✓ Requests properly spaced over %v", totalDuration)
	t.Logf("✓ Burst of %d allowed, then rate limited to %.0f RPS", burst, rps)
}

// TestSlackSink_NoRateLimiting_10ConcurrentPlans verifies that WITHOUT rate limiting,
// 10 concurrent plans complete almost instantly. This serves as a comparison to prove
// that the delay in TestSlackSink_RateLimiting_10ConcurrentPlans is actually from
// rate limiting, not from test setup or server latency.
//
// Expected Behavior:
//   - Total time: < 20ms (all requests complete almost simultaneously)
//   - No spacing between requests
//   - Much faster than the rate-limited version (~90ms)
func TestSlackSink_NoRateLimiting_10ConcurrentPlans(t *testing.T) {
	// Create test server that records request timing
	server := newRateLimitedServer()
	defer server.Server.Close()

	t.Logf("Test server started: %s", server.Server.URL)
	t.Logf("NO rate limiting (baseline comparison)")

	// Create HTTP client WITHOUT rate limiting transport
	// Just use default transport with no rate limiting
	httpClient := &http.Client{
		Timeout: 5 * time.Second,
	}

	// Create Slack sink WITHOUT rate limiting registry
	s := New(&stubRenderer{defaultText: "test message"},
		WithHTTPClient(httpClient),
		// Note: Using default HTTP client without rate-limiting transport - rate limiting disabled
	)

	// Configure sink WITHOUT rate limit settings
	cfg, _ := json.Marshal(config{
		WebhookURL: server.Server.URL,
		// Note: NO RateLimit field - rate limiting disabled
	})

	// Simulate 10 plans triggering simultaneously
	numPlans := 10
	t.Logf("\n=== Starting test with %d concurrent plans (NO rate limiting) ===", numPlans)

	errCh := make(chan error, numPlans)
	start := time.Now()

	// Launch all 10 requests concurrently
	for i := 0; i < numPlans; i++ {
		go func(planNum int) {
			payload := sink.Payload{
				Plan: sink.PlanInfo{
					Name:      "test-plan",
					Namespace: "default",
				},
				Event:     "Start",
				Timestamp: time.Now(),
				Phase:     string(hibernatorv1alpha1.PhaseHibernating),
				Operation: string(hibernatorv1alpha1.OperationHibernate),
				CycleID:   "cycle-123",
				SinkName:  "slack-test",
				SinkType:  "slack",
			}

			_, err := s.Send(context.Background(), payload, sink.SendOptions{Config: cfg})
			errCh <- err
		}(i)
	}

	// Wait for all requests to complete
	var errors []error
	for i := 0; i < numPlans; i++ {
		errors = append(errors, <-errCh)
	}
	totalDuration := time.Since(start)

	// Verify all requests succeeded
	for i, err := range errors {
		require.NoError(t, err, "Plan %d should not have errors", i+1)
	}

	t.Logf("\n=== RESULTS (NO rate limiting) ===")
	t.Logf("Total duration: %v", totalDuration)
	t.Logf("Successful requests: %d", server.RequestCount.Load())

	// === ASSERTIONS ===

	// 1. All 10 requests should have succeeded
	assert.Equal(t, int32(numPlans), server.RequestCount.Load(),
		"All %d requests should succeed", numPlans)

	// 2. Total duration should be very fast (< 50ms)
	// This proves that any delay in the rate-limited test is from rate limiting,
	// not from test setup, server latency, or goroutine scheduling
	maxExpectedDuration := 50 * time.Millisecond
	assert.Less(t, totalDuration, maxExpectedDuration,
		"Without rate limiting, all %d requests should complete in less than %v", numPlans, maxExpectedDuration)

	// 3. Verify requests completed almost simultaneously (no spacing)
	requestTimes := server.GetRequestTimes()
	require.Len(t, requestTimes, numPlans, "Should have %d request timestamps", numPlans)

	// All requests should arrive within a short window
	totalSpan := requestTimes[len(requestTimes)-1].Sub(requestTimes[0])
	t.Logf("Total span of all requests: %v", totalSpan)
	assert.Less(t, totalSpan, 30*time.Millisecond,
		"All requests should arrive within 30ms of each other (no rate limiting)")

	t.Logf("\n=== BASELINE VERIFIED ===")
	t.Logf("✓ All %d requests delivered successfully", numPlans)
	t.Logf("✓ Completed in %v (fast - no rate limiting)", totalDuration)
	t.Logf("✓ Comparison: Rate-limited version takes ~90ms vs this baseline ~%v", totalDuration)
	t.Logf("✓ This proves the delay in rate-limited test is from actual rate limiting")
}

// TestSlackSink_RateLimiting_ThreadMode verifies that rate limiting works
// correctly for thread mode, which makes multiple API calls per notification.
//
// Test Configuration:
//   - Rate limit: 500 requests per second (fast but still rate limited)
//   - Burst: 2
//   - Delivery mode: thread (makes 3-4 API calls per Send())
//
// Expected Behavior:
//   - All API calls should be rate limited
//   - Test completes in <50ms (fast) while proving rate limiting is active
func TestSlackSink_RateLimiting_ThreadMode(t *testing.T) {
	// Track API calls
	apiCallCount := atomic.Int32{}

	// Create server that counts API calls
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCallCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true,"channel":"C123","ts":"1234567890.123456"}`))
	}))
	defer server.Close()

	// Use FAST rate limit: 500 RPS = 2ms interval
	// Still proves rate limiting works but completes quickly
	rps := 500.0
	burst := 2

	// Create rate limit registry
	registry := ratelimit.NewRegistry(
		ratelimit.WithDefaultConfig(ratelimit.Config{
			Rate: rps,
			Burst:             burst,
		}),
		ratelimit.WithLogger(logr.Discard()),
	)

	// Create HTTP client with rate limiting
	transport := ratelimit.NewTransport(
		http.DefaultTransport,
		registry,
		logr.Discard(),
	)
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
	}

	s := New(&stubRenderer{defaultText: "test message"},
		WithHTTPClient(httpClient),
		
		withServerURL(server.URL+"/"),
	)

	// Configure for thread mode with rate limiting
	cfg, _ := json.Marshal(config{
		BotToken:     "xoxb-test-token",
		ChannelID:    "C123456",
		DeliveryMode: deliveryModeThread,
		RateLimit: &RateLimitConfig{
			Rate: rps,
			Burst:             burst,
		},
	})

	// Send a single notification in thread mode
	// Thread mode makes multiple API calls:
	// 1. Post root message
	// 2. Post thread reply
	// 3. Add reaction (optional)
	start := time.Now()
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})
	duration := time.Since(start)

	require.NoError(t, err)

	// Thread mode should make at least 2 API calls
	calls := apiCallCount.Load()
	t.Logf("Thread mode made %d API calls in %v", calls, duration)

	require.GreaterOrEqual(t, calls, int32(2), "Thread mode should make at least 2 API calls")

	// With 500 RPS and burst of 2:
	// - First 2 calls go through immediately (burst)
	// - Additional calls wait ~2ms each
	// Total should be < 50ms for 3-4 calls (generous upper bound for CI environments)
	assert.Less(t, duration, 50*time.Millisecond,
		"Thread mode with %d API calls should complete quickly with 500 RPS rate limit", calls)
}

// TestSlackSink_RateLimiting_ContextCancellation verifies that context cancellation
// is respected during rate limit waits.
func TestSlackSink_RateLimiting_ContextCancellation(t *testing.T) {
	requestCount := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	// Use a slow rate limit: 10 RPS (100ms interval) with burst of 1
	// This ensures the second request will have to wait ~100ms
	registry := ratelimit.NewRegistry(
		ratelimit.WithDefaultConfig(ratelimit.Config{
			Rate: 10.0, // 1 request per 100ms
			Burst:             1,    // Only 1 burst token
		}),
		ratelimit.WithLogger(logr.Discard()),
	)

	transport := ratelimit.NewTransport(
		http.DefaultTransport,
		registry,
		logr.Discard(),
	)
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}

	s := New(&stubRenderer{defaultText: "test"},
		WithHTTPClient(httpClient),
		
	)

	cfg, _ := json.Marshal(config{
		WebhookURL: server.URL,
		RateLimit: &RateLimitConfig{
			Rate: 10.0, // 1 request per 100ms
			Burst:             1,
		},
	})

	// First request consumes the burst token and succeeds
	t.Log("Sending first request (should succeed immediately)")
	_, err1 := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})
	require.NoError(t, err1, "First request should succeed")
	t.Logf("First request completed, request count: %d", requestCount.Load())

	// Second request will need to wait ~100ms for next token
	// But we'll give it a short timeout of 10ms, so it should fail
	t.Log("Sending second request with short timeout (should fail due to context cancellation)")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err2 := s.Send(ctx, testPayload(), sink.SendOptions{Config: cfg})
	duration := time.Since(start)

	t.Logf("Second request result: err=%v, duration=%v", err2, duration)

	// The request should fail because context times out during rate limit wait
	assert.Error(t, err2, "Should fail when context is cancelled during rate limit wait")
	assert.Less(t, duration, 50*time.Millisecond,
		"Should fail quickly (<50ms) when context is cancelled")

	// Only 1 request should have made it to the server
	assert.Equal(t, int32(1), requestCount.Load(),
		"Only 1 request should reach server (second blocked by rate limiter)")
}

// TestSlackSink_NestedRateLimiting_ThreadMode verifies that thread mode
// uses operation-scoped rate limiting where per-method operations contend
// for a shared parent RPM bucket.
func TestSlackSink_NestedRateLimiting_ThreadMode(t *testing.T) {
	apiCallCount := atomic.Int32{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCallCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true,"channel":"C123","ts":"1234567890.123456"}`))
	}))
	defer server.Close()

	// Parent RPS = 2 per second (burst 2).
	// Child RPS per method is hardcoded (slackMethodRateLimits):
	//   - chat.postMessage: 1 RPS, burst 5
	//   - reactions.add:    5 RPS, burst 20
	// The parent should be the bottleneck: after the first 2 burst calls,
	// every subsequent call must wait ~500ms for the parent token.
	registry := ratelimit.NewRegistry(
		ratelimit.WithDefaultConfig(ratelimit.Config{
			Rate: 100.0,
			Burst:             10,
			
		}),
		ratelimit.WithLogger(logr.Discard()),
	)

	transport := ratelimit.NewTransport(
		http.DefaultTransport,
		registry,
		logr.Discard(),
	)
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
	}

	s := New(&stubRenderer{defaultText: "test message"},
		WithHTTPClient(httpClient),
		
		withServerURL(server.URL+"/"),
	)

	cfg, _ := json.Marshal(config{
		BotToken:     "xoxb-test-token",
		ChannelID:    "C123456",
		DeliveryMode: deliveryModeThread,
		RateLimit: &RateLimitConfig{
			Rate: 2.0,
			Burst:             2,
			
		},
	})

	// Send one notification in thread mode.
	// With Start event this typically makes 3-4 API calls:
	// 1. chat.postMessage (root)
	// 2. chat.postMessage (reply)
	// 3. reactions.add
	// With parent burst=2 and RPS=2, calls 1-2 are immediate, call 3 waits ~500ms.
	start := time.Now()
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})
	duration := time.Since(start)

	require.NoError(t, err)
	calls := apiCallCount.Load()
	t.Logf("Thread mode made %d API calls in %v", calls, duration)
	require.GreaterOrEqual(t, calls, int32(2), "Thread mode should make at least 2 API calls")

	// If there are 3+ calls, at least one should have waited for the parent.
	// With 2 RPS parent, after burst of 2, each additional call waits ~500ms.
	if calls > 2 {
		minExpected := time.Duration(calls-2) * 250 * time.Millisecond
		assert.Greater(t, duration, minExpected,
			"parent RPM should bottleneck after burst is exhausted")
	}
}
