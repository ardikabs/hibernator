/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/internal/executor/cloudsql"
	"github.com/ardikabs/hibernator/internal/executor/ec2"
	"github.com/ardikabs/hibernator/internal/executor/eks"
	"github.com/ardikabs/hibernator/internal/executor/gke"
	"github.com/ardikabs/hibernator/internal/executor/karpenter"
	"github.com/ardikabs/hibernator/internal/executor/rds"
	"github.com/ardikabs/hibernator/internal/restore"
	streamclient "github.com/ardikabs/hibernator/internal/streaming/client"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(hibernatorv1alpha1.AddToScheme(scheme))
}

// Config holds runner configuration.
type Config struct {
	// Operation is "shutdown" or "wakeup".
	Operation string

	// Target is the target name.
	Target string

	// TargetType is the executor type (e.g., "eks", "rds", "ec2").
	TargetType string

	// Plan is the HibernatePlan name.
	Plan string

	// Namespace is the HibernatePlan namespace.
	Namespace string

	// ControlPlaneEndpoint is the streaming endpoint.
	ControlPlaneEndpoint string

	// ExecutionID is the unique execution identifier.
	ExecutionID string

	// TargetParams is the JSON-encoded target parameters.
	TargetParams string

	// ConnectorKind is the connector kind (CloudProvider, K8SCluster).
	ConnectorKind string

	// ConnectorName is the connector name.
	ConnectorName string

	// ConnectorNamespace is the connector namespace.
	ConnectorNamespace string

	// TokenPath is the path to the stream token.
	TokenPath string

	// GRPCEndpoint is the gRPC streaming endpoint (optional).
	GRPCEndpoint string

	// WebhookEndpoint is the HTTP webhook endpoint (optional fallback).
	WebhookEndpoint string

	// UseTLS enables TLS for gRPC connections.
	UseTLS bool
}

func main() {
	cfg := parseFlags()

	// Initialize logger
	zapLog, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	log := zapr.NewLogger(zapLog)

	// Set up signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		log.Info("received shutdown signal")
		cancel()
	}()

	// Run the execution
	if err := run(ctx, log, cfg); err != nil {
		log.Error(err, "execution failed")
		os.Exit(1)
	}

	log.Info("execution completed successfully")
}

func parseFlags() *Config {
	cfg := &Config{}

	flag.StringVar(&cfg.Operation, "operation", "", "Operation: shutdown or wakeup")
	flag.StringVar(&cfg.Target, "target", "", "Target name")
	flag.StringVar(&cfg.TargetType, "target-type", "", "Target type (executor type)")
	flag.StringVar(&cfg.Plan, "plan", "", "HibernatePlan name")
	flag.StringVar(&cfg.TokenPath, "token-path", "/var/run/secrets/stream/token", "Path to stream token")
	flag.Parse()

	// Override from environment
	if v := os.Getenv("HIBERNATOR_EXECUTION_ID"); v != "" {
		cfg.ExecutionID = v
	}
	if v := os.Getenv("HIBERNATOR_CONTROL_PLANE_ENDPOINT"); v != "" {
		cfg.ControlPlaneEndpoint = v
	}
	if v := os.Getenv("HIBERNATOR_TARGET_PARAMS"); v != "" {
		cfg.TargetParams = v
	}
	if v := os.Getenv("HIBERNATOR_CONNECTOR_KIND"); v != "" {
		cfg.ConnectorKind = v
	}
	if v := os.Getenv("HIBERNATOR_CONNECTOR_NAME"); v != "" {
		cfg.ConnectorName = v
	}
	if v := os.Getenv("HIBERNATOR_CONNECTOR_NAMESPACE"); v != "" {
		cfg.ConnectorNamespace = v
	}
	if v := os.Getenv("POD_NAMESPACE"); v != "" {
		cfg.Namespace = v
	}
	if v := os.Getenv("HIBERNATOR_GRPC_ENDPOINT"); v != "" {
		cfg.GRPCEndpoint = v
	}
	if v := os.Getenv("HIBERNATOR_WEBHOOK_ENDPOINT"); v != "" {
		cfg.WebhookEndpoint = v
	}
	if os.Getenv("HIBERNATOR_USE_TLS") == "true" {
		cfg.UseTLS = true
	}

	return cfg
}

