/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/runner/metadata"
	"github.com/ardikabs/hibernator/cmd/runner/state"
	"github.com/ardikabs/hibernator/cmd/runner/telemetry"
	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/internal/restore"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(hibernatorv1alpha1.AddToScheme(scheme))
}

// runner encapsulates the execution context and dependencies.
type runner struct {
	cfg           *Config
	log           logr.Logger
	k8sClient     client.Client
	telemetryMgr  *telemetry.Manager
	registry      *executor.Registry
	configBuilder *metadata.ConfigBuilder
}

// newRunner creates a new runner instance.
func newRunner(ctx context.Context, log logr.Logger, cfg *Config) (*runner, error) {
	r := &runner{
		cfg:      cfg,
		log:      log,
		registry: executor.NewRegistry(),
	}

	// Initialize telemetry config (non-fatal if unavailable)
	telemetryCfg := telemetry.Config{
		GRPCEndpoint:         cfg.GRPCEndpoint,
		WebSocketEndpoint:    cfg.WebSocketEndpoint,
		HTTPCallbackEndpoint: cfg.HTTPCallbackEndpoint,
		ControlPlaneEndpoint: cfg.ControlPlaneEndpoint,
		ExecutionID:          cfg.ExecutionID,
		TokenPath:            cfg.TokenPath,
		UseTLS:               cfg.UseTLS,
	}

	telemetryMgr, err := telemetry.NewManager(ctx, r.log, telemetryCfg)
	if err != nil {
		log.Error(err, "failed to initialize telemetry manager, continuing without telemetry")
	} else {
		r.telemetryMgr = telemetryMgr
		r.log = telemetryMgr.WithTelemetrySink()
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
	r.configBuilder = metadata.NewConfigBuilder(k8sClient, r.log)

	// Register executors
	factory := newExecutorFactoryRegistry()
	factory.registerTo(r.registry, r.log)

	return r, nil
}

// close cleans up runner resources.
func (r *runner) close() {
	// Then close streaming client
	if r.telemetryMgr != nil {
		if err := r.telemetryMgr.Close(); err != nil {
			r.log.Error(err, "failed to close telemetry manager")
		}
	}
}

// run executes the hibernation operation.
// Returns the executor result and any error.
func (r *runner) run(ctx context.Context) (*executor.Result, error) {
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
	if r.telemetryMgr != nil {
		r.telemetryMgr.StartHeartbeat(30 * time.Second)
	}

	// Report progress: initializing
	if r.telemetryMgr != nil {
		r.telemetryMgr.ReportProgress(ctx, "initializing", 10, "Loading executors")
	}

	// Get the executor
	exec, ok := r.registry.Get(cfg.TargetType)
	if !ok {
		err := fmt.Errorf("executor not found: %s", cfg.TargetType)
		r.log.Error(err, "failed to get executor")
		return nil, err
	}

	// Parse target parameters
	var params map[string]any
	if cfg.TargetParams != "" {
		if err := json.Unmarshal([]byte(cfg.TargetParams), &params); err != nil {
			r.log.Error(err, "failed to parse target params")
			return nil, fmt.Errorf("parse target params: %w", err)
		}
	}

	// Report progress: building spec
	if r.telemetryMgr != nil {
		r.telemetryMgr.ReportProgress(ctx, "preparing", 20, "Building executor spec")
	}

	// Build executor spec from connector
	spec, flusher, err := r.buildExecutorSpec(ctx, params)
	if err != nil {
		r.log.Error(err, "failed to build executor spec")
		return nil, fmt.Errorf("build executor spec: %w", err)
	}

	// Validate the spec
	if r.telemetryMgr != nil {
		r.telemetryMgr.ReportProgress(ctx, "validating", 30, "Validating executor spec")
	}
	if err := exec.Validate(*spec); err != nil {
		r.log.Error(err, "spec validation failed")
		return nil, fmt.Errorf("validate spec: %w", err)
	}

	// Report progress: executing
	if r.telemetryMgr != nil {
		r.telemetryMgr.ReportProgress(ctx, "executing", 50, fmt.Sprintf("Executing %s operation", cfg.Operation))
	}

	// Execute the operation
	result, err := r.executeOperation(ctx, exec, spec, flusher)

	// Operation failure: report and return
	if err != nil {
		if cfg.Operation == "shutdown" {
			r.log.Error(err, "shutdown failed")
		}
		if r.telemetryMgr != nil {
			r.telemetryMgr.ReportCompletion(ctx, false, err.Error(), result.ElapsedMs)
		}
		return nil, err
	}

	// Report progress: finalizing
	if r.telemetryMgr != nil {
		r.telemetryMgr.ReportProgress(ctx, "finalizing", 90, "Finalizing operation")
	}

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
	if r.telemetryMgr != nil {
		r.telemetryMgr.ReportCompletion(ctx, true, "", result.ElapsedMs)
	}

	return result, nil
}

// executeOperation runs the shutdown or wakeup operation.
// For shutdown operations, returns a flush function to save accumulated restore data.
// Returns the executor Result (always non-nil) for the caller to inspect.
// On error the Result still carries ElapsedMs so callers can report timing.
func (r *runner) executeOperation(ctx context.Context, exec executor.Executor, spec *executor.Spec, flushFunc func() error) (*executor.Result, error) {
	startTime := time.Now()

	var operationErr error
	var executorResult *executor.Result

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

		result, err := exec.Shutdown(ctx, r.log, *spec)
		if err != nil {
			operationErr = err
		} else {
			executorResult = result
		}
	case "wakeup":
		rd, err := state.LoadRestoreData(ctx, r.k8sClient, r.log, r.cfg.Namespace, r.cfg.Plan, r.cfg.Target)
		if err != nil {
			operationErr = fmt.Errorf("load restore data: %w", err)
			break
		}

		if !rd.IsLive {
			r.log.Info("warning: restore data is from non-live source; restore point may be outdated.")
		}

		result, err := exec.WakeUp(ctx, r.log, *spec, *rd)
		if err != nil {
			operationErr = err
		} else {
			executorResult = result
		}
	default:
		operationErr = fmt.Errorf("unknown operation: %s", r.cfg.Operation)
	}

	duration := time.Since(startTime)

	// Always return a non-nil result so callers can read ElapsedMs even on error.
	if executorResult == nil {
		executorResult = &executor.Result{}
	}
	executorResult.ElapsedMs = duration.Milliseconds()

	if operationErr != nil {
		r.log.Error(operationErr, "operation failed", "duration", duration)
		return executorResult, operationErr
	}

	r.log.Info("operation completed", "duration", duration)

	return executorResult, nil
}

// buildExecutorSpec constructs the executor spec from connector configuration.
func (r *runner) buildExecutorSpec(ctx context.Context, params map[string]any) (*executor.Spec, func() error, error) {
	paramsBytes, _ := json.Marshal(params)
	spec := &executor.Spec{
		TargetName: r.cfg.Target,
		TargetType: r.cfg.TargetType,
		Parameters: paramsBytes,
	}

	// Add incremental save callback for shutdown operations
	var flusher func() error
	if r.cfg.Operation == "shutdown" {
		callback, flush := state.NewReportStateHandlers(ctx, r.k8sClient, r.log, r.cfg.Namespace, r.cfg.Plan, r.cfg.Target, r.cfg.TargetType, r.cfg.CycleID)
		spec.ReportStateCallback = callback
		flusher = flush
	}

	if r.cfg.ConnectorKind == "CloudProvider" || r.cfg.ConnectorKind == "K8SCluster" {
		connectorCfg, err := r.configBuilder.BuildConnectorConfig(ctx, r.cfg.ConnectorKind, r.cfg.ConnectorNamespace, r.cfg.ConnectorName)
		if err != nil {
			return nil, nil, err
		}
		spec.ConnectorConfig = connectorCfg
	}

	return spec, flusher, nil
}
