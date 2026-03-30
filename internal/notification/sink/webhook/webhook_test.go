/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package webhook

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
	"k8s.io/apimachinery/pkg/types"

	"github.com/ardikabs/hibernator/internal/notification/sink"
)

type stubRenderer struct {
	renderFunc func(ctx context.Context, tmplStr string, payload sink.Payload, opts ...sink.RenderOption) string
}

func (r *stubRenderer) Render(ctx context.Context, tmplStr string, payload sink.Payload, opts ...sink.RenderOption) string {
	if r.renderFunc != nil {
		return r.renderFunc(ctx, tmplStr, payload, opts...)
	}
	return "rendered-content"
}

func testPayload() sink.Payload {
	return sink.Payload{
		ID:        types.NamespacedName{Namespace: "default", Name: "test-plan"},
		Event:     "Start",
		Timestamp: time.Date(2026, 3, 28, 10, 0, 0, 0, time.UTC),
		Phase:     "Hibernating",
		Operation: "Hibernate",
		CycleID:   "abc123",
		SinkName:  "test-sink",
		SinkType:  "webhook",
	}
}

func TestSinkType(t *testing.T) {
	s := New(nil)
	assert.Equal(t, "webhook", s.Type())
}

func TestSendWithoutRenderer(t *testing.T) {
	var receivedBody webhookBody
	var receivedContentType string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		receivedContentType = r.Header.Get("Content-Type")

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &receivedBody))

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg, _ := json.Marshal(webhookConfig{URL: server.URL})
	s := New(nil, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))

	err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Equal(t, "application/json", receivedContentType)
	assert.Equal(t, "test-plan", receivedBody.Context.ID.Name)
	assert.Equal(t, "Start", receivedBody.Context.Event)
	assert.Empty(t, receivedBody.Rendered, "rendered field should be empty when enable_renderer is false")
}

func TestSendWithRendererEnabled(t *testing.T) {
	var receivedBody webhookBody

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &receivedBody))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg, _ := json.Marshal(webhookConfig{URL: server.URL, EnableRenderer: true})
	s := New(&stubRenderer{}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))

	err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Equal(t, "rendered-content", receivedBody.Rendered)
	assert.Equal(t, "Start", receivedBody.Context.Event)
}

func TestSendWithRendererEnabledAndCustomTemplate(t *testing.T) {
	var receivedTmpl string

	renderer := &stubRenderer{
		renderFunc: func(_ context.Context, tmplStr string, _ sink.Payload, _ ...sink.RenderOption) string {
			receivedTmpl = tmplStr
			return "custom-rendered"
		},
	}

	var receivedBody webhookBody
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &receivedBody))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg, _ := json.Marshal(webhookConfig{URL: server.URL, EnableRenderer: true})
	customTmpl := "{{ .Plan.Name }} custom"
	s := New(renderer, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))

	err := s.Send(context.Background(), testPayload(), sink.SendOptions{
		Config:         cfg,
		CustomTemplate: &customTmpl,
	})

	require.NoError(t, err)
	assert.Equal(t, "{{ .Plan.Name }} custom", receivedTmpl)
	assert.Equal(t, "custom-rendered", receivedBody.Rendered)
}

func TestSendWithCustomHeaders(t *testing.T) {
	var receivedAuth string
	var receivedCustom string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		receivedCustom = r.Header.Get("X-Custom-Header")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg, _ := json.Marshal(webhookConfig{
		URL: server.URL,
		Headers: map[string]string{
			"Authorization":   "Bearer test-token",
			"X-Custom-Header": "custom-value",
		},
	})
	s := New(nil, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))

	err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Equal(t, "Bearer test-token", receivedAuth)
	assert.Equal(t, "custom-value", receivedCustom)
}

func TestSendMissingURL(t *testing.T) {
	cfg, _ := json.Marshal(webhookConfig{})
	s := New(nil)

	err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "url is required")
}

func TestSendInvalidConfig(t *testing.T) {
	s := New(nil)
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: []byte("not json")})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse webhook sink config")
}

func TestSendNon2xxStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg, _ := json.Marshal(webhookConfig{URL: server.URL})
	s := New(nil, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))

	err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-2xx status: 500")
}

func TestSendRendererEnabledButNilRenderer(t *testing.T) {
	var receivedBody webhookBody

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &receivedBody))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg, _ := json.Marshal(webhookConfig{URL: server.URL, EnableRenderer: true})
	s := New(nil, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))

	err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Empty(t, receivedBody.Rendered, "rendered should be empty when renderer is nil")
}

func TestSendContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg, _ := json.Marshal(webhookConfig{URL: server.URL})
	s := New(nil, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := s.Send(ctx, testPayload(), sink.SendOptions{Config: cfg})
	require.Error(t, err)
}
