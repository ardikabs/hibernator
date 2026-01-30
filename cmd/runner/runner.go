/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/internal/restore"
	streamclient "github.com/ardikabs/hibernator/internal/streaming/client"
)

// runner encapsulates the execution context and dependencies.
type runner struct {
	cfg          *Config
	log          logr.Logger
	k8sClient    client.Client
	streamClient streamclient.StreamingClient
	registry     *executor.Registry
}

// newRunner creates a new runner instance.
func newRunner(ctx context.Context, log logr.Logger, cfg *Config) (*runner, error) {
	r := &runner{
		cfg:      cfg,
		log:      log,
		registry: executor.NewRegistry(),
	}

	// Initialize streaming client (non-fatal if unavailable)
	streamClient, err := initStreamingClient(ctx, log, cfg)
	if err != nil {
		log.Error(err, "failed to initialize streaming client, continuing without streaming")
	}
	r.streamClient = streamClient

	// Build Kubernetes client
	restCfg, err := config.GetConfig()
	if err != nil {
		r.reportError(ctx, "Failed to get kubeconfig", err)
		return nil, fmt.Errorf("get kubeconfig: %w", err)
	}

	k8sClient, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		r.reportError(ctx, "Failed to create k8s client", err)
		return nil, fmt.Errorf("create k8s client: %w", err)
	}
	r.k8sClient = k8sClient

	// Register executors
	factory := newExecutorFactoryRegistry()
	factory.registerTo(r.registry, log)

	return r, nil
}

// close cleans up runner resources.
func (r *runner) close() {
	if r.streamClient != nil {
		r.streamClient.Close()
	}
}

// run executes the hibernation operation.
func (r *runner) run(ctx context.Context) error {
	cfg := r.cfg
	log := r.log

	log.Info("starting runner",
		"operation", cfg.Operation,
		"target", cfg.Target,
		"targetType", cfg.TargetType,
		"plan", cfg.Plan,
		"executionId", cfg.ExecutionID,
	)

	// Start heartbeat if streaming is available
	if r.streamClient != nil {
		r.streamClient.StartHeartbeat(30 * time.Second)
		r.streamClient.Log(ctx, "INFO", "Runner started", map[string]string{
			"operation":  cfg.Operation,
			"target":     cfg.Target,
			"targetType": cfg.TargetType,
			"plan":       cfg.Plan,
		})
	}

	// Report progress: initializing
	r.reportProgress(ctx, "initializing", 10, "Loading executors")

	// Get the executor
	exec, ok := r.registry.Get(cfg.TargetType)
	if !ok {
		err := fmt.Errorf("executor not found: %s", cfg.TargetType)
		r.reportError(ctx, "Failed to get executor", err)
		return err
	}

	// Parse target parameters
	var params map[string]interface{}
	if cfg.TargetParams != "" {
		if err := json.Unmarshal([]byte(cfg.TargetParams), &params); err != nil {
			r.reportError(ctx, "Failed to parse target params", err)
			return fmt.Errorf("parse target params: %w", err)
		}
	}

	// Report progress: building spec
	r.reportProgress(ctx, "preparing", 20, "Building executor spec")

	// Build executor spec from connector
	spec, err := r.buildExecutorSpec(ctx, params)
	if err != nil {
		r.reportError(ctx, "Failed to build executor spec", err)
		return fmt.Errorf("build executor spec: %w", err)
	}

	// Validate the spec
	r.reportProgress(ctx, "validating", 30, "Validating executor spec")
	if err := exec.Validate(*spec); err != nil {
		r.reportError(ctx, "Spec validation failed", err)
		return fmt.Errorf("validate spec: %w", err)
	}

	// Report progress: executing
	r.reportProgress(ctx, "executing", 50, fmt.Sprintf("Executing %s operation", cfg.Operation))

	// Execute the operation
	restoreData, durationMs, err := r.executeOperation(ctx, exec, spec)
	if err != nil {
		r.reportCompletion(ctx, false, err.Error(), durationMs, nil)
		return err
	}

	// Report progress: saving restore data
	r.reportProgress(ctx, "finalizing", 90, "Saving restore data")

	// Serialize and save restore data
	var restoreDataBytes []byte
	if restoreData != nil {
		restoreDataBytes, _ = json.Marshal(restoreData)

		if err := r.saveRestoreData(ctx, restoreData); err != nil {
			log.Error(err, "failed to save restore data to ConfigMap")
			// Non-fatal: operation succeeded, restore data save failed
		}
	}

	// Report completion
	r.reportCompletion(ctx, true, "", durationMs, restoreDataBytes)

	return nil
}

