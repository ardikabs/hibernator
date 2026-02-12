/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/gorilla/websocket"

	streamingv1alpha1 "github.com/ardikabs/hibernator/api/streaming/v1alpha1"
)

const (
	// DefaultWebSocketPingInterval is the interval between WebSocket pings.
	DefaultWebSocketPingInterval = 30 * time.Second

	// DefaultWebSocketWriteTimeout is the timeout for writing to WebSocket.
	DefaultWebSocketWriteTimeout = 10 * time.Second

	// DefaultWebSocketReadTimeout is the timeout for reading from WebSocket.
	DefaultWebSocketReadTimeout = 60 * time.Second
)

// WebSocketMessage wraps messages sent over WebSocket.
type WebSocketMessage struct {
	Type string          `json:"type"` // "log", "progress", "completion", "heartbeat"
	Data json.RawMessage `json:"data"`
}

// WebSocketClient provides streaming communication with the control plane via WebSocket.
type WebSocketClient struct {
	conn        *websocket.Conn
	url         string
	executionID string
	tokenPath   string
	log         logr.Logger

	// heartbeat management
	heartbeatCtx    context.Context
	heartbeatCancel context.CancelFunc
	heartbeatWg     sync.WaitGroup

	// connection management
	writeMu sync.Mutex
	mu      sync.Mutex
}

// WebSocketClientOptions configures the WebSocket client.
type WebSocketClientOptions struct {
	URL         string
	ExecutionID string
	TokenPath   string
	Log         logr.Logger
}

// NewWebSocketClient creates a new WebSocket client for runner-to-controller communication.
func NewWebSocketClient(opts WebSocketClientOptions) *WebSocketClient {
	if opts.TokenPath == "" {
		opts.TokenPath = DefaultTokenPath
	}

	return &WebSocketClient{
		url:         opts.URL,
		executionID: opts.ExecutionID,
		tokenPath:   opts.TokenPath,
		log:         opts.Log.WithName("websocket-client"),
	}
}

// Connect establishes WebSocket connection to the streaming server.
func (c *WebSocketClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		return nil // Already connected
	}

	// Read authentication token
	token, err := c.readToken()
	if err != nil {
		return fmt.Errorf("failed to read auth token: %w", err)
	}

	// Build WebSocket URL with execution ID
	wsURL, err := c.buildURL()
	if err != nil {
		return fmt.Errorf("failed to build WebSocket URL: %w", err)
	}

	// Prepare HTTP headers for WebSocket upgrade
	header := http.Header{}
	header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	header.Set("X-Execution-ID", c.executionID)

	// Dial WebSocket connection
	c.log.Info("connecting to WebSocket server", "url", wsURL)
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, resp, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		if resp != nil {
			c.log.Error(err, "WebSocket connection failed", "status", resp.StatusCode)
		}
		return fmt.Errorf("failed to connect to WebSocket: %w", err)
	}
	defer func() {
		if resp != nil && resp.Body != nil {
			if err := resp.Body.Close(); err != nil {
				c.log.Error(err, "failed to close response body")
			}
		}
	}()

	c.conn = conn
	c.log.Info("WebSocket connection established")

	// Set ping/pong handlers
	c.conn.SetPingHandler(func(appData string) error {
		c.log.V(2).Info("received ping from server")
		return c.conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(DefaultWebSocketWriteTimeout))
	})

	c.conn.SetPongHandler(func(appData string) error {
		c.log.V(2).Info("received pong from server")
		return nil
	})

	return nil
}

// buildURL constructs the WebSocket URL with execution ID.
func (c *WebSocketClient) buildURL() (string, error) {
	u, err := url.Parse(c.url)
	if err != nil {
		return "", fmt.Errorf("invalid WebSocket URL: %w", err)
	}

	// Convert http:// to ws:// and https:// to wss://
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
		// Already WebSocket scheme
	default:
		return "", fmt.Errorf("unsupported URL scheme: %s", u.Scheme)
	}

	// Append execution ID to path
	u.Path = fmt.Sprintf("%s/v1alpha1/stream/%s", u.Path, c.executionID)

	return u.String(), nil
}

// readToken reads the authentication token from the token path.
func (c *WebSocketClient) readToken() (string, error) {
	data, err := os.ReadFile(c.tokenPath)
	if err != nil {
		return "", fmt.Errorf("failed to read token file: %w", err)
	}
	return string(data), nil
}

// StartHeartbeat starts the background heartbeat goroutine.
func (c *WebSocketClient) StartHeartbeat(interval time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.heartbeatCancel != nil {
		c.log.Info("heartbeat already started")
		return
	}

	c.heartbeatCtx, c.heartbeatCancel = context.WithCancel(context.Background())
	c.heartbeatWg.Add(1)

	go func() {
		defer c.heartbeatWg.Done()
		c.runHeartbeat(interval)
	}()

	c.log.Info("started heartbeat", "interval", interval)
}

