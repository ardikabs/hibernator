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
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdsTypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/go-logr/logr"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/awsutil"
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/ardikabs/hibernator/pkg/waiter"
)

const (
	ExecutorType       = "rds"
	DefaultWaitTimeout = "15m"
)

// Parameters is an alias for the shared RDS parameter type.
type Parameters = executorparams.RDSParameters

// RestoreState holds RDS restore data.
type RestoreState struct {
	Instances []DBInstanceState `json:"instances,omitempty"`
	Clusters  []DBClusterState  `json:"clusters,omitempty"`
}

// DBInstanceState holds state for a single DB instance.
type DBInstanceState struct {
	InstanceId   string `json:"instanceId"`
	WasStopped   bool   `json:"wasStopped"`
	SnapshotId   string `json:"snapshotId,omitempty"`
	InstanceType string `json:"instanceType,omitempty"`
}

// DBClusterState holds state for a single DB cluster.
type DBClusterState struct {
	ClusterId  string `json:"clusterId"`
	WasStopped bool   `json:"wasStopped"`
	SnapshotId string `json:"snapshotId,omitempty"`
}

// Executor implements the RDS hibernation logic.
type Executor struct {
	rdsFactory      RDSClientFactory
	stsFactory      STSClientFactory
	awsConfigLoader AWSConfigLoader

	waitinglistForInstances []string
	waitinglistForClusters  []string
	completionWg            sync.WaitGroup
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
		len(params.Selector.InstanceIds) > 0 ||
		len(params.Selector.ClusterIds) > 0 ||
		params.Selector.IncludeAll

	if !hasSelection {
		return fmt.Errorf("selector must specify at least one of: tags, excludeTags, InstanceIds, ClusterIds, or includeAll")
	}

	// Tags and ExcludeTags are mutually exclusive
	if len(params.Selector.Tags) > 0 && len(params.Selector.ExcludeTags) > 0 {
		return fmt.Errorf("selector.tags and selector.excludeTags are mutually exclusive")
	}

	// IncludeAll cannot be combined with other selection methods
	if params.Selector.IncludeAll {
		if len(params.Selector.Tags) > 0 || len(params.Selector.ExcludeTags) > 0 ||
			len(params.Selector.InstanceIds) > 0 || len(params.Selector.ClusterIds) > 0 {
			return fmt.Errorf("selector.includeAll cannot be combined with tags, excludeTags, InstanceIds, or ClusterIds")
		}
	}

	return nil
}

