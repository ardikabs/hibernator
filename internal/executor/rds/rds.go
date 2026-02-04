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
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdsTypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/go-logr/logr"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/awsutil"
	"github.com/ardikabs/hibernator/pkg/executorparams"
)

const ExecutorType = "rds"

// Parameters is an alias for the shared RDS parameter type.
type Parameters = executorparams.RDSParameters

// RestoreState holds RDS restore data.
type RestoreState struct {
	Instances []DBInstanceState `json:"instances,omitempty"`
	Clusters  []DBClusterState  `json:"clusters,omitempty"`
}

// DBInstanceState holds state for a single DB instance.
type DBInstanceState struct {
	InstanceID   string `json:"instanceId"`
	WasStopped   bool   `json:"wasStopped"`
	SnapshotID   string `json:"snapshotId,omitempty"`
	InstanceType string `json:"instanceType,omitempty"`
}

// DBClusterState holds state for a single DB cluster.
type DBClusterState struct {
	ClusterID  string `json:"clusterId"`
	WasStopped bool   `json:"wasStopped"`
	SnapshotID string `json:"snapshotId,omitempty"`
}

// Executor implements the RDS hibernation logic.
type Executor struct {
	rdsFactory      RDSClientFactory
	stsFactory      STSClientFactory
	awsConfigLoader AWSConfigLoader
}

// RDSClientFactory is a function type for creating RDS clients.
type RDSClientFactory func(cfg aws.Config) RDSClient

// STSClientFactory is a function type for creating STS clients.
type STSClientFactory func(cfg aws.Config) STSClient

// AWSConfigLoader is a function type for loading AWS config.
type AWSConfigLoader func(ctx context.Context, spec executor.Spec) (aws.Config, error)

// New creates a new RDS executor.
func New() *Executor {
	return &Executor{
		rdsFactory: func(cfg aws.Config) RDSClient {
			return rds.NewFromConfig(cfg)
		},
		stsFactory: func(cfg aws.Config) STSClient {
			return sts.NewFromConfig(cfg)
		},
	}
}

// NewWithClients creates a new RDS executor with injected client factories.
// This is useful for testing with mock clients.
func NewWithClients(rdsFactory RDSClientFactory, stsFactory STSClientFactory, awsConfigLoader AWSConfigLoader) *Executor {
	return &Executor{
		rdsFactory:      rdsFactory,
		stsFactory:      stsFactory,
		awsConfigLoader: awsConfigLoader,
	}
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

	params, err := e.parseParams(spec.Parameters)
	if err != nil {
		return err
	}

	// Validate selector - at least one selection method required
	hasSelection := len(params.Selector.Tags) > 0 ||
		len(params.Selector.ExcludeTags) > 0 ||
		len(params.Selector.InstanceIDs) > 0 ||
		len(params.Selector.ClusterIDs) > 0 ||
		params.Selector.IncludeAll

	if !hasSelection {
		return fmt.Errorf("selector must specify at least one of: tags, excludeTags, instanceIds, clusterIds, or includeAll")
	}

	// Tags and ExcludeTags are mutually exclusive
	if len(params.Selector.Tags) > 0 && len(params.Selector.ExcludeTags) > 0 {
		return fmt.Errorf("selector.tags and selector.excludeTags are mutually exclusive")
	}

	// IncludeAll cannot be combined with other selection methods
	if params.Selector.IncludeAll {
		if len(params.Selector.Tags) > 0 || len(params.Selector.ExcludeTags) > 0 ||
			len(params.Selector.InstanceIDs) > 0 || len(params.Selector.ClusterIDs) > 0 {
			return fmt.Errorf("selector.includeAll cannot be combined with tags, excludeTags, instanceIds, or clusterIds")
		}
	}

	return nil
}

