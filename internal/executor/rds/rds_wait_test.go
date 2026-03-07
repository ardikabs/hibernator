/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package rds

// rds_wait_test.go tests the waitFor* helper methods directly to cover the
// AwaitCompletion code paths that are not exercised by the higher-level
// Shutdown/WakeUp tests.

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/ardikabs/hibernator/internal/executor/rds/mocks"
)

// ---- waitForInstanceStopped ----

func TestWaitForInstanceStopped_AlreadyStopped(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}

	mockRDS.On("DescribeDBInstances", mock.Anything, mock.Anything).
		Return(&awsrds.DescribeDBInstancesOutput{
			DBInstances: []types.DBInstance{
				{
					DBInstanceIdentifier: aws.String("db-1"),
					DBInstanceStatus:     aws.String("stopped"),
				},
			},
		}, nil)

	e := &Executor{}
	err := e.waitForInstanceStopped(ctx, logr.Discard(), mockRDS, "db-1", "5s")
	assert.NoError(t, err)
	mockRDS.AssertExpectations(t)
}

func TestWaitForInstanceStopped_DefaultTimeout(t *testing.T) {
	// Passing empty timeout should use DefaultWaitTimeout
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}

	mockRDS.On("DescribeDBInstances", mock.Anything, mock.Anything).
		Return(&awsrds.DescribeDBInstancesOutput{
			DBInstances: []types.DBInstance{
				{
					DBInstanceIdentifier: aws.String("db-1"),
					DBInstanceStatus:     aws.String("stopped"),
				},
			},
		}, nil)

	e := &Executor{}
	// empty timeout → picks up DefaultWaitTimeout constant
	err := e.waitForInstanceStopped(ctx, logr.Discard(), mockRDS, "db-1", "")
	assert.NoError(t, err)
}

func TestWaitForInstanceStopped_InvalidTimeout(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}

	e := &Executor{}
	err := e.waitForInstanceStopped(ctx, logr.Discard(), mockRDS, "db-1", "not-a-duration")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid timeout format")
}

func TestWaitForInstanceStopped_DescribeError(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}

	mockRDS.On("DescribeDBInstances", mock.Anything, mock.Anything).
		Return((*awsrds.DescribeDBInstancesOutput)(nil), assert.AnError)

	e := &Executor{}
	err := e.waitForInstanceStopped(ctx, logr.Discard(), mockRDS, "db-1", "5s")
	assert.Error(t, err)
	mockRDS.AssertExpectations(t)
}

func TestWaitForInstanceStopped_InstanceNotFound(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}

	mockRDS.On("DescribeDBInstances", mock.Anything, mock.Anything).
		Return(&awsrds.DescribeDBInstancesOutput{
			DBInstances: []types.DBInstance{},
		}, nil)

	e := &Executor{}
	err := e.waitForInstanceStopped(ctx, logr.Discard(), mockRDS, "db-1", "5s")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ---- waitForInstanceAvailable ----

func TestWaitForInstanceAvailable_AlreadyAvailable(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}

	mockRDS.On("DescribeDBInstances", mock.Anything, mock.Anything).
		Return(&awsrds.DescribeDBInstancesOutput{
			DBInstances: []types.DBInstance{
				{
					DBInstanceIdentifier: aws.String("db-1"),
					DBInstanceStatus:     aws.String("available"),
				},
			},
		}, nil)

	e := &Executor{}
	err := e.waitForInstanceAvailable(ctx, logr.Discard(), mockRDS, "db-1", "5s")
	assert.NoError(t, err)
	mockRDS.AssertExpectations(t)
}

func TestWaitForInstanceAvailable_InvalidTimeout(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}

	e := &Executor{}
	err := e.waitForInstanceAvailable(ctx, logr.Discard(), mockRDS, "db-1", "bad-timeout")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid timeout format")
}

func TestWaitForInstanceAvailable_DescribeError(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}

	mockRDS.On("DescribeDBInstances", mock.Anything, mock.Anything).
		Return((*awsrds.DescribeDBInstancesOutput)(nil), assert.AnError)

	e := &Executor{}
	err := e.waitForInstanceAvailable(ctx, logr.Discard(), mockRDS, "db-1", "5s")
	assert.Error(t, err)
	mockRDS.AssertExpectations(t)
}

