/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/internal/restore"
	streamclient "github.com/ardikabs/hibernator/internal/streaming/client"
	"github.com/ardikabs/hibernator/pkg/awsutil"
	"github.com/ardikabs/hibernator/pkg/logsink"
)

// runner encapsulates the execution context and dependencies.
type runner struct {
	cfg          *Config
	log          logr.Logger
	logSink      *logsink.DualWriteSink // For graceful shutdown
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

	// Wrap logger with DualWriteSink for automatic log streaming
	if streamClient != nil {
		r.streamClient = streamClient

		sender := &streamingLogSender{client: streamClient}
		r.logSink = logsink.NewDualWriteSink(log.GetSink(), sender)
		r.log = logr.New(r.logSink)
	}

	// Build Kubernetes client
	restCfg, err := config.GetConfig()
	if err != nil {
		r.log.Error(err, "failed to get kubeconfig")
		return nil, fmt.Errorf("get kubeconfig: %w", err)
	}

	k8sClient, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		r.log.Error(err, "failed to create k8s client")
		return nil, fmt.Errorf("create k8s client: %w", err)
	}
	r.k8sClient = k8sClient

	// Register executors
	factory := newExecutorFactoryRegistry()
	factory.registerTo(r.registry, r.log)

	return r, nil
}

// close cleans up runner resources.
func (r *runner) close() {
	// Stop log sink first to drain remaining logs
	if r.logSink != nil {
		r.logSink.Stop()
	}
	if r.streamClient != nil {
		r.streamClient.Close()
	}
}

// streamingLogSender adapts StreamingClient to logsink.LogSender interface.
type streamingLogSender struct {
	client streamclient.StreamingClient
}

func (s *streamingLogSender) Log(ctx context.Context, level, message string, fields map[string]string) error {
	return s.client.Log(ctx, level, message, fields)
}

// run executes the hibernation operation.
func (r *runner) run(ctx context.Context) error {
	cfg := r.cfg
	log := r.log.WithValues(
		"operation", cfg.Operation,
		"target", cfg.Target,
		"targetType", cfg.TargetType,
		"plan", cfg.Plan,
		"executionId", cfg.ExecutionID,
	)

	log.Info("starting runner")

	// Start heartbeat if streaming is available
	if r.streamClient != nil {
		r.streamClient.StartHeartbeat(30 * time.Second)
	}

	// Report progress: initializing
	r.reportProgress(ctx, "initializing", 10, "Loading executors")

	// Get the executor
	exec, ok := r.registry.Get(cfg.TargetType)
	if !ok {
		err := fmt.Errorf("executor not found: %s", cfg.TargetType)
		r.log.Error(err, "failed to get executor")
		return err
	}

	// Parse target parameters
	var params map[string]interface{}
	if cfg.TargetParams != "" {
		if err := json.Unmarshal([]byte(cfg.TargetParams), &params); err != nil {
			r.log.Error(err, "failed to parse target params")
			return fmt.Errorf("parse target params: %w", err)
		}
	}

	// Report progress: building spec
	r.reportProgress(ctx, "preparing", 20, "Building executor spec")

	// Build executor spec from connector
	spec, flusher, err := r.buildExecutorSpec(ctx, params)
	if err != nil {
		r.log.Error(err, "failed to build executor spec")
		return fmt.Errorf("build executor spec: %w", err)
	}

	// Validate the spec
	r.reportProgress(ctx, "validating", 30, "Validating executor spec")
	if err := exec.Validate(*spec); err != nil {
		r.log.Error(err, "spec validation failed")
		return fmt.Errorf("validate spec: %w", err)
	}

	// Report progress: executing
	r.reportProgress(ctx, "executing", 50, fmt.Sprintf("Executing %s operation", cfg.Operation))

	// Execute the operation
	durationMs, err := r.executeOperation(ctx, exec, spec, flusher)

	// Operation failure: report and return
	if err != nil {
		if cfg.Operation == "shutdown" {
			r.log.Error(err, "shutdown failed")
		}
		r.reportCompletion(ctx, false, err.Error(), durationMs)
		return err
	}

	// Report progress: finalizing
	r.reportProgress(ctx, "finalizing", 90, "Finalizing operation")

	// Wake-up success: mark target as restored for cleanup coordination
	if cfg.Operation == "wakeup" {
		rm := restore.NewManager(r.k8sClient)
		if err := rm.MarkTargetRestored(ctx, r.cfg.Namespace, r.cfg.Plan, r.cfg.Target); err != nil {
			// Non-fatal: continue even if marking fails
			r.log.Error(err, "failed to mark target as restored (non-fatal)")
		} else {
			r.log.Info("Target marked as restored",
				"plan", r.cfg.Plan,
				"target", r.cfg.Target,
			)
		}
	}

	// Report completion to controller (status only, no restore data payload)
	// The controller reads restore data from ConfigMap during wake-up
	r.reportCompletion(ctx, true, "", durationMs)

	return nil
}