// Shutdown stops RDS instances/clusters with optional snapshot.
func (e *Executor) Shutdown(ctx context.Context, log logr.Logger, spec executor.Spec) (executor.RestoreData, error) {
	log.Info("RDS executor starting shutdown",
		"target", spec.TargetName,
		"targetType", spec.TargetType,
	)

	params, err := e.parseParams(spec.Parameters)
	if err != nil {
		log.Error(err, "failed to parse parameters")
		return executor.RestoreData{}, fmt.Errorf("parse parameters: %w", err)
	}

	log.Info("parameters parsed",
		"hasTagSelector", len(params.Selector.Tags) > 0,
		"hasExcludeTagSelector", len(params.Selector.ExcludeTags) > 0,
		"hasInstanceIDs", len(params.Selector.InstanceIDs) > 0,
		"hasClusterIDs", len(params.Selector.ClusterIDs) > 0,
		"includeAll", params.Selector.IncludeAll,
		"snapshotBeforeStop", params.SnapshotBeforeStop,
	)

	cfg, err := e.loadAWSConfig(ctx, spec)
	if err != nil {
		log.Error(err, "failed to load AWS config")
		return executor.RestoreData{}, fmt.Errorf("load AWS config: %w", err)
	}

	client := e.rdsFactory(cfg)

	// Determine which resource types to discover
	var discoverInstances, discoverClusters bool

	// For intent-based selection (explicit IDs), resource types are implicit
	if len(params.Selector.InstanceIDs) > 0 || len(params.Selector.ClusterIDs) > 0 {
		discoverInstances = len(params.Selector.InstanceIDs) > 0
		discoverClusters = len(params.Selector.ClusterIDs) > 0
	} else {
		// For dynamic discovery (tags/excludeTags/includeAll), must be explicitly enabled (opt-out)
		// Default bool zero value is false (no-op)
		discoverInstances = params.Selector.DiscoverInstances
		discoverClusters = params.Selector.DiscoverClusters
	}

	var instances []rdsTypes.DBInstance
	var clusters []rdsTypes.DBCluster

	// Discover DB instances (only if relevant to selector)
	if discoverInstances {
		log.Info("discovering DB instances matching selector")
		var err error
		instances, err = e.findDBInstances(ctx, log, client, params.Selector)
		if err != nil {
			log.Error(err, "failed to find DB instances")
			return executor.RestoreData{}, fmt.Errorf("find DB instances: %w", err)
		}
		log.Info("DB instances discovered", "totalInstances", len(instances))
	}

	// Discover DB clusters (only if relevant to selector)
	if discoverClusters {
		log.Info("discovering DB clusters matching selector")
		var err error
		clusters, err = e.findDBClusters(ctx, log, client, params.Selector)
		if err != nil {
			log.Error(err, "failed to find DB clusters")
			return executor.RestoreData{}, fmt.Errorf("find DB clusters: %w", err)
		}
		log.Info("DB clusters discovered", "totalClusters", len(clusters))
	}

	restoreState := RestoreState{
		Instances: make([]DBInstanceState, 0, len(instances)),
		Clusters:  make([]DBClusterState, 0, len(clusters)),
	}

	// Stop DB instances
	for _, inst := range instances {
		instanceID := aws.ToString(inst.DBInstanceIdentifier)
		log.Info("processing DB instance", "instanceId", instanceID)

		state, err := e.stopInstance(ctx, log, client, instanceID, params.SnapshotBeforeStop)
		if err != nil {
			log.Error(err, "failed to stop instance", "instanceId", instanceID)
			return executor.RestoreData{}, fmt.Errorf("stop instance %s: %w", instanceID, err)
		}

		restoreState.Instances = append(restoreState.Instances, state)
		log.Info("instance processed successfully",
			"instanceId", instanceID,
			"wasStopped", state.WasStopped,
			"snapshotCreated", state.SnapshotID != "",
		)
	}

	// Stop DB clusters
	for _, cluster := range clusters {
		clusterID := aws.ToString(cluster.DBClusterIdentifier)
		log.Info("processing DB cluster", "clusterId", clusterID)

		state, err := e.stopCluster(ctx, log, client, clusterID, params.SnapshotBeforeStop)
		if err != nil {
			log.Error(err, "failed to stop cluster", "clusterId", clusterID)
			return executor.RestoreData{}, fmt.Errorf("stop cluster %s: %w", clusterID, err)
		}

		restoreState.Clusters = append(restoreState.Clusters, state)
		log.Info("cluster processed successfully",
			"clusterId", clusterID,
			"wasStopped", state.WasStopped,
			"snapshotCreated", state.SnapshotID != "",
		)
	}

	restoreData, err := json.Marshal(restoreState)
	if err != nil {
		log.Error(err, "failed to marshal restore state")
		return executor.RestoreData{}, fmt.Errorf("marshal restore state: %w", err)
	}

	log.Info("RDS shutdown completed successfully",
		"totalInstances", len(restoreState.Instances),
		"totalClusters", len(restoreState.Clusters),
	)

	return executor.RestoreData{
		Type: ExecutorType,
		Data: restoreData,
	}, nil
}

