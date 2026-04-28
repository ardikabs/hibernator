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
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/go-logr/logr"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/awsutil"
	"github.com/ardikabs/hibernator/pkg/executorparams"
)

const (
	ExecutorType       = "rds"
	DefaultWaitTimeout = "15m"
)

// Parameters is an alias for the shared RDS parameter type.
type Parameters = executorparams.RDSParameters

// DBInstanceState holds state for a single DB instance.
type DBInstanceState struct {
	InstanceId   string           `json:"instanceId"`
	WasRunning   bool             `json:"wasRunning"` // true if running when hibernator saw it (restore on wakeup), false if already stopped
	SnapshotId   string           `json:"snapshotId,omitempty"`
	InstanceType string           `json:"instanceType,omitempty"`
	Outcome      operationOutcome `json:"-"` // Result of the operation (not persisted)
}

// WasResourceRunning returns whether the instance was running
func (s DBInstanceState) WasResourceRunning() bool { return s.WasRunning }

// GetOutcome returns the operation outcome
func (s DBInstanceState) GetOutcome() operationOutcome { return s.Outcome }

// DBClusterState holds state for a single DB cluster.
type DBClusterState struct {
	ClusterId  string           `json:"clusterId"`
	WasRunning bool             `json:"wasRunning"` // true if running when hibernator saw it (restore on wakeup), false if already stopped
	SnapshotId string           `json:"snapshotId,omitempty"`
	Outcome    operationOutcome `json:"-"` // Result of the operation (not persisted)
}

// WasResourceRunning returns whether the cluster was running
func (s DBClusterState) WasResourceRunning() bool { return s.WasRunning }

// GetOutcome returns the operation outcome
func (s DBClusterState) GetOutcome() operationOutcome { return s.Outcome }

type operationOutcome string

const (
	operationOutcomeUnknown      operationOutcome = ""           // Zero value, indicates uninitialized or parsed state
	operationOutcomeApplied      operationOutcome = "applied"    // Operation was successfully applied
	operationOutcomeSkippedStale operationOutcome = "skipped"    // Resource was in stale state, operation skipped
	operationOutcomePending      operationOutcome = "pending"    // Resource needs async processing
)

type operationStats struct {
	processed    int
	applied      int
	skippedStale int
	skippedKey   int
	pending      int
}

func formatShutdownMessage(stats *operationStats) string {
	msg := fmt.Sprintf("stopped %d RDS resource(s)", stats.applied)
	msg = appendCountSegment(msg, "skipped", stats.skippedStale, "stale resource")
	msg = appendCountSegment(msg, "pending", stats.pending, "resource awaiting state transition")
	return msg
}

func formatWakeUpMessage(stats *operationStats) string {
	msg := fmt.Sprintf("started %d RDS resource(s)", stats.applied)
	msg = appendCountSegment(msg, "skipped", stats.skippedStale, "stale resource")
	msg = appendCountSegment(msg, "skipped", stats.skippedKey, "unrecognized restore key")
	msg = appendCountSegment(msg, "pending", stats.pending, "resource awaiting state transition")
	return msg
}

func appendCountSegment(msg, action string, count int, noun string) string {
	if count <= 0 {
		return msg
	}
	return fmt.Sprintf("%s, %s %d %s(s)", msg, action, count, noun)
}

// Executor implements the RDS hibernation logic using strategy pattern.
type Executor struct {
	rdsFactory      RDSClientFactory
	stsFactory      STSClientFactory
	awsConfigLoader AWSConfigLoader
	registry        *strategyRegistry
	trackers        map[ResourceType]*resourceTracker
	completionWg    sync.WaitGroup
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
		registry: newStrategyRegistry(),
		trackers: map[ResourceType]*resourceTracker{
			ResourceTypeInstance: newResourceTracker(),
			ResourceTypeCluster:  newResourceTracker(),
		},
	}
}