func run(ctx context.Context, log logr.Logger, cfg *Config) error {
	log.Info("starting runner",
		"operation", cfg.Operation,
		"target", cfg.Target,
		"targetType", cfg.TargetType,
		"plan", cfg.Plan,
		"executionId", cfg.ExecutionID,
	)

	// Initialize streaming client
	streamClient, err := initStreamingClient(ctx, log, cfg)
	if err != nil {
		log.Error(err, "failed to initialize streaming client, continuing without streaming")
		// Continue without streaming - logs will go to stdout only
	}
	defer func() {
		if streamClient != nil {
			streamClient.Close()
		}
	}()

	// Start heartbeat if streaming is available
	if streamClient != nil {
		streamClient.StartHeartbeat(30 * time.Second)
		streamClient.Log(ctx, "INFO", "Runner started", map[string]string{
			"operation":  cfg.Operation,
			"target":     cfg.Target,
			"targetType": cfg.TargetType,
			"plan":       cfg.Plan,
		})
	}

	// Build Kubernetes client
	restCfg, err := config.GetConfig()
	if err != nil {
		reportError(ctx, streamClient, log, "Failed to get kubeconfig", err)
		return fmt.Errorf("get kubeconfig: %w", err)
	}

	k8sClient, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		reportError(ctx, streamClient, log, "Failed to create k8s client", err)
		return fmt.Errorf("create k8s client: %w", err)
	}

	// Report progress: initializing
	reportProgress(ctx, streamClient, log, "initializing", 10, "Loading executors")

	// Register executors (fat runner approach)
	registry := executor.NewRegistry()
	registerExecutors(registry, log)

	// Get the executor
	exec, ok := registry.Get(cfg.TargetType)
	if !ok {
		err := fmt.Errorf("executor not found: %s", cfg.TargetType)
		reportError(ctx, streamClient, log, "Failed to get executor", err)
		return fmt.Errorf("get executor: %w", err)
	}

	// Parse target parameters
	var params map[string]interface{}
	if cfg.TargetParams != "" {
		if err := json.Unmarshal([]byte(cfg.TargetParams), &params); err != nil {
			reportError(ctx, streamClient, log, "Failed to parse target params", err)
			return fmt.Errorf("parse target params: %w", err)
		}
	}

	// Report progress: building spec
	reportProgress(ctx, streamClient, log, "preparing", 20, "Building executor spec")

	// Build executor spec from connector
	spec, err := buildExecutorSpec(ctx, k8sClient, cfg, params)
	if err != nil {
		reportError(ctx, streamClient, log, "Failed to build executor spec", err)
		return fmt.Errorf("build executor spec: %w", err)
	}

	// Validate the spec
	reportProgress(ctx, streamClient, log, "validating", 30, "Validating executor spec")
	if err := exec.Validate(*spec); err != nil {
		reportError(ctx, streamClient, log, "Spec validation failed", err)
		return fmt.Errorf("validate spec: %w", err)
	}

	// Report progress: executing
	reportProgress(ctx, streamClient, log, "executing", 50, fmt.Sprintf("Executing %s operation", cfg.Operation))

	// Execute the operation
	var restoreData *executor.RestoreData
	startTime := time.Now()

	switch cfg.Operation {
	case "shutdown":
		rd, err := exec.Shutdown(ctx, *spec)
		if err == nil {
			restoreData = &rd
		}
	case "wakeup":
		// For wakeup, we need to load restore data first
		rd, err := loadRestoreData(ctx, k8sClient, cfg)
		if err != nil {
			return fmt.Errorf("load restore data: %w", err)
		}
		err = exec.WakeUp(ctx, *spec, *rd)
	default:
		err = fmt.Errorf("unknown operation: %s", cfg.Operation)
	}

	duration := time.Since(startTime)
	durationMs := duration.Milliseconds()

	if err != nil {
		log.Error(err, "operation failed", "duration", duration)
		reportCompletion(ctx, streamClient, log, false, err.Error(), durationMs, nil)
		return err
	}

	log.Info("operation completed",
		"duration", duration,
		"hasRestoreData", restoreData != nil,
	)

	// Report progress: saving restore data
	reportProgress(ctx, streamClient, log, "finalizing", 90, "Saving restore data")

	// Serialize restore data for streaming
	var restoreDataBytes []byte
	if restoreData != nil {
		restoreDataBytes, _ = json.Marshal(restoreData)

		// Also save to ConfigMap as backup
		if err := saveRestoreData(ctx, k8sClient, cfg, restoreData); err != nil {
			log.Error(err, "failed to save restore data to ConfigMap")
			// Non-fatal: operation succeeded, restore data save failed
		}
	}

	// Report completion
	reportCompletion(ctx, streamClient, log, true, "", durationMs, restoreDataBytes)

	return nil
}

