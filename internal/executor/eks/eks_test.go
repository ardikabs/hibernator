/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package eks

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/internal/executor/eks/mocks"
)

func TestNew(t *testing.T) {
	e := New()
	assert.NotNil(t, e)
	assert.NotNil(t, e.eksFactory)
	assert.NotNil(t, e.stsFactory)
}

func TestNewWithClients(t *testing.T) {
	mockEKS := &mocks.EKSClient{}
	mockSTS := &mocks.STSClient{}

	eksFactory := func(cfg aws.Config) EKSClient { return mockEKS }
	stsFactory := func(cfg aws.Config) STSClient { return mockSTS }

	e := NewWithClients(eksFactory, stsFactory, nil)
	assert.NotNil(t, e)
}

func TestExecutorType(t *testing.T) {
	e := New()
	assert.Equal(t, "eks", e.Type())
	assert.Equal(t, ExecutorType, e.Type())
}

func TestValidate_MissingAWSConfig(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName:      "test-cluster",
		TargetType:      "eks",
		Parameters:      json.RawMessage(`{"clusterName": "my-cluster"}`),
		ConnectorConfig: executor.ConnectorConfig{},
	}
	err := e.Validate(spec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "AWS connector config required")
}

func TestValidate_MissingRegion(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
		Parameters: json.RawMessage(`{"clusterName": "my-cluster"}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{},
		},
	}
	err := e.Validate(spec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "region is required")
}

func TestValidate_MissingClusterName(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
		Parameters: json.RawMessage(`{}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}
	err := e.Validate(spec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "clusterName is required")
}

func TestValidate_Valid(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
		Parameters: json.RawMessage(`{"clusterName": "my-cluster"}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}
	err := e.Validate(spec)
	assert.NoError(t, err)
}

func TestValidate_WithNodeGroups(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
		Parameters: json.RawMessage(`{"clusterName": "my-cluster", "nodeGroups": [{"name": "ng-1"}, {"name": "ng-2"}]}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}
	err := e.Validate(spec)
	assert.NoError(t, err)
}

