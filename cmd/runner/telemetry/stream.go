/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package telemetry

import (
	"context"
	"fmt"
	"time"

	streamclient "github.com/ardikabs/hibernator/internal/streaming/client"
	"github.com/ardikabs/hibernator/pkg/logsink"
	"github.com/go-logr/logr"
)

// Config holds the configuration needed for the telemetry streaming client.
type Config struct {
	GRPCEndpoint         string
	WebSocketEndpoint    string
	HTTPCallbackEndpoint string
	ControlPlaneEndpoint string
	ExecutionID          string
	TokenPath            string
	UseTLS               bool
}

// Manager wraps the streaming client to report telemetry data (progress/completion).
type Manager struct {
	client   streamclient.StreamingClient
	log      logr.Logger
	dualSink *logsink.DualWriteSink
}

// NewManager initializes the streaming client based on configuration.
// Returns a Manager instance. If no endpoints are configured, it returns a Manager
// with a nil client, which safely ignores telemetry reporting.
func NewManager(ctx context.Context, log logr.Logger, cfg Config) (*Manager, error) {
	log = log.WithName("telemetry")
	if cfg.GRPCEndpoint == "" && cfg.WebSocketEndpoint == "" && cfg.HTTPCallbackEndpoint == "" && cfg.ControlPlaneEndpoint == "" {
		log.Info("no streaming endpoints configured, skipping streaming client")
		return &Manager{log: log}, nil
	}

	clientCfg := streamclient.ClientConfig{
		Type:         streamclient.ClientTypeAuto,
		GRPCAddress:  cfg.GRPCEndpoint,
		WebSocketURL: cfg.WebSocketEndpoint,
		WebhookURL:   cfg.HTTPCallbackEndpoint,
		ExecutionID:  cfg.ExecutionID,
		TokenPath:    cfg.TokenPath,
		UseTLS:       cfg.UseTLS,
		Timeout:      30 * time.Second,
		Log:          log,
	}

	client, err := streamclient.NewClient(clientCfg)
	if err != nil {
		return nil, fmt.Errorf("create streaming client: %w", err)
	}

	if err := client.Connect(ctx); err != nil {
		return nil, fmt.Errorf("connect to streaming server: %w", err)
	}

	log.Info("streaming client connected",
		"grpcEndpoint", cfg.GRPCEndpoint,
		"webSocketEndpoint", cfg.WebSocketEndpoint,
		"httpCallbackEndpoint", cfg.HTTPCallbackEndpoint,
	)

	return &Manager{client: client, log: log}, nil
}

// GetLogger introduces a logger with DualWriteSink
// that writes to both the original logger and the streaming client.
func (m *Manager) GetLogger() logr.Logger {
	m.dualSink = logsink.NewDualWriteSink(m.log.GetSink(), m.client)
	return logr.New(m.dualSink)
}

// StartHeartbeat starts the heartbeat if the client is connected.
func (m *Manager) StartHeartbeat(interval time.Duration) {
	if m.client != nil {
		m.client.StartHeartbeat(interval)
	}
}

// Close closes the underlying stream client.
func (m *Manager) Close() error {
	if m.dualSink != nil {
		m.dualSink.Stop()
	}

	if m.client != nil {
		return m.client.Close()
	}
	return nil
}

// ReportProgress logs progress to stdout and reports it via the streaming client if available.
func (m *Manager) ReportProgress(ctx context.Context, phase string, percent int32, message string) {
	// Always log to stdout
	m.log.Info("progress",
		"phase", phase,
		"percent", percent,
		"message", message,
	)

	// Report progress separately via ReportProgress RPC
	if m.client != nil {
		if err := m.client.ReportProgress(ctx, phase, percent, message); err != nil {
			m.log.Info("failed to report progress", "error", err.Error())
		}
	}
}

// ReportCompletion logs completion to stdout and reports it via the streaming client if available.
func (m *Manager) ReportCompletion(ctx context.Context, success bool, errorMsg string, durationMs int64) {
	// Always log to stdout
	m.log.Info("completion",
		"success", success,
		"durationMs", durationMs,
		"errorMessage", errorMsg,
	)

	// Stream to control plane if available
	if m.client != nil {
		if err := m.client.ReportCompletion(ctx, success, errorMsg, durationMs); err != nil {
			m.log.Info("failed to report completion", "error", err.Error())
		}
	}
}