func TestWaitForInstanceAvailable_InstanceNotFound(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}

	mockRDS.On("DescribeDBInstances", mock.Anything, mock.Anything).
		Return(&awsrds.DescribeDBInstancesOutput{
			DBInstances: []types.DBInstance{},
		}, nil)

	e := &Executor{}
	err := e.waitForInstanceAvailable(ctx, logr.Discard(), mockRDS, "db-1", "5s")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ---- waitForClusterStopped ----

func TestWaitForClusterStopped_AlreadyStopped(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}

	mockRDS.On("DescribeDBClusters", mock.Anything, mock.Anything).
		Return(&awsrds.DescribeDBClustersOutput{
			DBClusters: []types.DBCluster{
				{
					DBClusterIdentifier: aws.String("cluster-1"),
					Status:              aws.String("stopped"),
				},
			},
		}, nil)

	e := &Executor{}
	err := e.waitForClusterStopped(ctx, logr.Discard(), mockRDS, "cluster-1", "5s")
	assert.NoError(t, err)
	mockRDS.AssertExpectations(t)
}

func TestWaitForClusterStopped_DefaultTimeout(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}

	mockRDS.On("DescribeDBClusters", mock.Anything, mock.Anything).
		Return(&awsrds.DescribeDBClustersOutput{
			DBClusters: []types.DBCluster{
				{
					DBClusterIdentifier: aws.String("cluster-1"),
					Status:              aws.String("stopped"),
				},
			},
		}, nil)

	e := &Executor{}
	err := e.waitForClusterStopped(ctx, logr.Discard(), mockRDS, "cluster-1", "")
	assert.NoError(t, err)
}

func TestWaitForClusterStopped_InvalidTimeout(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}

	e := &Executor{}
	err := e.waitForClusterStopped(ctx, logr.Discard(), mockRDS, "cluster-1", "bad-timeout")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid timeout format")
}

func TestWaitForClusterStopped_DescribeError(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}

	mockRDS.On("DescribeDBClusters", mock.Anything, mock.Anything).
		Return((*awsrds.DescribeDBClustersOutput)(nil), assert.AnError)

	e := &Executor{}
	err := e.waitForClusterStopped(ctx, logr.Discard(), mockRDS, "cluster-1", "5s")
	assert.Error(t, err)
	mockRDS.AssertExpectations(t)
}

func TestWaitForClusterStopped_ClusterNotFound(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}

	mockRDS.On("DescribeDBClusters", mock.Anything, mock.Anything).
		Return(&awsrds.DescribeDBClustersOutput{
			DBClusters: []types.DBCluster{},
		}, nil)

	e := &Executor{}
	err := e.waitForClusterStopped(ctx, logr.Discard(), mockRDS, "cluster-1", "5s")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ---- waitForClusterAvailable ----

func TestWaitForClusterAvailable_AlreadyAvailable(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}

	mockRDS.On("DescribeDBClusters", mock.Anything, mock.Anything).
		Return(&awsrds.DescribeDBClustersOutput{
			DBClusters: []types.DBCluster{
				{
					DBClusterIdentifier: aws.String("cluster-1"),
					Status:              aws.String("available"),
				},
			},
		}, nil)

	e := &Executor{}
	err := e.waitForClusterAvailable(ctx, logr.Discard(), mockRDS, "cluster-1", "5s")
	assert.NoError(t, err)
	mockRDS.AssertExpectations(t)
}

func TestWaitForClusterAvailable_InvalidTimeout(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}

	e := &Executor{}
	err := e.waitForClusterAvailable(ctx, logr.Discard(), mockRDS, "cluster-1", "bad-timeout")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid timeout format")
}

func TestWaitForClusterAvailable_DescribeError(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}

	mockRDS.On("DescribeDBClusters", mock.Anything, mock.Anything).
		Return((*awsrds.DescribeDBClustersOutput)(nil), assert.AnError)

	e := &Executor{}
	err := e.waitForClusterAvailable(ctx, logr.Discard(), mockRDS, "cluster-1", "5s")
	assert.Error(t, err)
	mockRDS.AssertExpectations(t)
}

func TestWaitForClusterAvailable_ClusterNotFound(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}

	mockRDS.On("DescribeDBClusters", mock.Anything, mock.Anything).
		Return(&awsrds.DescribeDBClustersOutput{
			DBClusters: []types.DBCluster{},
		}, nil)

	e := &Executor{}
	err := e.waitForClusterAvailable(ctx, logr.Discard(), mockRDS, "cluster-1", "5s")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
