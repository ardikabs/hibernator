/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package rds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/smithy-go"
	"github.com/go-logr/logr"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/ardikabs/hibernator/pkg/waiter"
)

// clusterStrategy implements ResourceStrategy for DB clusters
type clusterStrategy struct{}

// ResourceType returns the type of resource this strategy handles
func (s *clusterStrategy) ResourceType() ResourceType {
	return ResourceTypeCluster
}

// Discover finds DB clusters matching the selector
func (s *clusterStrategy) Discover(ctx context.Context, log logr.Logger, client RDSClient, selector executorparams.RDSSelector) ([]string, error) {
	var clusterIDs []string

	// If explicit cluster IDs provided, use them directly
	if len(selector.ClusterIds) > 0 {
		return selector.ClusterIds, nil
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
		for _, cluster := range result.DBClusters {
			clusterIDs = append(clusterIDs, aws.ToString(cluster.DBClusterIdentifier))
		}
		return clusterIDs, nil
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
			if matchesTags(clusterTags, selector.Tags) {
				clusterIDs = append(clusterIDs, clusterID)
				log.Info("cluster included (tags match)", "clusterId", clusterID, "tags", clusterTags)
			}
		} else if len(selector.ExcludeTags) > 0 {
			// Include clusters NOT matching excludeTags
			if !matchesTags(clusterTags, selector.ExcludeTags) {
				clusterIDs = append(clusterIDs, clusterID)
				log.Info("cluster included (excludeTags don't match)", "clusterId", clusterID, "tags", clusterTags)
			}
		}
	}

	return clusterIDs, nil
}

// Stop stops a DB cluster and returns its state (with embedded outcome)
func (s *clusterStrategy) Stop(ctx context.Context, log logr.Logger, client RDSClient, id string, snapshotBefore bool, params Parameters, callback executor.ReportStateCallback) (ResourceState, error) {
	// Get cluster info
	desc, err := client.DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String(id),
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "DBClusterNotFoundFault" {
			log.Info("cluster not found, skipping ...", "clusterId", id)
			return DBClusterState{Outcome: operationOutcomeSkippedStale}, nil
		}
		return nil, err
	}

	if len(desc.DBClusters) == 0 {
		return nil, fmt.Errorf("cluster %s not found", id)
	}

	cluster := desc.DBClusters[0]
	state := DBClusterState{
		ClusterId: id,
	}

	status := aws.ToString(cluster.Status)

	switch status {
	case "available":
		state.WasRunning = true

		// Create snapshot if requested
		if snapshotBefore {
			snapshotManager := newSnapshotManager(client)
			snapshotID, err := snapshotManager.createClusterSnapshot(ctx, log, id)
			if err != nil {
				return nil, err
			}
			state.SnapshotId = snapshotID
		}

		// Stop cluster
		log.Info("stopping DB cluster", "clusterId", id)
		if _, err = client.StopDBCluster(ctx, &rds.StopDBClusterInput{
			DBClusterIdentifier: aws.String(id),
		}); err != nil {
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) && apiErr.ErrorCode() == "DBClusterNotFoundFault" {
				log.Info("cluster not found, skipping ...", "clusterId", id)
				return DBClusterState{Outcome: operationOutcomeSkippedStale}, nil
			}
			return nil, err
		}
		state.Outcome = operationOutcomeApplied
	case "stopped":
		state.WasRunning = false
		state.Outcome = operationOutcomeApplied
		log.Info("cluster is already stopped", "clusterId", id)
	default:
		// If awaitCompletion is enabled, mark as pending to wait for state transition
		if params.AwaitCompletion.Enabled {
			log.Info("cluster is in a transitional state, will wait for availability before stopping",
				"clusterId", id, "status", status)
			return DBClusterState{Outcome: operationOutcomePending}, nil
		}
		log.Info("cluster is in a status that cannot be stopped, skipping stop ...",
			"clusterId", id, "status", status)
		return DBClusterState{Outcome: operationOutcomeSkippedStale}, nil
	}

	// Incremental save: persist this cluster's restore data immediately
	if callback != nil {
		key := "cluster:" + state.ClusterId
		if err := callback(key, state); err != nil {
			log.Error(err, "failed to save restore data incrementally", "clusterId", id)
		}
	}

	log.Info("cluster processed successfully",
		"clusterId", id,
		"wasRunning", state.WasRunning,
		"snapshotCreated", state.SnapshotId != "",
	)

	return state, nil
}

