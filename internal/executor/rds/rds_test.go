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
	"github.com/ardikabs/hibernator/pkg/executorparams"
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
		Parameters: json.RawMessage(`{"selector": {"instanceIds": ["db-1"]}}`),
	}
	err := e.Validate(spec)
	assert.Error(t, err)
}

func TestValidate_WithAWSConfig(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		Parameters: json.RawMessage(`{"selector": {"instanceIds": ["db-1"]}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{
				Region: "us-east-1",
			},
		},
	}
	err := e.Validate(spec)
	assert.NoError(t, err)
}

func TestValidate_MissingSelector(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		Parameters: json.RawMessage(`{"selector": {}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}
	err := e.Validate(spec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "selector must specify at least one")
}

func TestShutdown_StopInstance(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	// Mock for finding instances (clusters not queried since selector only has instanceIds)
	mockRDS.On("DescribeDBInstances", mock.Anything, mock.Anything).Return(&rds.DescribeDBInstancesOutput{
		DBInstances: []types.DBInstance{
			{
				DBInstanceIdentifier: aws.String("db-instance-1"),
				DBInstanceStatus:     aws.String("available"),
				DBInstanceClass:      aws.String("db.t3.medium"),
				DBInstanceArn:        aws.String("arn:aws:rds:us-east-1:123456789012:db:db-instance-1"),
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
		Parameters: json.RawMessage(`{"selector": {"instanceIds": ["db-instance-1"]}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)

	var state RestoreState
	err = json.Unmarshal(restore.Data, &state)
	assert.NoError(t, err)
	assert.Len(t, state.Instances, 1)
	assert.Equal(t, "db-instance-1", state.Instances[0].InstanceID)
	assert.Equal(t, "db.t3.medium", state.Instances[0].InstanceType)
	assert.False(t, state.Instances[0].WasStopped)

	mockRDS.AssertExpectations(t)
}

func TestShutdown_StopInstanceAlreadyStopped(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	// Only instances queried since selector only has instanceIds
	mockRDS.On("DescribeDBInstances", mock.Anything, mock.Anything).Return(&rds.DescribeDBInstancesOutput{
		DBInstances: []types.DBInstance{
			{
				DBInstanceIdentifier: aws.String("db-instance-1"),
				DBInstanceStatus:     aws.String("stopped"),
				DBInstanceClass:      aws.String("db.t3.medium"),
				DBInstanceArn:        aws.String("arn:aws:rds:us-east-1:123456789012:db:db-instance-1"),
			},
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
		Parameters: json.RawMessage(`{"selector": {"instanceIds": ["db-instance-1"]}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)

	var state RestoreState
	err = json.Unmarshal(restore.Data, &state)
	assert.NoError(t, err)
	assert.Len(t, state.Instances, 1)
	assert.True(t, state.Instances[0].WasStopped)

	mockRDS.AssertExpectations(t)
}

func TestShutdown_StopCluster(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	// Only clusters queried since selector only has clusterIds
	mockRDS.On("DescribeDBClusters", mock.Anything, mock.Anything).Return(&rds.DescribeDBClustersOutput{
		DBClusters: []types.DBCluster{
			{
				DBClusterIdentifier: aws.String("cluster-1"),
				Status:              aws.String("available"),
				DBClusterArn:        aws.String("arn:aws:rds:us-east-1:123456789012:cluster:cluster-1"),
			},
		},
	}, nil)
	mockRDS.On("StopDBCluster", mock.Anything, mock.Anything).Return(&rds.StopDBClusterOutput{}, nil)

	e := NewWithClients(
		func(cfg aws.Config) RDSClient { return mockRDS },
		func(cfg aws.Config) STSClient { return mockSTS },
		nil,
	)

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "rds",
		Parameters: json.RawMessage(`{"selector": {"clusterIds": ["cluster-1"]}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)

	var state RestoreState
	err = json.Unmarshal(restore.Data, &state)
	assert.NoError(t, err)
	assert.Len(t, state.Clusters, 1)
	assert.Equal(t, "cluster-1", state.Clusters[0].ClusterID)

	mockRDS.AssertExpectations(t)
}

func TestShutdown_StopClusterAlreadyStopped(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	// Only clusters queried since selector only has clusterIds
	mockRDS.On("DescribeDBClusters", mock.Anything, mock.Anything).Return(&rds.DescribeDBClustersOutput{
		DBClusters: []types.DBCluster{
			{
				DBClusterIdentifier: aws.String("cluster-1"),
				Status:              aws.String("stopped"),
				DBClusterArn:        aws.String("arn:aws:rds:us-east-1:123456789012:cluster:cluster-1"),
			},
		},
	}, nil)

	e := NewWithClients(
		func(cfg aws.Config) RDSClient { return mockRDS },
		func(cfg aws.Config) STSClient { return mockSTS },
		nil,
	)

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "rds",
		Parameters: json.RawMessage(`{"selector": {"clusterIds": ["cluster-1"]}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)

	var state RestoreState
	err = json.Unmarshal(restore.Data, &state)
	assert.NoError(t, err)
	assert.Len(t, state.Clusters, 1)
	assert.True(t, state.Clusters[0].WasStopped)

	mockRDS.AssertExpectations(t)
}

func TestWakeUp_StartInstance(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	mockRDS.On("DescribeDBInstances", mock.Anything, mock.Anything).Return(&rds.DescribeDBInstancesOutput{
		DBInstances: []types.DBInstance{
			{
				DBInstanceIdentifier: aws.String("db-instance-1"),
				DBInstanceStatus:     aws.String("stopped"),
			},
		},
	}, nil)
	mockRDS.On("StartDBInstance", mock.Anything, mock.Anything).Return(&rds.StartDBInstanceOutput{}, nil)

	e := NewWithClients(
		func(cfg aws.Config) RDSClient { return mockRDS },
		func(cfg aws.Config) STSClient { return mockSTS },
		nil,
	)

	restoreData, _ := json.Marshal(RestoreState{
		Instances: []DBInstanceState{{InstanceID: "db-instance-1", WasStopped: false}},
	})

	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		Parameters: json.RawMessage(`{"selector": {"instanceIds": ["db-instance-1"]}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	err := e.WakeUp(ctx, logr.Discard(), spec, executor.RestoreData{Data: restoreData})
	assert.NoError(t, err)

	mockRDS.AssertExpectations(t)
}

func TestWakeUp_InstanceAlreadyRunning(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	mockRDS.On("DescribeDBInstances", mock.Anything, mock.Anything).Return(&rds.DescribeDBInstancesOutput{
		DBInstances: []types.DBInstance{
			{
				DBInstanceIdentifier: aws.String("db-instance-1"),
				DBInstanceStatus:     aws.String("available"),
			},
		},
	}, nil)

	e := NewWithClients(
		func(cfg aws.Config) RDSClient { return mockRDS },
		func(cfg aws.Config) STSClient { return mockSTS },
		nil,
	)

	restoreData, _ := json.Marshal(RestoreState{
		Instances: []DBInstanceState{{InstanceID: "db-instance-1", WasStopped: false}},
	})

	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		Parameters: json.RawMessage(`{"selector": {"instanceIds": ["db-instance-1"]}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	err := e.WakeUp(ctx, logr.Discard(), spec, executor.RestoreData{Data: restoreData})
	assert.NoError(t, err)

	mockRDS.AssertExpectations(t)
}

func TestWakeUp_StartCluster(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	mockRDS.On("DescribeDBClusters", mock.Anything, mock.Anything).Return(&rds.DescribeDBClustersOutput{
		DBClusters: []types.DBCluster{
			{
				DBClusterIdentifier: aws.String("cluster-1"),
				Status:              aws.String("stopped"),
			},
		},
	}, nil)
	mockRDS.On("StartDBCluster", mock.Anything, mock.Anything).Return(&rds.StartDBClusterOutput{}, nil)

	e := NewWithClients(
		func(cfg aws.Config) RDSClient { return mockRDS },
		func(cfg aws.Config) STSClient { return mockSTS },
		nil,
	)

	restoreData, _ := json.Marshal(RestoreState{
		Clusters: []DBClusterState{{ClusterID: "cluster-1", WasStopped: false}},
	})

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "rds",
		Parameters: json.RawMessage(`{"selector": {"clusterIds": ["cluster-1"]}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	err := e.WakeUp(ctx, logr.Discard(), spec, executor.RestoreData{Data: restoreData})
	assert.NoError(t, err)

	mockRDS.AssertExpectations(t)
}

func TestWakeUp_ClusterAlreadyRunning(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	mockRDS.On("DescribeDBClusters", mock.Anything, mock.Anything).Return(&rds.DescribeDBClustersOutput{
		DBClusters: []types.DBCluster{
			{
				DBClusterIdentifier: aws.String("cluster-1"),
				Status:              aws.String("available"),
			},
		},
	}, nil)

	e := NewWithClients(
		func(cfg aws.Config) RDSClient { return mockRDS },
		func(cfg aws.Config) STSClient { return mockSTS },
		nil,
	)

	restoreData, _ := json.Marshal(RestoreState{
		Clusters: []DBClusterState{{ClusterID: "cluster-1", WasStopped: false}},
	})

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "rds",
		Parameters: json.RawMessage(`{"selector": {"clusterIds": ["cluster-1"]}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	err := e.WakeUp(ctx, logr.Discard(), spec, executor.RestoreData{Data: restoreData})
	assert.NoError(t, err)

	mockRDS.AssertExpectations(t)
}

func TestWakeUp_InvalidRestoreData(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	e := NewWithClients(
		func(cfg aws.Config) RDSClient { return mockRDS },
		func(cfg aws.Config) STSClient { return mockSTS },
		nil,
	)

	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		Parameters: json.RawMessage(`{"selector": {"instanceIds": ["db-1"]}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	err := e.WakeUp(ctx, logr.Discard(), spec, executor.RestoreData{Data: []byte("invalid")})
	assert.Error(t, err)
}

func TestShutdown_DynamicDiscovery_TagsInstances(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	// Mock for dynamic discovery - instances only (discoverInstances: true)
	// First call: list all instances
	mockRDS.On("DescribeDBInstances", mock.Anything, &rds.DescribeDBInstancesInput{}).Return(&rds.DescribeDBInstancesOutput{
		DBInstances: []types.DBInstance{
			{
				DBInstanceIdentifier: aws.String("tagged-instance-1"),
				DBInstanceStatus:     aws.String("available"),
				DBInstanceClass:      aws.String("db.t3.small"),
				DBInstanceArn:        aws.String("arn:aws:rds:us-east-1:123456789012:db:tagged-instance-1"),
			},
		},
	}, nil)

	// Second call: get tags for each discovered instance
	mockRDS.On("ListTagsForResource", mock.Anything, &rds.ListTagsForResourceInput{
		ResourceName: aws.String("arn:aws:rds:us-east-1:123456789012:db:tagged-instance-1"),
	}).Return(&rds.ListTagsForResourceOutput{
		TagList: []types.Tag{
			{Key: aws.String("Environment"), Value: aws.String("production")},
		},
	}, nil)

	// Third call: get instance details before stopping (called by stopInstance)
	mockRDS.On("DescribeDBInstances", mock.Anything, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("tagged-instance-1"),
	}).Return(&rds.DescribeDBInstancesOutput{
		DBInstances: []types.DBInstance{
			{
				DBInstanceIdentifier: aws.String("tagged-instance-1"),
				DBInstanceStatus:     aws.String("available"),
				DBInstanceClass:      aws.String("db.t3.small"),
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
		TargetName: "test-tagged",
		TargetType: "rds",
		Parameters: json.RawMessage(`{"selector": {"tags": {"Environment": "production"}, "discoverInstances": true}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)

	var state RestoreState
	err = json.Unmarshal(restore.Data, &state)
	assert.NoError(t, err)
	assert.Len(t, state.Instances, 1)
	assert.Equal(t, "tagged-instance-1", state.Instances[0].InstanceID)
	assert.Len(t, state.Clusters, 0) // No clusters discovered

	mockRDS.AssertExpectations(t)
}

func TestShutdown_DynamicDiscovery_TagsClusters(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	// Mock for dynamic discovery - clusters only (discoverClusters: true)
	// First call: list all clusters
	mockRDS.On("DescribeDBClusters", mock.Anything, &rds.DescribeDBClustersInput{}).Return(&rds.DescribeDBClustersOutput{
		DBClusters: []types.DBCluster{
			{
				DBClusterIdentifier: aws.String("tagged-cluster-1"),
				Status:              aws.String("available"),
				DBClusterArn:        aws.String("arn:aws:rds:us-east-1:123456789012:cluster:tagged-cluster-1"),
			},
		},
	}, nil)

	// Second call: get tags for discovered cluster
	mockRDS.On("ListTagsForResource", mock.Anything, &rds.ListTagsForResourceInput{
		ResourceName: aws.String("arn:aws:rds:us-east-1:123456789012:cluster:tagged-cluster-1"),
	}).Return(&rds.ListTagsForResourceOutput{
		TagList: []types.Tag{
			{Key: aws.String("Team"), Value: aws.String("backend")},
		},
	}, nil)

	// Third call: get cluster details before stopping (called by stopCluster)
	mockRDS.On("DescribeDBClusters", mock.Anything, &rds.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String("tagged-cluster-1"),
	}).Return(&rds.DescribeDBClustersOutput{
		DBClusters: []types.DBCluster{
			{
				DBClusterIdentifier: aws.String("tagged-cluster-1"),
				Status:              aws.String("available"),
			},
		},
	}, nil)

	mockRDS.On("StopDBCluster", mock.Anything, mock.Anything).Return(&rds.StopDBClusterOutput{}, nil)

	e := NewWithClients(
		func(cfg aws.Config) RDSClient { return mockRDS },
		func(cfg aws.Config) STSClient { return mockSTS },
		nil,
	)

	spec := executor.Spec{
		TargetName: "test-tagged-cluster",
		TargetType: "rds",
		Parameters: json.RawMessage(`{"selector": {"tags": {"Team": "backend"}, "discoverClusters": true}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)

	var state RestoreState
	err = json.Unmarshal(restore.Data, &state)
	assert.NoError(t, err)
	assert.Len(t, state.Instances, 0) // No instances discovered
	assert.Len(t, state.Clusters, 1)
	assert.Equal(t, "tagged-cluster-1", state.Clusters[0].ClusterID)

	mockRDS.AssertExpectations(t)
}

func TestShutdown_DynamicDiscovery_ExcludeTags(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	// Mock for dynamic discovery with excludeTags - both resource types
	// First: list all instances
	mockRDS.On("DescribeDBInstances", mock.Anything, &rds.DescribeDBInstancesInput{}).Return(&rds.DescribeDBInstancesOutput{
		DBInstances: []types.DBInstance{
			{
				DBInstanceIdentifier: aws.String("instance-1"),
				DBInstanceStatus:     aws.String("available"),
				DBInstanceClass:      aws.String("db.t3.small"),
				DBInstanceArn:        aws.String("arn:aws:rds:us-east-1:123456789012:db:instance-1"),
			},
			{
				DBInstanceIdentifier: aws.String("instance-2"),
				DBInstanceStatus:     aws.String("available"),
				DBInstanceClass:      aws.String("db.t3.small"),
				DBInstanceArn:        aws.String("arn:aws:rds:us-east-1:123456789012:db:instance-2"),
			},
		},
	}, nil)
	// First: list all clusters
	mockRDS.On("DescribeDBClusters", mock.Anything, &rds.DescribeDBClustersInput{}).Return(&rds.DescribeDBClustersOutput{
		DBClusters: []types.DBCluster{
			{
				DBClusterIdentifier: aws.String("cluster-1"),
				Status:              aws.String("available"),
				DBClusterArn:        aws.String("arn:aws:rds:us-east-1:123456789012:cluster:cluster-1"),
			},
		},
	}, nil)

	// instance-1 has Critical tag (excluded)
	mockRDS.On("ListTagsForResource", mock.Anything, &rds.ListTagsForResourceInput{
		ResourceName: aws.String("arn:aws:rds:us-east-1:123456789012:db:instance-1"),
	}).Return(&rds.ListTagsForResourceOutput{
		TagList: []types.Tag{
			{Key: aws.String("Critical"), Value: aws.String("true")},
		},
	}, nil)

	// instance-2 has no Critical tag (included)
	mockRDS.On("ListTagsForResource", mock.Anything, &rds.ListTagsForResourceInput{
		ResourceName: aws.String("arn:aws:rds:us-east-1:123456789012:db:instance-2"),
	}).Return(&rds.ListTagsForResourceOutput{
		TagList: []types.Tag{},
	}, nil)

	// cluster-1 has no Critical tag (included)
	mockRDS.On("ListTagsForResource", mock.Anything, &rds.ListTagsForResourceInput{
		ResourceName: aws.String("arn:aws:rds:us-east-1:123456789012:cluster:cluster-1"),
	}).Return(&rds.ListTagsForResourceOutput{
		TagList: []types.Tag{},
	}, nil)

	// Get instance-2 details before stopping
	mockRDS.On("DescribeDBInstances", mock.Anything, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("instance-2"),
	}).Return(&rds.DescribeDBInstancesOutput{
		DBInstances: []types.DBInstance{
			{
				DBInstanceIdentifier: aws.String("instance-2"),
				DBInstanceStatus:     aws.String("available"),
				DBInstanceClass:      aws.String("db.t3.small"),
			},
		},
	}, nil)

	// Get cluster-1 details before stopping
	mockRDS.On("DescribeDBClusters", mock.Anything, &rds.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String("cluster-1"),
	}).Return(&rds.DescribeDBClustersOutput{
		DBClusters: []types.DBCluster{
			{
				DBClusterIdentifier: aws.String("cluster-1"),
				Status:              aws.String("available"),
			},
		},
	}, nil)

	mockRDS.On("StopDBInstance", mock.Anything, &rds.StopDBInstanceInput{
		DBInstanceIdentifier: aws.String("instance-2"),
	}).Return(&rds.StopDBInstanceOutput{}, nil)
	mockRDS.On("StopDBCluster", mock.Anything, &rds.StopDBClusterInput{
		DBClusterIdentifier: aws.String("cluster-1"),
	}).Return(&rds.StopDBClusterOutput{}, nil)

	e := NewWithClients(
		func(cfg aws.Config) RDSClient { return mockRDS },
		func(cfg aws.Config) STSClient { return mockSTS },
		nil,
	)

	spec := executor.Spec{
		TargetName: "test-exclude",
		TargetType: "rds",
		Parameters: json.RawMessage(`{"selector": {"excludeTags": {"Critical": "true"}, "discoverInstances": true, "discoverClusters": true}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)

	var state RestoreState
	err = json.Unmarshal(restore.Data, &state)
	assert.NoError(t, err)
	assert.Len(t, state.Instances, 1)
	assert.Equal(t, "instance-2", state.Instances[0].InstanceID)
	assert.Len(t, state.Clusters, 1)
	assert.Equal(t, "cluster-1", state.Clusters[0].ClusterID)

	mockRDS.AssertExpectations(t)
}

func TestShutdown_DynamicDiscovery_IncludeAll(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	// Mock for includeAll - both resource types
	// First: list all instances
	mockRDS.On("DescribeDBInstances", mock.Anything, &rds.DescribeDBInstancesInput{}).Return(&rds.DescribeDBInstancesOutput{
		DBInstances: []types.DBInstance{
			{
				DBInstanceIdentifier: aws.String("all-instance-1"),
				DBInstanceStatus:     aws.String("available"),
				DBInstanceClass:      aws.String("db.t3.micro"),
				DBInstanceArn:        aws.String("arn:aws:rds:us-east-1:123456789012:db:all-instance-1"),
			},
		},
	}, nil)
	// First: list all clusters
	mockRDS.On("DescribeDBClusters", mock.Anything, &rds.DescribeDBClustersInput{}).Return(&rds.DescribeDBClustersOutput{
		DBClusters: []types.DBCluster{
			{
				DBClusterIdentifier: aws.String("all-cluster-1"),
				Status:              aws.String("available"),
				DBClusterArn:        aws.String("arn:aws:rds:us-east-1:123456789012:cluster:all-cluster-1"),
			},
		},
	}, nil)

	// No ListTagsForResource calls needed for includeAll (optimized to skip tag fetching)

	// Get instance details before stopping
	mockRDS.On("DescribeDBInstances", mock.Anything, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("all-instance-1"),
	}).Return(&rds.DescribeDBInstancesOutput{
		DBInstances: []types.DBInstance{
			{
				DBInstanceIdentifier: aws.String("all-instance-1"),
				DBInstanceStatus:     aws.String("available"),
				DBInstanceClass:      aws.String("db.t3.micro"),
			},
		},
	}, nil)

	// Get cluster details before stopping
	mockRDS.On("DescribeDBClusters", mock.Anything, &rds.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String("all-cluster-1"),
	}).Return(&rds.DescribeDBClustersOutput{
		DBClusters: []types.DBCluster{
			{
				DBClusterIdentifier: aws.String("all-cluster-1"),
				Status:              aws.String("available"),
			},
		},
	}, nil)

	mockRDS.On("StopDBInstance", mock.Anything, mock.Anything).Return(&rds.StopDBInstanceOutput{}, nil)
	mockRDS.On("StopDBCluster", mock.Anything, mock.Anything).Return(&rds.StopDBClusterOutput{}, nil)

	e := NewWithClients(
		func(cfg aws.Config) RDSClient { return mockRDS },
		func(cfg aws.Config) STSClient { return mockSTS },
		nil,
	)

	spec := executor.Spec{
		TargetName: "test-include-all",
		TargetType: "rds",
		Parameters: json.RawMessage(`{"selector": {"includeAll": true, "discoverInstances": true, "discoverClusters": true}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)

	var state RestoreState
	err = json.Unmarshal(restore.Data, &state)
	assert.NoError(t, err)
	assert.Len(t, state.Instances, 1)
	assert.Equal(t, "all-instance-1", state.Instances[0].InstanceID)
	assert.Len(t, state.Clusters, 1)
	assert.Equal(t, "all-cluster-1", state.Clusters[0].ClusterID)

	mockRDS.AssertExpectations(t)
}

func TestShutdown_DynamicDiscovery_TagKeyOnly(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	// Mock for tag key-only matching (any value)
	// First: list all instances
	mockRDS.On("DescribeDBInstances", mock.Anything, &rds.DescribeDBInstancesInput{}).Return(&rds.DescribeDBInstancesOutput{
		DBInstances: []types.DBInstance{
			{
				DBInstanceIdentifier: aws.String("instance-with-env"),
				DBInstanceStatus:     aws.String("available"),
				DBInstanceClass:      aws.String("db.t3.small"),
				DBInstanceArn:        aws.String("arn:aws:rds:us-east-1:123456789012:db:instance-with-env"),
			},
			{
				DBInstanceIdentifier: aws.String("instance-no-env"),
				DBInstanceStatus:     aws.String("available"),
				DBInstanceClass:      aws.String("db.t3.small"),
				DBInstanceArn:        aws.String("arn:aws:rds:us-east-1:123456789012:db:instance-no-env"),
			},
		},
	}, nil)

	// instance-with-env has Environment tag (matches)
	mockRDS.On("ListTagsForResource", mock.Anything, &rds.ListTagsForResourceInput{
		ResourceName: aws.String("arn:aws:rds:us-east-1:123456789012:db:instance-with-env"),
	}).Return(&rds.ListTagsForResourceOutput{
		TagList: []types.Tag{
			{Key: aws.String("Environment"), Value: aws.String("staging")},
		},
	}, nil)

	// instance-no-env has no Environment tag (doesn't match)
	mockRDS.On("ListTagsForResource", mock.Anything, &rds.ListTagsForResourceInput{
		ResourceName: aws.String("arn:aws:rds:us-east-1:123456789012:db:instance-no-env"),
	}).Return(&rds.ListTagsForResourceOutput{
		TagList: []types.Tag{},
	}, nil)

	// Get instance details before stopping
	mockRDS.On("DescribeDBInstances", mock.Anything, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("instance-with-env"),
	}).Return(&rds.DescribeDBInstancesOutput{
		DBInstances: []types.DBInstance{
			{
				DBInstanceIdentifier: aws.String("instance-with-env"),
				DBInstanceStatus:     aws.String("available"),
				DBInstanceClass:      aws.String("db.t3.small"),
			},
		},
	}, nil)

	mockRDS.On("StopDBInstance", mock.Anything, &rds.StopDBInstanceInput{
		DBInstanceIdentifier: aws.String("instance-with-env"),
	}).Return(&rds.StopDBInstanceOutput{}, nil)

	e := NewWithClients(
		func(cfg aws.Config) RDSClient { return mockRDS },
		func(cfg aws.Config) STSClient { return mockSTS },
		nil,
	)

	spec := executor.Spec{
		TargetName: "test-key-only",
		TargetType: "rds",
		Parameters: json.RawMessage(`{"selector": {"tags": {"Environment": ""}, "discoverInstances": true}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)

	var state RestoreState
	err = json.Unmarshal(restore.Data, &state)
	assert.NoError(t, err)
	assert.Len(t, state.Instances, 1)
	assert.Equal(t, "instance-with-env", state.Instances[0].InstanceID)

	mockRDS.AssertExpectations(t)
}

func TestRestoreState_JSON(t *testing.T) {
	state := RestoreState{
		Instances: []DBInstanceState{
			{
				InstanceID:   "db-instance-1",
				SnapshotID:   "snap-123",
				WasStopped:   false,
				InstanceType: "db.t3.medium",
			},
		},
	}

	data, err := json.Marshal(state)
	assert.NoError(t, err)

	var decoded RestoreState
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Len(t, decoded.Instances, 1)
	assert.Equal(t, "db-instance-1", decoded.Instances[0].InstanceID)
	assert.Equal(t, "snap-123", decoded.Instances[0].SnapshotID)
	assert.False(t, decoded.Instances[0].WasStopped)
	assert.Equal(t, "db.t3.medium", decoded.Instances[0].InstanceType)

	clusterState := RestoreState{
		Clusters: []DBClusterState{
			{
				ClusterID:  "aurora-cluster-1",
				WasStopped: false,
			},
		},
	}

	data, err = json.Marshal(clusterState)
	assert.NoError(t, err)

	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Len(t, decoded.Clusters, 1)
	assert.Equal(t, "aurora-cluster-1", decoded.Clusters[0].ClusterID)
}

func TestParameters_JSON(t *testing.T) {
	params := Parameters{
		SnapshotBeforeStop: true,
		Selector: executorparams.RDSSelector{
			InstanceIDs: []string{"db-1"},
			ClusterIDs:  []string{"cluster-1"},
		},
	}

	data, err := json.Marshal(params)
	assert.NoError(t, err)

	var decoded Parameters
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.True(t, decoded.SnapshotBeforeStop)
	assert.Len(t, decoded.Selector.InstanceIDs, 1)
	assert.Equal(t, "db-1", decoded.Selector.InstanceIDs[0])
	assert.Len(t, decoded.Selector.ClusterIDs, 1)
	assert.Equal(t, "cluster-1", decoded.Selector.ClusterIDs[0])
}

func TestExecutorType_Constant(t *testing.T) {
	assert.Equal(t, "rds", ExecutorType)
}