// executeOperation runs the shutdown or wakeup operation.
// For shutdown operations, returns a flush function to save accumulated restore data.
func (r *runner) executeOperation(ctx context.Context, exec executor.Executor, spec *executor.Spec, flushFunc func() error) (int64, error) {
	startTime := time.Now()

	var operationErr error

	switch r.cfg.Operation {
	case "shutdown":
		// Defer flush to ensure accumulated restore data is saved even on error
		if flushFunc != nil {
			defer func() {
				if flushErr := flushFunc(); flushErr != nil {
					r.log.Error(flushErr, "failed to flush accumulated restore data")
					// If shutdown succeeded but flush failed, prioritize flush error
					if operationErr == nil {
						operationErr = fmt.Errorf("flush restore data: %w", flushErr)
					}
				}
			}()
		}

		err := exec.Shutdown(ctx, r.log, *spec)
		if err != nil {
			operationErr = err
		}
	case "wakeup":
		rd, err := r.loadRestoreData(ctx)
		if err != nil {
			operationErr = fmt.Errorf("load restore data: %w", err)
		} else {
			operationErr = exec.WakeUp(ctx, r.log, *spec, *rd)
		}
	default:
		operationErr = fmt.Errorf("unknown operation: %s", r.cfg.Operation)
	}

	duration := time.Since(startTime)
	durationMs := duration.Milliseconds()

	if operationErr != nil {
		r.log.Error(operationErr, "operation failed", "duration", duration)
		return durationMs, operationErr
	}

	r.log.Info("operation completed", "duration", duration)

	return durationMs, nil
}

// buildExecutorSpec constructs the executor spec from connector configuration.
func (r *runner) buildExecutorSpec(ctx context.Context, params map[string]interface{}) (*executor.Spec, func() error, error) {
	paramsBytes, _ := json.Marshal(params)
	spec := &executor.Spec{
		TargetName: r.cfg.Target,
		TargetType: r.cfg.TargetType,
		Parameters: paramsBytes,
	}

	// Add incremental save callback for shutdown operations
	var flusher func() error
	if r.cfg.Operation == "shutdown" {
		spec.SaveRestoreData, flusher = r.createSaveRestoreDataCallback(ctx)
	}

	switch r.cfg.ConnectorKind {
	case "CloudProvider":
		if err := r.loadCloudProviderConfig(ctx, spec); err != nil {
			return nil, nil, err
		}
	case "K8SCluster":
		if err := r.loadK8SClusterConfig(ctx, spec); err != nil {
			return nil, nil, err
		}
	}

	return spec, flusher, nil
}

const (
	awsAccessKeyIDKey     = "AWS_ACCESS_KEY_ID"
	awsSecretAccessKeyKey = "AWS_SECRET_ACCESS_KEY"
	awsSessionToken       = "AWS_SESSION_TOKEN"
	kubeconfigKey         = "kubeconfig"
)

func resolveNamespace(defaultNamespace, override string) string {
	if override != "" {
		return override
	}
	return defaultNamespace
}

// loadCloudProviderConfig populates the spec with CloudProvider configuration.
func (r *runner) loadCloudProviderConfig(ctx context.Context, spec *executor.Spec) error {
	provider, err := r.getCloudProvider(ctx, r.cfg.ConnectorNamespace, r.cfg.ConnectorName)
	if err != nil {
		return err
	}

	awsCfg, err := r.buildAWSConnectorConfig(ctx, &provider)
	if err != nil {
		return err
	}

	spec.ConnectorConfig.AWS = awsCfg
	return nil
}

func (r *runner) getCloudProvider(ctx context.Context, namespace, name string) (hibernatorv1alpha1.CloudProvider, error) {
	var provider hibernatorv1alpha1.CloudProvider
	key := client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}
	if err := r.k8sClient.Get(ctx, key, &provider); err != nil {
		return provider, fmt.Errorf("get CloudProvider: %w", err)
	}
	return provider, nil
}

