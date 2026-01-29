/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-logr/logr"

	streamingv1alpha1 "github.com/ardikabs/hibernator/api/streaming/v1alpha1"
)

// WebhookClient provides HTTP-based communication with the control plane.
// This is used as a fallback when gRPC is not available.
type WebhookClient struct {
	httpClient  *http.Client
	baseURL     string
	executionID string
	tokenPath   string
	log         logr.Logger

	// log buffering
	logBuffer   []streamingv1alpha1.LogEntry
	logBufferMu sync.Mutex

	// heartbeat management
	heartbeatCtx    context.Context
	heartbeatCancel context.CancelFunc
	heartbeatWg     sync.WaitGroup

	mu sync.Mutex
}

// WebhookClientOptions configures the webhook client.
type WebhookClientOptions struct {
	BaseURL     string
	ExecutionID string
	TokenPath   string
	Timeout     time.Duration
	Log         logr.Logger
}

// NewWebhookClient creates a new webhook client for runner-to-controller communication.
func NewWebhookClient(opts WebhookClientOptions) *WebhookClient {
	if opts.TokenPath == "" {
		opts.TokenPath = DefaultTokenPath
	}
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}

	return &WebhookClient{
		httpClient: &http.Client{
			Timeout: opts.Timeout,
		},
		baseURL:     opts.BaseURL,
		executionID: opts.ExecutionID,
		tokenPath:   opts.TokenPath,
		log:         opts.Log.WithName("webhook-client"),
		logBuffer:   make([]streamingv1alpha1.LogEntry, 0, 100),
	}
}

// Connect is a no-op for the webhook client (HTTP is stateless).
func (c *WebhookClient) Connect(ctx context.Context) error {
	// Verify connectivity with a simple request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned status %d", resp.StatusCode)
	}

	c.log.Info("webhook endpoint verified", "baseURL", c.baseURL)
	return nil
}

// StartHeartbeat starts the background heartbeat goroutine.
func (c *WebhookClient) StartHeartbeat(interval time.Duration) {
	c.mu.Lock()
	if c.heartbeatCancel != nil {
		c.mu.Unlock()
		return // Already running
	}
	c.heartbeatCtx, c.heartbeatCancel = context.WithCancel(context.Background())
	c.mu.Unlock()

	if interval == 0 {
		interval = DefaultHeartbeatInterval
	}

	c.heartbeatWg.Add(1)
	go func() {
		defer c.heartbeatWg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-c.heartbeatCtx.Done():
				return
			case <-ticker.C:
				if err := c.sendHeartbeat(c.heartbeatCtx); err != nil {
					c.log.Error(err, "heartbeat failed")
				}
			}
		}
	}()
}

// StopHeartbeat stops the background heartbeat.
func (c *WebhookClient) StopHeartbeat() {
	c.mu.Lock()
	if c.heartbeatCancel != nil {
		c.heartbeatCancel()
	}
	c.mu.Unlock()
	c.heartbeatWg.Wait()
}

// Log buffers a log entry for batch sending.
func (c *WebhookClient) Log(ctx context.Context, level, message string, fields map[string]string) error {
	entry := streamingv1alpha1.LogEntry{
		ExecutionID: c.executionID,
		Timestamp:   time.Now(),
		Level:       level,
		Message:     message,
		Fields:      fields,
	}

	c.logBufferMu.Lock()
	c.logBuffer = append(c.logBuffer, entry)
	shouldFlush := len(c.logBuffer) >= 50
	c.logBufferMu.Unlock()

	if shouldFlush {
		return c.FlushLogs(ctx)
	}

	return nil
}

// FlushLogs sends all buffered logs to the server.
func (c *WebhookClient) FlushLogs(ctx context.Context) error {
	c.logBufferMu.Lock()
	if len(c.logBuffer) == 0 {
		c.logBufferMu.Unlock()
		return nil
	}
	logs := c.logBuffer
	c.logBuffer = make([]streamingv1alpha1.LogEntry, 0, 100)
	c.logBufferMu.Unlock()

	body, err := json.Marshal(logs)
	if err != nil {
		return fmt.Errorf("failed to marshal logs: %w", err)
	}

	resp, err := c.doRequest(ctx, "POST", "/v1alpha1/logs", body)
	if err != nil {
		// Re-buffer logs on failure
		c.logBufferMu.Lock()
		c.logBuffer = append(logs, c.logBuffer...)
		c.logBufferMu.Unlock()
		return err
	}
	defer resp.Body.Close()

	c.log.V(1).Info("flushed logs", "count", len(logs))
	return nil
}

// ReportProgress sends a progress update to the server.
func (c *WebhookClient) ReportProgress(ctx context.Context, phase string, percent int32, message string) error {
	report := streamingv1alpha1.ProgressReport{
		ExecutionID:     c.executionID,
		Phase:           phase,
		ProgressPercent: percent,
		Message:         message,
	}

	body, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("failed to marshal progress: %w", err)
	}

	resp, err := c.doRequest(ctx, "POST", "/v1alpha1/progress", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var response streamingv1alpha1.ProgressResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if !response.Acknowledged {
		return fmt.Errorf("progress not acknowledged")
	}

	return nil
}

// ReportCompletion sends a completion report to the server.
func (c *WebhookClient) ReportCompletion(ctx context.Context, success bool, errorMsg string, durationMs int64, restoreData []byte) error {
	// Flush remaining logs first
	if err := c.FlushLogs(ctx); err != nil {
		c.log.Error(err, "failed to flush logs before completion")
	}

	report := streamingv1alpha1.CompletionReport{
		ExecutionID:  c.executionID,
		Success:      success,
		ErrorMessage: errorMsg,
		DurationMs:   durationMs,
		RestoreData:  restoreData,
	}

	body, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("failed to marshal completion: %w", err)
	}

	resp, err := c.doRequest(ctx, "POST", "/v1alpha1/completion", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var response streamingv1alpha1.CompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if !response.Acknowledged {
		return fmt.Errorf("completion not acknowledged")
	}

	c.log.Info("completion reported",
		"success", success,
		"restoreRef", response.RestoreRef,
	)

	return nil
}

// Close stops the heartbeat (HTTP connections are stateless).
func (c *WebhookClient) Close() error {
	c.StopHeartbeat()
	return nil
}

// sendHeartbeat sends a single heartbeat.
func (c *WebhookClient) sendHeartbeat(ctx context.Context) error {
	payload := streamingv1alpha1.WebhookPayload{
		Type: "heartbeat",
		Heartbeat: &streamingv1alpha1.HeartbeatRequest{
			ExecutionID: c.executionID,
			Timestamp:   time.Now(),
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal heartbeat: %w", err)
	}

	resp, err := c.doRequest(ctx, "POST", "/v1alpha1/callback", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	c.log.V(2).Info("heartbeat sent")
	return nil
}

// doRequest performs an authenticated HTTP request.
func (c *WebhookClient) doRequest(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	token, err := c.readToken()
	if err != nil {
		return nil, fmt.Errorf("failed to read token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Execution-ID", c.executionID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		return nil, fmt.Errorf("authentication failed")
	}

	if resp.StatusCode == http.StatusForbidden {
		resp.Body.Close()
		return nil, fmt.Errorf("access denied")
	}

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return resp, nil
}

// readToken reads the projected SA token from disk.
func (c *WebhookClient) readToken() (string, error) {
	data, err := os.ReadFile(c.tokenPath)
	if err != nil {
		return "", fmt.Errorf("failed to read token file: %w", err)
	}
	return string(data), nil
}
