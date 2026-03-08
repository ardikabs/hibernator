/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// stream_logs_test.go covers StreamLogs grpc path and webhook handler paths
// that require auth (handleProgress, handleCompletion, handleHeartbeat,
// handleCallback, and validateRequest error branches).

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	authv1 "k8s.io/api/authentication/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	streamingv1alpha1 "github.com/ardikabs/hibernator/api/streaming/v1alpha1"
	"github.com/ardikabs/hibernator/internal/streaming/auth"
	"github.com/ardikabs/hibernator/internal/streaming/types"
)

// ---- helpers ----

// buildValidWebhookServer returns a WebhookServer whose validator accepts any
// token as valid (namespace=default, serviceAccount=runner).
func buildValidWebhookServer() *WebhookServer {
	fakeClient := k8sfake.NewSimpleClientset()
	fakeClient.PrependReactor("create", "tokenreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, &authv1.TokenReview{
			Status: authv1.TokenReviewStatus{
				Authenticated: true,
				Audiences:     []string{auth.ExpectedAudience},
				User: authv1.UserInfo{
					Username: "system:serviceaccount:default:runner",
					Groups:   []string{"system:serviceaccounts"},
				},
			},
		}, nil
	})
	validator := auth.NewTokenValidator(fakeClient, logr.Discard())
	return &WebhookServer{
		execService: NewExecutionServiceServer(nil, nil, clk),
		validator:   validator,
		log:         logr.Discard(),
	}
}

// buildRejectingWebhookServer returns a WebhookServer whose validator rejects all tokens.
func buildRejectingWebhookServer() *WebhookServer {
	fakeClient := k8sfake.NewSimpleClientset()
	fakeClient.PrependReactor("create", "tokenreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, &authv1.TokenReview{
			Status: authv1.TokenReviewStatus{
				Authenticated: false,
			},
		}, nil
	})
	validator := auth.NewTokenValidator(fakeClient, logr.Discard())
	return &WebhookServer{
		execService: NewExecutionServiceServer(nil, nil, clk),
		validator:   validator,
		log:         logr.Discard(),
	}
}

// addBearerAuth sets a Bearer token Authorization header on req.
func addBearerAuth(req *http.Request) {
	req.Header.Set("Authorization", "Bearer some-valid-token")
}

// ---- validateRequest ----

func TestWebhookServer_ValidateRequest_InvalidToken(t *testing.T) {
	ws := buildRejectingWebhookServer()

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	addBearerAuth(req)

	result, err := ws.validateRequest(req)
	if err == nil {
		t.Error("expected error when token is rejected")
	}
	if result == nil {
		t.Error("expected result struct even on error")
	}
}

func TestWebhookServer_ValidateRequest_ValidToken(t *testing.T) {
	ws := buildValidWebhookServer()

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	addBearerAuth(req)

	result, err := ws.validateRequest(req)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Valid {
		t.Error("expected valid=true")
	}
}

// ---- handleProgress via HTTP handler ----