func (r *runner) buildAWSConnectorConfig(ctx context.Context, provider *hibernatorv1alpha1.CloudProvider) (*executor.AWSConnectorConfig, error) {
	if provider.Spec.Type != hibernatorv1alpha1.CloudProviderAWS {
		return nil, fmt.Errorf("unsupported cloud provider type: %s", provider.Spec.Type)
	}
	if provider.Spec.AWS == nil {
		return nil, fmt.Errorf("AWS config is required")
	}

	awsCfg := &executor.AWSConnectorConfig{
		Region:    provider.Spec.AWS.Region,
		AccountID: provider.Spec.AWS.AccountId,
	}

	// AssumeRoleArn is now at AWS spec level (cross-cutting for both auth methods)
	if provider.Spec.AWS.AssumeRoleArn != "" {
		awsCfg.AssumeRoleArn = provider.Spec.AWS.AssumeRoleArn
	}

	if provider.Spec.AWS.Auth.Static != nil {
		ref := provider.Spec.AWS.Auth.Static.SecretRef
		secretNamespace := resolveNamespace(provider.Namespace, ref.Namespace)
		secret, err := r.getSecret(ctx, secretNamespace, ref.Name)
		if err != nil {
			return nil, err
		}

		accessKeyID := string(secret.Data[awsAccessKeyIDKey])
		secretAccessKey := string(secret.Data[awsSecretAccessKeyKey])
		if accessKeyID == "" || secretAccessKey == "" {
			return nil, fmt.Errorf("AWS static credentials must include %s and %s", awsAccessKeyIDKey, awsSecretAccessKeyKey)
		}

		awsCfg.AccessKeyID = accessKeyID
		awsCfg.SecretAccessKey = secretAccessKey

		session, ok := secret.Data[awsSessionToken]
		if ok {
			awsCfg.SessionToken = string(session)
		}
	}

	return awsCfg, nil
}

func (r *runner) getSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
	var secret corev1.Secret
	key := client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}
	if err := r.k8sClient.Get(ctx, key, &secret); err != nil {
		return nil, fmt.Errorf("get Secret %s/%s: %w", namespace, name, err)
	}
	return &secret, nil
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

	if cluster.Spec.EKS != nil && cluster.Spec.K8S != nil {
		return fmt.Errorf("spec.eks and spec.k8s are mutually exclusive")
	}

	if cluster.Spec.EKS != nil {
		if cluster.Spec.ProviderRef == nil {
			return fmt.Errorf("providerRef is required for EKS clusters")
		}

		providerNamespace := resolveNamespace(cluster.Namespace, cluster.Spec.ProviderRef.Namespace)
		provider, err := r.getCloudProvider(ctx, providerNamespace, cluster.Spec.ProviderRef.Name)
		if err != nil {
			return err
		}

		awsCfg, err := r.buildAWSConnectorConfig(ctx, &provider)
		if err != nil {
			return err
		}

		if cluster.Spec.EKS.Region != "" {
			awsCfg.Region = cluster.Spec.EKS.Region
		}

		awsSDKConfig, err := awsutil.BuildAWSConfig(ctx, awsCfg)
		if err != nil {
			return err
		}

		eksClient := eks.NewFromConfig(awsSDKConfig)
		clusterInfo, err := eksClient.DescribeCluster(ctx, &eks.DescribeClusterInput{
			Name: aws.String(cluster.Spec.EKS.Name),
		})
		if err != nil {
			return fmt.Errorf("describe EKS cluster: %w", err)
		}

		if clusterInfo.Cluster == nil {
			return fmt.Errorf("EKS cluster %s not found", cluster.Spec.EKS.Name)
		}

		endpoint := aws.ToString(clusterInfo.Cluster.Endpoint)
		if endpoint == "" {
			return fmt.Errorf("EKS cluster endpoint not available")
		}

		caData := aws.ToString(clusterInfo.Cluster.CertificateAuthority.Data)
		if caData == "" {
			return fmt.Errorf("EKS cluster certificate authority data missing")
		}

		decodedCA, err := base64.StdEncoding.DecodeString(caData)
		if err != nil {
			return fmt.Errorf("decode EKS certificate authority data: %w", err)
		}

		spec.ConnectorConfig.K8S = &executor.K8SConnectorConfig{
			ClusterName:     cluster.Spec.EKS.Name,
			Region:          cluster.Spec.EKS.Region,
			ClusterEndpoint: endpoint,
			ClusterCAData:   decodedCA,
			UseEKSToken:     true,
			AWS:             awsCfg,
		}
		return nil
	}

	if cluster.Spec.K8S != nil {
		if cluster.Spec.K8S.InCluster {
			spec.ConnectorConfig.K8S = &executor.K8SConnectorConfig{}
			return nil
		}

		if cluster.Spec.K8S.KubeconfigRef != nil {
			ref := cluster.Spec.K8S.KubeconfigRef
			secretNamespace := resolveNamespace(cluster.Namespace, ref.Namespace)
			secret, err := r.getSecret(ctx, secretNamespace, ref.Name)
			if err != nil {
				return err
			}
			kubeconfigBytes := secret.Data[kubeconfigKey]
			if len(kubeconfigBytes) == 0 {
				return fmt.Errorf("kubeconfig secret %s/%s missing %s key", secretNamespace, ref.Name, kubeconfigKey)
			}

			spec.ConnectorConfig.K8S = &executor.K8SConnectorConfig{
				Kubeconfig: kubeconfigBytes,
			}
			return nil
		}

		return fmt.Errorf("kubeconfigRef or inCluster must be specified for K8S access")
	}

	if cluster.Spec.GKE != nil {
		spec.ConnectorConfig.K8S = &executor.K8SConnectorConfig{
			ClusterName: cluster.Spec.GKE.Name,
			Region:      cluster.Spec.GKE.Location,
		}
	}

	return nil
}