// Shutdown stops RDS instances/clusters with optional snapshot.
func (e *Executor) Shutdown(ctx context.Context, log logr.Logger, spec executor.Spec) error {
	log.Info("RDS executor starting shutdown",
		"target", spec.TargetName,
		"targetType", spec.TargetType,
	)

	params, err := e.parseParams(spec.Parameters)
	if err != nil {
		log.Error(err, "failed to parse parameters")
		return fmt.Errorf("parse parameters: %w", err)
	}

	log.Info("parameters parsed",
		"hasTagSelector", len(params.Selector.Tags) > 0,
		"hasExcludeTagSelector", len(params.Selector.ExcludeTags) > 0,
		"hasInstanceIDs", len(params.Selector.InstanceIds) > 0,
		"hasClusterIDs", len(params.Selector.ClusterIds) > 0,
		"includeAll", params.Selector.IncludeAll,
		"snapshotBeforeStop", params.SnapshotBeforeStop,
	)

	cfg, err := e.loadAWSConfig(ctx, spec)
	if err != nil {
		log.Error(err, "failed to load AWS config")
		return fmt.Errorf("load AWS config: %w", err)
	}

	client := e.rdsFactory(cfg)

	// Determine which resource types to discover
	var discoverInstances, discoverClusters bool

	// For intent-based selection (explicit IDs), resource types are implicit
	if len(params.Selector.InstanceIds) > 0 || len(params.Selector.ClusterIds) > 0 {
		discoverInstances = len(params.Selector.InstanceIds) > 0
		discoverClusters = len(params.Selector.ClusterIds) > 0
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
			return fmt.Errorf("find DB instances: %w", err)
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
			return fmt.Errorf("find DB clusters: %w", err)
		}
		log.Info("DB clusters discovered", "totalClusters", len(clusters))
	}

	// Stop DB instances
	for _, inst := range instances {
		instanceID := aws.ToString(inst.DBInstanceIdentifier)
		log.Info("processing DB instance", "instanceId", instanceID)

		if err := e.stopInstance(ctx, log, client, instanceID, params.SnapshotBeforeStop, params, spec.SaveRestoreData); err != nil {
			log.Error(err, "failed to stop instance", "instanceId", instanceID)
			return fmt.Errorf("stop instance %s: %w", instanceID, err)
		}
	}

	// Stop DB clusters
	for _, cluster := range clusters {
		clusterID := aws.ToString(cluster.DBClusterIdentifier)
		log.Info("processing DB cluster", "clusterId", clusterID)

		if err := e.stopCluster(ctx, log, client, clusterID, params.SnapshotBeforeStop, params, spec.SaveRestoreData); err != nil {
			log.Error(err, "failed to stop cluster", "clusterId", clusterID)
			return fmt.Errorf("stop cluster %s: %w", clusterID, err)
		}
	}

	// Wait for all stopping operations to complete if configured
	if params.AwaitCompletion.Enabled {
		timeout := params.AwaitCompletion.Timeout
		if timeout == "" {
			timeout = DefaultWaitTimeout
		}

		for _, inst := range e.waitinglistForInstances {
			e.completionWg.Add(1)
			go func(id string) {
				defer e.completionWg.Done()
				if err := e.waitForInstanceStopped(ctx, log, client, id, timeout); err != nil {
					log.Error(err, "failed to wait for RDS instance stopped", "instanceId", id)
				}
			}(inst)
		}

		for _, cluster := range e.waitinglistForClusters {
			e.completionWg.Add(1)
			go func(id string) {
				defer e.completionWg.Done()
				if err := e.waitForClusterStopped(ctx, log, client, id, timeout); err != nil {
					log.Error(err, "failed to wait for Aurora cluster stopped", "clusterId", id)
				}
			}(cluster)

		}

		e.completionWg.Wait()
	}

	log.Info("RDS shutdown completed successfully",
		"totalInstances", len(instances),
		"totalClusters", len(clusters),
	)

	return nil
}

