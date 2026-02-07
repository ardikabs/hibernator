/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package noop

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/executorparams"
)

func TestExecutor_Type(t *testing.T) {
	e := New()
	assert.Equal(t, "noop", e.Type())
}

func TestExecutor_Validate(t *testing.T) {
	tests := []struct {
		name    string
		spec    executor.Spec
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid with AWS connector",
			spec: executor.Spec{
				ConnectorConfig: executor.ConnectorConfig{
					AWS: &executor.AWSConnectorConfig{},
				},
				Parameters: json.RawMessage(`{"randomDelay": "2s"}`),
			},
			wantErr: false,
		},
		{
			name: "valid with K8S connector",
			spec: executor.Spec{
				ConnectorConfig: executor.ConnectorConfig{
					K8S: &executor.K8SConnectorConfig{},
				},
				Parameters: json.RawMessage(`{"randomDelay": "500ms"}`),
			},
			wantErr: false,
		},
		{
			name: "no connector provided",
			spec: executor.Spec{
				ConnectorConfig: executor.ConnectorConfig{},
				Parameters:      json.RawMessage(`{}`),
			},
			wantErr: true,
			errMsg:  "at least one connector",
		},
		{
			name: "delay exceeds maximum",
			spec: executor.Spec{
				ConnectorConfig: executor.ConnectorConfig{
					AWS: &executor.AWSConnectorConfig{},
				},
				Parameters: json.RawMessage(`{"randomDelaySeconds": 60}`),
			},
			wantErr: true,
			errMsg:  "randomDelaySeconds must be between 0 and 30",
		},
		{
			name: "negative delay",
			spec: executor.Spec{
				ConnectorConfig: executor.ConnectorConfig{
					AWS: &executor.AWSConnectorConfig{},
				},
				Parameters: json.RawMessage(`{"randomDelaySeconds": -5}`),
			},
			wantErr: true,
			errMsg:  "randomDelaySeconds must be between 0 and 30",
		},
		{
			name: "invalid failure mode",
			spec: executor.Spec{
				ConnectorConfig: executor.ConnectorConfig{
					AWS: &executor.AWSConnectorConfig{},
				},
				Parameters: json.RawMessage(`{"failureMode": "invalid"}`),
			},
			wantErr: true,
			errMsg:  "invalid failureMode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := New()
			err := e.Validate(tt.spec)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestExecutor_Shutdown(t *testing.T) {
	tests := []struct {
		name       string
		params     executorparams.NoOpParameters
		targetName string
		wantErr    bool
		errMsg     string
	}{
		{
			name: "successful shutdown",
			params: executorparams.NoOpParameters{
				RandomDelaySeconds: 1,
				FailureMode:        "none",
			},
			targetName: "test-target",
			wantErr:    false,
		},
		{
			name: "simulated shutdown failure",
			params: executorparams.NoOpParameters{
				RandomDelaySeconds: 1,
				FailureMode:        "shutdown",
			},
			targetName: "test-target-fail",
			wantErr:    true,
			errMsg:     "simulated shutdown failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := New()
			ctx := context.Background()

			paramsJSON, err := json.Marshal(tt.params)
			require.NoError(t, err)

			spec := executor.Spec{
				TargetName: tt.targetName,
				TargetType: "noop",
				Parameters: paramsJSON,
				ConnectorConfig: executor.ConnectorConfig{
					AWS: &executor.AWSConnectorConfig{},
				},
			}

			err = e.Shutdown(ctx, logr.Discard(), spec)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestExecutor_WakeUp(t *testing.T) {
	tests := []struct {
		name        string
		restoreData RestoreState
		wantErr     bool
		errMsg      string
	}{
		{
			name: "successful wakeup",
			restoreData: RestoreState{
				Parameters: executorparams.NoOpParameters{
					RandomDelaySeconds: 1,
					FailureMode:        "none",
				},
				OperationTime: time.Now(),
				GeneratedID:   "test-id",
				TargetName:    "test-target",
			},
			wantErr: false,
		},
		{
			name: "simulated wakeup failure",
			restoreData: RestoreState{
				Parameters: executorparams.NoOpParameters{
					RandomDelaySeconds: 1,
					FailureMode:        "wakeup",
				},
				OperationTime: time.Now(),
				GeneratedID:   "test-id",
				TargetName:    "test-target",
			},
			wantErr: true,
			errMsg:  "simulated wakeup failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := New()
			ctx := context.Background()

			restoreDataJSON, err := json.Marshal(tt.restoreData)
			require.NoError(t, err)

			spec := executor.Spec{
				TargetName: tt.restoreData.TargetName,
				TargetType: "noop",
				ConnectorConfig: executor.ConnectorConfig{
					AWS: &executor.AWSConnectorConfig{},
				},
			}

			restore := executor.RestoreData{
				Type: "noop",
				Data: map[string]json.RawMessage{
					"state": restoreDataJSON,
				},
			}

			err = e.WakeUp(ctx, logr.Discard(), spec, restore)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestExecutor_Shutdown_ContextCancellation(t *testing.T) {
	e := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	spec := executor.Spec{
		TargetName: "test-target",
		Parameters: json.RawMessage(`{"randomDelay": "5s"}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{},
		},
	}

	err := e.Shutdown(ctx, logr.Discard(), spec)
	require.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

func TestExecutor_parseParams_Defaults(t *testing.T) {
	e := New()

	params, err := e.parseParams(json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.Equal(t, 1, params.RandomDelaySeconds)
	assert.Equal(t, "none", params.FailureMode)
}

func TestExecutor_getDelay(t *testing.T) {
	e := New()

	tests := []struct {
		name       string
		maxSeconds int
		wantMin    time.Duration
		wantMax    time.Duration
	}{
		{
			name:       "valid seconds returns random between 0 and max",
			maxSeconds: 2,
			wantMin:    0,
			wantMax:    2 * time.Second,
		},
		{
			name:       "zero defaults to random between 0-1s",
			maxSeconds: 0,
			wantMin:    0,
			wantMax:    time.Second,
		},
		{
			name:       "exceeds max caps at 30s",
			maxSeconds: 60,
			wantMin:    0,
			wantMax:    30 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test multiple times to verify randomness
			for i := 0; i < 10; i++ {
				got := e.getDelay(tt.maxSeconds)
				assert.GreaterOrEqual(t, got, tt.wantMin, "delay should be >= 0")
				assert.LessOrEqual(t, got, tt.wantMax, "delay should be <= max")
			}
		})
	}
}
