/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package noop implements a no-operation executor for testing hibernation workflows
// without external dependencies. It simulates realistic operations with configurable
// delays and failure modes, making it ideal for local development and testing.
package noop

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/uuid"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/executorparams"
)

const ExecutorType = "noop"

// Parameters is an alias for the shared NoOp parameter type.
type Parameters = executorparams.NoOpParameters

// RestoreState holds NoOp restore data that echoes back parameters and operation metadata.
type RestoreState struct {
	// Parameters are the original parameters passed to the executor
	Parameters Parameters `json:"parameters"`
	// OperationTime records when the shutdown operation was performed
	OperationTime time.Time `json:"operationTime"`
	// GeneratedID is a unique identifier for this operation
	GeneratedID string `json:"generatedId"`
	// TargetName echoes back the target name for verification
	TargetName string `json:"targetName"`
}

// Executor implements the NoOp hibernation logic.
type Executor struct{}

// New creates a new NoOp executor.
func New() *Executor {
	return &Executor{}
}

// Type returns the executor type.
func (e *Executor) Type() string {
	return ExecutorType
}

// Validate validates the executor spec.
func (e *Executor) Validate(spec executor.Spec) error {
	// Validate that at least one connector is provided (but don't use it)
	if spec.ConnectorConfig.AWS == nil && spec.ConnectorConfig.K8S == nil {
		return fmt.Errorf("at least one connector (AWS or K8S) must be provided")
	}

	params, err := e.parseParams(spec.Parameters)
	if err != nil {
		return err
	}

	return e.validateParams(params)
}

// Shutdown simulates hibernation with configurable delay and failure modes.
func (e *Executor) Shutdown(ctx context.Context, log logr.Logger, spec executor.Spec) error {
	log.Info("NoOp executor starting shutdown",
		"target", spec.TargetName,
		"targetType", spec.TargetType,
	)

	params, err := e.parseParams(spec.Parameters)
	if err != nil {
		log.Error(err, "failed to parse parameters")
		return fmt.Errorf("parse parameters: %w", err)
	}

	log.Info("parameters parsed and validated",
		"randomDelaySeconds", params.RandomDelaySeconds,
		"failureMode", params.FailureMode,
	)

	// Check for failure simulation
	if params.FailureMode == "shutdown" || params.FailureMode == "both" {
		log.Info("simulating shutdown failure", "failureMode", params.FailureMode)
		return fmt.Errorf("simulated shutdown failure (failureMode=%s)", params.FailureMode)
	}

	// Simulate work with delay
	delay := e.getDelay(params.RandomDelaySeconds)
	log.Info("simulating work with random delay",
		"maxDelaySeconds", params.RandomDelaySeconds,
		"actualDelay", delay,
	)

	select {
	case <-ctx.Done():
		log.Info("operation cancelled by context")
		return ctx.Err()
	case <-time.After(delay):
		log.Info("work simulation completed")
	}

	// Create restore state
	restoreState := RestoreState{
		Parameters:    params,
		OperationTime: time.Now().UTC(),
		GeneratedID:   uuid.New().String(),
		TargetName:    spec.TargetName,
	}

	log.Info("generating restore state",
		"generatedID", restoreState.GeneratedID,
		"operationTime", restoreState.OperationTime,
	)

	// Build unified map-based restore data (key = operationID)
	unifiedData := make(map[string]json.RawMessage)
	stateBytes, _ := json.Marshal(restoreState)
	unifiedData[restoreState.GeneratedID] = stateBytes

	log.Info("NoOp shutdown completed successfully",
		"target", spec.TargetName,
		"generatedID", restoreState.GeneratedID,
		"restoreDataSize", len(stateBytes),
	)

	return nil
}

// WakeUp simulates restoration using saved restore data.
func (e *Executor) WakeUp(ctx context.Context, log logr.Logger, spec executor.Spec, restore executor.RestoreData) error {
	log.Info("NoOp executor starting wakeup",
		"target", spec.TargetName,
		"targetType", spec.TargetType,
		"restoreDataCount", len(restore.Data),
	)

	// Iterate over all operations in restore data (should be single operation for noop)
	for operationID, stateBytes := range restore.Data {
		var restoreState RestoreState
		if err := json.Unmarshal(stateBytes, &restoreState); err != nil {
			log.Error(err, "failed to unmarshal restore state", "operationId", operationID)
			return fmt.Errorf("unmarshal restore state %s: %w", operationID, err)
		}

		log.Info("restore state loaded",
			"generatedID", restoreState.GeneratedID,
			"shutdownTime", restoreState.OperationTime,
			"originalTarget", restoreState.TargetName,
			"failureMode", restoreState.Parameters.FailureMode,
		)

		// Check for failure simulation
		if restoreState.Parameters.FailureMode == "wakeup" || restoreState.Parameters.FailureMode == "both" {
			log.Info("simulating wakeup failure", "failureMode", restoreState.Parameters.FailureMode)
			return fmt.Errorf("simulated wakeup failure (failureMode=%s)", restoreState.Parameters.FailureMode)
		}

		// Simulate work with delay
		delay := e.getDelay(restoreState.Parameters.RandomDelaySeconds)
		log.Info("simulating restoration work with random delay",
			"maxDelaySeconds", restoreState.Parameters.RandomDelaySeconds,
			"actualDelay", delay,
		)

		select {
		case <-ctx.Done():
			log.Info("operation cancelled by context")
			return ctx.Err()
		case <-time.After(delay):
			log.Info("restoration work simulation completed")
		}

		log.Info("NoOp wakeup completed for operation",
			"target", spec.TargetName,
			"generatedID", restoreState.GeneratedID,
		)
	}

	return nil
}

// parseParams parses and returns parameters with defaults applied.
func (e *Executor) parseParams(raw json.RawMessage) (Parameters, error) {
	var params Parameters
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return Parameters{}, fmt.Errorf("unmarshal parameters: %w", err)
		}
	}

	// Apply defaults
	if params.RandomDelaySeconds == 0 {
		params.RandomDelaySeconds = 1
	}
	if params.FailureMode == "" {
		params.FailureMode = "none"
	}

	return params, nil
}

// validateParams validates parsed parameters.
func (e *Executor) validateParams(params Parameters) error {
	// Validate randomDelaySeconds is within allowed range
	if params.RandomDelaySeconds < 0 || params.RandomDelaySeconds > 30 {
		return fmt.Errorf("randomDelaySeconds must be between 0 and 30")
	}

	// Validate failure mode
	validFailureModes := map[string]bool{
		"none":     true,
		"shutdown": true,
		"wakeup":   true,
		"both":     true,
	}
	if !validFailureModes[params.FailureMode] {
		return fmt.Errorf("invalid failureMode: %s. Valid values: none, shutdown, wakeup, both", params.FailureMode)
	}

	return nil
}

// getDelay returns a random duration between 0 and the specified seconds.
// maxSeconds must be between 0-30. Returns random duration between 0-1s if maxSeconds is 0.
func (e *Executor) getDelay(maxSeconds int) time.Duration {
	if maxSeconds <= 0 {
		// Random delay between 0-1s
		return time.Duration(rand.Int64N(int64(time.Second)))
	}

	// Cap at 30 seconds
	if maxSeconds > 30 {
		maxSeconds = 30
	}

	maxDelay := time.Duration(maxSeconds) * time.Second
	// Return random duration between 0 and maxDelay
	return time.Duration(rand.Int64N(int64(maxDelay)))
}
