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
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/smithy-go"
	"github.com/go-logr/logr"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/ardikabs/hibernator/pkg/waiter"
)

// instanceStrategy implements ResourceStrategy for DB instances
type instanceStrategy struct{}

// ResourceType returns the type of resource this strategy handles
func (s *instanceStrategy) ResourceType() ResourceType {
	return ResourceTypeInstance
}

// Discover finds DB instances matching the selector
func (s *instanceStrategy) Discover(ctx context.Context, log logr.Logger, client RDSClient, selector executorparams.RDSSelector) ([]string, error) {
	var instanceIDs []string

	// If explicit instance IDs provided, use them directly
	if len(selector.InstanceIds) > 0 {
		return selector.InstanceIds, nil
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
		for _, inst := range result.DBInstances {
			instanceIDs = append(instanceIDs, aws.ToString(inst.DBInstanceIdentifier))
		}
		return instanceIDs, nil
	}

	// Apply tag-based filtering
	for _, inst := range result.DBInstances {
		instanceID := aws.ToString(inst.DBInstanceIdentifier)

		// Skip AWS-managed restore job resources
		if strings.HasPrefix(instanceID, "aws-restore-") {
			log.Info("skipping instance that appears to be an AWS-managed restore job resource", "instanceId", instanceID)
			continue
		}

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
			if matchesTags(instanceTags, selector.Tags) {
				instanceIDs = append(instanceIDs, instanceID)
				log.Info("instance included (tags match)", "instanceId", instanceID, "tags", instanceTags)
			}
		} else if len(selector.ExcludeTags) > 0 {
			// Include instances NOT matching excludeTags
			if !matchesTags(instanceTags, selector.ExcludeTags) {
				instanceIDs = append(instanceIDs, instanceID)
				log.Info("instance included (excludeTags don't match)", "instanceId", instanceID, "tags", instanceTags)
			}
		}
	}

	return instanceIDs, nil
}

// Stop stops a DB instance and returns its state (with embedded outcome)
func (s *instanceStrategy) Stop(ctx context.Context, log logr.Logger, client RDSClient, id string, snapshotBefore bool, params Parameters, callback executor.ReportStateCallback) (ResourceState, error) {
	// Get instance info
	desc, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String(id),
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "DBInstanceNotFound" {
			log.Info("instance not found, skipping ...", "instanceId", id)
			return DBInstanceState{Outcome: operationOutcomeSkippedStale}, nil
		}
		return nil, err
	}

	if len(desc.DBInstances) == 0 {
		return nil, fmt.Errorf("instance %s not found", id)
	}

	instance := desc.DBInstances[0]
	state := DBInstanceState{
		InstanceId:   id,
		InstanceType: aws.ToString(instance.DBInstanceClass),
	}

	status := aws.ToString(instance.DBInstanceStatus)

	switch status {
	case "available":
		state.WasRunning = true

		// Create snapshot if requested
		if snapshotBefore {
			snapshotManager := newSnapshotManager(client)
			snapshotID, err := snapshotManager.createInstanceSnapshot(ctx, log, id)
			if err != nil {
				return nil, err
			}
			state.SnapshotId = snapshotID
		}

		// Stop instance
		log.Info("stopping DB instance", "instanceId", id)
		if _, err = client.StopDBInstance(ctx, &rds.StopDBInstanceInput{
			DBInstanceIdentifier: aws.String(id),
		}); err != nil {
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) && apiErr.ErrorCode() == "DBInstanceNotFound" {
				log.Info("instance not found, skipping ...", "instanceId", id)
				return DBInstanceState{Outcome: operationOutcomeSkippedStale}, nil
			}
			return nil, err
		}
		state.Outcome = operationOutcomeApplied
	case "stopped":
		state.WasRunning = false
		state.Outcome = operationOutcomeApplied
		log.Info("instance is already stopped", "instanceId", id)
	default:
		// If awaitCompletion is enabled, mark as pending to wait for state transition
		if params.AwaitCompletion.Enabled {
			log.Info("instance is in a transitional state, will wait for availability before stopping",
				"instanceId", id, "status", status)
			return DBInstanceState{Outcome: operationOutcomePending}, nil
		}
		log.Info("instance is in a status that cannot be stopped, skipping stop ...",
			"instanceId", id, "status", status)
		return DBInstanceState{Outcome: operationOutcomeSkippedStale}, nil
	}

	// Incremental save: persist this instance's restore data immediately
	if callback != nil {
		key := "instance:" + state.InstanceId
		if err := callback(key, state); err != nil {
			log.Error(err, "failed to save restore data incrementally", "instanceId", id)
		}
	}

	log.Info("instance processed successfully",
		"instanceId", id,
		"wasRunning", state.WasRunning,
		"snapshotCreated", state.SnapshotId != "",
	)

	return state, nil
}