// WakeUp starts RDS instances/clusters.
func (e *Executor) WakeUp(ctx context.Context, log logr.Logger, spec executor.Spec, restore executor.RestoreData) error {
	log.Info("RDS executor starting wakeup",
		"target", spec.TargetName,
		"targetType", spec.TargetType,
	)

	var restoreState RestoreState
	if err := json.Unmarshal(restore.Data, &restoreState); err != nil {
		log.Error(err, "failed to unmarshal restore state")
		return fmt.Errorf("unmarshal restore state: %w", err)
	}

	log.Info("restore state loaded",
		"totalInstances", len(restoreState.Instances),
		"totalClusters", len(restoreState.Clusters),
	)

	cfg, err := e.loadAWSConfig(ctx, spec)
	if err != nil {
		log.Error(err, "failed to load AWS config")
		return fmt.Errorf("load AWS config: %w", err)
	}

	client := e.rdsFactory(cfg)

	// Start DB instances
	for _, inst := range restoreState.Instances {
		if !inst.WasStopped {
			log.Info("starting RDS instance", "instanceId", inst.InstanceID)
			if err := e.startInstance(ctx, client, inst.InstanceID); err != nil {
				log.Error(err, "failed to start instance", "instanceId", inst.InstanceID)
				return fmt.Errorf("start instance %s: %w", inst.InstanceID, err)
			}
			log.Info("instance started successfully", "instanceId", inst.InstanceID)
		} else {
			log.Info("instance was already stopped, skipping start", "instanceId", inst.InstanceID)
		}
	}

	// Start DB clusters
	for _, cluster := range restoreState.Clusters {
		if !cluster.WasStopped {
			log.Info("starting RDS cluster", "clusterId", cluster.ClusterID)
			if err := e.startCluster(ctx, client, cluster.ClusterID); err != nil {
				log.Error(err, "failed to start cluster", "clusterId", cluster.ClusterID)
				return fmt.Errorf("start cluster %s: %w", cluster.ClusterID, err)
			}
			log.Info("cluster started successfully", "clusterId", cluster.ClusterID)
		} else {
			log.Info("cluster was already stopped, skipping start", "clusterId", cluster.ClusterID)
		}
	}

	log.Info("RDS wakeup completed successfully")
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
	if e.awsConfigLoader != nil {
		return e.awsConfigLoader(ctx, spec)
	}

	if spec.ConnectorConfig.AWS == nil {
		return aws.Config{}, fmt.Errorf("AWS connector config is required")
	}

	return awsutil.BuildAWSConfig(ctx, spec.ConnectorConfig.AWS)
}

func (e *Executor) stopInstance(ctx context.Context, log logr.Logger, client RDSClient, instanceID string, snapshotBeforeStop bool) (DBInstanceState, error) {
	// Get instance info
	desc, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String(instanceID),
	})
	if err != nil {
		return DBInstanceState{}, err
	}

	if len(desc.DBInstances) == 0 {
		return DBInstanceState{}, fmt.Errorf("instance %s not found", instanceID)
	}

	instance := desc.DBInstances[0]
	state := DBInstanceState{
		InstanceID:   instanceID,
		InstanceType: aws.ToString(instance.DBInstanceClass),
	}

	// Check if already stopped
	if aws.ToString(instance.DBInstanceStatus) == "stopped" {
		state.WasStopped = true
		return state, nil
	}

	// Create snapshot if requested
	if snapshotBeforeStop {
		snapshotID := fmt.Sprintf("%s-hibernate-%d", instanceID, time.Now().Unix())
		log.Info("creating DB snapshot before stop", "instanceId", instanceID, "snapshotId", snapshotID)
		_, err := client.CreateDBSnapshot(ctx, &rds.CreateDBSnapshotInput{
			DBInstanceIdentifier: aws.String(instanceID),
			DBSnapshotIdentifier: aws.String(snapshotID),
		})
		if err != nil {
			return DBInstanceState{}, fmt.Errorf("create snapshot: %w", err)
		}
		state.SnapshotID = snapshotID

		// Wait for snapshot to be available
		waiter := rds.NewDBSnapshotAvailableWaiter(client)
		log.Info("waiting for snapshot to be available", "snapshotId", snapshotID)
		if err := waiter.Wait(ctx, &rds.DescribeDBSnapshotsInput{
			DBSnapshotIdentifier: aws.String(snapshotID),
		}, 30*time.Minute); err != nil {
			return DBInstanceState{}, fmt.Errorf("wait for snapshot: %w", err)
		}
		log.Info("snapshot available", "snapshotId", snapshotID)
	}

	// Stop instance
	log.Info("stopping DB instance", "instanceId", instanceID)
	_, err = client.StopDBInstance(ctx, &rds.StopDBInstanceInput{
		DBInstanceIdentifier: aws.String(instanceID),
	})
	if err != nil {
		return DBInstanceState{}, err
	}

	return state, nil
}