// WakeUp starts RDS instances/clusters.
func (e *Executor) WakeUp(ctx context.Context, log logr.Logger, spec executor.Spec, restore executor.RestoreData) error {
	log.Info("RDS executor starting wakeup",
		"target", spec.TargetName,
		"targetType", spec.TargetType,
	)

	// Parse parameters
	params, err := e.parseParams(spec.Parameters)
	if err != nil {
		log.Error(err, "failed to parse parameters")
		return fmt.Errorf("parse parameters: %w", err)
	}

	log.Info("restore state loaded", "totalResources", len(restore.Data))

	cfg, err := e.loadAWSConfig(ctx, spec)
	if err != nil {
		log.Error(err, "failed to load AWS config")
		return fmt.Errorf("load AWS config: %w", err)
	}

	client := e.rdsFactory(cfg)

	// Iterate over all resources in restore data
	for key, stateBytes := range restore.Data {
		// Parse key to determine resource type
		if strings.HasPrefix(key, "instance:") {
			instanceID := strings.TrimPrefix(key, "instance:")
			var state DBInstanceState
			if err := json.Unmarshal(stateBytes, &state); err != nil {
				log.Error(err, "failed to unmarshal instance state", "instanceId", instanceID)
				return fmt.Errorf("unmarshal instance state %s: %w", instanceID, err)
			}

			if !state.WasStopped {
				log.Info("starting RDS instance", "instanceId", state.InstanceId)
				if err := e.startInstance(ctx, log, client, state.InstanceId, params); err != nil {
					log.Error(err, "failed to start instance", "instanceId", state.InstanceId)
					return fmt.Errorf("start instance %s: %w", state.InstanceId, err)
				}
				log.Info("instance started successfully", "instanceId", state.InstanceId)
			} else {
				log.Info("instance was already started, skipping start", "instanceId", state.InstanceId)
			}

		} else if strings.HasPrefix(key, "cluster:") {
			clusterID := strings.TrimPrefix(key, "cluster:")
			var state DBClusterState
			if err := json.Unmarshal(stateBytes, &state); err != nil {
				log.Error(err, "failed to unmarshal cluster state", "clusterId", clusterID)
				return fmt.Errorf("unmarshal cluster state %s: %w", clusterID, err)
			}

			if !state.WasStopped {
				log.Info("starting RDS cluster", "clusterId", state.ClusterId)
				if err := e.startCluster(ctx, log, client, state.ClusterId, params); err != nil {
					log.Error(err, "failed to start cluster", "clusterId", state.ClusterId)
					return fmt.Errorf("start cluster %s: %w", state.ClusterId, err)
				}
				log.Info("cluster started successfully", "clusterId", state.ClusterId)
			} else {
				log.Info("cluster was already started, skipping start", "clusterId", state.ClusterId)
			}
		} else {
			log.Info("unknown resource type in restore data, skipping", "key", key)
		}
	}

	if params.AwaitCompletion.Enabled {
		timeout := params.AwaitCompletion.Timeout
		if timeout == "" {
			timeout = DefaultWaitTimeout
		}
		for _, inst := range e.waitinglistForInstances {
			e.completionWg.Add(1)
			go func(id string) {
				defer e.completionWg.Done()
				if err := e.waitForInstanceAvailable(ctx, log, client, id, timeout); err != nil {
					log.Error(err, "failed to wait for RDS instance available", "instanceId", id)
				}
			}(inst)
		}

		for _, clust := range e.waitinglistForClusters {
			e.completionWg.Add(1)
			go func(id string) {
				defer e.completionWg.Done()
				if err := e.waitForClusterAvailable(ctx, log, client, id, timeout); err != nil {
					log.Error(err, "failed to wait for RDS cluster available", "clusterId", id)
				}
			}(clust)
		}

		e.completionWg.Wait()
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

func (e *Executor) stopInstance(ctx context.Context, log logr.Logger, client RDSClient, instanceId string, snapshotBeforeStop bool, params Parameters, callback executor.SaveRestoreDataFunc) error {
	// Get instance info
	desc, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String(instanceId),
	})
	if err != nil {
		return err
	}

	if len(desc.DBInstances) == 0 {
		return fmt.Errorf("instance %s not found", instanceId)
	}

	instance := desc.DBInstances[0]
	state := DBInstanceState{
		InstanceId:   instanceId,
		InstanceType: aws.ToString(instance.DBInstanceClass),
	}

	// Check if already stopped
	if aws.ToString(instance.DBInstanceStatus) == "stopped" {
		state.WasStopped = true
		return nil
	}

	// Create snapshot if requested
	if snapshotBeforeStop {
		snapshotId := fmt.Sprintf("%s-hibernate-%d", instanceId, time.Now().Unix())
		log.Info("creating DB snapshot before stop", "instanceId", instanceId, "snapshotId", snapshotId)
		_, err := client.CreateDBSnapshot(ctx, &rds.CreateDBSnapshotInput{
			DBInstanceIdentifier: aws.String(instanceId),
			DBSnapshotIdentifier: aws.String(snapshotId),
		})
		if err != nil {
			return fmt.Errorf("create snapshot: %w", err)
		}
		state.SnapshotId = snapshotId

		// Wait for snapshot to be available
		waiter := rds.NewDBSnapshotAvailableWaiter(client)
		log.Info("waiting for snapshot to be available", "snapshotId", snapshotId)
		if err := waiter.Wait(ctx, &rds.DescribeDBSnapshotsInput{
			DBSnapshotIdentifier: aws.String(snapshotId),
		}, 30*time.Minute); err != nil {
			return fmt.Errorf("wait for snapshot: %w", err)
		}
		log.Info("snapshot available", "snapshotId", snapshotId)
	}

	// Stop instance
	log.Info("stopping DB instance", "instanceId", instanceId)
	if _, err = client.StopDBInstance(ctx, &rds.StopDBInstanceInput{
		DBInstanceIdentifier: aws.String(instanceId),
	}); err != nil {
		return err
	}
	log.Info("instance processed successfully",
		"instanceId", instanceId,
		"wasStopped", state.WasStopped,
		"snapshotCreated", state.SnapshotId != "",
	)

	// Incremental save: persist this instance's restore data immediately
	if callback != nil {
		key := "instance:" + state.InstanceId
		if err := callback(key, state, !state.WasStopped); err != nil {
			log.Error(err, "failed to save restore data incrementally", "instanceId", instanceId)
			// Continue processing - save at end as fallback
		}
	}

	// Add to waiting list for awaiting completion if configured
	if params.AwaitCompletion.Enabled {
		e.waitinglistForInstances = append(e.waitinglistForInstances, instanceId)
	}

	return nil
}

