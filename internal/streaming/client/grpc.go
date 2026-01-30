/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package client

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	streamingv1alpha1 "github.com/ardikabs/hibernator/api/streaming/v1alpha1"
)

const (
	// DefaultTokenPath is the default path for the projected SA token.
	DefaultTokenPath = "/var/run/secrets/stream/token"

	// DefaultHeartbeatInterval is the default interval between heartbeats.
	DefaultHeartbeatInterval = 30 * time.Second

	// DefaultReconnectDelay is the delay between reconnection attempts.
	DefaultReconnectDelay = 5 * time.Second
)

// GRPCClient provides streaming communication with the control plane.
type GRPCClient struct {
	conn        *grpc.ClientConn
	address     string
	executionID string
	tokenPath   string
	useTLS      bool
	log         logr.Logger

	// heartbeat management
	heartbeatCtx    context.Context
	heartbeatCancel context.CancelFunc
	heartbeatWg     sync.WaitGroup

	// log buffering
	logBuffer   []streamingv1alpha1.LogEntry
	logBufferMu sync.Mutex
	logFlushCh  chan struct{}

	mu sync.Mutex
}

// GRPCClientOptions configures the gRPC client.
type GRPCClientOptions struct {
	Address     string
	ExecutionID string
	TokenPath   string
	UseTLS      bool
	Log         logr.Logger
}

// NewGRPCClient creates a new gRPC client for runner-to-controller communication.
func NewGRPCClient(opts GRPCClientOptions) *GRPCClient {
	if opts.TokenPath == "" {
		opts.TokenPath = DefaultTokenPath
	}

	return &GRPCClient{
		address:     opts.Address,
		executionID: opts.ExecutionID,
		tokenPath:   opts.TokenPath,
		useTLS:      opts.UseTLS,
		log:         opts.Log.WithName("grpc-client"),
		logBuffer:   make([]streamingv1alpha1.LogEntry, 0, 100),
		logFlushCh:  make(chan struct{}, 1),
	}
}

// Connect establishes connection to the streaming server.
func (c *GRPCClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		return nil // Already connected
	}

	// Configure credentials
	var creds credentials.TransportCredentials
	if c.useTLS {
		creds = credentials.NewClientTLSFromCert(nil, "")
	} else {
		creds = insecure.NewCredentials()
	}

	// Connect with retry and context cancellation support
	var conn *grpc.ClientConn
	var err error

	for attempt := 0; attempt < 3; attempt++ {
		// Check for context cancellation before attempting
		select {
		case <-ctx.Done():
			return fmt.Errorf("connection cancelled: %w", ctx.Err())
		default:
		}

		conn, err = grpc.NewClient(
			c.address,
			grpc.WithTransportCredentials(creds),
			grpc.WithUnaryInterceptor(c.authInterceptor()),
			grpc.WithStreamInterceptor(c.streamAuthInterceptor()),
		)
		if err == nil {
			break
		}
		c.log.Error(err, "connection attempt failed", "attempt", attempt+1)

		// Wait with context cancellation support
		select {
		case <-ctx.Done():
			return fmt.Errorf("connection cancelled during retry: %w", ctx.Err())
		case <-time.After(DefaultReconnectDelay):
		}
	}

	if err != nil {
		return fmt.Errorf("failed to connect after retries: %w", err)
	}

	c.conn = conn
	c.log.Info("connected to streaming server", "address", c.address)

	return nil
}

// StartHeartbeat starts the background heartbeat goroutine.
func (c *GRPCClient) StartHeartbeat(interval time.Duration) {
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
func (c *GRPCClient) StopHeartbeat() {
	c.mu.Lock()
	if c.heartbeatCancel != nil {
		c.heartbeatCancel()
	}
	c.mu.Unlock()
	c.heartbeatWg.Wait()
}

// Log sends a log entry to the server.
func (c *GRPCClient) Log(ctx context.Context, level, message string, fields map[string]string) error {
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
func (c *GRPCClient) FlushLogs(ctx context.Context) error {
	c.logBufferMu.Lock()
	if len(c.logBuffer) == 0 {
		c.logBufferMu.Unlock()
		return nil
	}
	logs := c.logBuffer
	c.logBuffer = make([]streamingv1alpha1.LogEntry, 0, 100)
	c.logBufferMu.Unlock()

	// Note: In a real implementation with generated protobuf code,
	// this would use a streaming RPC. For now, we log locally.
	for _, log := range logs {
		c.log.V(1).Info(log.Message, "level", log.Level, "fields", log.Fields)
	}

	c.log.V(1).Info("flushed logs", "count", len(logs))
	return nil
}

// ReportProgress sends a progress update to the server.
func (c *GRPCClient) ReportProgress(ctx context.Context, phase string, percent int32, message string) error {
	c.log.Info("reporting progress",
		"phase", phase,
		"percent", percent,
		"message", message,
	)

	// Note: In a real implementation with generated protobuf code,
	// this would call the ReportProgress RPC.
	// For now, just log locally.

	return nil
}

// ReportCompletion sends a completion report to the server.
func (c *GRPCClient) ReportCompletion(ctx context.Context, success bool, errorMsg string, durationMs int64, restoreData []byte) error {
	// Flush remaining logs first
	if err := c.FlushLogs(ctx); err != nil {
		c.log.Error(err, "failed to flush logs before completion")
	}

	c.log.Info("reporting completion",
		"success", success,
		"errorMessage", errorMsg,
		"durationMs", durationMs,
		"restoreDataSize", len(restoreData),
	)

	// Note: In a real implementation with generated protobuf code,
	// this would call the ReportCompletion RPC.

	return nil
}

// Close closes the gRPC connection.
func (c *GRPCClient) Close() error {
	c.StopHeartbeat()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

// sendHeartbeat sends a single heartbeat.
func (c *GRPCClient) sendHeartbeat(ctx context.Context) error {
	// Note: In a real implementation, this would call the Heartbeat RPC.
	c.log.V(2).Info("heartbeat sent")
	return nil
}

// authInterceptor returns a unary client interceptor that adds the auth token.
func (c *GRPCClient) authInterceptor() grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply interface{},
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		token, err := c.readToken()
		if err != nil {
			return fmt.Errorf("failed to read token: %w", err)
		}

		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		ctx = metadata.AppendToOutgoingContext(ctx, "x-execution-id", c.executionID)

		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// streamAuthInterceptor returns a streaming client interceptor that adds the auth token.
func (c *GRPCClient) streamAuthInterceptor() grpc.StreamClientInterceptor {
	return func(
		ctx context.Context,
		desc *grpc.StreamDesc,
		cc *grpc.ClientConn,
		method string,
		streamer grpc.Streamer,
		opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		token, err := c.readToken()
		if err != nil {
			return nil, fmt.Errorf("failed to read token: %w", err)
		}

		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		ctx = metadata.AppendToOutgoingContext(ctx, "x-execution-id", c.executionID)

		return streamer(ctx, desc, cc, method, opts...)
	}
}

// readToken reads the projected SA token from disk.
func (c *GRPCClient) readToken() (string, error) {
	data, err := os.ReadFile(c.tokenPath)
	if err != nil {
		return "", fmt.Errorf("failed to read token file: %w", err)
	}
	return string(data), nil
}
