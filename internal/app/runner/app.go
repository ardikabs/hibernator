/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package runner

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/go-logr/zapr"
	"go.uber.org/zap"

	"github.com/ardikabs/hibernator/internal/wellknown"
)

// Config holds runner configuration.
type Config struct {
	Timeout              time.Duration // Overall execution timeout
	Operation            string        // "shutdown" or "wakeup"
	Target               string        // Target name
	TargetType           string        // Executor type (e.g., "eks", "rds", "ec2")
	Plan                 string        // HibernatePlan name
	Namespace            string        // HibernatePlan namespace
	ExecutionID          string        // Unique execution identifier
	TargetParams         string        // JSON-encoded target parameters
	ConnectorKind        string        // Connector kind (CloudProvider, K8SCluster)
	ConnectorName        string        // Connector name
	ConnectorNamespace   string        // Connector namespace
	TokenPath            string        // Path to the stream token
	ControlPlaneEndpoint string        // Legacy streaming endpoint
	GRPCEndpoint         string        // gRPC streaming endpoint
	WebSocketEndpoint    string        // WebSocket streaming endpoint
	HTTPCallbackEndpoint string        // HTTP callback endpoint (fallback)
	UseTLS               bool          // Enable TLS for gRPC connections
}

// ParseFlags parses command-line flags and environment variables.
func ParseFlags() *Config {
	cfg := &Config{}

	flag.DurationVar(&cfg.Timeout, "timeout", time.Hour, "Overall execution timeout, default 1h")
	flag.StringVar(&cfg.Operation, "operation", "", "Operation: shutdown or wakeup")
	flag.StringVar(&cfg.Target, "target", "", "Target name")
	flag.StringVar(&cfg.TargetType, "target-type", "", "Target type (executor type)")
	flag.StringVar(&cfg.Plan, "plan", "", "HibernatePlan name")
	flag.StringVar(&cfg.TokenPath, "token-path", "/var/run/secrets/stream/token", "Path to stream token")
	flag.Parse()

	// Environment variable overrides
	envMappings := map[string]*string{
		"HIBERNATOR_EXECUTION_ID":           &cfg.ExecutionID,
		"HIBERNATOR_CONTROL_PLANE_ENDPOINT": &cfg.ControlPlaneEndpoint,
		"HIBERNATOR_GRPC_ENDPOINT":          &cfg.GRPCEndpoint,
		"HIBERNATOR_WEBSOCKET_ENDPOINT":     &cfg.WebSocketEndpoint,
		"HIBERNATOR_HTTP_CALLBACK_ENDPOINT": &cfg.HTTPCallbackEndpoint,
		"HIBERNATOR_TARGET_PARAMS":          &cfg.TargetParams,
		"HIBERNATOR_CONNECTOR_KIND":         &cfg.ConnectorKind,
		"HIBERNATOR_CONNECTOR_NAME":         &cfg.ConnectorName,
		"HIBERNATOR_CONNECTOR_NAMESPACE":    &cfg.ConnectorNamespace,
		"POD_NAMESPACE":                     &cfg.Namespace,
	}
	for envKey, target := range envMappings {
		if v := os.Getenv(envKey); v != "" {
			*target = v
		}
	}

	cfg.UseTLS = os.Getenv("HIBERNATOR_USE_TLS") == "true"

	return cfg
}

// Run starts the runner with the given configuration.
func Run(cfg *Config) error {
	// Initialize logger
	zapLog, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logger: %v\n", err)
		return err
	}
	log := zapr.NewLogger(zapLog).WithName("runner")

	// Set up signal handling and context
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	// Create and run the runner
	r, err := newRunner(ctx, log, cfg)
	if err != nil {
		log.Error(err, "failed to initialize runner")
		return err
	}
	defer r.close()

	if err := r.run(ctx); err != nil {
		// Write the error message to the termination log path
		// This allows the controller to read the specific error message from the failed pod
		if errWrite := os.WriteFile(wellknown.TerminationLogPath, []byte(err.Error()), 0644); errWrite != nil {
			log.Error(errWrite, "failed to write termination log")
		}

		log.Error(err, "execution failed")
		return err
	}

	log.Info("execution completed successfully")
	return nil
}
