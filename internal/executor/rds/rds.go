/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package rds implements the RDS executor for hibernating RDS instances.
package rds

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/rds/types"

	"github.com/ardikabs/hibernator/internal/executor"
)

const ExecutorType = "rds"

// Parameters for the RDS executor.
type Parameters struct {
	SnapshotBeforeStop bool   `json:"snapshotBeforeStop,omitempty"`
	InstanceID         string `json:"instanceId,omitempty"`
	ClusterID          string `json:"clusterId,omitempty"`
}

// RestoreState holds RDS restore data.
type RestoreState struct {
	InstanceID   string `json:"instanceId,omitempty"`
	ClusterID    string `json:"clusterId,omitempty"`
	SnapshotID   string `json:"snapshotId,omitempty"`
	WasStopped   bool   `json:"wasStopped"`
	InstanceType string `json:"instanceType,omitempty"`
}

// Executor implements the RDS hibernation logic.
type Executor struct{}

// New creates a new RDS executor.
func New() *Executor {
	return &Executor{}
}

// Type returns the executor type.
func (e *Executor) Type() string {
	return ExecutorType
}

// Validate validates the executor spec.
func (e *Executor) Validate(spec executor.Spec) error {
	if spec.ConnectorConfig.AWS == nil {
		return fmt.Errorf("AWS connector config required for RDS executor")
	}
	return nil
}

// Shutdown stops RDS instances/clusters with optional snapshot.
func (e *Executor) Shutdown(ctx context.Context, spec executor.Spec) (executor.RestoreData, error) {
	params, err := e.parseParams(spec.Parameters)
	if err != nil {
		return executor.RestoreData{}, fmt.Errorf("parse parameters: %w", err)
	}

	cfg, err := e.loadAWSConfig(ctx, spec)
	if err != nil {
		return executor.RestoreData{}, fmt.Errorf("load AWS config: %w", err)
	}

	client := rds.NewFromConfig(cfg)
	restoreState := RestoreState{}

	if params.InstanceID != "" {
		state, err := e.stopInstance(ctx, client, params)
		if err != nil {
			return executor.RestoreData{}, fmt.Errorf("stop instance: %w", err)
		}
		restoreState = state
	} else if params.ClusterID != "" {
		state, err := e.stopCluster(ctx, client, params)
		if err != nil {
			return executor.RestoreData{}, fmt.Errorf("stop cluster: %w", err)
		}
		restoreState = state
	} else {
		return executor.RestoreData{}, fmt.Errorf("either instanceId or clusterId must be specified")
	}

	restoreData, err := json.Marshal(restoreState)
	if err != nil {
		return executor.RestoreData{}, fmt.Errorf("marshal restore state: %w", err)
	}

	return executor.RestoreData{
		Type: ExecutorType,
		Data: restoreData,
	}, nil
}

// WakeUp starts RDS instances/clusters.
func (e *Executor) WakeUp(ctx context.Context, spec executor.Spec, restore executor.RestoreData) error {
	var restoreState RestoreState
	if err := json.Unmarshal(restore.Data, &restoreState); err != nil {
		return fmt.Errorf("unmarshal restore state: %w", err)
	}

	cfg, err := e.loadAWSConfig(ctx, spec)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	client := rds.NewFromConfig(cfg)

	if restoreState.InstanceID != "" {
		if err := e.startInstance(ctx, client, restoreState.InstanceID); err != nil {
			return fmt.Errorf("start instance: %w", err)
		}
	} else if restoreState.ClusterID != "" {
		if err := e.startCluster(ctx, client, restoreState.ClusterID); err != nil {
			return fmt.Errorf("start cluster: %w", err)
		}
	}

	return nil
}

func (e *Executor) parseParams(raw json.RawMessage) (Parameters, error) {
	var params Parameters
	if len(raw) == 0 {
		return params, nil
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return params, err
	}
	return params, nil
}

func (e *Executor) loadAWSConfig(ctx context.Context, spec executor.Spec) (aws.Config, error) {
	region := spec.ConnectorConfig.AWS.Region

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return aws.Config{}, err
	}

	if spec.ConnectorConfig.AWS.AssumeRoleArn != "" {
		stsClient := stscreds.NewAssumeRoleProvider(
			stscreds.NewAssumeRoleProvider(nil, spec.ConnectorConfig.AWS.AssumeRoleArn),
		)
		cfg.Credentials = aws.NewCredentialsCache(stsClient)
	}

	return cfg, nil
}

