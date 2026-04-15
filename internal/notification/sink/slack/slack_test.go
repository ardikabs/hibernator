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