func TestWebhookServer_HandleProgress_Unauthorized(t *testing.T) {
	ws := buildRejectingWebhookServer()

	body, _ := json.Marshal(streamingv1alpha1.ProgressReport{
		ExecutionId: "exec-1",
		Phase:       "Running",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1alpha1/progress", bytes.NewReader(body))
	addBearerAuth(req)
	rec := httptest.NewRecorder()

	ws.handleProgress(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestWebhookServer_HandleProgress_Success(t *testing.T) {
	ws := buildValidWebhookServer()

	body, _ := json.Marshal(streamingv1alpha1.ProgressReport{
		ExecutionId:     "exec-1",
		Phase:           "Running",
		ProgressPercent: 50,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1alpha1/progress", bytes.NewReader(body))
	addBearerAuth(req)
	rec := httptest.NewRecorder()

	ws.handleProgress(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestWebhookServer_HandleProgress_InvalidJSON(t *testing.T) {
	ws := buildValidWebhookServer()

	req := httptest.NewRequest(http.MethodPost, "/v1alpha1/progress", bytes.NewReader([]byte(`{invalid}`)))
	addBearerAuth(req)
	rec := httptest.NewRecorder()

	ws.handleProgress(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// ---- handleCompletion via HTTP handler ----

func TestWebhookServer_HandleCompletion_Unauthorized(t *testing.T) {
	ws := buildRejectingWebhookServer()

	body, _ := json.Marshal(streamingv1alpha1.CompletionReport{
		ExecutionId: "exec-1",
		Success:     true,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1alpha1/completion", bytes.NewReader(body))
	addBearerAuth(req)
	rec := httptest.NewRecorder()

	ws.handleCompletion(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestWebhookServer_HandleCompletion_Success(t *testing.T) {
	ws := buildValidWebhookServer()

	body, _ := json.Marshal(streamingv1alpha1.CompletionReport{
		ExecutionId: "exec-1",
		Success:     true,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1alpha1/completion", bytes.NewReader(body))
	addBearerAuth(req)
	rec := httptest.NewRecorder()

	ws.handleCompletion(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestWebhookServer_HandleCompletion_InvalidJSON(t *testing.T) {
	ws := buildValidWebhookServer()

	req := httptest.NewRequest(http.MethodPost, "/v1alpha1/completion", bytes.NewReader([]byte(`{invalid}`)))
	addBearerAuth(req)
	rec := httptest.NewRecorder()

	ws.handleCompletion(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// ---- handleHeartbeat via HTTP handler ----

func TestWebhookServer_HandleHeartbeat_Unauthorized(t *testing.T) {
	ws := buildRejectingWebhookServer()

	body, _ := json.Marshal(streamingv1alpha1.HeartbeatRequest{
		ExecutionId: "exec-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1alpha1/heartbeat", bytes.NewReader(body))
	addBearerAuth(req)
	rec := httptest.NewRecorder()

	ws.handleHeartbeat(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestWebhookServer_HandleHeartbeat_Success(t *testing.T) {
	ws := buildValidWebhookServer()

	body, _ := json.Marshal(streamingv1alpha1.HeartbeatRequest{
		ExecutionId: "exec-1",
		Timestamp:   time.Now().Format(time.RFC3339),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1alpha1/heartbeat", bytes.NewReader(body))
	addBearerAuth(req)
	rec := httptest.NewRecorder()

	ws.handleHeartbeat(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestWebhookServer_HandleHeartbeat_InvalidJSON(t *testing.T) {
	ws := buildValidWebhookServer()

	req := httptest.NewRequest(http.MethodPost, "/v1alpha1/heartbeat", bytes.NewReader([]byte(`{invalid}`)))
	addBearerAuth(req)
	rec := httptest.NewRecorder()

	ws.handleHeartbeat(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// ---- handleCallback ----

func TestWebhookServer_HandleCallback_MissingExecutionID(t *testing.T) {
	ws := buildValidWebhookServer()

	// payload with known type but nil inner object (executionID stays "")
	payload := types.WebhookPayload{
		Type: "log",
		Log:  nil, // no execution ID
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1alpha1/callback", bytes.NewReader(body))
	addBearerAuth(req)
	rec := httptest.NewRecorder()

	ws.handleCallback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestWebhookServer_HandleCallback_UnknownType(t *testing.T) {
	ws := buildValidWebhookServer()

	// unknown type gives empty executionID from switch default → bad request first
	// But "unknown" doesn't match any case so executionID stays "" → bad request
	payload := map[string]interface{}{
		"type": "unknown-type",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1alpha1/callback", bytes.NewReader(body))
	addBearerAuth(req)
	rec := httptest.NewRecorder()

	ws.handleCallback(rec, req)

	// executionID will be empty → 400
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, rec.Code)
	}
}

func TestWebhookServer_HandleCallback_LogPayload(t *testing.T) {
	ws := buildValidWebhookServer()

	payload := types.WebhookPayload{
		Type: "log",
		Log: &types.LogEntry{
			ExecutionID: "exec-callbacks",
			Level:       "INFO",
			Message:     "callback test log",
			Timestamp:   time.Now(),
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1alpha1/callback", bytes.NewReader(body))
	addBearerAuth(req)
	rec := httptest.NewRecorder()

	ws.handleCallback(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestWebhookServer_HandleCallback_ProgressPayload(t *testing.T) {
	ws := buildValidWebhookServer()

	payload := types.WebhookPayload{
		Type: "progress",
		Progress: &types.ProgressReport{
			ExecutionID:     "exec-cb-progress",
			Phase:           "Running",
			ProgressPercent: 60,
			Timestamp:       time.Now(),
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1alpha1/callback", bytes.NewReader(body))
	addBearerAuth(req)
	rec := httptest.NewRecorder()

	ws.handleCallback(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestWebhookServer_HandleCallback_CompletionPayload(t *testing.T) {
	ws := buildValidWebhookServer()

	payload := types.WebhookPayload{
		Type: "completion",
		Completion: &types.CompletionReport{
			ExecutionID: "exec-cb-completion",
			Success:     true,
			Timestamp:   time.Now(),
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1alpha1/callback", bytes.NewReader(body))
	addBearerAuth(req)
	rec := httptest.NewRecorder()

	ws.handleCallback(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestWebhookServer_HandleCallback_HeartbeatPayload(t *testing.T) {
	ws := buildValidWebhookServer()

	payload := types.WebhookPayload{
		Type: "heartbeat",
		Heartbeat: &types.HeartbeatRequest{
			ExecutionID: "exec-cb-hb",
			Timestamp:   time.Now(),
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1alpha1/callback", bytes.NewReader(body))
	addBearerAuth(req)
	rec := httptest.NewRecorder()

	ws.handleCallback(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// ---- StreamLogs (gRPC mock) ----

// mockLogStream implements grpc.ClientStreamingServer[LogEntry, StreamLogsResponse].
type mockLogStream struct {
	grpc.ServerStream // embedded to satisfy RecvMsg/SendMsg and other methods
	ctx               context.Context
	entries           []*streamingv1alpha1.LogEntry
	idx               int
	recvErr           error // error to return after entries are exhausted (nil = io.EOF)
	closed            *streamingv1alpha1.StreamLogsResponse
}

func (m *mockLogStream) Recv() (*streamingv1alpha1.LogEntry, error) {
	if m.idx < len(m.entries) {
		e := m.entries[m.idx]
		m.idx++
		return e, nil
	}
	if m.recvErr != nil {
		return nil, m.recvErr
	}
	return nil, io.EOF
}

func (m *mockLogStream) SendAndClose(resp *streamingv1alpha1.StreamLogsResponse) error {
	m.closed = resp
	return nil
}

func (m *mockLogStream) Context() context.Context {
	if m.ctx == nil {
		return metadata.NewIncomingContext(context.Background(), metadata.MD{})
	}
	return m.ctx
}

func TestStreamLogs_EOF_Empty(t *testing.T) {
	svc := NewExecutionServiceServer(nil, nil, clk)

	stream := &mockLogStream{}
	err := svc.StreamLogs(stream)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if stream.closed == nil {
		t.Fatal("expected SendAndClose to be called")
	}
	if stream.closed.ReceivedCount != 0 {
		t.Errorf("ReceivedCount = %d, want 0", stream.closed.ReceivedCount)
	}
}

func TestStreamLogs_EOF_WithEntries(t *testing.T) {
	svc := NewExecutionServiceServer(nil, nil, clk)

	entries := []*streamingv1alpha1.LogEntry{
		{ExecutionId: "exec-1", Level: "INFO", Message: "first", Timestamp: time.Now().Format(time.RFC3339)},
		{ExecutionId: "exec-1", Level: "WARN", Message: "second", Timestamp: time.Now().Format(time.RFC3339)},
		{ExecutionId: "exec-1", Level: "ERROR", Message: "err-log", Timestamp: time.Now().Format(time.RFC3339)},
	}

	stream := &mockLogStream{entries: entries}
	err := svc.StreamLogs(stream)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if stream.closed == nil {
		t.Fatal("expected SendAndClose to be called")
	}
	if stream.closed.ReceivedCount != 3 {
		t.Errorf("ReceivedCount = %d, want 3", stream.closed.ReceivedCount)
	}
}

func TestStreamLogs_GracefulClosure_AfterNormalLog(t *testing.T) {
	svc := NewExecutionServiceServer(nil, nil, clk)

	entries := []*streamingv1alpha1.LogEntry{
		{ExecutionId: "exec-gc", Level: "INFO", Message: "ok", Timestamp: time.Now().Format(time.RFC3339)},
	}
	// context.Canceled triggers isGracefulStreamClosure
	stream := &mockLogStream{entries: entries, recvErr: context.Canceled}
	err := svc.StreamLogs(stream)
	if err != nil {
		t.Errorf("expected nil for graceful closure, got: %v", err)
	}
}

func TestStreamLogs_GracefulClosure_AfterErrorLog(t *testing.T) {
	svc := NewExecutionServiceServer(nil, nil, clk)

	entries := []*streamingv1alpha1.LogEntry{
		{ExecutionId: "exec-gc-err", Level: "ERROR", Message: "failed", Timestamp: time.Now().Format(time.RFC3339)},
	}
	stream := &mockLogStream{entries: entries, recvErr: context.Canceled}
	err := svc.StreamLogs(stream)
	if err != nil {
		t.Errorf("expected nil for graceful closure after error log, got: %v", err)
	}
}

func TestStreamLogs_UnexpectedError(t *testing.T) {
	svc := NewExecutionServiceServer(nil, nil, clk)

	// A non-graceful, non-EOF error should return an Internal status error
	stream := &mockLogStream{recvErr: fmt.Errorf("disk full")}
	err := svc.StreamLogs(stream)
	if err == nil {
		t.Error("expected error for unexpected stream error")
	}
}


