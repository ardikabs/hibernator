/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockStreamingClient struct {
	connectCalled          bool
	startHeartbeatCalled   bool
	stopHeartbeatCalled    bool
	closeCalled            bool
	reportProgressCalled   bool
	reportCompletionCalled bool

	connectErr          error
	reportProgressErr   error
	reportCompletionErr error

	logEntries []mockLogEntry
}

type mockLogEntry struct {
	level   string
	message string
	fields  map[string]string
}

func (m *mockStreamingClient) Connect(ctx context.Context) error {
	m.connectCalled = true
	return m.connectErr
}

func (m *mockStreamingClient) StartHeartbeat(interval time.Duration) {
	m.startHeartbeatCalled = true
}

func (m *mockStreamingClient) StopHeartbeat() {
	m.stopHeartbeatCalled = true
}

func (m *mockStreamingClient) Log(ctx context.Context, level, message string, fields map[string]string) error {
	m.logEntries = append(m.logEntries, mockLogEntry{level: level, message: message, fields: fields})
	return nil
}

func (m *mockStreamingClient) ReportProgress(ctx context.Context, phase string, percent int32, message string) error {
	m.reportProgressCalled = true
	return m.reportProgressErr
}

func (m *mockStreamingClient) ReportCompletion(ctx context.Context, success bool, errorMsg string, durationMs int64) error {
	m.reportCompletionCalled = true
	return m.reportCompletionErr
}

func (m *mockStreamingClient) Close() error {
	m.closeCalled = true
	return nil
}

func TestNewManager_NoEndpoints(t *testing.T) {
	log := logr.Discard()
	cfg := Config{
		GRPCEndpoint:         "",
		WebSocketEndpoint:    "",
		HTTPCallbackEndpoint: "",
		ControlPlaneEndpoint: "",
	}

	mgr, err := NewManager(context.Background(), log, cfg)
	require.NoError(t, err)
	assert.NotNil(t, mgr)
	assert.Nil(t, mgr.client)
}

func TestNewManager_UnknownKind_ReturnsAutoClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	log := logr.Discard()
	cfg := Config{
		GRPCEndpoint:         "",
		HTTPCallbackEndpoint: srv.URL,
	}

	mgr, err := NewManager(context.Background(), log, cfg)
	require.NoError(t, err)
	assert.NotNil(t, mgr)
}

// Note: WithTelemetrySink is tested through runner integration tests.
// It requires a non-nil underlying log sink (logr.Discard().GetSink() returns nil,
// causing DualWriteSink.Init() to panic). The runner tests use a real k8s-backed logger.

func TestManager_StartHeartbeat_NilClient(t *testing.T) {
	mgr := &Manager{client: nil, log: logr.Discard()}
	mgr.StartHeartbeat(30 * time.Second)
}

func TestManager_StartHeartbeat_WithClient(t *testing.T) {
	mockClient := &mockStreamingClient{}
	mgr := &Manager{client: mockClient, log: logr.Discard()}

	mgr.StartHeartbeat(30 * time.Second)
	assert.True(t, mockClient.startHeartbeatCalled)
}

func TestManager_Close_NilDualSink(t *testing.T) {
	mockClient := &mockStreamingClient{}
	mgr := &Manager{client: mockClient, dualSink: nil, log: logr.Discard()}

	err := mgr.Close()
	require.NoError(t, err)
	assert.True(t, mockClient.closeCalled)
}

func TestManager_ReportProgress_NilClient(t *testing.T) {
	mgr := &Manager{client: nil, log: logr.Discard()}

	mgr.ReportProgress(context.Background(), "initializing", 10, "test message")
}

func TestManager_ReportProgress_WithClient(t *testing.T) {
	mockClient := &mockStreamingClient{}
	mgr := &Manager{client: mockClient, log: logr.Discard()}

	mgr.ReportProgress(context.Background(), "initializing", 10, "test message")
	assert.True(t, mockClient.reportProgressCalled)
}

func TestManager_ReportProgress_WithClient_Error(t *testing.T) {
	mockClient := &mockStreamingClient{
		reportProgressErr: assert.AnError,
	}
	mgr := &Manager{client: mockClient, log: logr.Discard()}

	mgr.ReportProgress(context.Background(), "initializing", 10, "test message")
	assert.True(t, mockClient.reportProgressCalled)
}

func TestManager_ReportCompletion_NilClient(t *testing.T) {
	mgr := &Manager{client: nil, log: logr.Discard()}

	mgr.ReportCompletion(context.Background(), true, "", 100)
}

func TestManager_ReportCompletion_WithClient(t *testing.T) {
	mockClient := &mockStreamingClient{}
	mgr := &Manager{client: mockClient, log: logr.Discard()}

	mgr.ReportCompletion(context.Background(), true, "", 100)
	assert.True(t, mockClient.reportCompletionCalled)
}

func TestManager_ReportCompletion_WithClient_Error(t *testing.T) {
	mockClient := &mockStreamingClient{
		reportCompletionErr: assert.AnError,
	}
	mgr := &Manager{client: mockClient, log: logr.Discard()}

	mgr.ReportCompletion(context.Background(), false, "something failed", 100)
	assert.True(t, mockClient.reportCompletionCalled)
}