// Start starts a DB cluster and returns its state (with embedded outcome)
func (s *clusterStrategy) Start(ctx context.Context, log logr.Logger, client RDSClient, id string, params Parameters) (ResourceState, error) {
	// Check current status
	desc, err := client.DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String(id),
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "DBClusterNotFoundFault" {
			log.Info("cluster not found, skipping ...", "clusterId", id)
			return DBClusterState{Outcome: operationOutcomeSkippedStale}, nil
		}
		return nil, err
	}

	if len(desc.DBClusters) == 0 {
		return nil, fmt.Errorf("cluster %s not found", id)
	}

	status := aws.ToString(desc.DBClusters[0].Status)
	if status == "available" {
		log.Info("cluster is already running", "clusterId", id)
		return DBClusterState{Outcome: operationOutcomeSkippedStale}, nil
	}

	if status != "stopped" {
		// If awaitCompletion is enabled, mark as pending to wait for state transition
		if params.AwaitCompletion.Enabled {
			log.Info("cluster is in a transitional state, will wait for stopped state before starting",
				"clusterId", id, "status", status)
			return DBClusterState{Outcome: operationOutcomePending}, nil
		}

		log.Info("cluster is in a status that cannot be started, skipping start ...",
			"clusterId", id, "status", status)
		return DBClusterState{Outcome: operationOutcomeSkippedStale}, nil
	}

	_, err = client.StartDBCluster(ctx, &rds.StartDBClusterInput{
		DBClusterIdentifier: aws.String(id),
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "DBClusterNotFoundFault" {
			log.Info("cluster not found, skipping ...", "clusterId", id)
			return DBClusterState{Outcome: operationOutcomeSkippedStale}, nil
		}
		return nil, err
	}

	return DBClusterState{Outcome: operationOutcomeApplied}, nil
}

// WaitForAvailable waits for a DB cluster to reach available state
func (s *clusterStrategy) WaitForAvailable(ctx context.Context, log logr.Logger, client RDSClient, id string, timeout string) error {
	log.Info("waiting for DB cluster to be available",
		"clusterId", id,
		"timeout", timeout,
	)

	w, err := waiter.NewWaiter(ctx, log, timeout)
	if err != nil {
		return err
	}

	description := fmt.Sprintf("DB cluster %s to be available", id)
	return w.Poll(description, func() (bool, string, error) {
		desc, err := client.DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{
			DBClusterIdentifier: aws.String(id),
		})
		if err != nil {
			return false, "", err
		}

		if len(desc.DBClusters) == 0 {
			return false, "", fmt.Errorf("cluster %s not found", id)
		}

		status := aws.ToString(desc.DBClusters[0].Status)
		done := status == "available"
		statusStr := fmt.Sprintf("status=%s", status)

		return done, statusStr, nil
	})
}

// WaitForStopped waits for a DB cluster to reach stopped state
func (s *clusterStrategy) WaitForStopped(ctx context.Context, log logr.Logger, client RDSClient, id string, timeout string) error {
	if timeout == "" {
		timeout = DefaultWaitTimeout
	}

	w, err := waiter.NewWaiter(ctx, log, timeout)
	if err != nil {
		return err
	}

	description := fmt.Sprintf("DB cluster %s to stop", id)
	return w.Poll(description, func() (bool, string, error) {
		desc, err := client.DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{
			DBClusterIdentifier: aws.String(id),
		})
		if err != nil {
			return false, "", err
		}

		if len(desc.DBClusters) == 0 {
			return false, "", fmt.Errorf("cluster %s not found", id)
		}

		status := aws.ToString(desc.DBClusters[0].Status)
		done := status == "stopped"
		statusStr := fmt.Sprintf("status=%s", status)

		return done, statusStr, nil
	})
}

// ParseState parses DBClusterState from JSON
func (s *clusterStrategy) ParseState(data json.RawMessage) (ResourceState, error) {
	var state DBClusterState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return state, nil
}

// GetResourceKey returns the key prefix for restore data
func (s *clusterStrategy) GetResourceKey(id string) string {
	return "cluster:" + id
}
