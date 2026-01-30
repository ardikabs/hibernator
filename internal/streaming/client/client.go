/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package client provides streaming clients for runner-to-controller communication.
// It supports both gRPC (preferred) and HTTP webhook transports with automatic fallback.
// The client handles log streaming, progress reporting, completion notifications, and heartbeats.
package client

import (
	"context"
	"time"

	"github.com/go-logr/logr"
)

// StreamingClient defines the interface for runner-to-controller communication.
type StreamingClient interface {
	// Connect establishes connection to the streaming server.
	Connect(ctx context.Context) error

	// StartHeartbeat starts the background heartbeat goroutine.
	StartHeartbeat(interval time.Duration)

	// StopHeartbeat stops the background heartbeat.
	StopHeartbeat()

	// Log sends a log entry to the server.
	Log(ctx context.Context, level, message string, fields map[string]string) error

	// FlushLogs sends all buffered logs to the server.
	FlushLogs(ctx context.Context) error

	// ReportProgress sends a progress update to the server.
	ReportProgress(ctx context.Context, phase string, percent int32, message string) error

	// ReportCompletion sends a completion report to the server.
	ReportCompletion(ctx context.Context, success bool, errorMsg string, durationMs int64, restoreData []byte) error

	// Close closes the connection.
	Close() error
}

// ClientType represents the type of streaming client.
type ClientType string

const (
	// ClientTypeGRPC uses gRPC for streaming.
	ClientTypeGRPC ClientType = "grpc"

	// ClientTypeWebhook uses HTTP webhooks for communication.
	ClientTypeWebhook ClientType = "webhook"

	// ClientTypeAuto automatically selects the best available transport.
	ClientTypeAuto ClientType = "auto"
)

// ClientConfig contains configuration for creating a streaming client.
type ClientConfig struct {
	// Type specifies the client type (grpc, webhook, or auto).
	Type ClientType

	// GRPCAddress is the gRPC server address (e.g., "controller-grpc:9443").
	GRPCAddress string

	// WebhookURL is the webhook server base URL (e.g., "http://controller:8080").
	WebhookURL string

	// ExecutionID is the unique ID for this execution.
	ExecutionID string

	// TokenPath is the path to the projected SA token.
	TokenPath string

	// UseTLS enables TLS for gRPC connections.
	UseTLS bool

	// Timeout is the HTTP client timeout for webhook requests.
	Timeout time.Duration

	// Log is the logger to use.
	Log logr.Logger
}

// NewClient creates a streaming client based on the configuration.
func NewClient(cfg ClientConfig) (StreamingClient, error) {
	switch cfg.Type {
	case ClientTypeGRPC:
		return NewGRPCClient(GRPCClientOptions{
			Address:     cfg.GRPCAddress,
			ExecutionID: cfg.ExecutionID,
			TokenPath:   cfg.TokenPath,
			UseTLS:      cfg.UseTLS,
			Log:         cfg.Log,
		}), nil

	case ClientTypeWebhook:
		return NewWebhookClient(WebhookClientOptions{
			BaseURL:     cfg.WebhookURL,
			ExecutionID: cfg.ExecutionID,
			TokenPath:   cfg.TokenPath,
			Timeout:     cfg.Timeout,
			Log:         cfg.Log,
		}), nil

	case ClientTypeAuto:
		return NewAutoClient(cfg), nil

	default:
		// Default to webhook as it's more universally available
		return NewWebhookClient(WebhookClientOptions{
			BaseURL:     cfg.WebhookURL,
			ExecutionID: cfg.ExecutionID,
			TokenPath:   cfg.TokenPath,
			Timeout:     cfg.Timeout,
			Log:         cfg.Log,
		}), nil
	}
}

// AutoClient attempts gRPC first, then falls back to webhook.
type AutoClient struct {
	grpcClient    *GRPCClient
	webhookClient *WebhookClient
	active        StreamingClient
	cfg           ClientConfig
	log           logr.Logger
}