func (e *Executor) stopCluster(ctx context.Context, log logr.Logger, client RDSClient, clusterId string, snapshotBeforeStop bool, params Parameters, callback executor.SaveRestoreDataFunc) error {
	// Get cluster info
	desc, err := client.DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String(clusterId),
	})
	if err != nil {
		return err
	}

	if len(desc.DBClusters) == 0 {
		return fmt.Errorf("cluster %s not found", clusterId)
	}

	cluster := desc.DBClusters[0]
	state := DBClusterState{
		ClusterId: clusterId,
	}

	// Check if already stopped
	if aws.ToString(cluster.Status) == "stopped" {
		state.WasStopped = true
		return nil
	}

	// Create snapshot if requested
	if snapshotBeforeStop {
		snapshotId := fmt.Sprintf("%s-hibernate-%d", clusterId, time.Now().Unix())
		log.Info("creating DB cluster snapshot before stop", "clusterId", clusterId, "snapshotId", snapshotId)
		_, err := client.CreateDBClusterSnapshot(ctx, &rds.CreateDBClusterSnapshotInput{
			DBClusterIdentifier:         aws.String(clusterId),
			DBClusterSnapshotIdentifier: aws.String(snapshotId),
		})
		if err != nil {
			return fmt.Errorf("create cluster snapshot: %w", err)
		}
		state.SnapshotId = snapshotId

		// Wait for snapshot
		waiter := rds.NewDBClusterSnapshotAvailableWaiter(client)
		log.Info("waiting for cluster snapshot to be available", "snapshotId", snapshotId)
		if err := waiter.Wait(ctx, &rds.DescribeDBClusterSnapshotsInput{
			DBClusterSnapshotIdentifier: aws.String(snapshotId),
		}, 30*time.Minute); err != nil {
			return fmt.Errorf("wait for cluster snapshot: %w", err)
		}
		log.Info("cluster snapshot available", "snapshotId", snapshotId)
	}

	// Stop cluster
	log.Info("stopping DB cluster", "clusterId", clusterId)
	if _, err = client.StopDBCluster(ctx, &rds.StopDBClusterInput{
		DBClusterIdentifier: aws.String(clusterId),
	}); err != nil {
		return err
	}

	log.Info("cluster processed successfully",
		"clusterId", clusterId,
		"wasStopped", state.WasStopped,
		"snapshotCreated", state.SnapshotId != "",
	)

	// Incremental save: persist this cluster's restore data immediately
	if callback != nil {
		key := "cluster:" + state.ClusterId
		if err := callback(key, state, !state.WasStopped); err != nil {
			log.Error(err, "failed to save restore data incrementally", "clusterId", clusterId)
			// Continue processing - save at end as fallback
		}
	}

	// Add to waiting list for awaiting completion if configured
	if params.AwaitCompletion.Enabled {
		e.waitinglistForClusters = append(e.waitinglistForClusters, clusterId)
	}

	return nil
}

