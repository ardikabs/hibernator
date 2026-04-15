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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/notification/sink"
)

func TestSend_DeliveryModeThread_StartReturnsThreadMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true,"channel":"C123","ts":"12345.67890"}`)) //nolint:errcheck
	}))
	defer server.Close()

	p := testPayload()
	p.Event = string(hibernatorv1alpha1.EventStart)

	cfg, _ := json.Marshal(config{BotToken: "xoxb-test", ChannelID: "C123", Format: formatJSON, DeliveryMode: deliveryModeThread})
	s := newWithServerURL(&stubRenderer{defaultText: "rendered:slack"}, &http.Client{Timeout: 5 * time.Second}, server.URL+"/")
	result, err := s.Send(context.Background(), p, sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	require.NotNil(t, result.States)
	assert.Equal(t, "root_sent", result.States["slack.thread.state"])
	assert.Equal(t, "12345.67890", result.States["slack.thread.root_ts"])
	assert.Equal(t, "default/test-plan/abc123/shutdown", result.States["slack.thread.ref"])
}

func TestSend_DeliveryModeThread_ReplyUsesRootTsFromSinkState(t *testing.T) {
	var postBodyRaw string
	removeCalls := 0
	addCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		if strings.Contains(r.URL.Path, "chat.postMessage") {
			postBodyRaw = string(body)
		} else if strings.Contains(r.URL.Path, "reactions.remove") {
			removeCalls++
		} else if strings.Contains(r.URL.Path, "reactions.add") {
			addCalls++
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true,"channel":"C123","ts":"99999.00001"}`)) //nolint:errcheck
	}))
	defer server.Close()

	p := testPayload()
	p.Event = string(hibernatorv1alpha1.EventExecutionProgress)
	p.TargetExecution = &sink.TargetInfo{Name: "db", Executor: "noop", State: "Running"}

	cfg, _ := json.Marshal(config{BotToken: "xoxb-test", ChannelID: "C123", Format: formatJSON, BlockLayout: blockLayoutAuto, DeliveryMode: deliveryModeThread})
	s := newWithServerURL(&stubRenderer{defaultText: "rendered:slack"}, &http.Client{Timeout: 5 * time.Second}, server.URL+"/")
	result, err := s.Send(context.Background(), p, sink.SendOptions{
		Config: cfg,
		SinkState: map[string]string{
			"slack.thread.root_ts":       "12345.67890",
			"slack.thread.ref":           "default/test-plan/abc123/shutdown",
			"slack.thread.last_reaction": "loading",
		},
	})

	require.NoError(t, err)
	assert.Contains(t, postBodyRaw, "thread_ts=12345.67890")
	assert.Equal(t, 0, removeCalls)
	assert.Equal(t, 0, addCalls)
	require.NotNil(t, result.States)
	assert.Equal(t, "reply_sent", result.States["slack.thread.state"])
	assert.Equal(t, "default/test-plan/abc123/shutdown", result.States["slack.thread.ref"])
}

