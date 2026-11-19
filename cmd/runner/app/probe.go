/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package app

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/runner/metadata"
	"github.com/ardikabs/hibernator/internal/connectorprobe"
	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// OperationProbe is the runner operation that checks a connector.
const OperationProbe = "probe"

// terminationLogPath is a var so tests can redirect it.
var terminationLogPath = wellknown.TerminationLogPath

type connectorConfigBuilder interface {
	BuildConnectorConfig(ctx context.Context, kind, namespace, name string) (executor.ConnectorConfig, error)
}

type connectorProber interface {
	Probe(ctx context.Context, kind string, cfg executor.ConnectorConfig, execs []executor.Executor) (connectorprobe.Result, error)
}

// runProbeMode runs the probe operation: no telemetry, streaming or plan needed.
func runProbeMode(ctx context.Context, log logr.Logger, cfg *Config) error {
	restCfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("get kubeconfig: %w", err)
	}
	k8sClient, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("create k8s client: %w", err)
	}

	builder := metadata.NewConfigBuilder(k8sClient, log)
	return executeProbe(ctx, log, cfg, builder, connectorprobe.NewProber(), probeExecutors(log))
}

// executeProbe runs the probe and records the verdict. A non-nil error means
// no verdict was produced and the process must exit non-zero.
func executeProbe(ctx context.Context, log logr.Logger, cfg *Config, builder connectorConfigBuilder, prober connectorProber, execs []executor.Executor) error {
	res, err := runProbe(ctx, log, cfg, builder, prober, execs)
	if err != nil {
		log.Error(err, "probe could not complete")
		writeProbeErrorLog(log, err)
		return err
	}
	writeProbeTerminationLog(log, res)
	log.Info("probe completed", "ready", res.Ready, "reason", res.Reason)
	return nil
}

// probeExecutors returns every executor registered for this runner process.
func probeExecutors(log logr.Logger) []executor.Executor {
	registry := executor.NewRegistry()
	newExecutorFactoryRegistry().registerTo(registry, log)

	var execs []executor.Executor
	for _, t := range registry.List() {
		if e, ok := registry.Get(t); ok {
			execs = append(execs, e)
		}
	}
	return execs
}

func runProbe(ctx context.Context, log logr.Logger, cfg *Config, builder connectorConfigBuilder, prober connectorProber, execs []executor.Executor) (connectorprobe.Result, error) {
	kind := cfg.ConnectorKind
	if kind != executor.ConnectorKindCloudProvider && kind != executor.ConnectorKindK8SCluster {
		return connectorprobe.Result{}, fmt.Errorf("unsupported connector kind %q", kind)
	}
	if cfg.ConnectorName == "" {
		return connectorprobe.Result{}, errors.New("connector name is required")
	}

	connCfg, err := builder.BuildConnectorConfig(ctx, kind, cfg.ConnectorNamespace, cfg.ConnectorName)
	if err != nil {
		switch {
		case errors.Is(err, metadata.ErrSecretNotFound):
			log.Info("connector secret not found", "error", err.Error())
			return connectorprobe.Result{Reason: hibernatorv1alpha1.ReasonSecretNotFound, Message: err.Error()}, nil
		case errors.Is(err, metadata.ErrCredentialsIncomplete):
			log.Info("connector credentials incomplete", "error", err.Error())
			return connectorprobe.Result{Reason: hibernatorv1alpha1.ReasonInvalidCredentials, Message: err.Error()}, nil
		default:
			return connectorprobe.Result{}, fmt.Errorf("build connector config: %w", err)
		}
	}

	return prober.Probe(ctx, kind, connCfg, execs)
}

func writeProbeTerminationLog(log logr.Logger, res connectorprobe.Result) {
	if err := os.WriteFile(terminationLogPath, []byte(res.Marshal()), 0644); err != nil {
		log.Error(err, "failed to write termination log")
	}
}

func writeProbeErrorLog(log logr.Logger, probeErr error) {
	if err := os.WriteFile(terminationLogPath, []byte("probe failed: "+probeErr.Error()), 0644); err != nil {
		log.Error(err, "failed to write termination log")
	}
}