func (e *Executor) startInstance(ctx context.Context, log logr.Logger, client RDSClient, instanceId string, params Parameters) error {
	// Check current status
	desc, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String(instanceId),
	})
	if err != nil {
		return err
	}

	if len(desc.DBInstances) == 0 {
		return fmt.Errorf("instance %s not found", instanceId)
	}

	status := aws.ToString(desc.DBInstances[0].DBInstanceStatus)
	if status == "available" {
		// Instance is already running, no action needed
		return nil
	}

	if status == "stopped" {
		// For now we simplify it that only RDS in "stopped" status can be started.
		// In practice, there are some other statuses that can be started (e.g. incompatible-network),
		// but we would need to do more complex handling to determine if start is valid in those cases (e.g. only certain instance types, only non-SQLServer engines).
		// For simplicity, we only allow starting from "stopped" status for now, and we can expand support later if needed.
		// As of now here are following statuses that are startable:
		// stopped, inaccessible-encryption-credentials-recoverable, incompatible-network (only valid for non-SqlServer instances)

		_, err = client.StartDBInstance(ctx, &rds.StartDBInstanceInput{
			DBInstanceIdentifier: aws.String(instanceId),
		})
		if err != nil {
			return err
		}
	}

	// Add to waiting list for awaiting completion if configured
	if params.AwaitCompletion.Enabled {
		e.waitinglistForInstances = append(e.waitinglistForInstances, instanceId)
	}

	return nil
}

func (e *Executor) startCluster(ctx context.Context, log logr.Logger, client RDSClient, clusterId string, params Parameters) error {
	// Check current status
	desc, err := client.DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String(clusterId),
	})
	if err != nil {
		return err
	}

	if len(desc.DBClusters) == 0 {
		return fmt.Errorf("cluster %s not found", clusterId)
	}

	status := aws.ToString(desc.DBClusters[0].Status)
	if status == "available" {
		// Cluster is already running, no action needed
		return nil
	}

	if status == "stopped" {
		// Only "stopped" clusters can be started

		_, err = client.StartDBCluster(ctx, &rds.StartDBClusterInput{
			DBClusterIdentifier: aws.String(clusterId),
		})
		if err != nil {
			return err
		}
	}

	// Wait for cluster to be available if configured
	if params.AwaitCompletion.Enabled {
		e.waitinglistForClusters = append(e.waitinglistForClusters, clusterId)
	}

	return nil
}