// restoreDataAccumulator batches incremental saves in memory before flushing to ConfigMap.
// This reduces Kubernetes API calls from N*2 to 1 (where N = number of resources).
type restoreDataAccumulator struct {
	mu          sync.Mutex
	liveKeys    map[string]interface{} // High-quality state (isLive=true)
	nonLiveKeys map[string]interface{} // Low-quality state (isLive=false)
	log         logr.Logger
}

// add accumulates a key-value pair in memory with per-key quality tracking.
func (a *restoreDataAccumulator) add(key string, value interface{}, isLive bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Track quality per key by storing in separate maps
	if isLive {
		a.liveKeys[key] = value
		// Remove from nonLive if it was there (quality upgrade)
		delete(a.nonLiveKeys, key)
	} else {
		// Only add to nonLive if NOT already in live (preserve higher quality)
		if _, existsInLive := a.liveKeys[key]; !existsInLive {
			a.nonLiveKeys[key] = value
		}
	}

	a.log.V(1).Info("restore data accumulated in memory",
		"key", key,
		"isLive", isLive,
		"totalLiveKeys", len(a.liveKeys),
		"totalNonLiveKeys", len(a.nonLiveKeys),
	)
	return nil
}

// flush saves all accumulated data to ConfigMap in separate batches by quality.
// This preserves per-key quality information during merge while batching API calls.
func (a *restoreDataAccumulator) flush(ctx context.Context, rm *restore.Manager, namespace, plan, target, executorType string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	totalKeys := len(a.liveKeys) + len(a.nonLiveKeys)
	if totalKeys == 0 {
		a.log.V(1).Info("no restore data to flush")
		return nil
	}

	// Flush high-quality (isLive=true) keys first
	if len(a.liveKeys) > 0 {
		liveData := &restore.Data{
			Target:     target,
			Executor:   executorType,
			Version:    1,
			CreatedAt:  metav1.Now(),
			IsLive:     true,
			CapturedAt: time.Now().Format(time.RFC3339),
			State:      a.liveKeys,
		}

		if err := rm.SaveOrPreserve(ctx, namespace, plan, target, liveData); err != nil {
			return fmt.Errorf("flush live keys to ConfigMap: %w", err)
		}

		a.log.Info("restore data (live) flushed to ConfigMap",
			"liveKeys", len(a.liveKeys),
			"plan", plan,
			"target", target,
		)
	}

	// Flush low-quality (isLive=false) keys
	if len(a.nonLiveKeys) > 0 {
		nonLiveData := &restore.Data{
			Target:     target,
			Executor:   executorType,
			Version:    1,
			CreatedAt:  metav1.Now(),
			IsLive:     false,
			CapturedAt: time.Now().Format(time.RFC3339),
			State:      a.nonLiveKeys,
		}

		if err := rm.SaveOrPreserve(ctx, namespace, plan, target, nonLiveData); err != nil {
			return fmt.Errorf("flush non-live keys to ConfigMap: %w", err)
		}

		a.log.Info("restore data (non-live) flushed to ConfigMap",
			"nonLiveKeys", len(a.nonLiveKeys),
			"plan", plan,
			"target", target,
		)
	}

	return nil
}