// executeOperation runs the shutdown or wakeup operation.
func (r *runner) executeOperation(ctx context.Context, exec executor.Executor, spec *executor.Spec) (*executor.RestoreData, int64, error) {
	startTime := time.Now()

	var restoreData *executor.RestoreData
	var operationErr error

	switch r.cfg.Operation {
	case "shutdown":
		rd, err := exec.Shutdown(ctx, *spec)
		if err != nil {
			operationErr = err
		} else {
			restoreData = &rd
		}
	case "wakeup":
		rd, err := r.loadRestoreData(ctx)
		if err != nil {
			operationErr = fmt.Errorf("load restore data: %w", err)
		} else {
			operationErr = exec.WakeUp(ctx, *spec, *rd)
		}
	default:
		operationErr = fmt.Errorf("unknown operation: %s", r.cfg.Operation)
	}

	duration := time.Since(startTime)
	durationMs := duration.Milliseconds()

	if operationErr != nil {
		r.log.Error(operationErr, "operation failed", "duration", duration)
		return nil, durationMs, operationErr
	}

	r.log.Info("operation completed",
		"duration", duration,
		"hasRestoreData", restoreData != nil,
	)

	return restoreData, durationMs, nil
}

// buildExecutorSpec constructs the executor spec from connector configuration.
func (r *runner) buildExecutorSpec(ctx context.Context, params map[string]interface{}) (*executor.Spec, error) {
	paramsBytes, _ := json.Marshal(params)
	spec := &executor.Spec{
		TargetName: r.cfg.Target,
		TargetType: r.cfg.TargetType,
		Parameters: paramsBytes,
	}

	switch r.cfg.ConnectorKind {
	case "CloudProvider":
		if err := r.loadCloudProviderConfig(ctx, spec); err != nil {
			return nil, err
		}
	case "K8SCluster":
		if err := r.loadK8SClusterConfig(ctx, spec); err != nil {
			return nil, err
		}
	}

	return spec, nil
}

// loadCloudProviderConfig populates the spec with CloudProvider configuration.
func (r *runner) loadCloudProviderConfig(ctx context.Context, spec *executor.Spec) error {
	var provider hibernatorv1alpha1.CloudProvider
	key := client.ObjectKey{
		Namespace: r.cfg.ConnectorNamespace,
		Name:      r.cfg.ConnectorName,
	}
	if err := r.k8sClient.Get(ctx, key, &provider); err != nil {
		return fmt.Errorf("get CloudProvider: %w", err)
	}

	if provider.Spec.Type == hibernatorv1alpha1.CloudProviderAWS {
		spec.ConnectorConfig.AWS = &executor.AWSConnectorConfig{
			Region:    provider.Spec.AWS.Region,
			AccountID: provider.Spec.AWS.AccountId,
		}
		if provider.Spec.AWS.Auth.ServiceAccount != nil {
			spec.ConnectorConfig.AWS.AssumeRoleArn = provider.Spec.AWS.Auth.ServiceAccount.AssumeRoleArn
		}
	}

	return nil
}

// loadK8SClusterConfig populates the spec with K8SCluster configuration.
func (r *runner) loadK8SClusterConfig(ctx context.Context, spec *executor.Spec) error {
	var cluster hibernatorv1alpha1.K8SCluster
	key := client.ObjectKey{
		Namespace: r.cfg.ConnectorNamespace,
		Name:      r.cfg.ConnectorName,
	}
	if err := r.k8sClient.Get(ctx, key, &cluster); err != nil {
		return fmt.Errorf("get K8SCluster: %w", err)
	}

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

	return nil
}

