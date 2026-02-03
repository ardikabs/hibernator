/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package rds

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/internal/executor/rds/mocks"
)

func TestNew(t *testing.T) {
	e := New()
	assert.NotNil(t, e)
	assert.NotNil(t, e.rdsFactory)
	assert.NotNil(t, e.stsFactory)
}

func TestNewWithClients(t *testing.T) {
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	rdsFactory := func(cfg aws.Config) RDSClient {
		return mockRDS
	}
	stsFactory := func(cfg aws.Config) STSClient {
		return mockSTS
	}

	e := NewWithClients(rdsFactory, stsFactory, nil)
	assert.NotNil(t, e)
}

func TestExecutorType(t *testing.T) {
	e := New()
	assert.Equal(t, "rds", e.Type())
}

func TestValidate_MissingAWSConfig(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		Parameters: json.RawMessage(`{}`),
	}
	err := e.Validate(spec)
	assert.Error(t, err)
}

func TestValidate_WithAWSConfig(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		Parameters: json.RawMessage(`{}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{
				Region: "us-east-1",
			},
		},
	}
	err := e.Validate(spec)
	assert.NoError(t, err)
}

func TestShutdown_MissingTarget(t *testing.T) {
	e := New()
	ctx := context.Background()

	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		Parameters: json.RawMessage(`{}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	_, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "either instanceId or clusterId")
}

func TestShutdown_InvalidParameters(t *testing.T) {
	e := New()
	ctx := context.Background()

	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		Parameters: json.RawMessage(`{invalid json}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	_, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse parameters")
}

func TestShutdown_StopInstance(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	mockRDS.On("DescribeDBInstances", mock.Anything, mock.Anything).Return(&rds.DescribeDBInstancesOutput{
		DBInstances: []types.DBInstance{
			{
				DBInstanceStatus: aws.String("available"),
				DBInstanceClass:  aws.String("db.t3.medium"),
			},
		},
	}, nil)
	mockRDS.On("StopDBInstance", mock.Anything, mock.Anything).Return(&rds.StopDBInstanceOutput{}, nil)

	e := NewWithClients(
		func(cfg aws.Config) RDSClient { return mockRDS },
		func(cfg aws.Config) STSClient { return mockSTS },
		nil,
	)

	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		Parameters: json.RawMessage(`{"instanceId": "db-instance-1"}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)

	var state RestoreState
	err = json.Unmarshal(restore.Data, &state)
	assert.NoError(t, err)
	assert.Equal(t, "db-instance-1", state.InstanceID)
	assert.Equal(t, "db.t3.medium", state.InstanceType)
	assert.False(t, state.WasStopped)

	mockRDS.AssertExpectations(t)
}

func TestShutdown_StopInstanceAlreadyStopped(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	mockRDS.On("DescribeDBInstances", mock.Anything, mock.Anything).Return(&rds.DescribeDBInstancesOutput{
		DBInstances: []types.DBInstance{
			{DBInstanceStatus: aws.String("stopped")},
		},
	}, nil)

	e := NewWithClients(
		func(cfg aws.Config) RDSClient { return mockRDS },
		func(cfg aws.Config) STSClient { return mockSTS },
		nil,
	)

	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		Parameters: json.RawMessage(`{"instanceId": "db-instance-1"}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)

	var state RestoreState
	err = json.Unmarshal(restore.Data, &state)
	assert.NoError(t, err)
	assert.True(t, state.WasStopped)
	mockRDS.AssertNotCalled(t, "StopDBInstance", mock.Anything, mock.Anything)
}

func TestShutdown_StopCluster(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	mockRDS.On("DescribeDBClusters", mock.Anything, mock.Anything).Return(&rds.DescribeDBClustersOutput{
		DBClusters: []types.DBCluster{
			{Status: aws.String("available")},
		},
	}, nil)
	mockRDS.On("StopDBCluster", mock.Anything, mock.Anything).Return(&rds.StopDBClusterOutput{}, nil)

	e := NewWithClients(
		func(cfg aws.Config) RDSClient { return mockRDS },
		func(cfg aws.Config) STSClient { return mockSTS },
		nil,
	)

	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		Parameters: json.RawMessage(`{"clusterId": "cluster-1"}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)

	var state RestoreState
	err = json.Unmarshal(restore.Data, &state)
	assert.NoError(t, err)
	assert.Equal(t, "cluster-1", state.ClusterID)

	mockRDS.AssertExpectations(t)
}

