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
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

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
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{
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
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{
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
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{
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

func TestSendJSONAutoLayoutFallsBackForNonProgressEvent(t *testing.T) {
	var bodyRaw string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		bodyRaw = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true,"channel":"C123","ts":"99999.00001"}`)) //nolint:errcheck
	}))
	defer server.Close()

	cfg, _ := json.Marshal(config{WebhookURL: server.URL, Format: "json", BlockLayout: "auto"})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true,"channel":"C123","ts":"99999.00001"}`)) //nolint:errcheck
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
	_, err := s.Send(context.Background(), p, sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Contains(t, bodyRaw, "... and 1 more target(s)")
	assert.Contains(t, bodyRaw, "alpha")
}

func TestSendJSONPresetDefaultLayoutDoesNotIncludeScopeLine(t *testing.T) {
	var bodyRaw string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		bodyRaw = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true,"channel":"C123","ts":"99999.00001"}`)) //nolint:errcheck
	}))
	defer server.Close()

	p := testPayload()
	p.Event = "Failure"
	p.Targets = []sink.TargetInfo{{
		Name:     "rds-main",
		Executor: "rds",
		State:    "Failed",
		Connector: sink.ConnectorInfo{
			AccountID:   "123456789012",
			ClusterName: "prod-eks",
		},
	}}

	cfg, _ := json.Marshal(config{WebhookURL: server.URL, Format: formatJSON, BlockLayout: blockLayoutDefault})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), p, sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.NotContains(t, bodyRaw, "*Account:* `123456789012`")
	assert.NotContains(t, bodyRaw, "*Cluster:* `prod-eks`")
}

func TestSendJSONPresetCompactLayoutDoesNotIncludeAdditionalScopes(t *testing.T) {
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
	p.Targets = []sink.TargetInfo{{
		Name:     "rds-main",
		Executor: "rds",
		State:    "Failed",
		Connector: sink.ConnectorInfo{
			AccountID:   "123456789012",
			ClusterName: "prod-eks",
			Region:      "us-east-1",
			Provider:    "aws",
		},
	}}

	cfg, _ := json.Marshal(config{WebhookURL: server.URL, Format: formatJSON, BlockLayout: blockLayoutCompact, AdditionalScopes: []string{"environment", "region", "provider"}})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), p, sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.NotContains(t, bodyRaw, "*Account:* `123456789012`")
	assert.NotContains(t, bodyRaw, "*Cluster:* `prod-eks`")
	assert.NotContains(t, bodyRaw, "*Environment:* `prod`")
	assert.NotContains(t, bodyRaw, "*Region:* `us-east-1`")
	assert.NotContains(t, bodyRaw, "*Provider:* `aws`")
}

func TestSendJSONPresetDefaultLayoutDoesNotIncludeEnvScopeAlias(t *testing.T) {
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
	p.Targets = []sink.TargetInfo{{
		Name:     "eks-app",
		Executor: "eks",
		State:    "Completed",
		Connector: sink.ConnectorInfo{
			AccountID:   "111111111111",
			ClusterName: "staging-eks",
		},
	}}

	cfg, _ := json.Marshal(config{WebhookURL: server.URL, Format: formatJSON, BlockLayout: blockLayoutDefault, AdditionalScopes: []string{"env"}})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), p, sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.NotContains(t, bodyRaw, "*Environment:* `staging`")
}

func TestSendJSONAutoLayoutIncludesScopeLine(t *testing.T) {
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
	p.Event = "ExecutionProgress"
	p.TargetExecution = &sink.TargetInfo{
		Name:     "rds-main",
		Executor: "rds",
		State:    "Running",
		Connector: sink.ConnectorInfo{
			AccountID:   "123456789012",
			ClusterName: "prod-eks",
			Region:      "us-east-1",
			Provider:    "aws",
		},
	}
	p.Plan.Labels = map[string]string{"env": "prod"}

	cfg, _ := json.Marshal(config{WebhookURL: server.URL, Format: formatJSON, BlockLayout: blockLayoutAuto, AdditionalScopes: []string{"environment", "region", "provider"}})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), p, sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Contains(t, bodyRaw, "Execution Progress")
	assert.Contains(t, bodyRaw, "*Account:* `123456789012`")
	assert.Contains(t, bodyRaw, "*Cluster:* `prod-eks`")
	assert.Contains(t, bodyRaw, "*Environment:* `prod`")
	assert.Contains(t, bodyRaw, "*Region:* `us-east-1`")
	assert.Contains(t, bodyRaw, "*Provider:* `aws`")
}

func TestSendJSONPresetContextTime_SlackDynamic(t *testing.T) {
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
	p.Timestamp = time.Date(2026, 1, 20, 15, 4, 5, 0, time.UTC)

	cfg, _ := json.Marshal(config{WebhookURL: server.URL, Format: formatJSON, BlockLayout: blockLayoutDefault, TimeDisplay: timeDisplaySlackDynamic})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), p, sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Contains(t, bodyRaw, "\\u003c!date^")
	assert.Contains(t, bodyRaw, "{time_secs}")
}

func TestSendJSONPresetContextTime_FixedTimezone(t *testing.T) {
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
	p.Timestamp = time.Date(2026, 1, 20, 15, 4, 5, 0, time.UTC)

	cfg, _ := json.Marshal(config{WebhookURL: server.URL, Format: formatJSON, BlockLayout: blockLayoutDefault, TimeDisplay: timeDisplayFixed, Timezone: "Asia/Jakarta"})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), p, sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Contains(t, bodyRaw, "Tue, 20 Jan 2026 22:04:05 WIB")
}

func TestSendJSONPresetContextTime_UTC(t *testing.T) {
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
	p.Timestamp = time.Date(2026, 1, 20, 15, 4, 5, 0, time.UTC)

	cfg, _ := json.Marshal(config{WebhookURL: server.URL, Format: formatJSON, BlockLayout: blockLayoutDefault, TimeDisplay: timeDisplayUTC})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), p, sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Contains(t, bodyRaw, "Tue, 20 Jan 2026 15:04:05 UTC")
}

func TestSendJSONPresetContextUsesCycleReference(t *testing.T) {
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
	p.Event = "ExecutionProgress"
	p.CycleID = "cycle-0001"
	p.TargetExecution = &sink.TargetInfo{Name: "rds-main", Executor: "rds", State: "Running"}

	cfg, _ := json.Marshal(config{WebhookURL: server.URL, Format: formatJSON, BlockLayout: blockLayoutAuto, TimeDisplay: timeDisplayUTC})
	s := New(&stubRenderer{defaultText: "rendered:slack"}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	_, err := s.Send(context.Background(), p, sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Contains(t, bodyRaw, "Event: *ExecutionProgress*")
	assert.Contains(t, bodyRaw, "Cycle: `cycle-0001`")
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
	_, err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Equal(t, "rendered:slack", payload["text"])
	_, hasBlocks := payload["blocks"]
	assert.False(t, hasBlocks)
}
