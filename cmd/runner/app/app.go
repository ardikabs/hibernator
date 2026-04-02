/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package app

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/internal/version"
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
	var showVersion bool

	flag.BoolVar(&showVersion, "version", false, "Print version and exit.")
	flag.DurationVar(&cfg.Timeout, "timeout", time.Hour, "Overall execution timeout, default 1h")
	flag.StringVar(&cfg.Operation, "operation", "", "Operation: shutdown or wakeup")
	flag.StringVar(&cfg.Target, "target", "", "Target name")
	flag.StringVar(&cfg.TargetType, "target-type", "", "Target type (executor type)")
	flag.StringVar(&cfg.Plan, "plan", "", "HibernatePlan name")
	flag.StringVar(&cfg.TokenPath, "token-path", "/var/run/secrets/stream/token", "Path to stream token")
	flag.Parse()

	// Check if version flag is set
	if showVersion {
		fmt.Println("hibernator-runner", version.GetVersion())
		os.Exit(0)
	}

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

	var result *executor.Result
	defer func() {
		writeTerminationLog(log, result, err)
	}()

	result, err = r.run(ctx)
	if err != nil {
		log.Error(err, "execution failed")
		return err
	}

	log.Info("execution completed successfully")
	return nil
}

// writeTerminationLog writes the executor outcome to the Kubernetes termination log.
// On error it writes the error message; on success it writes the executor result message.
// This is the single place where the runner writes to /dev/termination-log,
// allowing the controller to read the outcome from the pod's termination message.
func writeTerminationLog(log logr.Logger, result *executor.Result, err error) {
	var msg string
	switch {
	case err != nil:
		msg = err.Error()
	case result != nil && result.Message != "":
		msg = result.Message
	default:
		msg = "execution completed successfully"
	}

	if writeErr := os.WriteFile(wellknown.TerminationLogPath, []byte(msg), 0644); writeErr != nil {
		log.Error(writeErr, "failed to write termination log")
	}
}