func TestSend_DeliveryModeThread_ReplyOverridesReactionOnStateChange(t *testing.T) {
	removeCalls := 0
	addCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "reactions.remove") {
			removeCalls++
		} else if strings.Contains(r.URL.Path, "reactions.add") {
			addCalls++
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true,"channel":"C123","ts":"99999.00001"}`)) //nolint:errcheck
	}))
	defer server.Close()

	p := testPayload()
	p.Event = string(hibernatorv1alpha1.EventSuccess)
	p.Targets = []sink.TargetInfo{{Name: "db", Executor: "noop", State: "Completed"}}

	cfg, _ := json.Marshal(config{BotToken: "xoxb-test", ChannelID: "C123", Format: formatJSON, BlockLayout: blockLayoutAuto, DeliveryMode: deliveryModeThread})
	s := newWithServerURL(&stubRenderer{defaultText: "rendered:slack"}, &http.Client{Timeout: 5 * time.Second}, server.URL+"/")
	result, err := s.Send(context.Background(), p, sink.SendOptions{
		Config: cfg,
		SinkState: map[string]string{
			"slack.thread.root_ts":       "12345.67890",
			"slack.thread.ref":           "default/test-plan/abc123/shutdown",
			"slack.thread.last_reaction": "loading",
		},
	})

	require.NoError(t, err)
	assert.Equal(t, 1, removeCalls)
	assert.Equal(t, 1, addCalls)
	require.NotNil(t, result.States)
	assert.Equal(t, "white_check_mark", result.States["slack.thread.last_reaction"])
}

func TestSend_DeliveryModeThread_ReplySkipsRootTsOnOperationMismatch(t *testing.T) {
	var postBodyRaw string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		if strings.Contains(r.URL.Path, "chat.postMessage") {
			postBodyRaw = string(body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true,"channel":"C123","ts":"99999.00001"}`)) //nolint:errcheck
	}))
	defer server.Close()

	p := testPayload()
	p.Event = string(hibernatorv1alpha1.EventExecutionProgress)
	p.Operation = string(hibernatorv1alpha1.OperationWakeUp)
	p.TargetExecution = &sink.TargetInfo{Name: "db", Executor: "noop", State: "Running"}

	cfg, _ := json.Marshal(config{BotToken: "xoxb-test", ChannelID: "C123", Format: formatJSON, BlockLayout: blockLayoutAuto, DeliveryMode: deliveryModeThread})
	s := newWithServerURL(&stubRenderer{defaultText: "rendered:slack"}, &http.Client{Timeout: 5 * time.Second}, server.URL+"/")
	result, err := s.Send(context.Background(), p, sink.SendOptions{
		Config: cfg,
		SinkState: map[string]string{
			"slack.thread.root_ts": "12345.67890",
			"slack.thread.ref":     "default/test-plan/abc123/shutdown",
		},
	})

	require.NoError(t, err)
	assert.NotContains(t, postBodyRaw, "thread_ts=12345.67890")
	require.NotNil(t, result.States)
	assert.Equal(t, "reply_sent", result.States["slack.thread.state"])
	assert.Equal(t, "default/test-plan/abc123/wakeup", result.States["slack.thread.ref"])
}

func TestSendJSONExecutionProgressDefaultSuppressesNonTerminal(t *testing.T) {
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	}))
	defer server.Close()

	p := testPayload()
	p.Event = "ExecutionProgress"
	p.TargetExecution = &sink.TargetInfo{Name: "rds-main", Executor: "rds", State: "Running"}

	cfg, _ := json.Marshal(config{WebhookURL: server.URL, Format: formatJSON, BlockLayout: blockLayoutDefault})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), p, sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Equal(t, 0, requestCount)
}

func TestSendJSONExecutionProgressCompactSuppressesNonTerminal(t *testing.T) {
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	}))
	defer server.Close()

	p := testPayload()
	p.Event = "ExecutionProgress"
	p.TargetExecution = &sink.TargetInfo{Name: "rds-main", Executor: "rds", State: "Pending"}

	cfg, _ := json.Marshal(config{WebhookURL: server.URL, Format: formatJSON, BlockLayout: blockLayoutCompact})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), p, sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Equal(t, 0, requestCount)
}

func TestSendJSONExecutionProgressDefaultSendsTerminalState(t *testing.T) {
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	}))
	defer server.Close()

	p := testPayload()
	p.Event = "ExecutionProgress"
	p.TargetExecution = &sink.TargetInfo{Name: "rds-main", Executor: "rds", State: "Completed"}

	cfg, _ := json.Marshal(config{WebhookURL: server.URL, Format: formatJSON, BlockLayout: blockLayoutDefault})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), p, sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Equal(t, 1, requestCount)
}

func TestSendJSONExecutionProgressAutoSendsNonTerminal(t *testing.T) {
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	}))
	defer server.Close()

	p := testPayload()
	p.Event = "ExecutionProgress"
	p.TargetExecution = &sink.TargetInfo{Name: "rds-main", Executor: "rds", State: "Running"}

	cfg, _ := json.Marshal(config{WebhookURL: server.URL, Format: formatJSON, BlockLayout: blockLayoutAuto})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), p, sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Equal(t, 1, requestCount)
}

func TestSendHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error")) //nolint:errcheck
	}))
	defer server.Close()

	cfg, _ := json.Marshal(config{WebhookURL: server.URL})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

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
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

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
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(ctx, testPayload(), sink.SendOptions{Config: cfg})

	require.Error(t, err)
}

func TestSendInvalidURL(t *testing.T) {
	cfg, _ := json.Marshal(config{WebhookURL: "://invalid"})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.Error(t, err)
}
