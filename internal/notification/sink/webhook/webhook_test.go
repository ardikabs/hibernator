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

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/notification/sink"
)

type stubRenderer struct {
	renderFunc func(ctx context.Context, payload sink.Payload, opts ...sink.RenderOption) string
}

func (r *stubRenderer) Render(ctx context.Context, payload sink.Payload, opts ...sink.RenderOption) string {
	if r.renderFunc != nil {
		return r.renderFunc(ctx, payload, opts...)
	}
	return "rendered-content"
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

	cfg, _ := json.Marshal(config{URL: server.URL})
	s := New(nil, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))

	err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Equal(t, "application/json", receivedContentType)
	assert.Equal(t, "test-plan", receivedBody.Context.Plan.Name)
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

	cfg, _ := json.Marshal(config{URL: server.URL, EnableRenderer: true})
	s := New(&stubRenderer{}, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))

	err := s.Send(context.Background(), testPayload(), sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	assert.Equal(t, "rendered-content", receivedBody.Rendered)
	assert.Equal(t, "Start", receivedBody.Context.Event)
}

func TestSendWithRendererEnabledAndCustomTemplate(t *testing.T) {
	var receivedOpts []sink.RenderOption

	renderer := &stubRenderer{
		renderFunc: func(_ context.Context, _ sink.Payload, opts ...sink.RenderOption) string {
			receivedOpts = opts
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

	cfg, _ := json.Marshal(config{URL: server.URL, EnableRenderer: true})
	ct := &sink.CustomTemplate{
		Content: "{{ .Plan.Name }} custom",
		Key:     types.NamespacedName{Namespace: "default", Name: "custom-tmpl"},
	}
	s := New(renderer, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))

	err := s.Send(context.Background(), testPayload(), sink.SendOptions{
		Config:         cfg,
		CustomTemplate: ct,
	})

	require.NoError(t, err)
	assert.Len(t, receivedOpts, 1, "should pass WithCustomTemplate option")
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

	cfg, _ := json.Marshal(config{
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
	cfg, _ := json.Marshal(config{})
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

	cfg, _ := json.Marshal(config{URL: server.URL})
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

	cfg, _ := json.Marshal(config{URL: server.URL, EnableRenderer: true})
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

	cfg, _ := json.Marshal(config{URL: server.URL})
	s := New(nil, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := s.Send(ctx, testPayload(), sink.SendOptions{Config: cfg})
	require.Error(t, err)
}

func TestSendWithConnectorInfo(t *testing.T) {
	var receivedBody webhookBody

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &receivedBody))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg, _ := json.Marshal(config{URL: server.URL})
	s := New(nil, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))

	p := testPayload()
	p.Targets = []sink.TargetInfo{
		{
			Name:     "eks-prod",
			Executor: "eks",
			State:    "Completed",
			Connector: sink.ConnectorInfo{
				Kind:        "K8SCluster",
				Name:        "prod-cluster",
				Provider:    "aws",
				AccountID:   "123456789012",
				Region:      "us-east-1",
				ClusterName: "prod-eks",
			},
		},
	}

	err := s.Send(context.Background(), p, sink.SendOptions{Config: cfg})

	require.NoError(t, err)
	require.Len(t, receivedBody.Context.Targets, 1)

	target := receivedBody.Context.Targets[0]
	assert.Equal(t, "eks-prod", target.Name)
	assert.Equal(t, "K8SCluster", target.Connector.Kind)
	assert.Equal(t, "prod-cluster", target.Connector.Name)
	assert.Equal(t, "aws", target.Connector.Provider)
	assert.Equal(t, "123456789012", target.Connector.AccountID)
	assert.Equal(t, "us-east-1", target.Connector.Region)
	assert.Equal(t, "prod-eks", target.Connector.ClusterName)
}

func TestSendWithTargetExecution(t *testing.T) {
	var receivedBody webhookBody

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &receivedBody))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg, _ := json.Marshal(config{URL: server.URL})
	s := New(nil, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))

	p := testPayload()
	p.Event = "ExecutionProgress"
	te := sink.TargetInfo{
		Name:     "rds-main",
		Executor: "rds",
		State:    "Running",
		Message:  "job active",
		Connector: sink.ConnectorInfo{
			Kind:      "CloudProvider",
			Name:      "aws-prod",
			Provider:  "aws",
			AccountID: "111222333444",
			Region:    "eu-west-1",
		},
	}
	p.TargetExecution = &te

	err := s.Send(context.Background(), p, sink.SendOptions{Config: cfg})
	require.NoError(t, err)

	require.NotNil(t, receivedBody.Context.TargetExecution)
	assert.Equal(t, "rds-main", receivedBody.Context.TargetExecution.Name)
	assert.Equal(t, "rds", receivedBody.Context.TargetExecution.Executor)
	assert.Equal(t, "Running", receivedBody.Context.TargetExecution.State)
	assert.Equal(t, "job active", receivedBody.Context.TargetExecution.Message)
	assert.Equal(t, "CloudProvider", receivedBody.Context.TargetExecution.Connector.Kind)
	assert.Equal(t, "aws-prod", receivedBody.Context.TargetExecution.Connector.Name)
	assert.Equal(t, "111222333444", receivedBody.Context.TargetExecution.Connector.AccountID)
}

func TestSendWithoutTargetExecution_OmitsField(t *testing.T) {
	var receivedBody webhookBody

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &receivedBody))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg, _ := json.Marshal(config{URL: server.URL})
	s := New(nil, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))

	p := testPayload()
	p.TargetExecution = nil

	err := s.Send(context.Background(), p, sink.SendOptions{Config: cfg})
	require.NoError(t, err)
	assert.Nil(t, receivedBody.Context.TargetExecution)
}