func registerExecutors(registry *executor.Registry, log logr.Logger) {
	// Register EKS executor
	eksExec := eks.New()
	registry.Register(eksExec)

	// Register RDS executor
	rdsExec := rds.New()
	registry.Register(rdsExec)

	// Register EC2 executor
	ec2Exec := ec2.New()
	registry.Register(ec2Exec)

	// Register Karpenter executor
	karpenterExec := karpenter.New()
	registry.Register(karpenterExec)

	// Register GKE executor
	gkeExec := gke.New()
	registry.Register(gkeExec)

	// Register Cloud SQL executor
	cloudsqlExec := cloudsql.New()
	registry.Register(cloudsqlExec)

	log.Info("registered executors", "count", 6)
}

func buildExecutorSpec(ctx context.Context, k8sClient client.Client, cfg *Config, params map[string]interface{}) (*executor.Spec, error) {
	paramsBytes, _ := json.Marshal(params)
	spec := &executor.Spec{
		TargetName: cfg.Target,
		TargetType: cfg.TargetType,
		Parameters: paramsBytes,
	}

	// Load connector configuration
	switch cfg.ConnectorKind {
	case "CloudProvider":
		var provider hibernatorv1alpha1.CloudProvider
		key := client.ObjectKey{
			Namespace: cfg.ConnectorNamespace,
			Name:      cfg.ConnectorName,
		}
		if err := k8sClient.Get(ctx, key, &provider); err != nil {
			return nil, fmt.Errorf("get CloudProvider: %w", err)
		}

		// Build AWS config from provider
		if provider.Spec.Type == hibernatorv1alpha1.CloudProviderAWS {
			spec.ConnectorConfig.AWS = &executor.AWSConnectorConfig{
				Region:    provider.Spec.AWS.Region,
				AccountID: provider.Spec.AWS.AccountId,
			}
			if provider.Spec.AWS.Auth.ServiceAccount != nil {
				spec.ConnectorConfig.AWS.AssumeRoleArn = provider.Spec.AWS.Auth.ServiceAccount.AssumeRoleArn
			}
		}

	case "K8SCluster":
		var cluster hibernatorv1alpha1.K8SCluster
		key := client.ObjectKey{
			Namespace: cfg.ConnectorNamespace,
			Name:      cfg.ConnectorName,
		}
		if err := k8sClient.Get(ctx, key, &cluster); err != nil {
			return nil, fmt.Errorf("get K8SCluster: %w", err)
		}

		// Build cluster config
		if cluster.Spec.EKS != nil {
			spec.ConnectorConfig.K8S = &executor.K8SConnectorConfig{
				ClusterName: cluster.Spec.EKS.Name,
				Region:      cluster.Spec.EKS.Region,
			}
		} else if cluster.Spec.GKE != nil {
			spec.ConnectorConfig.K8S = &executor.K8SConnectorConfig{
				ClusterName: cluster.Spec.GKE.Name,
				Region:      cluster.Spec.GKE.Location,
			}
		}
	}

	return spec, nil
}