func TestShutdown_StopClusterAlreadyStopped(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	mockRDS.On("DescribeDBClusters", mock.Anything, mock.Anything).Return(&rds.DescribeDBClustersOutput{
		DBClusters: []types.DBCluster{
			{Status: aws.String("stopped")},
		},
	}, nil)

	e := NewWithClients(
		func(cfg aws.Config) RDSClient { return mockRDS },
		func(cfg aws.Config) STSClient { return mockSTS },
		nil,
	)

	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		Parameters: json.RawMessage(`{"clusterId": "cluster-1"}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)

	var state RestoreState
	err = json.Unmarshal(restore.Data, &state)
	assert.NoError(t, err)
	assert.True(t, state.WasStopped)
	mockRDS.AssertNotCalled(t, "StopDBCluster", mock.Anything, mock.Anything)
}

func TestWakeUp_StartInstance(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	mockRDS.On("DescribeDBInstances", mock.Anything, mock.Anything).Return(&rds.DescribeDBInstancesOutput{
		DBInstances: []types.DBInstance{
			{DBInstanceStatus: aws.String("stopped")},
		},
	}, nil)
	mockRDS.On("StartDBInstance", mock.Anything, mock.Anything).Return(&rds.StartDBInstanceOutput{}, nil)

	e := NewWithClients(
		func(cfg aws.Config) RDSClient { return mockRDS },
		func(cfg aws.Config) STSClient { return mockSTS },
		nil,
	)

	restoreData, _ := json.Marshal(RestoreState{InstanceID: "db-instance-1"})
	restore := executor.RestoreData{Type: "rds", Data: restoreData}

	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	err := e.WakeUp(ctx, logr.Discard(), spec, restore)
	assert.NoError(t, err)
	mockRDS.AssertExpectations(t)
}

func TestWakeUp_InstanceAlreadyRunning(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	mockRDS.On("DescribeDBInstances", mock.Anything, mock.Anything).Return(&rds.DescribeDBInstancesOutput{
		DBInstances: []types.DBInstance{
			{DBInstanceStatus: aws.String("available")},
		},
	}, nil)

	e := NewWithClients(
		func(cfg aws.Config) RDSClient { return mockRDS },
		func(cfg aws.Config) STSClient { return mockSTS },
		nil,
	)

	restoreData, _ := json.Marshal(RestoreState{InstanceID: "db-instance-1"})
	restore := executor.RestoreData{Type: "rds", Data: restoreData}

	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	err := e.WakeUp(ctx, logr.Discard(), spec, restore)
	assert.NoError(t, err)
	mockRDS.AssertNotCalled(t, "StartDBInstance", mock.Anything, mock.Anything)
}

func TestWakeUp_StartCluster(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	mockRDS.On("DescribeDBClusters", mock.Anything, mock.Anything).Return(&rds.DescribeDBClustersOutput{
		DBClusters: []types.DBCluster{
			{Status: aws.String("stopped")},
		},
	}, nil)
	mockRDS.On("StartDBCluster", mock.Anything, mock.Anything).Return(&rds.StartDBClusterOutput{}, nil)

	e := NewWithClients(
		func(cfg aws.Config) RDSClient { return mockRDS },
		func(cfg aws.Config) STSClient { return mockSTS },
		nil,
	)

	restoreData, _ := json.Marshal(RestoreState{ClusterID: "cluster-1"})
	restore := executor.RestoreData{Type: "rds", Data: restoreData}

	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	err := e.WakeUp(ctx, logr.Discard(), spec, restore)
	assert.NoError(t, err)
	mockRDS.AssertExpectations(t)
}

func TestWakeUp_ClusterAlreadyRunning(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	mockRDS.On("DescribeDBClusters", mock.Anything, mock.Anything).Return(&rds.DescribeDBClustersOutput{
		DBClusters: []types.DBCluster{
			{Status: aws.String("available")},
		},
	}, nil)

	e := NewWithClients(
		func(cfg aws.Config) RDSClient { return mockRDS },
		func(cfg aws.Config) STSClient { return mockSTS },
		nil,
	)

	restoreData, _ := json.Marshal(RestoreState{ClusterID: "cluster-1"})
	restore := executor.RestoreData{Type: "rds", Data: restoreData}

	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	err := e.WakeUp(ctx, logr.Discard(), spec, restore)
	assert.NoError(t, err)
	mockRDS.AssertNotCalled(t, "StartDBCluster", mock.Anything, mock.Anything)
}

func TestWakeUp_InvalidRestoreData(t *testing.T) {
	e := New()
	ctx := context.Background()

	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore := executor.RestoreData{Type: "rds", Data: json.RawMessage(`{invalid json}`)}

	err := e.WakeUp(ctx, logr.Discard(), spec, restore)
	assert.Error(t, err)
}

func TestRestoreState_JSON(t *testing.T) {
	state := RestoreState{
		InstanceID:   "db-instance-1",
		SnapshotID:   "snap-123",
		WasStopped:   false,
		InstanceType: "db.t3.medium",
	}

	data, err := json.Marshal(state)
	assert.NoError(t, err)

	var decoded RestoreState
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, "db-instance-1", decoded.InstanceID)
	assert.Equal(t, "snap-123", decoded.SnapshotID)
	assert.False(t, decoded.WasStopped)
	assert.Equal(t, "db.t3.medium", decoded.InstanceType)
}

func TestRestoreState_Cluster(t *testing.T) {
	state := RestoreState{
		ClusterID:  "aurora-cluster-1",
		WasStopped: false,
	}

	data, err := json.Marshal(state)
	assert.NoError(t, err)

	var decoded RestoreState
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, "aurora-cluster-1", decoded.ClusterID)
}

func TestParameters_JSON(t *testing.T) {
	params := Parameters{
		InstanceID:         "db-1",
		ClusterID:          "cluster-1",
		SnapshotBeforeStop: true,
	}

	data, err := json.Marshal(params)
	assert.NoError(t, err)

	var decoded Parameters
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, "db-1", decoded.InstanceID)
	assert.Equal(t, "cluster-1", decoded.ClusterID)
	assert.True(t, decoded.SnapshotBeforeStop)
}

func TestExecutorType_Constant(t *testing.T) {
	assert.Equal(t, "rds", ExecutorType)
}