// NewWithClients creates a new RDS executor with injected client factories.
func NewWithClients(rdsFactory RDSClientFactory, stsFactory STSClientFactory, awsConfigLoader AWSConfigLoader) *Executor {
	return &Executor{
		rdsFactory:      rdsFactory,
		stsFactory:      stsFactory,
		awsConfigLoader: awsConfigLoader,
		registry:        newStrategyRegistry(),
		trackers: map[ResourceType]*resourceTracker{
			ResourceTypeInstance: newResourceTracker(),
			ResourceTypeCluster:  newResourceTracker(),
		},
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
func (e *Executor) Shutdown(ctx context.Context, log logr.Logger, spec executor.Spec) (*executor.Result, error) {
	log = log.WithName("rds").WithValues("target", spec.TargetName, "targetType", spec.TargetType)
	log.Info("executor starting shutdown")

	// Reset trackers
	for _, tracker := range e.trackers {
		tracker.Reset()
	}

	params, err := e.parseParams(spec.Parameters)
	if err != nil {
		return nil, fmt.Errorf("parse parameters: %w", err)
	}

	cfg, err := e.loadAWSConfig(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	client := e.rdsFactory(cfg)
	stats := new(operationStats)

	// Determine which resource types to discover
	discoverInstances, discoverClusters := e.determineResourceTypes(params)

	// Process instances
	if discoverInstances {
		if err := e.processResources(ctx, log, client, params, spec.ReportStateCallback, ResourceTypeInstance, stats); err != nil {
			return nil, err
		}
	}

	// Process clusters
	if discoverClusters {
		if err := e.processResources(ctx, log, client, params, spec.ReportStateCallback, ResourceTypeCluster, stats); err != nil {
			return nil, err
		}
	}

	// Handle await completion
	msg := formatShutdownMessage(stats)
	if params.AwaitCompletion.Enabled {
		msg = e.handleShutdownAwaitCompletion(ctx, log, client, params, msg, stats)
	}

	log.Info("shutdown completed",
		"processed", stats.processed,
		"stopped", stats.applied,
		"skippedStale", stats.skippedStale,
		"pending", stats.pending,
	)

	return &executor.Result{Message: msg}, nil
}

// WakeUp starts RDS instances/clusters.
func (e *Executor) WakeUp(ctx context.Context, log logr.Logger, spec executor.Spec, restore executor.RestoreData) (*executor.Result, error) {
	log = log.WithName("rds").WithValues("target", spec.TargetName, "targetType", spec.TargetType)
	log.Info("executor starting wakeup")

	// Reset trackers
	for _, tracker := range e.trackers {
		tracker.Reset()
	}

	if len(restore.Data) == 0 {
		return &executor.Result{Message: "wakeup completed for RDS (no restore data)"}, nil
	}

	params, err := e.parseParams(spec.Parameters)
	if err != nil {
		return nil, fmt.Errorf("parse parameters: %w", err)
	}

	cfg, err := e.loadAWSConfig(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	client := e.rdsFactory(cfg)
	stats := &operationStats{processed: len(restore.Data)}

	// Process each resource in restore data
	for key, stateBytes := range restore.Data {
		if err := e.restoreResource(ctx, log, client, params, key, stateBytes, stats); err != nil {
			return nil, err
		}
	}

	// Handle await completion
	msg := formatWakeUpMessage(stats)
	if params.AwaitCompletion.Enabled {
		msg = e.handleWakeupAwaitCompletion(ctx, log, client, params, msg, stats)
	}

	log.Info("wakeup completed",
		"processed", stats.processed,
		"started", stats.applied,
		"skippedStale", stats.skippedStale,
		"skippedUnknownKey", stats.skippedKey,
		"pending", stats.pending,
	)

	return &executor.Result{Message: msg}, nil
}

// determineResourceTypes determines which resource types to discover based on params
func (e *Executor) determineResourceTypes(params Parameters) (instances, clusters bool) {
	// For intent-based selection (explicit IDs), resource types are implicit
	if len(params.Selector.InstanceIds) > 0 || len(params.Selector.ClusterIds) > 0 {
		return len(params.Selector.InstanceIds) > 0, len(params.Selector.ClusterIds) > 0
	}
	// For dynamic discovery, use explicit flags
	return params.Selector.DiscoverInstances, params.Selector.DiscoverClusters
}

// processResources discovers and stops resources of the given type
func (e *Executor) processResources(ctx context.Context, log logr.Logger, client RDSClient, params Parameters, callback executor.ReportStateCallback, resourceType ResourceType, stats *operationStats) error {
	strategy, ok := e.registry.Get(resourceType)
	if !ok {
		return fmt.Errorf("unknown resource type: %s", resourceType)
	}

	// Discover resources
	log.Info("discovering resources", "resourceType", resourceType)
	ids, err := strategy.Discover(ctx, log, client, params.Selector)
	if err != nil {
		return fmt.Errorf("discover %s: %w", resourceType, err)
	}
	log.Info("resources discovered", "resourceType", resourceType, "count", len(ids))
	stats.processed += len(ids)

	// Process each resource
	tracker := e.trackers[resourceType]
	for _, id := range ids {
		log.Info("processing resource", "resourceType", resourceType, "id", id)

		// Execute stop operation and get the result state
		resultState, err := strategy.Stop(ctx, log, client, id, params.SnapshotBeforeStop, params, callback)
		if err != nil {
			log.Error(err, "failed to stop resource", "resourceType", resourceType, "id", id)
			return fmt.Errorf("stop %s %s: %w", resourceType, id, err)
		}

		switch resultState.GetOutcome() {
		case operationOutcomeApplied:
			stats.applied++
			tracker.AddToWaitingList(id)
		case operationOutcomeSkippedStale:
			stats.skippedStale++
		case operationOutcomePending:
			stats.pending++
			tracker.AddToPendingList(id, params.SnapshotBeforeStop)
		default:
			// This should not happen - log warning for debugging
			log.Error(nil, "unexpected operation outcome",
				"outcome", resultState.GetOutcome(),
				"resourceType", resourceType,
				"id", id)
		}
	}

	return nil
}

// restoreResource restores a single resource from restore data
func (e *Executor) restoreResource(ctx context.Context, log logr.Logger, client RDSClient, params Parameters, key string, stateBytes json.RawMessage, stats *operationStats) error {
	var resourceType ResourceType
	var id string

	if strings.HasPrefix(key, "instance:") {
		resourceType = ResourceTypeInstance
		id = strings.TrimPrefix(key, "instance:")
	} else if strings.HasPrefix(key, "cluster:") {
		resourceType = ResourceTypeCluster
		id = strings.TrimPrefix(key, "cluster:")
	} else {
		stats.skippedKey++
		log.Info("unknown resource type in restore data, skipping", "key", key)
		return nil
	}

	strategy, ok := e.registry.Get(resourceType)
	if !ok {
		return fmt.Errorf("unknown resource type: %s", resourceType)
	}

	// Parse the persisted state from restore data
	persistedState, err := strategy.ParseState(stateBytes)
	if err != nil {
		return fmt.Errorf("unmarshal %s state %s: %w", resourceType, id, err)
	}

	// Check if the resource was running before hibernation
	if !persistedState.WasResourceRunning() {
		stats.skippedStale++
		log.Info("resource was already stopped before hibernation, skipping start",
			"resourceType", resourceType, "id", id)
		return nil
	}

	log.Info("starting resource", "resourceType", resourceType, "id", id)
	// Execute start operation and get the result state
	resultState, err := strategy.Start(ctx, log, client, id, params)
	if err != nil {
		return fmt.Errorf("start %s %s: %w", resourceType, id, err)
	}

	tracker := e.trackers[resourceType]
	switch resultState.GetOutcome() {
	case operationOutcomeApplied:
		stats.applied++
		tracker.AddToWaitingList(id)
		log.Info("resource started successfully", "resourceType", resourceType, "id", id)
	case operationOutcomeSkippedStale:
		stats.skippedStale++
	case operationOutcomePending:
		stats.pending++
		tracker.AddToPendingList(id, false)
	default:
		// This should not happen - log warning for debugging
		log.Error(nil, "unexpected operation outcome",
			"outcome", resultState.GetOutcome(),
			"resourceType", resourceType,
			"id", id)
	}

	return nil
}

// handleShutdownAwaitCompletion handles the await completion logic for shutdown
// All resources (pending and waiting) share the same timeout window concurrently.
// For pending resources: wait for available → stop → wait for stopped (all in one goroutine)
// For waiting resources: just wait for stopped
func (e *Executor) handleShutdownAwaitCompletion(ctx context.Context, log logr.Logger, client RDSClient, params Parameters, msg string, stats *operationStats) string {
	timeout := params.AwaitCompletion.Timeout
	if timeout == "" {
		timeout = DefaultWaitTimeout
	}

	var timedOut atomic.Int32
	var pendingFailed atomic.Int32
	var pendingApplied atomic.Int32

	// Start all operations concurrently - both pending and waiting share the same timeout window
	for resourceType, tracker := range e.trackers {
		strategy, _ := e.registry.Get(resourceType)

		// Process pending resources: complete full lifecycle in one goroutine
		for _, pending := range tracker.getAllPending() {
			e.completionWg.Add(1)
			go func(p pendingResource, s ResourceStrategy, rt ResourceType) {
				defer e.completionWg.Done()

				// Wait for resource to become available
				if err := s.WaitForAvailable(ctx, log, client, p.id, timeout); err != nil {
					pendingFailed.Add(1)
					log.Error(err, "failed to wait for resource to become available", "resourceType", rt, "id", p.id)
					return
				}

				// Stop the resource
				stopState, err := s.Stop(ctx, log, client, p.id, p.snapshotBefore, params, nil)
				if err != nil {
					pendingFailed.Add(1)
					log.Error(err, "failed to stop pending resource", "resourceType", rt, "id", p.id)
					return
				}

				if stopState.GetOutcome() == operationOutcomeApplied {
					pendingApplied.Add(1)
					// Continue to wait for the resource to reach stopped state
					if err := s.WaitForStopped(ctx, log, client, p.id, timeout); err != nil {
						timedOut.Add(1)
						log.Error(err, "failed to wait for pending resource stopped", "resourceType", rt, "id", p.id)
					}
				}
			}(pending, strategy, resourceType)
		}

		// Process waiting resources: just wait for stopped
		for _, id := range tracker.getAllWaitingIDs() {
			e.completionWg.Add(1)
			go func(resourceID string, s ResourceStrategy, rt ResourceType) {
				defer e.completionWg.Done()
				if err := s.WaitForStopped(ctx, log, client, resourceID, timeout); err != nil {
					timedOut.Add(1)
					log.Error(err, "failed to wait for resource stopped", "resourceType", rt, "id", resourceID)
				}
			}(id, strategy, resourceType)
		}
	}

	// Wait for ALL operations to complete (both pending and waiting)
	e.completionWg.Wait()

	totalOperations := 0
	for _, tracker := range e.trackers {
		totalOperations += len(tracker.getAllWaitingIDs()) + len(tracker.getAllPending())
	}

	if failed := int(timedOut.Load()); failed > 0 {
		msg += fmt.Sprintf("; %d of %d resource(s) not yet stopped after %s timeout", failed, totalOperations, timeout)
	} else {
		msg += "; all resources confirmed stopped"
	}

	// Update stats for pending resources that were processed
	if pendingCount := int(pendingApplied.Load()); pendingCount > 0 {
		stats.applied += pendingCount
		stats.pending -= pendingCount
	}

	return msg
}

// handleWakeupAwaitCompletion handles the await completion logic for wakeup
// All resources (pending and waiting) share the same timeout window concurrently.
// For pending resources: wait for stopped → start → wait for available (all in one goroutine)
// For waiting resources: just wait for available
func (e *Executor) handleWakeupAwaitCompletion(ctx context.Context, log logr.Logger, client RDSClient, params Parameters, msg string, stats *operationStats) string {
	timeout := params.AwaitCompletion.Timeout
	if timeout == "" {
		timeout = DefaultWaitTimeout
	}

	var timedOut atomic.Int32
	var pendingFailed atomic.Int32
	var pendingApplied atomic.Int32

	// Start all operations concurrently - both pending and waiting share the same timeout window
	for resourceType, tracker := range e.trackers {
		strategy, _ := e.registry.Get(resourceType)

		// Process pending resources: complete full lifecycle in one goroutine
		for _, pending := range tracker.getAllPending() {
			e.completionWg.Add(1)
			go func(p pendingResource, s ResourceStrategy, rt ResourceType) {
				defer e.completionWg.Done()

				// Wait for resource to become stopped
				if err := s.WaitForStopped(ctx, log, client, p.id, timeout); err != nil {
					pendingFailed.Add(1)
					log.Error(err, "failed to wait for resource to become stopped", "resourceType", rt, "id", p.id)
					return
				}

				// Start the resource
				startState, err := s.Start(ctx, log, client, p.id, params)
				if err != nil {
					pendingFailed.Add(1)
					log.Error(err, "failed to start pending resource", "resourceType", rt, "id", p.id)
					return
				}

				if startState.GetOutcome() == operationOutcomeApplied {
					pendingApplied.Add(1)
					// Continue to wait for the resource to reach available state
					if err := s.WaitForAvailable(ctx, log, client, p.id, timeout); err != nil {
						timedOut.Add(1)
						log.Error(err, "failed to wait for pending resource available", "resourceType", rt, "id", p.id)
					}
				}
			}(pending, strategy, resourceType)
		}

		// Process waiting resources: just wait for available
		for _, id := range tracker.getAllWaitingIDs() {
			e.completionWg.Add(1)
			go func(resourceID string, s ResourceStrategy, rt ResourceType) {
				defer e.completionWg.Done()
				if err := s.WaitForAvailable(ctx, log, client, resourceID, timeout); err != nil {
					timedOut.Add(1)
					log.Error(err, "failed to wait for resource available", "resourceType", rt, "id", resourceID)
				}
			}(id, strategy, resourceType)
		}
	}

	// Wait for ALL operations to complete (both pending and waiting)
	e.completionWg.Wait()

	totalOperations := 0
	for _, tracker := range e.trackers {
		totalOperations += len(tracker.getAllWaitingIDs()) + len(tracker.getAllPending())
	}

	if failed := int(timedOut.Load()); failed > 0 {
		msg += fmt.Sprintf("; %d of %d resource(s) not yet available after %s timeout", failed, totalOperations, timeout)
	} else {
		msg += "; all resources confirmed available"
	}

	// Update stats for pending resources that were processed
	if pendingCount := int(pendingApplied.Load()); pendingCount > 0 {
		stats.applied += pendingCount
		stats.pending -= pendingCount
	}

	return msg
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