// createSaveRestoreDataCallback returns a callback function and flush function for batched persistence.
// The callback accumulates saves in memory; flush writes accumulated data in two API calls (live + non-live).
// This reduces API calls significantly (1000 resources: 2000 calls → 2 calls) while preserving per-key quality.
func (r *runner) createSaveRestoreDataCallback(ctx context.Context) (executor.SaveRestoreDataFunc, func() error) {
	accumulator := &restoreDataAccumulator{
		liveKeys:    make(map[string]interface{}),
		nonLiveKeys: make(map[string]interface{}),
		log:         r.log,
	}

	// Callback: accumulate in memory (no API call)
	callback := func(key string, value interface{}, isLive bool) error {
		return accumulator.add(key, value, isLive)
	}

	// Flush function: save all accumulated data at once
	flush := func() error {
		rm := restore.NewManager(r.k8sClient)
		return accumulator.flush(ctx, rm, r.cfg.Namespace, r.cfg.Plan, r.cfg.Target, r.cfg.TargetType)
	}

	return callback, flush
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

	// Convert state map to unified map[string]json.RawMessage format
	unifiedData := make(map[string]json.RawMessage)
	for key, value := range data.State {
		valueBytes, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("marshal state value for key %s: %w", key, err)
		}
		unifiedData[key] = valueBytes
	}

	return &executor.RestoreData{
		Type: data.Executor,
		Data: unifiedData,
	}, nil
}

// ----------------------------------------------------------------------------
// Streaming Helpers
// ----------------------------------------------------------------------------

// initStreamingClient initializes the streaming client based on configuration.
func initStreamingClient(ctx context.Context, log logr.Logger, cfg *Config) (streamclient.StreamingClient, error) {
	if cfg.GRPCEndpoint == "" && cfg.WebSocketEndpoint == "" && cfg.HTTPCallbackEndpoint == "" && cfg.ControlPlaneEndpoint == "" {
		log.Info("no streaming endpoints configured, skipping streaming client")
		return nil, nil
	}

	grpcEndpoint := cfg.GRPCEndpoint
	webSocketEndpoint := cfg.WebSocketEndpoint
	httpCallbackEndpoint := cfg.HTTPCallbackEndpoint

	log.Info("DEBUG: Initializing streaming client", "executionID", cfg.ExecutionID)

	clientCfg := streamclient.ClientConfig{
		Type:         streamclient.ClientTypeAuto,
		GRPCAddress:  grpcEndpoint,
		WebSocketURL: webSocketEndpoint,
		WebhookURL:   httpCallbackEndpoint,
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
		"grpcEndpoint", grpcEndpoint,
		"webSocketEndpoint", webSocketEndpoint,
		"httpCallbackEndpoint", httpCallbackEndpoint,
	)

	return client, nil
}

// reportProgress reports execution progress via streaming client.
func (r *runner) reportProgress(ctx context.Context, phase string, percent int32, message string) {
	// Always log to stdout (via DualWriteSink which also streams)
	r.log.Info("progress",
		"phase", phase,
		"percent", percent,
		"message", message,
	)

	// Report progress separately via ReportProgress RPC
	if r.streamClient != nil {
		if err := r.streamClient.ReportProgress(ctx, phase, percent, message); err != nil {
			r.log.Error(err, "failed to report progress")
		}
	}
}

// reportCompletion reports execution completion via streaming client.
// Note: Restore data is persisted directly to ConfigMap, not sent via streaming.
func (r *runner) reportCompletion(ctx context.Context, success bool, errorMsg string, durationMs int64) {
	// Always log to stdout
	r.log.Info("completion",
		"success", success,
		"durationMs", durationMs,
		"errorMessage", errorMsg,
	)

	// Stream to control plane if available
	if r.streamClient != nil {
		if err := r.streamClient.ReportCompletion(ctx, success, errorMsg, durationMs); err != nil {
			r.log.Error(err, "failed to report completion")
		}
	}
}
