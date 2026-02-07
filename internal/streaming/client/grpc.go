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
	client      streamingv1alpha1.ExecutionServiceClient
	address     string
	executionID string
	tokenPath   string
	useTLS      bool
	log         logr.Logger

	// log streaming management
	logStream  grpc.ClientStreamingClient[streamingv1alpha1.LogEntry, streamingv1alpha1.StreamLogsResponse]
	logChannel chan *streamingv1alpha1.LogEntry

	// heartbeat management
	heartbeatCtx    context.Context
	heartbeatCancel context.CancelFunc
	heartbeatWg     sync.WaitGroup

	// mutex for connnection
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
	c.client = streamingv1alpha1.NewExecutionServiceClient(conn)
	c.log.Info("connected to streaming server", "address", c.address)

	// Open log stream asynchronously in background to avoid blocking Connect()
	// This allows runner to proceed immediately even if stream setup is slow
	if err := c.openLogStream(context.Background()); err != nil {
		c.log.Info("streaming logs disabled, failed to open log stream", "error", err)
	}

	return nil
}

// openLogStream opens a persistent log stream for reuse across multiple log entries.
// Called internally by Connect() to establish the stream immediately after connection.
func (c *GRPCClient) openLogStream(ctx context.Context) error {
	if c.logStream != nil {
		return nil // Already open
	}

	// Use a buffered channel to handle bursts and prevent blocking
	c.logChannel = make(chan *streamingv1alpha1.LogEntry, 100)

	if c.client == nil {
		return fmt.Errorf("client not initialized")
	}

	stream, err := c.client.StreamLogs(ctx)
	if err != nil {
		return fmt.Errorf("failed to open log stream: %w", err)
	}

	go func() {
		var streamFail bool
		for {
			select {
			case entry, ok := <-c.logChannel:
				if !ok {
					// Channel closed, exit goroutine
					return
				}

				if err := stream.Send(entry); err != nil {
					// Log the first failure only to avoid log spam
					if !streamFail {
						c.log.Info("streaming logs failing, ignoring...", "error", err)
						streamFail = true
					}

					// Ignore further errors and continue
					continue
				}

				// Reset failure flag on successful send
				streamFail = false
			}
		}
	}()

	c.logStream = stream
	c.log.V(1).Info("opened persistent log stream")
	return nil
}

// Log sends a log entry to the server via persistent stream.
// The stream is opened during Connect() and reused for all log entries.
// Errors are logged silently - streaming failures don't interrupt execution.
func (c *GRPCClient) Log(ctx context.Context, level, message string, fields map[string]string) error {
	if c.logChannel == nil {
		// Log channel is not initialized, skip silently
		return nil
	}

	entry := &streamingv1alpha1.LogEntry{
		ExecutionId: c.executionID,
		Timestamp:   time.Now().Format(time.RFC3339),
		Level:       level,
		Message:     message,
		Fields:      fields,
	}

	select {
	case c.logChannel <- entry:
		c.log.V(4).Info("sent log entry via gRPC stream", "entry", entry)
	case <-ctx.Done():
		// An intentional cancellation from caller if any
		return fmt.Errorf("log sending cancelled: %w", ctx.Err())
	default:
		// Buffer full, drop log to ensure main execution never blocks
		// Ideally we would increment a metric here
	}

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

// ReportProgress sends a progress update to the server via ReportProgress RPC.
func (c *GRPCClient) ReportProgress(ctx context.Context, phase string, percent int32, message string) error {
	c.mu.Lock()
	if c.conn == nil {
		c.mu.Unlock()
		return fmt.Errorf("not connected to streaming server")
	}
	client := c.client
	c.mu.Unlock()

	report := &streamingv1alpha1.ProgressReport{
		ExecutionId:     c.executionID,
		Phase:           phase,
		ProgressPercent: percent,
		Message:         message,
		Timestamp:       time.Now().Format(time.RFC3339),
	}

	_, err := client.ReportProgress(ctx, report)
	if err != nil {
		c.log.Error(err, "failed to report progress")
		return fmt.Errorf("failed to report progress: %w", err)
	}

	c.log.Info("reported progress via gRPC", "phase", phase, "percent", percent)
	return nil
}

// ReportCompletion sends a completion report to the server via ReportCompletion RPC.
// Note: Restore data is persisted directly by runner to ConfigMap, not sent via streaming.
func (c *GRPCClient) ReportCompletion(ctx context.Context, success bool, errorMsg string, durationMs int64) error {
	c.mu.Lock()
	if c.conn == nil {
		c.mu.Unlock()
		return fmt.Errorf("not connected to streaming server")
	}
	client := c.client
	c.mu.Unlock()

	report := &streamingv1alpha1.CompletionReport{
		ExecutionId:  c.executionID,
		Success:      success,
		ErrorMessage: errorMsg,
		DurationMs:   durationMs,
		Timestamp:    time.Now().Format(time.RFC3339),
	}

	c.log.V(1).Info(
		"Sending completion report",
		"executionId", report.ExecutionId,
		"success", success)

	_, err := client.ReportCompletion(ctx, report)
	if err != nil {
		c.log.Error(err, "failed to report completion")
		return fmt.Errorf("failed to report completion: %w", err)
	}

	c.log.Info("reported completion via gRPC", "success", success)
	return nil
}

// Close closes the log stream and gRPC connection.
func (c *GRPCClient) Close() error {
	c.StopHeartbeat()

	// Close log stream
	if c.logStream != nil {
		if _, err := c.logStream.CloseAndRecv(); err != nil {
			c.log.V(1).Error(err, "failed to close log stream gracefully")
		}
		c.logStream = nil
		c.log.V(1).Info("closed persistent log stream")
	}

	// Close connection
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

// sendHeartbeat sends a single heartbeat via Heartbeat RPC.
func (c *GRPCClient) sendHeartbeat(ctx context.Context) error {
	c.mu.Lock()
	if c.conn == nil {
		c.mu.Unlock()
		return fmt.Errorf("not connected")
	}
	client := c.client
	c.mu.Unlock()

	req := &streamingv1alpha1.HeartbeatRequest{
		ExecutionId: c.executionID,
		Timestamp:   time.Now().Format(time.RFC3339),
	}

	_, err := client.Heartbeat(ctx, req)
	if err != nil {
		c.log.V(2).Error(err, "heartbeat failed")
		return fmt.Errorf("heartbeat failed: %w", err)
	}

	c.log.V(2).Info("heartbeat sent via gRPC")
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