// Start starts a DB instance and returns its state (with embedded outcome)
func (s *instanceStrategy) Start(ctx context.Context, log logr.Logger, client RDSClient, id string, params Parameters) (ResourceState, error) {
	// Check current status
	desc, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String(id),
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "DBInstanceNotFound" {
			log.Info("instance not found, skipping ...", "instanceId", id)
			return DBInstanceState{Outcome: operationOutcomeSkippedStale}, nil
		}
		return nil, err
	}

	if len(desc.DBInstances) == 0 {
		return nil, fmt.Errorf("instance %s not found", id)
	}

	status := aws.ToString(desc.DBInstances[0].DBInstanceStatus)
	if status == "available" {
		log.Info("instance is already running", "instanceId", id)
		return DBInstanceState{Outcome: operationOutcomeSkippedStale}, nil
	}

	if status != "stopped" {
		// If awaitCompletion is enabled, mark as pending to wait for state transition
		if params.AwaitCompletion.Enabled {
			log.Info("instance is in a transitional state, will wait for stopped state before starting",
				"instanceId", id, "status", status)
			return DBInstanceState{Outcome: operationOutcomePending}, nil
		}

		log.Info("instance is in a status that cannot be started, skipping start ...",
			"instanceId", id, "status", status)
		return DBInstanceState{Outcome: operationOutcomeSkippedStale}, nil
	}

	_, err = client.StartDBInstance(ctx, &rds.StartDBInstanceInput{
		DBInstanceIdentifier: aws.String(id),
	})

	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "DBInstanceNotFound" {
			log.Info("instance not found, skipping ...", "instanceId", id)
			return DBInstanceState{Outcome: operationOutcomeSkippedStale}, nil
		}
		return nil, err
	}

	return DBInstanceState{Outcome: operationOutcomeApplied}, nil
}

// WaitForAvailable waits for a DB instance to reach available state
func (s *instanceStrategy) WaitForAvailable(ctx context.Context, log logr.Logger, client RDSClient, id string, timeout string) error {
	log.Info("waiting for RDS instance to be available",
		"instanceId", id,
		"timeout", timeout,
	)

	w, err := waiter.NewWaiter(ctx, log, timeout)
	if err != nil {
		return err
	}

	description := fmt.Sprintf("DB instance %s to be available", id)
	return w.Poll(description, func() (bool, string, error) {
		desc, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
			DBInstanceIdentifier: aws.String(id),
		})
		if err != nil {
			return false, "", err
		}

		if len(desc.DBInstances) == 0 {
			return false, "", fmt.Errorf("instance %s not found", id)
		}

		status := aws.ToString(desc.DBInstances[0].DBInstanceStatus)
		done := status == "available"
		statusStr := fmt.Sprintf("status=%s", status)

		return done, statusStr, nil
	})
}

// WaitForStopped waits for a DB instance to reach stopped state
func (s *instanceStrategy) WaitForStopped(ctx context.Context, log logr.Logger, client RDSClient, id string, timeout string) error {
	if timeout == "" {
		timeout = DefaultWaitTimeout
	}

	w, err := waiter.NewWaiter(ctx, log, timeout)
	if err != nil {
		return err
	}

	description := fmt.Sprintf("DB instance %s to stop", id)
	return w.Poll(description, func() (bool, string, error) {
		desc, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
			DBInstanceIdentifier: aws.String(id),
		})
		if err != nil {
			return false, "", err
		}

		if len(desc.DBInstances) == 0 {
			return false, "", fmt.Errorf("instance %s not found", id)
		}

		status := aws.ToString(desc.DBInstances[0].DBInstanceStatus)
		done := status == "stopped"
		statusStr := fmt.Sprintf("status=%s", status)

		return done, statusStr, nil
	})
}

// ParseState parses DBInstanceState from JSON
func (s *instanceStrategy) ParseState(data json.RawMessage) (ResourceState, error) {
	var state DBInstanceState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return state, nil
}

// GetResourceKey returns the key prefix for restore data
func (s *instanceStrategy) GetResourceKey(id string) string {
	return "instance:" + id
}

// matchesTags checks if resource tags match the selector tags
func matchesTags(resourceTags map[string]string, selectorTags map[string]string) bool {
	for key, value := range selectorTags {
		resourceValue, hasKey := resourceTags[key]
		if !hasKey {
			return false
		}
		if value != "" && resourceValue != value {
			return false
		}
	}
	return true
}