func (e *Executor) stopCluster(ctx context.Context, log logr.Logger, client RDSClient, clusterID string, snapshotBeforeStop bool) (DBClusterState, error) {
	// Get cluster info
	desc, err := client.DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String(clusterID),
	})
	if err != nil {
		return DBClusterState{}, err
	}

	if len(desc.DBClusters) == 0 {
		return DBClusterState{}, fmt.Errorf("cluster %s not found", clusterID)
	}

	cluster := desc.DBClusters[0]
	state := DBClusterState{
		ClusterID: clusterID,
	}

	// Check if already stopped
	if aws.ToString(cluster.Status) == "stopped" {
		state.WasStopped = true
		return state, nil
	}

	// Create snapshot if requested
	if snapshotBeforeStop {
		snapshotID := fmt.Sprintf("%s-hibernate-%d", clusterID, time.Now().Unix())
		log.Info("creating DB cluster snapshot before stop", "clusterId", clusterID, "snapshotId", snapshotID)
		_, err := client.CreateDBClusterSnapshot(ctx, &rds.CreateDBClusterSnapshotInput{
			DBClusterIdentifier:         aws.String(clusterID),
			DBClusterSnapshotIdentifier: aws.String(snapshotID),
		})
		if err != nil {
			return DBClusterState{}, fmt.Errorf("create cluster snapshot: %w", err)
		}
		state.SnapshotID = snapshotID

		// Wait for snapshot
		waiter := rds.NewDBClusterSnapshotAvailableWaiter(client)
		log.Info("waiting for cluster snapshot to be available", "snapshotId", snapshotID)
		if err := waiter.Wait(ctx, &rds.DescribeDBClusterSnapshotsInput{
			DBClusterSnapshotIdentifier: aws.String(snapshotID),
		}, 30*time.Minute); err != nil {
			return DBClusterState{}, fmt.Errorf("wait for cluster snapshot: %w", err)
		}
		log.Info("cluster snapshot available", "snapshotId", snapshotID)
	}

	// Stop cluster
	log.Info("stopping DB cluster", "clusterId", clusterID)
	_, err = client.StopDBCluster(ctx, &rds.StopDBClusterInput{
		DBClusterIdentifier: aws.String(clusterID),
	})
	if err != nil {
		return DBClusterState{}, err
	}

	return state, nil
}

func (e *Executor) startInstance(ctx context.Context, client RDSClient, instanceID string) error {
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

func (e *Executor) startCluster(ctx context.Context, client RDSClient, clusterID string) error {
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

// findDBInstances discovers DB instances matching the selector.
func (e *Executor) findDBInstances(ctx context.Context, log logr.Logger, client RDSClient, selector executorparams.RDSSelector) ([]rdsTypes.DBInstance, error) {
	var instances []rdsTypes.DBInstance

	// If explicit instance IDs provided, fetch them directly
	if len(selector.InstanceIDs) > 0 {
		for _, instanceID := range selector.InstanceIDs {
			desc, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
				DBInstanceIdentifier: aws.String(instanceID),
			})
			if err != nil {
				log.Error(err, "failed to describe instance", "instanceId", instanceID)
				return nil, fmt.Errorf("describe instance %s: %w", instanceID, err)
			}
			if len(desc.DBInstances) > 0 {
				instances = append(instances, desc.DBInstances...)
			}
		}
		return instances, nil
	}

	// Otherwise, discover all instances and apply tag filtering
	log.Info("listing all DB instances in account/region")
	result, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{})
	if err != nil {
		return nil, fmt.Errorf("describe DB instances: %w", err)
	}

	// If includeAll, skip tag fetching and include everything
	if selector.IncludeAll {
		log.Info("including all DB instances (includeAll=true)", "count", len(result.DBInstances))
		return result.DBInstances, nil
	}

	// Apply tag-based filtering
	for _, inst := range result.DBInstances {
		instanceID := aws.ToString(inst.DBInstanceIdentifier)

		// Get tags for tag-based filtering
		instanceARN := aws.ToString(inst.DBInstanceArn)
		tagsResp, err := client.ListTagsForResource(ctx, &rds.ListTagsForResourceInput{
			ResourceName: aws.String(instanceARN),
		})
		if err != nil {
			log.Error(err, "failed to list tags for instance", "instanceId", instanceID)
			return nil, fmt.Errorf("list tags for instance %s: %w", instanceID, err)
		}

		instanceTags := make(map[string]string)
		for _, tag := range tagsResp.TagList {
			instanceTags[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
		}

		// Apply tag filtering
		if len(selector.Tags) > 0 {
			// Include instances matching tags
			if e.matchesTags(instanceTags, selector.Tags) {
				instances = append(instances, inst)
				log.Info("instance included (tags match)", "instanceId", instanceID, "tags", instanceTags)
			}
		} else if len(selector.ExcludeTags) > 0 {
			// Include instances NOT matching excludeTags
			if !e.matchesTags(instanceTags, selector.ExcludeTags) {
				instances = append(instances, inst)
				log.Info("instance included (excludeTags don't match)", "instanceId", instanceID, "tags", instanceTags)
			}
		}
	}

	return instances, nil
}

