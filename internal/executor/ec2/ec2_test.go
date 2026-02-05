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

	err := e.Shutdown(ctx, logr.Discard(), spec)
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

	err := e.Shutdown(ctx, logr.Discard(), spec)
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

	err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access denied")
}

func TestWakeUp_StartPreviouslyRunningInstances(t *testing.T) {
	ctx := context.Background()

	mockEC2 := &mocks.EC2Client{}

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

	err := e.WakeUp(ctx, logr.Discard(), spec, restore)
	assert.NoError(t, err)

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

	err := e.WakeUp(ctx, logr.Discard(), spec, restore)
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

	err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.Error(t, err)
}

// ============================================================================
// Data type tests
// ============================================================================

func TestRestoreState_JSON(t *testing.T) {
	state := RestoreState{
		Instances: []InstanceState{
			{InstanceID: "i-123456", WasRunning: true},
			{InstanceID: "i-789012", WasRunning: false},
		},
	}

	data, err := json.Marshal(state)
	assert.NoError(t, err)

	var decoded RestoreState
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(decoded.Instances))
	assert.Equal(t, "i-123456", decoded.Instances[0].InstanceID)
	assert.True(t, decoded.Instances[0].WasRunning)
}

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
