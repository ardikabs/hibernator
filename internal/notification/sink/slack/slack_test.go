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

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	}))
	defer server.Close()

	cfg, _ := json.Marshal(config{WebhookURL: server.URL})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{
		Config: cfg,
	})

	require.NoError(t, err)
	assert.Contains(t, receivedText, "rendered:")
}

func TestSendJSONPresetWithoutTemplate(t *testing.T) {
	var payload map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &payload))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	}))
	defer server.Close()

	cfg, _ := json.Marshal(config{WebhookURL: server.URL, Format: "json", BlockLayout: "default"})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	text, _ := payload["text"].(string)
	assert.Contains(t, text, "[Start]")
	assert.Contains(t, text, "default/test-plan")

	blocks, ok := payload["blocks"].([]any)
	require.True(t, ok)
	assert.NotEmpty(t, blocks)
}

func TestSendJSONTemplateMessageObject(t *testing.T) {
	var payload map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &payload))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	}))
	defer server.Close()

	tmpl := `{"text":"custom fallback","blocks":[{"type":"section","text":{"type":"mrkdwn","text":"hello"}}]}`
	cfg, _ := json.Marshal(config{WebhookURL: server.URL, Format: "json"})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{
		Config: cfg,
		CustomTemplate: &sink.CustomTemplate{
			Content: tmpl,
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "custom fallback", payload["text"])
	blocks, ok := payload["blocks"].([]any)
	require.True(t, ok)
	assert.Len(t, blocks, 1)
}

func TestSendJSONTemplateArrayPayload(t *testing.T) {
	var payload map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &payload))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	}))
	defer server.Close()

	tmpl := `[{"type":"section","text":{"type":"mrkdwn","text":"from array"}}]`
	cfg, _ := json.Marshal(config{WebhookURL: server.URL, Format: "json"})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{
		Config:         cfg,
		CustomTemplate: &sink.CustomTemplate{Content: tmpl},
	})

	require.NoError(t, err)
	text, _ := payload["text"].(string)
	assert.Contains(t, text, "[Start]")
	blocks, ok := payload["blocks"].([]any)
	require.True(t, ok)
	assert.Len(t, blocks, 1)
}

func TestSendJSONTemplateInvalidFallsBackToPreset(t *testing.T) {
	var payload map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &payload))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	}))
	defer server.Close()

	cfg, _ := json.Marshal(config{WebhookURL: server.URL, Format: "json", BlockLayout: "compact"})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{
		Config:         cfg,
		CustomTemplate: &sink.CustomTemplate{Content: "not-json"},
	})

	require.NoError(t, err)
	text, _ := payload["text"].(string)
	assert.Contains(t, text, "[Start]")
	blocks, ok := payload["blocks"].([]any)
	require.True(t, ok)
	assert.NotEmpty(t, blocks)
}

func TestSendJSONRejectsRemovedPerTargetAlias(t *testing.T) {
	cfg, _ := json.Marshal(config{WebhookURL: "https://hooks.slack.com/services/test", Format: "json", BlockLayout: "per_target"})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "block_layout must be one of")
}

func TestSendJSONProgressLayoutFallsBackForNonProgressEvent(t *testing.T) {
	var bodyRaw string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		bodyRaw = string(body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	}))
	defer server.Close()

	cfg, _ := json.Marshal(config{WebhookURL: server.URL, Format: "json", BlockLayout: "progress"})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Contains(t, bodyRaw, "Hibernation Starting")
	assert.NotContains(t, bodyRaw, "Execution Progress")
}

func TestSendJSONPresetMaxTargets(t *testing.T) {
	var bodyRaw string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		bodyRaw = string(body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	}))
	defer server.Close()

	p := testPayload()
	p.Event = "Failure"
	p.Phase = string(hibernatorv1alpha1.PhaseError)
	p.ErrorMessage = "boom"
	p.Targets = []sink.TargetInfo{
		{Name: "zeta", Executor: "rds", State: "Completed"},
		{Name: "alpha", Executor: "eks", State: "Failed"},
		{Name: "beta", Executor: "ec2", State: "Completed"},
	}

	cfg, _ := json.Marshal(config{WebhookURL: server.URL, Format: "json", BlockLayout: "default", MaxTargets: 2})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(context.Background(), p, sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Contains(t, bodyRaw, "... and 1 more target(s)")
	assert.Contains(t, bodyRaw, "alpha")
}

func TestSendJSONPresetDefaultScopeLine(t *testing.T) {
	var bodyRaw string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		bodyRaw = string(body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	}))
	defer server.Close()

	p := testPayload()
	p.Event = "Failure"
	p.Targets = []sink.TargetInfo{
		{
			Name:     "rds-main",
			Executor: "rds",
			State:    "Failed",
			Connector: sink.ConnectorInfo{
				AccountID:   "123456789012",
				ClusterName: "prod-eks",
			},
		},
	}

	cfg, _ := json.Marshal(config{WebhookURL: server.URL, Format: formatJSON, BlockLayout: blockLayoutDefault})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(context.Background(), p, sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Contains(t, bodyRaw, "*Account:* `123456789012`")
	assert.Contains(t, bodyRaw, "*Cluster:* `prod-eks`")
}

