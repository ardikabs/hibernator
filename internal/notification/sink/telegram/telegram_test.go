/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"github.com/ardikabs/hibernator/internal/notification/sink"
)

// stubRenderer implements sink.Renderer for tests.
type stubRenderer struct{}

func (r *stubRenderer) Render(_ context.Context, _ string, _ sink.Payload, _ ...sink.RenderOption) string {
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
		SinkType:  "telegram",
	}
}

// sendMessageOK is a canned Telegram Bot API sendMessage success response.
const sendMessageOK = `{"ok":true,"result":{"message_id":1,"date":0,"chat":{"id":12345,"type":"private"}}}`

// newTestServer creates a test HTTP server that routes sendMessage requests
// to the provided handler. The go-telegram/bot SDK uses multipart form data
// and sends to /bot{token}/sendMessage.
func newTestServer(t *testing.T, sendHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			sendHandler(w, r)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
}

// parseMultipartField extracts a field value from a multipart form request.
func parseMultipartField(t *testing.T, r *http.Request, field string) string {
	t.Helper()
	err := r.ParseMultipartForm(1 << 20)
	require.NoError(t, err)
	return r.FormValue(field)
}

func TestSinkType(t *testing.T) {
	s := New(&stubRenderer{})
	assert.Equal(t, "telegram", s.Type())
}

func TestSendSuccess(t *testing.T) {
	var (
		receivedChatID    string
		receivedText      string
		receivedParseMode string
	)

	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)

		receivedChatID = parseMultipartField(t, r, "chat_id")
		receivedText = parseMultipartField(t, r, "text")
		receivedParseMode = parseMultipartField(t, r, "parse_mode")

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sendMessageOK)) //nolint:errcheck
	})
	defer server.Close()

	s := newWithServerURL(&stubRenderer{}, &http.Client{Timeout: 5 * time.Second}, server.URL)

	cfg, _ := json.Marshal(config{
		Token:     "my-test-token",
		ChatID:    "-100123456789",
		ParseMode: ptr.To("MarkdownV2"),
	})
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{
		Config: cfg,
	})

	require.NoError(t, err)
	assert.Equal(t, "-100123456789", receivedChatID)
	assert.Equal(t, "rendered-content", receivedText)
	assert.Equal(t, "MarkdownV2", receivedParseMode)
}

func TestSendWithHTMLParseMode(t *testing.T) {
	var receivedParseMode string

	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		receivedParseMode = parseMultipartField(t, r, "parse_mode")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sendMessageOK)) //nolint:errcheck
	})
	defer server.Close()

	s := newWithServerURL(&stubRenderer{}, &http.Client{Timeout: 5 * time.Second}, server.URL)
	cfg, _ := json.Marshal(config{Token: "token", ChatID: "12345", ParseMode: ptr.To("HTML")})
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{
		Config: cfg,
	})

	require.NoError(t, err)
	assert.Equal(t, "HTML", receivedParseMode)
}

func TestSendWithoutParseMode(t *testing.T) {
	var receivedParseMode string

	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		receivedParseMode = parseMultipartField(t, r, "parse_mode")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sendMessageOK)) //nolint:errcheck
	})
	defer server.Close()

	s := newWithServerURL(&stubRenderer{}, &http.Client{Timeout: 5 * time.Second}, server.URL)
	cfg, _ := json.Marshal(config{Token: "token", ChatID: "12345"})
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{
		Config: cfg,
	})

	require.NoError(t, err)
	assert.Equal(t, string(models.ParseModeHTML), receivedParseMode)
}

func TestSendWithChannelUsername(t *testing.T) {
	var receivedChatID string

	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		receivedChatID = parseMultipartField(t, r, "chat_id")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sendMessageOK)) //nolint:errcheck
	})
	defer server.Close()

	s := newWithServerURL(&stubRenderer{}, &http.Client{Timeout: 5 * time.Second}, server.URL)
	cfg, _ := json.Marshal(config{Token: "token", ChatID: "@mychannel"})
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{
		Config: cfg,
	})

	require.NoError(t, err)
	assert.Equal(t, "@mychannel", receivedChatID)
}

func TestSendMissingChatID(t *testing.T) {
	s := New(&stubRenderer{})
	cfg, _ := json.Marshal(config{Token: "token"})
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "chat_id is required")
}

func TestSendMissingToken(t *testing.T) {
	s := New(&stubRenderer{})
	cfg, _ := json.Marshal(config{ChatID: "12345"})
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "token is required")
}

func TestSendInvalidConfig(t *testing.T) {
	s := New(&stubRenderer{})
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: []byte("not json")})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse telegram sink config")
}

func TestSendHTTPError(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":false,"error_code":502,"description":"Bad Gateway"}`)) //nolint:errcheck
	})
	defer server.Close()

	s := newWithServerURL(&stubRenderer{}, &http.Client{Timeout: 5 * time.Second}, server.URL)
	cfg, _ := json.Marshal(config{Token: "token", ChatID: "12345"})
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{
		Config: cfg,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Bad Gateway")
}

func TestSendAPIError(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`)) //nolint:errcheck
	})
	defer server.Close()

	s := newWithServerURL(&stubRenderer{}, &http.Client{Timeout: 5 * time.Second}, server.URL)
	cfg, _ := json.Marshal(config{Token: "token", ChatID: "nonexistent"})
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{
		Config: cfg,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "chat not found")
}

func TestSendContextCanceled(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(10 * time.Second)
		w.WriteHeader(http.StatusOK)
	})
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s := newWithServerURL(&stubRenderer{}, &http.Client{Timeout: 5 * time.Second}, server.URL)
	cfg, _ := json.Marshal(config{Token: "token", ChatID: "12345"})
	err := s.Send(ctx, testPayload(), sink.SendOptions{
		Config: cfg,
	})

	require.Error(t, err)
}

func TestSendRateLimited(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 30","parameters":{"retry_after":30}}`)) //nolint:errcheck
	})
	defer server.Close()

	s := newWithServerURL(&stubRenderer{}, &http.Client{Timeout: 5 * time.Second}, server.URL)
	cfg, _ := json.Marshal(config{Token: "token", ChatID: "12345"})
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{
		Config: cfg,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Too Many Requests")
}