func (e *Executor) stopInstance(ctx context.Context, client *rds.Client, params Parameters) (RestoreState, error) {
	// Get instance info
	desc, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String(params.InstanceID),
	})
	if err != nil {
		return RestoreState{}, err
	}

	if len(desc.DBInstances) == 0 {
		return RestoreState{}, fmt.Errorf("instance %s not found", params.InstanceID)
	}

	instance := desc.DBInstances[0]
	state := RestoreState{
		InstanceID:   params.InstanceID,
		InstanceType: aws.ToString(instance.DBInstanceClass),
	}

	// Check if already stopped
	if aws.ToString(instance.DBInstanceStatus) == "stopped" {
		state.WasStopped = true
		return state, nil
	}

	// Create snapshot if requested
	if params.SnapshotBeforeStop {
		snapshotID := fmt.Sprintf("%s-hibernate-%d", params.InstanceID, time.Now().Unix())
		_, err := client.CreateDBSnapshot(ctx, &rds.CreateDBSnapshotInput{
			DBInstanceIdentifier: aws.String(params.InstanceID),
			DBSnapshotIdentifier: aws.String(snapshotID),
		})
		if err != nil {
			return RestoreState{}, fmt.Errorf("create snapshot: %w", err)
		}
		state.SnapshotID = snapshotID

		// Wait for snapshot to be available
		waiter := rds.NewDBSnapshotAvailableWaiter(client)
		if err := waiter.Wait(ctx, &rds.DescribeDBSnapshotsInput{
			DBSnapshotIdentifier: aws.String(snapshotID),
		}, 30*time.Minute); err != nil {
			return RestoreState{}, fmt.Errorf("wait for snapshot: %w", err)
		}
	}

	// Stop instance
	_, err = client.StopDBInstance(ctx, &rds.StopDBInstanceInput{
		DBInstanceIdentifier: aws.String(params.InstanceID),
	})
	if err != nil {
		return RestoreState{}, err
	}

	return state, nil
}

func (e *Executor) stopCluster(ctx context.Context, client *rds.Client, params Parameters) (RestoreState, error) {
	// Get cluster info
	desc, err := client.DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String(params.ClusterID),
	})
	if err != nil {
		return RestoreState{}, err
	}

	if len(desc.DBClusters) == 0 {
		return RestoreState{}, fmt.Errorf("cluster %s not found", params.ClusterID)
	}

	cluster := desc.DBClusters[0]
	state := RestoreState{
		ClusterID: params.ClusterID,
	}

	// Check if already stopped
	if aws.ToString(cluster.Status) == "stopped" {
		state.WasStopped = true
		return state, nil
	}

	// Create snapshot if requested
	if params.SnapshotBeforeStop {
		snapshotID := fmt.Sprintf("%s-hibernate-%d", params.ClusterID, time.Now().Unix())
		_, err := client.CreateDBClusterSnapshot(ctx, &rds.CreateDBClusterSnapshotInput{
			DBClusterIdentifier:         aws.String(params.ClusterID),
			DBClusterSnapshotIdentifier: aws.String(snapshotID),
		})
		if err != nil {
			return RestoreState{}, fmt.Errorf("create cluster snapshot: %w", err)
		}
		state.SnapshotID = snapshotID

		// Wait for snapshot
		waiter := rds.NewDBClusterSnapshotAvailableWaiter(client)
		if err := waiter.Wait(ctx, &rds.DescribeDBClusterSnapshotsInput{
			DBClusterSnapshotIdentifier: aws.String(snapshotID),
		}, 30*time.Minute); err != nil {
			return RestoreState{}, fmt.Errorf("wait for cluster snapshot: %w", err)
		}
	}

	// Stop cluster
	_, err = client.StopDBCluster(ctx, &rds.StopDBClusterInput{
		DBClusterIdentifier: aws.String(params.ClusterID),
	})
	if err != nil {
		return RestoreState{}, err
	}

	return state, nil
}

func (e *Executor) startInstance(ctx context.Context, client *rds.Client, instanceID string) error {
	// Check current status
	desc, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String(instanceID),
	})
	if err != nil {
		return err
	}

	if len(desc.DBInstances) == 0 {
		return fmt.Errorf("instance %s not found", instanceID)
	}

	status := aws.ToString(desc.DBInstances[0].DBInstanceStatus)
	if status == "available" {
		return nil // Already running
	}

	_, err = client.StartDBInstance(ctx, &rds.StartDBInstanceInput{
		DBInstanceIdentifier: aws.String(instanceID),
	})
	return err
}

func (e *Executor) startCluster(ctx context.Context, client *rds.Client, clusterID string) error {
	// Check current status
	desc, err := client.DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String(clusterID),
	})
	if err != nil {
		return err
	}

	if len(desc.DBClusters) == 0 {
		return fmt.Errorf("cluster %s not found", clusterID)
	}

	status := aws.ToString(desc.DBClusters[0].Status)
	if status == "available" {
		return nil // Already running
	}

	_, err = client.StartDBCluster(ctx, &rds.StartDBClusterInput{
		DBClusterIdentifier: aws.String(clusterID),
	})
	return err
}

// Ensure we use the types package to avoid import errors
var _ types.DBInstance

func init() {
	executor.Register(New())
}