func TestShutdown_WithSpecificNodeGroups(t *testing.T) {
	ctx := context.Background()

	mockEKS := &mocks.EKSClient{}

	// Setup expectations for DescribeNodegroup calls
	mockEKS.On("DescribeNodegroup", mock.Anything, &eks.DescribeNodegroupInput{
		ClusterName:   aws.String("my-cluster"),
		NodegroupName: aws.String("ng-1"),
	}).Return(&eks.DescribeNodegroupOutput{
		Nodegroup: &types.Nodegroup{
			ScalingConfig: &types.NodegroupScalingConfig{
				DesiredSize: aws.Int32(3),
				MinSize:     aws.Int32(1),
				MaxSize:     aws.Int32(5),
			},
		},
	}, nil)

	// Setup expectations for UpdateNodegroupConfig calls
	mockEKS.On("UpdateNodegroupConfig", mock.Anything, mock.MatchedBy(func(input *eks.UpdateNodegroupConfigInput) bool {
		return aws.ToString(input.ClusterName) == "my-cluster" &&
			aws.ToString(input.NodegroupName) == "ng-1" &&
			aws.ToInt32(input.ScalingConfig.DesiredSize) == 0
	})).Return(&eks.UpdateNodegroupConfigOutput{}, nil)

	eksFactory := func(cfg aws.Config) EKSClient { return mockEKS }
	stsFactory := func(cfg aws.Config) STSClient { return &mocks.STSClient{} }

	e := NewWithClients(eksFactory, stsFactory, nil)

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
		Parameters: json.RawMessage(`{"clusterName": "my-cluster", "nodeGroups": [{"name": "ng-1"}]}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore, err := e.Shutdown(ctx, spec)
	assert.NoError(t, err)
	assert.Equal(t, "eks", restore.Type)

	var state RestoreState
	err = json.Unmarshal(restore.Data, &state)
	assert.NoError(t, err)
	assert.Equal(t, "my-cluster", state.ClusterName)
	assert.Equal(t, 1, len(state.NodeGroups))
	assert.Equal(t, int32(3), state.NodeGroups["ng-1"].DesiredSize)
	assert.Equal(t, int32(1), state.NodeGroups["ng-1"].MinSize)

	mockEKS.AssertExpectations(t)
}

func TestShutdown_WithListAllNodeGroups(t *testing.T) {
	ctx := context.Background()

	mockEKS := &mocks.EKSClient{}

	// Setup expectation for ListNodegroups
	mockEKS.On("ListNodegroups", mock.Anything, &eks.ListNodegroupsInput{
		ClusterName: aws.String("my-cluster"),
	}).Return(&eks.ListNodegroupsOutput{
		Nodegroups: []string{"ng-1", "ng-2"},
	}, nil)

	// Setup expectations for DescribeNodegroup calls for both node groups
	mockEKS.On("DescribeNodegroup", mock.Anything, mock.MatchedBy(func(input *eks.DescribeNodegroupInput) bool {
		return aws.ToString(input.ClusterName) == "my-cluster"
	})).Return(&eks.DescribeNodegroupOutput{
		Nodegroup: &types.Nodegroup{
			ScalingConfig: &types.NodegroupScalingConfig{
				DesiredSize: aws.Int32(3),
				MinSize:     aws.Int32(1),
				MaxSize:     aws.Int32(5),
			},
		},
	}, nil)

	// Setup expectations for UpdateNodegroupConfig calls
	mockEKS.On("UpdateNodegroupConfig", mock.Anything, mock.MatchedBy(func(input *eks.UpdateNodegroupConfigInput) bool {
		return aws.ToString(input.ClusterName) == "my-cluster" &&
			aws.ToInt32(input.ScalingConfig.DesiredSize) == 0
	})).Return(&eks.UpdateNodegroupConfigOutput{}, nil)

	eksFactory := func(cfg aws.Config) EKSClient { return mockEKS }
	stsFactory := func(cfg aws.Config) STSClient { return &mocks.STSClient{} }

	e := NewWithClients(eksFactory, stsFactory, nil)

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
		Parameters: json.RawMessage(`{"clusterName": "my-cluster"}`), // Empty nodeGroups means all
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore, err := e.Shutdown(ctx, spec)
	assert.NoError(t, err)

	var state RestoreState
	err = json.Unmarshal(restore.Data, &state)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(state.NodeGroups))

	mockEKS.AssertExpectations(t)
}

func TestShutdown_DescribeNodegroupError(t *testing.T) {
	ctx := context.Background()

	mockEKS := &mocks.EKSClient{}

	// Setup expectation to return error
	mockEKS.On("DescribeNodegroup", mock.Anything, mock.Anything).
		Return(nil, errors.New("access denied"))

	eksFactory := func(cfg aws.Config) EKSClient { return mockEKS }
	stsFactory := func(cfg aws.Config) STSClient { return &mocks.STSClient{} }

	e := NewWithClients(eksFactory, stsFactory, nil)

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
		Parameters: json.RawMessage(`{"clusterName": "my-cluster", "nodeGroups": [{"name": "ng-1"}]}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	_, err := e.Shutdown(ctx, spec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access denied")
}

func TestWakeUp_RestoreNodeGroups(t *testing.T) {
	ctx := context.Background()

	mockEKS := &mocks.EKSClient{}

	// Setup expectation for UpdateNodegroupConfig call during restore
	mockEKS.On("UpdateNodegroupConfig", mock.Anything, mock.MatchedBy(func(input *eks.UpdateNodegroupConfigInput) bool {
		return aws.ToString(input.ClusterName) == "my-cluster" &&
			aws.ToString(input.NodegroupName) == "ng-1" &&
			aws.ToInt32(input.ScalingConfig.DesiredSize) == 3 // Restored to original
	})).Return(&eks.UpdateNodegroupConfigOutput{}, nil)

	eksFactory := func(cfg aws.Config) EKSClient { return mockEKS }
	stsFactory := func(cfg aws.Config) STSClient { return &mocks.STSClient{} }

	e := NewWithClients(eksFactory, stsFactory, nil)

	restoreState := RestoreState{
		ClusterName: "my-cluster",
		NodeGroups: map[string]NodeGroupState{
			"ng-1": {DesiredSize: 3, MinSize: 1, MaxSize: 5},
		},
	}

	restoreData, _ := json.Marshal(restoreState)

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore := executor.RestoreData{
		Type: "eks",
		Data: restoreData,
	}

	err := e.WakeUp(ctx, spec, restore)
	assert.NoError(t, err)

	mockEKS.AssertExpectations(t)
}

func TestWakeUp_InvalidRestoreData(t *testing.T) {
	e := New()
	ctx := context.Background()

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore := executor.RestoreData{
		Type: "eks",
		Data: json.RawMessage(`{invalid json}`),
	}

	err := e.WakeUp(ctx, spec, restore)
	assert.Error(t, err)
}

func TestShutdown_InvalidParameters(t *testing.T) {
	e := New()
	ctx := context.Background()

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
		Parameters: json.RawMessage(`{invalid json}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	_, err := e.Shutdown(ctx, spec)
	assert.Error(t, err)
}

// ============================================================================
// Data type tests
// ============================================================================

func TestRestoreState_JSON(t *testing.T) {
	state := RestoreState{
		ClusterName: "my-cluster",
		NodeGroups: map[string]NodeGroupState{
			"ng-1": {DesiredSize: 3, MinSize: 1, MaxSize: 5},
			"ng-2": {DesiredSize: 5, MinSize: 2, MaxSize: 10},
		},
	}

	data, err := json.Marshal(state)
	assert.NoError(t, err)

	var decoded RestoreState
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, "my-cluster", decoded.ClusterName)
	assert.Equal(t, 2, len(decoded.NodeGroups))
	assert.Equal(t, int32(3), decoded.NodeGroups["ng-1"].DesiredSize)
	assert.Equal(t, int32(5), decoded.NodeGroups["ng-2"].DesiredSize)
}

func TestNodeGroupState_JSON(t *testing.T) {
	state := NodeGroupState{
		DesiredSize: 3,
		MinSize:     1,
		MaxSize:     5,
	}

	data, _ := json.Marshal(state)
	var decoded NodeGroupState
	json.Unmarshal(data, &decoded)

	assert.Equal(t, int32(3), decoded.DesiredSize)
	assert.Equal(t, int32(1), decoded.MinSize)
	assert.Equal(t, int32(5), decoded.MaxSize)
}

func TestParameters_JSON(t *testing.T) {
	params := Parameters{
		ClusterName: "my-cluster",
		NodeGroups: []NodeGroup{
			{Name: "ng-1"},
			{Name: "ng-2"},
		},
	}

	data, _ := json.Marshal(params)
	var decoded Parameters
	json.Unmarshal(data, &decoded)

	assert.Equal(t, "my-cluster", decoded.ClusterName)
	assert.Equal(t, 2, len(decoded.NodeGroups))
}

func TestExecutorType_Constant(t *testing.T) {
	assert.Equal(t, "eks", ExecutorType)
}
