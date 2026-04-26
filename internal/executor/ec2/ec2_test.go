/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package ec2

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/internal/executor/ec2/mocks"
)

func TestNew(t *testing.T) {
	e := New()
	assert.NotNil(t, e)
	assert.NotNil(t, e.ec2Factory)
}

func TestNewWithClients(t *testing.T) {
	mockEC2 := &mocks.EC2Client{}

	ec2Factory := func(cfg aws.Config) EC2Client { return mockEC2 }

	e := NewWithClients(ec2Factory, nil)
	assert.NotNil(t, e)
}

func TestExecutorType(t *testing.T) {
	e := New()
	assert.Equal(t, "ec2", e.Type())
	assert.Equal(t, ExecutorType, e.Type())
}

func TestValidate_MissingAWSConfig(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName:      "test-instances",
		TargetType:      "ec2",
		Parameters:      json.RawMessage(`{"selector": {"tags": {"Environment": "dev"}}}`),
		ConnectorConfig: executor.ConnectorConfig{},
	}
	err := e.Validate(spec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "AWS connector config required")
}

func TestValidate_MissingSelector(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-instances",
		TargetType: "ec2",
		Parameters: json.RawMessage(`{"selector": {}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}
	err := e.Validate(spec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "either tags or instanceIds must be specified")
}

func TestValidate_WithTags(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-instances",
		TargetType: "ec2",
		Parameters: json.RawMessage(`{"selector": {"tags": {"Environment": "dev"}}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}
	err := e.Validate(spec)
	assert.NoError(t, err)
}

func TestValidate_WithInstanceIDs(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-instances",
		TargetType: "ec2",
		Parameters: json.RawMessage(`{"selector": {"instanceIds": ["i-1234567890abcdef0"]}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}
	err := e.Validate(spec)
	assert.NoError(t, err)
}

func TestShutdown_StopRunningInstances(t *testing.T) {
	ctx := context.Background()

	mockEC2 := &mocks.EC2Client{}

	// Setup expectation for DescribeInstances
	mockEC2.On("DescribeInstances", mock.Anything, mock.MatchedBy(func(input *awsec2.DescribeInstancesInput) bool {
		return len(input.Filters) > 0
	})).Return(&awsec2.DescribeInstancesOutput{
		Reservations: []types.Reservation{
			{
				Instances: []types.Instance{
					{
						InstanceId: aws.String("i-123456"),
						State: &types.InstanceState{
							Name: types.InstanceStateNameRunning,
						},
					},
					{
						InstanceId: aws.String("i-789012"),
						State: &types.InstanceState{
							Name: types.InstanceStateNameStopped,
						},
					},
				},
			},
		},
	}, nil)

	// Setup expectation for StopInstances (only running instance)
	mockEC2.On("StopInstances", mock.Anything, &awsec2.StopInstancesInput{
		InstanceIds: []string{"i-123456"},
	}).Return(&awsec2.StopInstancesOutput{}, nil)

	ec2Factory := func(cfg aws.Config) EC2Client { return mockEC2 }

	e := NewWithClients(ec2Factory, nil)

	spec := executor.Spec{
		TargetName: "test-instances",
		TargetType: "ec2",
		Parameters: json.RawMessage(`{"selector": {"tags": {"Environment": "dev"}}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	_, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)

	mockEC2.AssertExpectations(t)
}

func TestShutdown_NoInstancesToStop(t *testing.T) {
	ctx := context.Background()

	mockEC2 := &mocks.EC2Client{}

	// Setup expectation for DescribeInstances returning empty
	mockEC2.On("DescribeInstances", mock.Anything, mock.Anything).Return(&awsec2.DescribeInstancesOutput{
		Reservations: []types.Reservation{},
	}, nil)

	ec2Factory := func(cfg aws.Config) EC2Client { return mockEC2 }

	e := NewWithClients(ec2Factory, nil)

	spec := executor.Spec{
		TargetName: "test-instances",
		TargetType: "ec2",
		Parameters: json.RawMessage(`{"selector": {"tags": {"NonExistent": "tag"}}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	_, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)
}

func TestShutdown_DescribeInstancesError(t *testing.T) {
	ctx := context.Background()

	mockEC2 := &mocks.EC2Client{}

	// Setup expectation to return error
	mockEC2.On("DescribeInstances", mock.Anything, mock.Anything).
		Return(nil, errors.New("access denied"))

	ec2Factory := func(cfg aws.Config) EC2Client { return mockEC2 }

	e := NewWithClients(ec2Factory, nil)

	spec := executor.Spec{
		TargetName: "test-instances",
		TargetType: "ec2",
		Parameters: json.RawMessage(`{"selector": {"tags": {"Environment": "dev"}}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	_, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access denied")
}

func TestWakeUp_StartPreviouslyRunningInstances(t *testing.T) {
	ctx := context.Background()

	mockEC2 := &mocks.EC2Client{}

	// Setup expectation for DescribeInstances to discover stopped instances.
	mockEC2.On("DescribeInstances", mock.Anything, mock.Anything).Return(&awsec2.DescribeInstancesOutput{
		Reservations: []types.Reservation{
			{
				Instances: []types.Instance{
					{
						InstanceId: aws.String("i-123456"),
						State: &types.InstanceState{
							Name: types.InstanceStateNameStopped,
						},
					},
					{
						InstanceId: aws.String("i-789012"),
						State: &types.InstanceState{
							Name: types.InstanceStateNameStopped,
						},
					},
				},
			},
		},
	}, nil)

	// Setup expectation for StartInstances
	mockEC2.On("StartInstances", mock.Anything, &awsec2.StartInstancesInput{
		InstanceIds: []string{"i-123456"},
	}).Return(&awsec2.StartInstancesOutput{}, nil)

	ec2Factory := func(cfg aws.Config) EC2Client { return mockEC2 }

	e := NewWithClients(ec2Factory, nil)

	// Create per-instance restore data (key = instanceID)
	instance1State, _ := json.Marshal(InstanceState{InstanceID: "i-123456", WasRunning: true})
	instance2State, _ := json.Marshal(InstanceState{InstanceID: "i-789012", WasRunning: false})

	spec := executor.Spec{
		TargetName: "test-instances",
		TargetType: "ec2",
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore := executor.RestoreData{
		Type: "ec2",
		Data: map[string]json.RawMessage{
			"i-123456": instance1State,
			"i-789012": instance2State,
		},
	}

	_, err := e.WakeUp(ctx, logr.Discard(), spec, restore)
	assert.NoError(t, err)

	mockEC2.AssertExpectations(t)
}

func TestWakeUp_StartInstancesSkipsMissingIDs(t *testing.T) {
	ctx := context.Background()

	mockEC2 := &mocks.EC2Client{}

	// Setup expectation for DescribeInstances to discover stopped instances.
	mockEC2.On("DescribeInstances", mock.Anything, mock.Anything).Return(&awsec2.DescribeInstancesOutput{
		Reservations: []types.Reservation{
			{
				Instances: []types.Instance{
					{
						InstanceId: aws.String("i-valid"),
						State: &types.InstanceState{
							Name: types.InstanceStateNameStopped,
						},
					},
					{
						InstanceId: aws.String("i-missing"),
						State: &types.InstanceState{
							Name: types.InstanceStateNameStopped,
						},
					},
				},
			},
		},
	}, nil)

	isBulkStartInput := func(input *awsec2.StartInstancesInput) bool {
		if input == nil || len(input.InstanceIds) != 2 {
			return false
		}

		hasValid := false
		hasMissing := false
		for _, id := range input.InstanceIds {
			switch id {
			case "i-valid":
				hasValid = true
			case "i-missing":
				hasMissing = true
			}
		}

		return hasValid && hasMissing
	}

	// First bulk start fails because one instance no longer exists.
	mockEC2.On("StartInstances", mock.Anything, mock.MatchedBy(isBulkStartInput)).
		Return((*awsec2.StartInstancesOutput)(nil), &smithy.GenericAPIError{Code: "InvalidInstanceID.NotFound", Message: "The instance ID 'i-missing' does not exist", Fault: smithy.FaultClient}).
		Once()

	// Fallback starts valid instance successfully.
	mockEC2.On("StartInstances", mock.Anything, &awsec2.StartInstancesInput{InstanceIds: []string{"i-valid"}}).
		Return(&awsec2.StartInstancesOutput{}, nil).
		Once()

	// Missing instance is skipped during fallback.
	mockEC2.On("StartInstances", mock.Anything, &awsec2.StartInstancesInput{InstanceIds: []string{"i-missing"}}).
		Return((*awsec2.StartInstancesOutput)(nil), &smithy.GenericAPIError{Code: "InvalidInstanceID.NotFound", Message: "The instance ID 'i-missing' does not exist", Fault: smithy.FaultClient}).
		Once()

	ec2Factory := func(cfg aws.Config) EC2Client { return mockEC2 }

	e := NewWithClients(ec2Factory, nil)

	instanceValidState, _ := json.Marshal(InstanceState{InstanceID: "i-valid", WasRunning: true})
	instanceMissingState, _ := json.Marshal(InstanceState{InstanceID: "i-missing", WasRunning: true})

	spec := executor.Spec{
		TargetName: "test-instances",
		TargetType: "ec2",
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore := executor.RestoreData{
		Type: "ec2",
		Data: map[string]json.RawMessage{
			"i-valid":   instanceValidState,
			"i-missing": instanceMissingState,
		},
	}

	result, err := e.WakeUp(ctx, logr.Discard(), spec, restore)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Contains(t, result.Message, "started 1 EC2 instance(s)")
	assert.Contains(t, result.Message, "skipped 1 missing instance(s)")

	mockEC2.AssertExpectations(t)
}

func TestWakeUp_InvalidRestoreData(t *testing.T) {
	e := New()
	ctx := context.Background()

	spec := executor.Spec{
		TargetName: "test-instances",
		TargetType: "ec2",
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore := executor.RestoreData{
		Type: "ec2",
		Data: map[string]json.RawMessage{
			"invalid": json.RawMessage(`{invalid json}`),
		},
	}

	_, err := e.WakeUp(ctx, logr.Discard(), spec, restore)
	assert.Error(t, err)
}

func TestShutdown_InvalidParameters(t *testing.T) {
	e := New()
	ctx := context.Background()

	spec := executor.Spec{
		TargetName: "test-instances",
		TargetType: "ec2",
		Parameters: json.RawMessage(`{invalid json}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	_, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.Error(t, err)
}

// ============================================================================
// Data type tests
// ============================================================================

func TestInstanceState_JSON(t *testing.T) {
	state := InstanceState{
		InstanceID: "i-123456",
		WasRunning: true,
	}

	data, _ := json.Marshal(state)
	var decoded InstanceState
	json.Unmarshal(data, &decoded)

	assert.Equal(t, "i-123456", decoded.InstanceID)
	assert.True(t, decoded.WasRunning)
}

func TestSelector_JSON(t *testing.T) {
	selector := Selector{
		Tags: map[string]string{
			"Environment": "dev",
			"Team":        "platform",
		},
		InstanceIDs: []string{"i-123456"},
	}

	data, _ := json.Marshal(selector)
	var decoded Selector
	json.Unmarshal(data, &decoded)

	assert.Equal(t, 2, len(decoded.Tags))
	assert.Equal(t, "dev", decoded.Tags["Environment"])
	assert.Equal(t, 1, len(decoded.InstanceIDs))
}

func TestExecutorType_Constant(t *testing.T) {
	assert.Equal(t, "ec2", ExecutorType)
}

// TestShutdown_CapturesAllStatesButOnlyStopsRunning tests that shutdown discovers
// all instances regardless of state, records their actual state, but only stops
// instances that are actually running.
func TestShutdown_CapturesAllStatesButOnlyStopsRunning(t *testing.T) {
	ctx := context.Background()

	mockEC2 := &mocks.EC2Client{}

	savedRestoreData := make(map[string]InstanceState)
	saveFunc := func(key string, value any) error {
		if state, ok := value.(InstanceState); ok {
			savedRestoreData[key] = state
		}
		return nil
	}

	// Setup expectation for DescribeInstances to return instances in various states
	mockEC2.On("DescribeInstances", mock.Anything, mock.Anything).Return(&awsec2.DescribeInstancesOutput{
		Reservations: []types.Reservation{
			{
				Instances: []types.Instance{
					{
						// Running instance - should be stopped
						InstanceId: aws.String("i-running"),
						State: &types.InstanceState{
							Name: types.InstanceStateNameRunning,
						},
					},
					{
						// Stopped instance - should NOT be stopped, but recorded with WasRunning=false
						InstanceId: aws.String("i-stopped"),
						State: &types.InstanceState{
							Name: types.InstanceStateNameStopped,
						},
					},
					{
						// Stopping instance - should NOT be stopped (already stopping)
						InstanceId: aws.String("i-stopping"),
						State: &types.InstanceState{
							Name: types.InstanceStateNameStopping,
						},
					},
				},
			},
		},
	}, nil)

	// Setup expectation for StopInstances - only the running instance
	mockEC2.On("StopInstances", mock.Anything, &awsec2.StopInstancesInput{
		InstanceIds: []string{"i-running"},
	}).Return(&awsec2.StopInstancesOutput{}, nil)

	ec2Factory := func(cfg aws.Config) EC2Client { return mockEC2 }

	e := NewWithClients(ec2Factory, nil)

	spec := executor.Spec{
		TargetName: "test-instances",
		TargetType: "ec2",
		Parameters: json.RawMessage(`{"selector": {"tags": {"Environment": "dev"}}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
		ReportStateCallback: saveFunc,
	}

	_, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)

	// Verify restore data was captured for ALL instances
	assert.Len(t, savedRestoreData, 3, "Should capture all 3 instances")

	// Verify WasRunning is correctly set based on actual state
	assert.True(t, savedRestoreData["i-running"].WasRunning, "Running instance should have WasRunning=true")
	assert.False(t, savedRestoreData["i-stopped"].WasRunning, "Stopped instance should have WasRunning=false")
	assert.False(t, savedRestoreData["i-stopping"].WasRunning, "Stopping instance should have WasRunning=false")

	mockEC2.AssertExpectations(t)
}

// TestWakeUp_SkipsAlreadyRunningInstances tests that wakeup skips instances
// that are already running, only starting those that are stopped.
func TestWakeUp_SkipsAlreadyRunningInstances(t *testing.T) {
	ctx := context.Background()

	mockEC2 := &mocks.EC2Client{}

	// Setup expectation for DescribeInstances - discovers instances in mixed states
	mockEC2.On("DescribeInstances", mock.Anything, mock.Anything).Return(&awsec2.DescribeInstancesOutput{
		Reservations: []types.Reservation{
			{
				Instances: []types.Instance{
					{
						// Stopped instance with WasRunning=true - should be started
						InstanceId: aws.String("i-stopped"),
						State: &types.InstanceState{
							Name: types.InstanceStateNameStopped,
						},
					},
					{
						// Running instance with WasRunning=true - should NOT be started (already running)
						InstanceId: aws.String("i-running"),
						State: &types.InstanceState{
							Name: types.InstanceStateNameRunning,
						},
					},
					{
						// Stopped instance with WasRunning=false - should NOT be started
						InstanceId: aws.String("i-manually-stopped"),
						State: &types.InstanceState{
							Name: types.InstanceStateNameStopped,
						},
					},
				},
			},
		},
	}, nil)

	// Setup expectation for StartInstances - only the stopped instance with WasRunning=true
	mockEC2.On("StartInstances", mock.Anything, &awsec2.StartInstancesInput{
		InstanceIds: []string{"i-stopped"},
	}).Return(&awsec2.StartInstancesOutput{}, nil)

	ec2Factory := func(cfg aws.Config) EC2Client { return mockEC2 }

	e := NewWithClients(ec2Factory, nil)

	// Create restore data - all three instances were captured during shutdown
	stoppedState, _ := json.Marshal(InstanceState{InstanceID: "i-stopped", WasRunning: true})
	runningState, _ := json.Marshal(InstanceState{InstanceID: "i-running", WasRunning: true})
	manuallyStoppedState, _ := json.Marshal(InstanceState{InstanceID: "i-manually-stopped", WasRunning: false})

	spec := executor.Spec{
		TargetName: "test-instances",
		TargetType: "ec2",
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore := executor.RestoreData{
		Type: "ec2",
		Data: map[string]json.RawMessage{
			"i-stopped":           stoppedState,
			"i-running":           runningState,
			"i-manually-stopped":  manuallyStoppedState,
		},
	}

	result, err := e.WakeUp(ctx, logr.Discard(), spec, restore)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Contains(t, result.Message, "started 1 EC2 instance(s)")

	mockEC2.AssertExpectations(t)
}