func TestSendJSONPresetAdditionalScopes(t *testing.T) {
	var bodyRaw string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		bodyRaw = string(body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	}))
	defer server.Close()

	p := testPayload()
	p.Plan.Labels = map[string]string{"env": "prod"}
	p.Targets = []sink.TargetInfo{
		{
			Name:     "rds-main",
			Executor: "rds",
			State:    "Failed",
			Connector: sink.ConnectorInfo{
				AccountID:   "123456789012",
				ClusterName: "prod-eks",
				Region:      "us-east-1",
				Provider:    "aws",
			},
		},
	}

	cfg, _ := json.Marshal(config{
		WebhookURL:       server.URL,
		Format:           formatJSON,
		BlockLayout:      blockLayoutCompact,
		AdditionalScopes: []string{"environment", "region", "provider"},
	})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(context.Background(), p, sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Contains(t, bodyRaw, "*Account:* `123456789012`")
	assert.Contains(t, bodyRaw, "*Cluster:* `prod-eks`")
	assert.Contains(t, bodyRaw, "*Environment:* `prod`")
	assert.Contains(t, bodyRaw, "*Region:* `us-east-1`")
	assert.Contains(t, bodyRaw, "*Provider:* `aws`")
}

func TestSendJSONPresetAdditionalScopesEnvAlias(t *testing.T) {
	var bodyRaw string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		bodyRaw = string(body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	}))
	defer server.Close()

	p := testPayload()
	p.Plan.Annotations = map[string]string{"environment": "staging"}
	p.Targets = []sink.TargetInfo{
		{
			Name:     "eks-app",
			Executor: "eks",
			State:    "Completed",
			Connector: sink.ConnectorInfo{
				AccountID:   "111111111111",
				ClusterName: "staging-eks",
			},
		},
	}

	cfg, _ := json.Marshal(config{
		WebhookURL:       server.URL,
		Format:           formatJSON,
		BlockLayout:      blockLayoutDefault,
		AdditionalScopes: []string{"env"},
	})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(context.Background(), p, sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Contains(t, bodyRaw, "*Environment:* `staging`")
}

func TestSendHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error")) //nolint:errcheck
	}))
	defer server.Close()

	cfg, _ := json.Marshal(config{WebhookURL: server.URL})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
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
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
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
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(ctx, testPayload(), sink.SendOptions{
		Config: cfg,
	})

	require.Error(t, err)
}

func TestSendInvalidURL(t *testing.T) {
	cfg, _ := json.Marshal(config{WebhookURL: "://invalid"})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{
		Config: cfg,
	})

	require.Error(t, err)
}

func TestSendMissingWebhookURL(t *testing.T) {
	cfg, _ := json.Marshal(config{})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "webhook_url is required")
}

func TestSendInvalidConfig(t *testing.T) {
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: []byte("not json")})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse slack sink config")
}

func TestSendInvalidFormat(t *testing.T) {
	cfg, _ := json.Marshal(config{WebhookURL: "https://hooks.slack.com/services/test", Format: "yaml"})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "format must be")
}

func TestSendInvalidBlockLayout(t *testing.T) {
	cfg, _ := json.Marshal(config{WebhookURL: "https://hooks.slack.com/services/test", Format: "json", BlockLayout: "oncall"})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

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
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported additional scope")
}

func TestSendTextIgnoresInvalidBlockLayout(t *testing.T) {
	var payload map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &payload))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	}))
	defer server.Close()

	cfg, _ := json.Marshal(config{WebhookURL: server.URL, Format: "text", BlockLayout: "oncall"})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Equal(t, "rendered:slack", payload["text"])
	_, hasBlocks := payload["blocks"]
	assert.False(t, hasBlocks)
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
	assert.Empty(t, cfg.AdditionalScopes)
}

func TestConfigUseDefaults_NormalizeAdditionalScopes(t *testing.T) {
	cfg := config{AdditionalScopes: []string{" env ", "ACCOUNT_ID", "cluster_id", "environment", "env"}}
	cfg.useDefaults()

	assert.Equal(t, []string{scopeEnvironment, scopeAccount, scopeCluster}, cfg.AdditionalScopes)
}

func TestLayoutFactory_UnknownLayoutFallsBackToDefault(t *testing.T) {
	p := testPayload()
	composer := newLayoutComposer(p, defaultMaxTargets, nil)
	factory := newLayoutFactory()

	blocks := factory.build("unknown-layout", composer)
	require.NotEmpty(t, blocks)

	raw, err := json.Marshal(blocks)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "Hibernation Starting")
}

func TestLayoutFactory_ProgressFallsBackForNonProgressEvent(t *testing.T) {
	p := testPayload() // Event=Start
	composer := newLayoutComposer(p, defaultMaxTargets, nil)
	factory := newLayoutFactory()

	blocks := factory.build(blockLayoutProgress, composer)
	require.NotEmpty(t, blocks)

	raw, err := json.Marshal(blocks)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "Hibernation Starting")
	assert.NotContains(t, string(raw), "Execution Progress")
}

func TestLayoutFactory_ProgressUsesProgressLayoutForExecutionProgress(t *testing.T) {
	p := testPayload()
	p.Event = "ExecutionProgress"
	p.TargetExecution = &sink.TargetInfo{
		Name:     "rds-main",
		Executor: "rds",
		State:    "Running",
		Message:  "stopping",
	}
	p.Operation = "shutdown"

	composer := newLayoutComposer(p, defaultMaxTargets, nil)
	factory := newLayoutFactory()

	blocks := factory.build(blockLayoutProgress, composer)
	require.NotEmpty(t, blocks)

	raw, err := json.Marshal(blocks)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "Execution Progress")
	assert.Contains(t, string(raw), "rds-main")
	assert.Contains(t, string(raw), fmt.Sprintf("*Operation:* %s", p.Operation))
}