func loadRestoreData(ctx context.Context, k8sClient client.Client, cfg *Config) (*executor.RestoreData, error) {
	// Use restore manager to load data from ConfigMap
	restoreMgr := restore.NewManager(k8sClient)

	data, err := restoreMgr.Load(ctx, cfg.Namespace, cfg.Plan, cfg.Target)
	if err != nil {
		return nil, fmt.Errorf("load from ConfigMap: %w", err)
	}

	if data == nil {
		return nil, fmt.Errorf("no restore data found for plan=%s target=%s", cfg.Plan, cfg.Target)
	}

	// Convert restore.Data to executor.RestoreData
	stateBytes, err := json.Marshal(data.State)
	if err != nil {
		return nil, fmt.Errorf("marshal state: %w", err)
	}

	return &executor.RestoreData{
		Type: data.Executor,
		Data: stateBytes,
	}, nil
}

func saveRestoreData(ctx context.Context, k8sClient client.Client, cfg *Config, data *executor.RestoreData) error {
	// Store restore data in a ConfigMap
	// TODO: Implement proper restore data storage
	_ = ctx
	_ = k8sClient
	_ = cfg
	_ = data
	return nil
}

// initStreamingClient initializes the streaming client based on configuration.
func initStreamingClient(ctx context.Context, log logr.Logger, cfg *Config) (streamclient.StreamingClient, error) {
	// Skip if no endpoints configured
	if cfg.GRPCEndpoint == "" && cfg.WebhookEndpoint == "" && cfg.ControlPlaneEndpoint == "" {
		log.Info("no streaming endpoints configured, skipping streaming client")
		return nil, nil
	}

	// Determine endpoints
	grpcEndpoint := cfg.GRPCEndpoint
	webhookEndpoint := cfg.WebhookEndpoint

	// Legacy support: use ControlPlaneEndpoint if specific endpoints not set
	if grpcEndpoint == "" && webhookEndpoint == "" && cfg.ControlPlaneEndpoint != "" {
		// Assume it's a webhook endpoint for backward compatibility
		webhookEndpoint = cfg.ControlPlaneEndpoint
	}

	clientCfg := streamclient.ClientConfig{
		Type:        streamclient.ClientTypeAuto,
		GRPCAddress: grpcEndpoint,
		WebhookURL:  webhookEndpoint,
		ExecutionID: cfg.ExecutionID,
		TokenPath:   cfg.TokenPath,
		UseTLS:      cfg.UseTLS,
		Timeout:     30 * time.Second,
		Log:         log,
	}

	client, err := streamclient.NewClient(clientCfg)
	if err != nil {
		return nil, fmt.Errorf("create streaming client: %w", err)
	}

	// Connect to the server
	if err := client.Connect(ctx); err != nil {
		return nil, fmt.Errorf("connect to streaming server: %w", err)
	}

	log.Info("streaming client connected",
		"grpcEndpoint", grpcEndpoint,
		"webhookEndpoint", webhookEndpoint,
	)

	return client, nil
}

// reportProgress reports execution progress via streaming client.
func reportProgress(ctx context.Context, client streamclient.StreamingClient, log logr.Logger, phase string, percent int32, message string) {
	log.Info("progress", "phase", phase, "percent", percent, "message", message)

	if client != nil {
		if err := client.ReportProgress(ctx, phase, percent, message); err != nil {
			log.Error(err, "failed to report progress")
		}
	}
}

// reportError logs and streams an error.
func reportError(ctx context.Context, client streamclient.StreamingClient, log logr.Logger, message string, err error) {
	log.Error(err, message)

	if client != nil {
		client.Log(ctx, "ERROR", fmt.Sprintf("%s: %v", message, err), nil)
	}
}

// reportCompletion reports execution completion via streaming client.
func reportCompletion(ctx context.Context, client streamclient.StreamingClient, log logr.Logger, success bool, errorMsg string, durationMs int64, restoreData []byte) {
	log.Info("completion",
		"success", success,
		"durationMs", durationMs,
		"errorMessage", errorMsg,
	)

	if client != nil {
		if err := client.ReportCompletion(ctx, success, errorMsg, durationMs, restoreData); err != nil {
			log.Error(err, "failed to report completion")
		}
	}
}