// NewAutoClient creates a client that auto-selects the best transport.
func NewAutoClient(cfg ClientConfig) *AutoClient {
	return &AutoClient{
		grpcClient: NewGRPCClient(GRPCClientOptions{
			Address:     cfg.GRPCAddress,
			ExecutionID: cfg.ExecutionID,
			TokenPath:   cfg.TokenPath,
			UseTLS:      cfg.UseTLS,
			Log:         cfg.Log,
		}),
		webhookClient: NewWebhookClient(WebhookClientOptions{
			BaseURL:     cfg.WebhookURL,
			ExecutionID: cfg.ExecutionID,
			TokenPath:   cfg.TokenPath,
			Timeout:     cfg.Timeout,
			Log:         cfg.Log,
		}),
		cfg: cfg,
		log: cfg.Log.WithName("auto-client"),
	}
}

// Connect tries gRPC first, then falls back to webhook.
func (c *AutoClient) Connect(ctx context.Context) error {
	// Try gRPC first if configured
	if c.cfg.GRPCAddress != "" {
		c.log.Info("attempting gRPC connection", "address", c.cfg.GRPCAddress)
		if err := c.grpcClient.Connect(ctx); err == nil {
			c.active = c.grpcClient
			c.log.Info("using gRPC transport")
			return nil
		} else {
			c.log.Info("gRPC connection failed, falling back to webhook", "error", err.Error())
		}
	}

	// Fall back to webhook
	if c.cfg.WebhookURL != "" {
		c.log.Info("attempting webhook connection", "url", c.cfg.WebhookURL)
		if err := c.webhookClient.Connect(ctx); err == nil {
			c.active = c.webhookClient
			c.log.Info("using webhook transport")
			return nil
		} else {
			return err
		}
	}

	return nil
}

// StartHeartbeat starts the background heartbeat.
func (c *AutoClient) StartHeartbeat(interval time.Duration) {
	if c.active != nil {
		c.active.StartHeartbeat(interval)
	}
}

// StopHeartbeat stops the background heartbeat.
func (c *AutoClient) StopHeartbeat() {
	if c.active != nil {
		c.active.StopHeartbeat()
	}
}

// Log sends a log entry.
func (c *AutoClient) Log(ctx context.Context, level, message string, fields map[string]string) error {
	if c.active != nil {
		return c.active.Log(ctx, level, message, fields)
	}
	// Log locally if no active connection
	c.log.Info(message, "level", level, "fields", fields)
	return nil
}

// FlushLogs flushes buffered logs.
func (c *AutoClient) FlushLogs(ctx context.Context) error {
	if c.active != nil {
		return c.active.FlushLogs(ctx)
	}
	return nil
}

// ReportProgress reports execution progress.
func (c *AutoClient) ReportProgress(ctx context.Context, phase string, percent int32, message string) error {
	if c.active != nil {
		return c.active.ReportProgress(ctx, phase, percent, message)
	}
	c.log.Info("progress (no active connection)", "phase", phase, "percent", percent, "message", message)
	return nil
}

// ReportCompletion reports execution completion.
func (c *AutoClient) ReportCompletion(ctx context.Context, success bool, errorMsg string, durationMs int64, restoreData []byte) error {
	if c.active != nil {
		return c.active.ReportCompletion(ctx, success, errorMsg, durationMs, restoreData)
	}
	c.log.Info("completion (no active connection)", "success", success, "error", errorMsg)
	return nil
}

// Close closes the active connection.
func (c *AutoClient) Close() error {
	if c.active != nil {
		return c.active.Close()
	}
	return nil
}

// Ensure implementations satisfy the interface.
var (
	_ StreamingClient = (*GRPCClient)(nil)
	_ StreamingClient = (*WebhookClient)(nil)
	_ StreamingClient = (*AutoClient)(nil)
)
