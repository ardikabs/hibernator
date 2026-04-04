/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package slack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/notification/sink"
)

// stubRenderer implements sink.Renderer for tests.
type stubRenderer struct{}

func (r *stubRenderer) Render(_ context.Context, p sink.Payload, _ ...sink.RenderOption) string {
	return "rendered:" + p.SinkType
}

func testPayload() sink.Payload {
	return sink.Payload{
		Plan: sink.PlanInfo{
			Name:      "test-plan",
			Namespace: "default",
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
	s := New(&stubRenderer{})
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

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	}))
	defer server.Close()

	cfg, _ := json.Marshal(config{WebhookURL: server.URL})
	s := New(&stubRenderer{}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{
		Config: cfg,
	})

	require.NoError(t, err)
	assert.Contains(t, receivedText, "rendered:")
}

func TestSendHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error")) //nolint:errcheck
	}))
	defer server.Close()

	cfg, _ := json.Marshal(config{WebhookURL: server.URL})
	s := New(&stubRenderer{}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{
		Config: cfg,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "send slack notification")
}

func TestSendRateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("rate limited")) //nolint:errcheck
	}))
	defer server.Close()

	cfg, _ := json.Marshal(config{WebhookURL: server.URL})
	s := New(&stubRenderer{}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{
		Config: cfg,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "send slack notification")
}

func TestSendContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(10 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg, _ := json.Marshal(config{WebhookURL: server.URL})
	s := New(&stubRenderer{}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(ctx, testPayload(), sink.SendOptions{
		Config: cfg,
	})

	require.Error(t, err)
}

func TestSendInvalidURL(t *testing.T) {
	cfg, _ := json.Marshal(config{WebhookURL: "://invalid"})
	s := New(&stubRenderer{}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{
		Config: cfg,
	})

	require.Error(t, err)
}

func TestSendMissingWebhookURL(t *testing.T) {
	cfg, _ := json.Marshal(config{})
	s := New(&stubRenderer{}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "webhook_url is required")
}

func TestSendInvalidConfig(t *testing.T) {
	s := New(&stubRenderer{}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: []byte("not json")})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse slack sink config")
}