// findDBClusters discovers DB clusters matching the selector.
func (e *Executor) findDBClusters(ctx context.Context, log logr.Logger, client RDSClient, selector executorparams.RDSSelector) ([]rdsTypes.DBCluster, error) {
	var clusters []rdsTypes.DBCluster

	// If explicit cluster IDs provided, fetch them directly
	if len(selector.ClusterIDs) > 0 {
		for _, clusterID := range selector.ClusterIDs {
			desc, err := client.DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{
				DBClusterIdentifier: aws.String(clusterID),
			})
			if err != nil {
				log.Error(err, "failed to describe cluster", "clusterId", clusterID)
				return nil, fmt.Errorf("describe cluster %s: %w", clusterID, err)
			}
			if len(desc.DBClusters) > 0 {
				clusters = append(clusters, desc.DBClusters...)
			}
		}
		return clusters, nil
	}

	// Otherwise, discover all clusters and apply tag filtering
	log.Info("listing all DB clusters in account/region")
	result, err := client.DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{})
	if err != nil {
		return nil, fmt.Errorf("describe DB clusters: %w", err)
	}

	// If includeAll, skip tag fetching and include everything
	if selector.IncludeAll {
		log.Info("including all DB clusters (includeAll=true)", "count", len(result.DBClusters))
		return result.DBClusters, nil
	}

	// Apply tag-based filtering
	for _, cluster := range result.DBClusters {
		clusterID := aws.ToString(cluster.DBClusterIdentifier)

		// Get tags for tag-based filtering
		clusterARN := aws.ToString(cluster.DBClusterArn)
		tagsResp, err := client.ListTagsForResource(ctx, &rds.ListTagsForResourceInput{
			ResourceName: aws.String(clusterARN),
		})
		if err != nil {
			log.Error(err, "failed to list tags for cluster", "clusterId", clusterID)
			return nil, fmt.Errorf("list tags for cluster %s: %w", clusterID, err)
		}

		clusterTags := make(map[string]string)
		for _, tag := range tagsResp.TagList {
			clusterTags[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
		}

		// Apply tag filtering
		if len(selector.Tags) > 0 {
			// Include clusters matching tags
			if e.matchesTags(clusterTags, selector.Tags) {
				clusters = append(clusters, cluster)
				log.Info("cluster included (tags match)", "clusterId", clusterID, "tags", clusterTags)
			}
		} else if len(selector.ExcludeTags) > 0 {
			// Include clusters NOT matching excludeTags
			if !e.matchesTags(clusterTags, selector.ExcludeTags) {
				clusters = append(clusters, cluster)
				log.Info("cluster included (excludeTags don't match)", "clusterId", clusterID, "tags", clusterTags)
			}
		}
	}

	return clusters, nil
}

// matchesTags checks if resource tags match the selector tags.
// If selector tag value is empty string, matches any resource with that key.
// If selector tag value is non-empty, matches only exact key=value.
func (e *Executor) matchesTags(resourceTags map[string]string, selectorTags map[string]string) bool {
	for key, value := range selectorTags {
		resourceValue, hasKey := resourceTags[key]
		if !hasKey {
			// Resource doesn't have this key
			return false
		}
		if value != "" && resourceValue != value {
			// Selector specifies a value, but it doesn't match
			return false
		}
		// If value == "", we only check key existence (already verified above)
	}
	return true
}
