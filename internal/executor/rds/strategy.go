/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package rds

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/go-logr/logr"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/executorparams"
)

// ResourceType represents the type of RDS resource
type ResourceType string

const (
	ResourceTypeInstance ResourceType = "instance"
	ResourceTypeCluster  ResourceType = "cluster"
)

// ResourceState holds the common interface for resource states.
// Note: GetOutcome() should only be called on states returned from Stop() or Start()
// operations, NOT on states returned from ParseState() (which will have empty/unknown outcome).
type ResourceState interface {
	// WasResourceRunning returns true if the resource was running before the operation.
	// This is used during WakeUp to determine if the resource should be started.
	WasResourceRunning() bool

	// GetOutcome returns the result of the Stop() or Start() operation.
	// Returns operationOutcomeUnknown if called on a parsed state (from ParseState).
	GetOutcome() operationOutcome
}

// ResourceStrategy defines the interface for RDS resource operations
type ResourceStrategy interface {
	// ResourceType returns the type of resource this strategy handles
	ResourceType() ResourceType

	// Discover finds resources matching the selector
	Discover(ctx context.Context, log logr.Logger, client RDSClient, selector executorparams.RDSSelector) ([]string, error)

	// Stop stops a resource and returns its state (with embedded outcome)
	Stop(ctx context.Context, log logr.Logger, client RDSClient, id string, snapshotBefore bool, params Parameters, callback executor.ReportStateCallback) (ResourceState, error)

	// Start starts a resource and returns its state (with embedded outcome)
	Start(ctx context.Context, log logr.Logger, client RDSClient, id string, params Parameters) (ResourceState, error)

	// WaitForAvailable waits for a resource to reach available state
	WaitForAvailable(ctx context.Context, log logr.Logger, client RDSClient, id string, timeout string) error

	// WaitForStopped waits for a resource to reach stopped state
	WaitForStopped(ctx context.Context, log logr.Logger, client RDSClient, id string, timeout string) error

	// ParseState parses state from JSON
	ParseState(data json.RawMessage) (ResourceState, error)

	// GetResourceKey returns the key prefix for restore data
	GetResourceKey(id string) string
}

// pendingResource tracks resources that need to wait for state transition before operation
type pendingResource struct {
	id             string
	snapshotBefore bool // only for stop operation
}

// strategyRegistry holds all resource strategies
type strategyRegistry struct {
	strategies map[ResourceType]ResourceStrategy
}

// newStrategyRegistry creates a new registry with all strategies registered
func newStrategyRegistry() *strategyRegistry {
	return &strategyRegistry{
		strategies: map[ResourceType]ResourceStrategy{
			ResourceTypeInstance: &instanceStrategy{},
			ResourceTypeCluster:  &clusterStrategy{},
		},
	}
}

// Get returns the strategy for the given resource type
func (r *strategyRegistry) Get(resourceType ResourceType) (ResourceStrategy, bool) {
	s, ok := r.strategies[resourceType]
	return s, ok
}

// resourceTracker tracks resources for completion and pending processing
type resourceTracker struct {
	waitingList []string
	pendingList []pendingResource
}

// newResourceTracker creates a new resource tracker
func newResourceTracker() *resourceTracker {
	return &resourceTracker{
		waitingList: make([]string, 0),
		pendingList: make([]pendingResource, 0),
	}
}

// AddToWaitingList adds a resource to the waiting list
func (t *resourceTracker) AddToWaitingList(id string) {
	t.waitingList = append(t.waitingList, id)
}

// AddToPendingList adds a resource to the pending list
func (t *resourceTracker) AddToPendingList(id string, snapshotBefore bool) {
	t.pendingList = append(t.pendingList, pendingResource{
		id:             id,
		snapshotBefore: snapshotBefore,
	})
}

// Reset clears all tracked resources
func (t *resourceTracker) Reset() {
	t.waitingList = t.waitingList[:0]
	t.pendingList = t.pendingList[:0]
}

// getAllWaitingIDs returns all IDs from the waiting list
func (t *resourceTracker) getAllWaitingIDs() []string {
	result := make([]string, len(t.waitingList))
	copy(result, t.waitingList)
	return result
}

// getAllPending returns all pending resources
func (t *resourceTracker) getAllPending() []pendingResource {
	result := make([]pendingResource, len(t.pendingList))
	copy(result, t.pendingList)
	return result
}

// snapshotManager handles snapshot creation and waiting
type snapshotManager struct {
	client RDSClient
}

// newSnapshotManager creates a new snapshot manager
func newSnapshotManager(client RDSClient) *snapshotManager {
	return &snapshotManager{client: client}
}

// createInstanceSnapshot creates a snapshot for a DB instance and waits for it to be available
func (m *snapshotManager) createInstanceSnapshot(ctx context.Context, log logr.Logger, instanceID string) (string, error) {
	snapshotID := fmt.Sprintf("%s-hibernate-%d", instanceID, time.Now().Unix())
	log.Info("creating DB snapshot before stop", "instanceId", instanceID, "snapshotId", snapshotID)

	_, err := m.client.CreateDBSnapshot(ctx, &rds.CreateDBSnapshotInput{
		DBInstanceIdentifier: aws.String(instanceID),
		DBSnapshotIdentifier: aws.String(snapshotID),
	})
	if err != nil {
		return "", fmt.Errorf("create snapshot: %w", err)
	}

	// Wait for snapshot to be available
	waiter := rds.NewDBSnapshotAvailableWaiter(m.client)
	log.Info("waiting for snapshot to be available", "snapshotId", snapshotID)
	if err := waiter.Wait(ctx, &rds.DescribeDBSnapshotsInput{
		DBSnapshotIdentifier: aws.String(snapshotID),
	}, 30*time.Minute); err != nil {
		return "", fmt.Errorf("wait for snapshot: %w", err)
	}
	log.Info("snapshot available", "snapshotId", snapshotID)

	return snapshotID, nil
}

// createClusterSnapshot creates a snapshot for a DB cluster and waits for it to be available
func (m *snapshotManager) createClusterSnapshot(ctx context.Context, log logr.Logger, clusterID string) (string, error) {
	snapshotID := fmt.Sprintf("%s-hibernate-%d", clusterID, time.Now().Unix())
	log.Info("creating DB cluster snapshot before stop", "clusterId", clusterID, "snapshotId", snapshotID)

	_, err := m.client.CreateDBClusterSnapshot(ctx, &rds.CreateDBClusterSnapshotInput{
		DBClusterIdentifier:         aws.String(clusterID),
		DBClusterSnapshotIdentifier: aws.String(snapshotID),
	})
	if err != nil {
		return "", fmt.Errorf("create cluster snapshot: %w", err)
	}

	// Wait for snapshot to be available
	waiter := rds.NewDBClusterSnapshotAvailableWaiter(m.client)
	log.Info("waiting for cluster snapshot to be available", "snapshotId", snapshotID)
	if err := waiter.Wait(ctx, &rds.DescribeDBClusterSnapshotsInput{
		DBClusterSnapshotIdentifier: aws.String(snapshotID),
	}, 30*time.Minute); err != nil {
		return "", fmt.Errorf("wait for cluster snapshot: %w", err)
	}
	log.Info("cluster snapshot available", "snapshotId", snapshotID)

	return snapshotID, nil
}