// loadRestoreData retrieves restore data from ConfigMap.
func (r *runner) loadRestoreData(ctx context.Context) (*executor.RestoreData, error) {
	restoreMgr := restore.NewManager(r.k8sClient)

	data, err := restoreMgr.Load(ctx, r.cfg.Namespace, r.cfg.Plan, r.cfg.Target)
	if err != nil {
		return nil, fmt.Errorf("load from ConfigMap: %w", err)
	}

	if data == nil {
		return nil, fmt.Errorf("no restore data found for plan=%s target=%s", r.cfg.Plan, r.cfg.Target)
	}

	stateBytes, err := json.Marshal(data.State)
	if err != nil {
		return nil, fmt.Errorf("marshal state: %w", err)
	}

	return &executor.RestoreData{
		Type: data.Executor,
		Data: stateBytes,
	}, nil
}

// saveRestoreData persists restore data to ConfigMap.
func (r *runner) saveRestoreData(ctx context.Context, data *executor.RestoreData) error {
	if r.cfg.Namespace == "" || r.cfg.Plan == "" {
		return fmt.Errorf("plan namespace and name required for restore data storage")
	}

	if r.cfg.Target == "" || data == nil || data.Type == "" {
		return fmt.Errorf("target name and restore data type required")
	}

	restoreData := &restore.Data{
		Target:    r.cfg.Target,
		Executor:  data.Type,
		Version:   1,
		CreatedAt: metav1.Now(),
	}

	if len(data.Data) > 0 {
		var stateMap map[string]interface{}
		if err := json.Unmarshal(data.Data, &stateMap); err != nil {
			return fmt.Errorf("unmarshal restore data: %w", err)
		}
		restoreData.State = stateMap
	}

	rm := restore.NewManager(r.k8sClient)
	if err := rm.Save(ctx, r.cfg.Namespace, r.cfg.Plan, r.cfg.Target, restoreData); err != nil {
		return fmt.Errorf("save restore data: %w", err)
	}

	return nil
}

// ----------------------------------------------------------------------------
// Streaming Helpers
// ----------------------------------------------------------------------------

// initStreamingClient initializes the streaming client based on configuration.
func initStreamingClient(ctx context.Context, log logr.Logger, cfg *Config) (streamclient.StreamingClient, error) {
	if cfg.GRPCEndpoint == "" && cfg.WebhookEndpoint == "" && cfg.ControlPlaneEndpoint == "" {
		log.Info("no streaming endpoints configured, skipping streaming client")
		return nil, nil
	}

	grpcEndpoint := cfg.GRPCEndpoint
	webhookEndpoint := cfg.WebhookEndpoint

	// Legacy support: use ControlPlaneEndpoint if specific endpoints not set
	if grpcEndpoint == "" && webhookEndpoint == "" && cfg.ControlPlaneEndpoint != "" {
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
func (r *runner) reportProgress(ctx context.Context, phase string, percent int32, message string) {
	r.log.Info("progress", "phase", phase, "percent", percent, "message", message)

	if r.streamClient != nil {
		if err := r.streamClient.ReportProgress(ctx, phase, percent, message); err != nil {
			r.log.Error(err, "failed to report progress")
		}
	}
}

// reportError logs and streams an error.
func (r *runner) reportError(ctx context.Context, message string, err error) {
	r.log.Error(err, message)

	if r.streamClient != nil {
		r.streamClient.Log(ctx, "ERROR", fmt.Sprintf("%s: %v", message, err), nil)
	}
}

// reportCompletion reports execution completion via streaming client.
func (r *runner) reportCompletion(ctx context.Context, success bool, errorMsg string, durationMs int64, restoreData []byte) {
	r.log.Info("completion",
		"success", success,
		"durationMs", durationMs,
		"errorMessage", errorMsg,
	)

	if r.streamClient != nil {
		if err := r.streamClient.ReportCompletion(ctx, success, errorMsg, durationMs, restoreData); err != nil {
			r.log.Error(err, "failed to report completion")
		}
	}
}