// runHeartbeat sends periodic heartbeat messages.
func (c *WebSocketClient) runHeartbeat(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.heartbeatCtx.Done():
			c.log.Info("heartbeat stopped")
			return
		case <-ticker.C:
			if err := c.sendHeartbeat(); err != nil {
				c.log.Error(err, "failed to send heartbeat")
			}
		}
	}
}

// sendHeartbeat sends a heartbeat message to the server.
func (c *WebSocketClient) sendHeartbeat() error {
	heartbeat := &streamingv1alpha1.HeartbeatRequest{
		ExecutionId: c.executionID,
		Timestamp:   time.Now().Format(time.RFC3339),
	}

	data, err := json.Marshal(heartbeat)
	if err != nil {
		return fmt.Errorf("failed to marshal heartbeat: %w", err)
	}

	msg := WebSocketMessage{
		Type: "heartbeat",
		Data: data,
	}

	return c.sendMessage(msg)
}

// StopHeartbeat stops the background heartbeat.
func (c *WebSocketClient) StopHeartbeat() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.heartbeatCancel == nil {
		return
	}

	c.heartbeatCancel()
	c.heartbeatWg.Wait()
	c.heartbeatCancel = nil
	c.log.Info("heartbeat stopped")
}

// Log sends a log entry to the server.
func (c *WebSocketClient) Log(ctx context.Context, level, message string, fields map[string]string) error {
	logEntry := &streamingv1alpha1.LogEntry{
		ExecutionId: c.executionID,
		Timestamp:   time.Now().Format(time.RFC3339),
		Level:       level,
		Message:     message,
		Fields:      fields,
	}

	data, err := json.Marshal(logEntry)
	if err != nil {
		return fmt.Errorf("failed to marshal log entry: %w", err)
	}

	msg := WebSocketMessage{
		Type: "log",
		Data: data,
	}

	return c.sendMessage(msg)
}

// ReportProgress sends a progress update to the server.
func (c *WebSocketClient) ReportProgress(ctx context.Context, phase string, percent int32, message string) error {
	progress := &streamingv1alpha1.ProgressReport{
		ExecutionId:     c.executionID,
		Phase:           phase,
		ProgressPercent: percent,
		Message:         message,
		Timestamp:       time.Now().Format(time.RFC3339),
	}

	data, err := json.Marshal(progress)
	if err != nil {
		return fmt.Errorf("failed to marshal progress report: %w", err)
	}

	msg := WebSocketMessage{
		Type: "progress",
		Data: data,
	}

	return c.sendMessage(msg)
}

// ReportCompletion sends a completion report to the server.
// Note: Restore data is persisted directly by runner to ConfigMap, not sent via streaming.
func (c *WebSocketClient) ReportCompletion(ctx context.Context, success bool, errorMsg string, durationMs int64) error {
	completion := &streamingv1alpha1.CompletionReport{
		ExecutionId:  c.executionID,
		Success:      success,
		ErrorMessage: errorMsg,
		DurationMs:   durationMs,
		Timestamp:    time.Now().Format(time.RFC3339),
	}

	data, err := json.Marshal(completion)
	if err != nil {
		return fmt.Errorf("failed to marshal completion report: %w", err)
	}

	msg := WebSocketMessage{
		Type: "completion",
		Data: data,
	}

	return c.sendMessage(msg)
}

// sendMessage sends a WebSocket message to the server.
func (c *WebSocketClient) sendMessage(msg WebSocketMessage) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if c.conn == nil {
		return fmt.Errorf("WebSocket connection not established")
	}

	// Set write deadline
	if err := c.conn.SetWriteDeadline(time.Now().Add(DefaultWebSocketWriteTimeout)); err != nil {
		return fmt.Errorf("failed to set write deadline: %w", err)
	}

	// Marshal and send message
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal WebSocket message: %w", err)
	}

	if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("failed to write WebSocket message: %w", err)
	}

	c.log.V(2).Info("sent WebSocket message", "type", msg.Type)
	return nil
}

// Close closes the WebSocket connection.
func (c *WebSocketClient) Close() error {
	c.StopHeartbeat()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil
	}

	// Send close message
	err := c.conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(DefaultWebSocketWriteTimeout),
	)

	if err != nil {
		c.log.Error(err, "failed to send close message")
	}

	// Close connection
	if closeErr := c.conn.Close(); closeErr != nil {
		c.log.Error(closeErr, "failed to close WebSocket connection")
		if err == nil {
			err = closeErr
		}
	}

	c.conn = nil
	c.log.Info("WebSocket connection closed")
	return err
}
