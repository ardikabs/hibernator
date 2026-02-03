/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package cloudsql

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/executorparams"
)

// Executor implements hibernation for GCP Cloud SQL instances.
type Executor struct{}

// New creates a new Cloud SQL executor.
func New() *Executor {
	return &Executor{}
}

// Type returns the executor type.
func (e *Executor) Type() string {
	return "cloudsql"
}

// Validate validates the executor spec.
func (e *Executor) Validate(spec executor.Spec) error {
	var params executorparams.CloudSQLParameters
	if err := json.Unmarshal(spec.Parameters, &params); err != nil {
		return fmt.Errorf("parse parameters: %w", err)
	}

	if params.InstanceName == "" {
		return fmt.Errorf("instanceName is required")
	}
	if params.Project == "" {
		return fmt.Errorf("project is required")
	}

	return nil
}

// Shutdown stops a Cloud SQL instance.
func (e *Executor) Shutdown(ctx context.Context, log logr.Logger, spec executor.Spec) (executor.RestoreData, error) {
	_ = log
	var params executorparams.CloudSQLParameters
	if err := json.Unmarshal(spec.Parameters, &params); err != nil {
		return executor.RestoreData{}, fmt.Errorf("parse parameters: %w", err)
	}

	// TODO: Implement actual Cloud SQL API calls using google.golang.org/api/sqladmin/v1
	// For now, return a placeholder implementation

	state := InstanceState{
		InstanceName: params.InstanceName,
		Project:      params.Project,
		Tier:         "",         // Would be fetched from Cloud SQL API
		Status:       "RUNNABLE", // Current status before stopping
	}

	// Build restore data
	stateBytes, err := json.Marshal(state)
	if err != nil {
		return executor.RestoreData{}, fmt.Errorf("marshal restore data: %w", err)
	}

	return executor.RestoreData{
		Type: e.Type(),
		Data: stateBytes,
	}, nil
}

// WakeUp starts a Cloud SQL instance.
func (e *Executor) WakeUp(ctx context.Context, log logr.Logger, spec executor.Spec, restore executor.RestoreData) error {
	_ = log
	if len(restore.Data) == 0 {
		return fmt.Errorf("restore data is required for wake-up")
	}

	var state InstanceState
	if err := json.Unmarshal(restore.Data, &state); err != nil {
		return fmt.Errorf("unmarshal restore data: %w", err)
	}

	// TODO: Implement actual Cloud SQL API calls to start the instance
	// For now, this is a placeholder
	_ = state

	return nil
}

// InstanceState stores the original state of a Cloud SQL instance.
type InstanceState struct {
	InstanceName string `json:"instanceName"`
	Project      string `json:"project"`
	Tier         string `json:"tier"`
	Status       string `json:"status"`
}