// findDBInstances discovers DB instances matching the selector.
func (e *Executor) findDBInstances(ctx context.Context, log logr.Logger, client RDSClient, selector executorparams.RDSSelector) ([]rdsTypes.DBInstance, error) {
	var instances []rdsTypes.DBInstance

	// If explicit instance IDs provided, fetch them directly
	if len(selector.InstanceIds) > 0 {
		for _, instanceID := range selector.InstanceIds {
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

		if strings.HasPrefix(instanceID, "aws-restore-") {
			log.Info("skipping instance that appears to be an AWS-managed restore job resource", "instanceId", instanceID)
			continue
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
	if len(selector.ClusterIds) > 0 {
		for _, clusterID := range selector.ClusterIds {
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

// waitForInstanceStopped waits for a DB instance to reach stopped state.
func (e *Executor) waitForInstanceStopped(ctx context.Context, log logr.Logger, client RDSClient, instanceID string, timeout string) error {
	if timeout == "" {
		timeout = DefaultWaitTimeout
	}

	w, err := waiter.NewWaiter(ctx, log, timeout)
	if err != nil {
		return err
	}

	description := fmt.Sprintf("DB instance %s to stop", instanceID)
	return w.Poll(description, func() (bool, string, error) {
		desc, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
			DBInstanceIdentifier: aws.String(instanceID),
		})
		if err != nil {
			return false, "", err
		}

		if len(desc.DBInstances) == 0 {
			return false, "", fmt.Errorf("instance %s not found", instanceID)
		}

		status := aws.ToString(desc.DBInstances[0].DBInstanceStatus)
		done := status == "stopped"
		statusStr := fmt.Sprintf("status=%s", status)

		return done, statusStr, nil
	})
}

// waitForInstanceAvailable waits for a DB instance to reach available state.
func (e *Executor) waitForInstanceAvailable(ctx context.Context, log logr.Logger, client RDSClient, instanceId string, timeout string) error {
	log.Info("waiting for RDS instance to be available",
		"instanceId", instanceId,
		"timeout", timeout,
	)

	w, err := waiter.NewWaiter(ctx, log, timeout)
	if err != nil {
		return err
	}

	description := fmt.Sprintf("DB instance %s to be available", instanceId)
	return w.Poll(description, func() (bool, string, error) {
		desc, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
			DBInstanceIdentifier: aws.String(instanceId),
		})
		if err != nil {
			return false, "", err
		}

		if len(desc.DBInstances) == 0 {
			return false, "", fmt.Errorf("instance %s not found", instanceId)
		}

		status := aws.ToString(desc.DBInstances[0].DBInstanceStatus)
		done := status == "available"
		statusStr := fmt.Sprintf("status=%s", status)

		return done, statusStr, nil
	})
}

// waitForClusterStopped waits for a DB cluster to reach stopped state.
func (e *Executor) waitForClusterStopped(ctx context.Context, log logr.Logger, client RDSClient, clusterID string, timeout string) error {
	if timeout == "" {
		timeout = DefaultWaitTimeout
	}

	w, err := waiter.NewWaiter(ctx, log, timeout)
	if err != nil {
		return err
	}

	description := fmt.Sprintf("DB cluster %s to stop", clusterID)
	return w.Poll(description, func() (bool, string, error) {
		desc, err := client.DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{
			DBClusterIdentifier: aws.String(clusterID),
		})
		if err != nil {
			return false, "", err
		}

		if len(desc.DBClusters) == 0 {
			return false, "", fmt.Errorf("cluster %s not found", clusterID)
		}

		status := aws.ToString(desc.DBClusters[0].Status)
		done := status == "stopped"
		statusStr := fmt.Sprintf("status=%s", status)

		return done, statusStr, nil
	})
}

// waitForClusterAvailable waits for a DB cluster to reach available state.
func (e *Executor) waitForClusterAvailable(ctx context.Context, log logr.Logger, client RDSClient, clusterId string, timeout string) error {
	log.Info("waiting for DB cluster to be available",
		"clusterId", clusterId,
		"timeout", timeout,
	)
	w, err := waiter.NewWaiter(ctx, log, timeout)
	if err != nil {
		return err
	}

	description := fmt.Sprintf("DB cluster %s to be available", clusterId)
	return w.Poll(description, func() (bool, string, error) {
		desc, err := client.DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{
			DBClusterIdentifier: aws.String(clusterId),
		})
		if err != nil {
			return false, "", err
		}

		if len(desc.DBClusters) == 0 {
			return false, "", fmt.Errorf("cluster %s not found", clusterId)
		}

		status := aws.ToString(desc.DBClusters[0].Status)
		done := status == "available"
		statusStr := fmt.Sprintf("status=%s", status)

		return done, statusStr, nil
	})
}
