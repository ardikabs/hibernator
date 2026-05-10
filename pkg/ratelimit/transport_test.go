package ratelimit

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/samber/lo"
)

func TestTransportRateLimiting(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))
	defer server.Close()

	// Create registry with config
	registry := NewRegistry(
		WithDefaultConfig(Config{
			Rate:  2.0,
			Unit:  time.Second,
			Burst: 1,
		}),
		WithLogger(logr.Discard()),
	)

	// Create HTTP client with rate limiting transport
	transport := NewTransport(
		http.DefaultTransport,
		registry,
		logr.Discard(),
	)
	client := &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
	}

	key := "test-webhook-url"
	cfg := Config{Rate: 2.0, Unit: time.Second, Burst: 1}

	// Make first request (should be fast - within burst)
	start := time.Now()
	ctx1 := WithRateLimit(context.TODO(), key, cfg)
	req1 := lo.Must(http.NewRequestWithContext(ctx1, "GET", server.URL, nil))
	resp1, err1 := client.Do(req1)
	duration1 := time.Since(start)
	if err1 != nil {
		t.Fatalf("Request 1 failed: %v", err1)
	}
	resp1.Body.Close()

	// Make second request (should wait ~500ms for 2 rps)
	start = time.Now()
	ctx2 := WithRateLimit(context.TODO(), key, cfg)
	req2 := lo.Must(http.NewRequestWithContext(ctx2, "GET", server.URL, nil))
	resp2, err2 := client.Do(req2)
	duration2 := time.Since(start)
	if err2 != nil {
		t.Fatalf("Request 2 failed: %v", err2)
	}
	resp2.Body.Close()

	t.Logf("Request 1: %v", duration1)
	t.Logf("Request 2: %v", duration2)

	if duration2 < 400*time.Millisecond {
		t.Errorf("Request 2 should have waited at least 400ms for rate limit, but took %v", duration2)
	}

	_ = fmt.Sprintf("requests: %d", requestCount)
}
