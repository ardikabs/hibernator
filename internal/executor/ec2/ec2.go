/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package ec2 implements the EC2 executor for hibernating EC2 instances.
package ec2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/go-logr/logr"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/awsutil"
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/ardikabs/hibernator/pkg/waiter"
)

const (
	ExecutorType       = "ec2"
	DefaultWaitTimeout = "5m"
)

// Parameters is an alias for the shared EC2 parameter type.
type Parameters = executorparams.EC2Parameters

// Selector is an alias for the shared EC2 selector type.
type Selector = executorparams.EC2Selector

// InstanceState holds state for a single instance.
type InstanceState struct {
	InstanceID string `json:"instanceId"`
	WasRunning bool   `json:"wasRunning"`
}

// Executor implements the EC2 hibernation logic.
type Executor struct {
	ec2Factory      EC2ClientFactory
	awsConfigLoader AWSConfigLoader
}

// EC2ClientFactory is a function type for creating EC2 clients.
type EC2ClientFactory func(cfg aws.Config) EC2Client

// AWSConfigLoader is a function type for loading AWS config.
type AWSConfigLoader func(ctx context.Context, spec executor.Spec) (aws.Config, error)

// New creates a new EC2 executor with real AWS clients.
func New() *Executor {
	return &Executor{
		ec2Factory: func(cfg aws.Config) EC2Client {
			return ec2.NewFromConfig(cfg)
		},
	}
}

// NewWithClients creates a new EC2 executor with injected client factories.
// This is useful for testing with mock clients.
func NewWithClients(ec2Factory EC2ClientFactory, awsConfigLoader AWSConfigLoader) *Executor {
	return &Executor{
		ec2Factory:      ec2Factory,
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
		return fmt.Errorf("AWS connector config required for EC2 executor")
	}

	params, err := e.parseParams(spec.Parameters)
	if err != nil {
		return err
	}

	if len(params.Selector.Tags) == 0 && len(params.Selector.InstanceIDs) == 0 {
		return fmt.Errorf("either tags or instanceIds must be specified in selector")
	}

	return nil
}

// Shutdown stops EC2 instances matching the selector.
func (e *Executor) Shutdown(ctx context.Context, log logr.Logger, spec executor.Spec) (*executor.Result, error) {
	log = log.WithName("ec2").WithValues("target", spec.TargetName, "targetType", spec.TargetType)
	log.Info("executor starting shutdown")

	params, err := e.parseParams(spec.Parameters)
	if err != nil {
		log.Error(err, "failed to parse parameters")
		return nil, fmt.Errorf("parse parameters: %w", err)
	}

	log.Info("parameters parsed",
		"hasTagSelector", len(params.Selector.Tags) > 0,
		"hasInstanceIDs", len(params.Selector.InstanceIDs) > 0,
		"tagCount", len(params.Selector.Tags),
		"instanceIDCount", len(params.Selector.InstanceIDs),
	)

	cfg, err := e.loadAWSConfig(ctx, spec)
	if err != nil {
		log.Error(err, "failed to load AWS config")
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	client := e.ec2Factory(cfg)

	// Find all instances matching the selector (regardless of state).
	log.Info("discovering EC2 instances matching selector")
	instances, err := e.findInstances(ctx, client, params.Selector)
	if err != nil {
		log.Error(err, "failed to find instances")
		return nil, fmt.Errorf("find instances: %w", err)
	}

	log.Info("instances discovered", "totalInstances", len(instances))

	// Determine if we're capturing from live running state
	hasRunningInstances := false
	for _, inst := range instances {
		if inst.State.Name == types.InstanceStateNameRunning {
			hasRunningInstances = true
			break
		}
	}

	var instancesToStop []string
	for _, inst := range instances {
		instanceID := aws.ToString(inst.InstanceId)
		actualState := inst.State.Name
		wasRunning := actualState == types.InstanceStateNameRunning

		// Store state with instanceID as key
		// WasRunning reflects the actual state at time of capture
		state := InstanceState{
			InstanceID: instanceID,
			WasRunning: wasRunning,
		}

		log.Info("instance state captured",
			"instanceId", instanceID,
			"actualState", actualState,
			"wasRunning", wasRunning,
		)

		// Add to stop list if running
		if wasRunning {
			instancesToStop = append(instancesToStop, instanceID)
		}

		// Incremental save: persist this instance's restore data immediately.
		if spec.ReportStateCallback != nil {
			if err := spec.ReportStateCallback(instanceID, state); err != nil {
				log.Error(err, "failed to save restore data incrementally", "instanceId", instanceID)
				// Continue processing - save at end as fallback
			}
		}
	}

	// Stop running instances
	msg := fmt.Sprintf("stopped %d of %d EC2 instance(s)", len(instancesToStop), len(instances))

	if len(instancesToStop) > 0 {
		log.Info("stopping running instances", "count", len(instancesToStop))
		_, err = client.StopInstances(ctx, &ec2.StopInstancesInput{
			InstanceIds: instancesToStop,
		})
		if err != nil {
			log.Error(err, "failed to stop instances")
			return nil, fmt.Errorf("stop instances: %w", err)
		}
		log.Info("instances stopped successfully", "count", len(instancesToStop))

		// Wait for instances to reach stopped state if configured
		if params.AwaitCompletion.Enabled {
			timeout := params.AwaitCompletion.Timeout
			if timeout == "" {
				timeout = DefaultWaitTimeout
			}
			if err := e.waitForInstancesStopped(ctx, log, client, instancesToStop, timeout); err != nil {
				log.Error(err, "timeout waiting for instances to stop")
				msg += fmt.Sprintf("; not all instances confirmed stopped after %s timeout", timeout)
			} else {
				msg += "; all instances confirmed stopped"
			}
		}
	} else {
		log.Info("no running instances to stop, all already at desired state")
	}

	log.Info("shutdown completed",
		"totalInstances", len(instances),
		"stoppedInstances", len(instancesToStop),
		"isLive", hasRunningInstances,
	)

	return &executor.Result{Message: msg}, nil
}

// WakeUp starts previously running EC2 instances.
func (e *Executor) WakeUp(ctx context.Context, log logr.Logger, spec executor.Spec, restore executor.RestoreData) (*executor.Result, error) {
	log = log.WithName("ec2").WithValues("target", spec.TargetName, "targetType", spec.TargetType)
	log.Info("executor starting wakeup")

	if len(restore.Data) == 0 {
		log.Info("no restore data available, wakeup operation is no-op")
		return &executor.Result{Message: "wakeup completed for EC2 (no restore data)"}, nil
	}

	params, err := e.parseParams(spec.Parameters)
	if err != nil {
		log.Error(err, "failed to parse parameters")
		return nil, fmt.Errorf("parse parameters: %w", err)
	}

	log.Info("restore state loaded", "instanceCount", len(restore.Data))

	cfg, err := e.loadAWSConfig(ctx, spec)
	if err != nil {
		log.Error(err, "failed to load AWS config")
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	client := e.ec2Factory(cfg)

	// Build restore lookup for instances that were running before shutdown.
	previouslyRunning := make(map[string]struct{}, len(restore.Data))
	for instanceID, stateBytes := range restore.Data {
		var inst InstanceState
		if err := json.Unmarshal(stateBytes, &inst); err != nil {
			log.Error(err, "failed to unmarshal instance state", "instanceId", instanceID)
			return nil, fmt.Errorf("unmarshal instance state %s: %w", instanceID, err)
		}

		if inst.WasRunning {
			id := inst.InstanceID
			if id == "" {
				id = instanceID
			}

			previouslyRunning[id] = struct{}{}
		}
	}

	// Re-discover all current instances from selector
	instances, err := e.findInstances(ctx, client, params.Selector)
	if err != nil {
		log.Error(err, "failed to find instances eligible for wakeup")
		return nil, fmt.Errorf("find instances: %w", err)
	}

	instancesToStart := make([]string, 0, len(instances))
	for _, inst := range instances {
		instanceID := aws.ToString(inst.InstanceId)
		actualState := inst.State.Name

		// Only start instances that:
		// 1. Were running before shutdown (in restore data with WasRunning=true)
		// 2. Are currently in a startable state (stopped or stopping)
		if _, wasRunning := previouslyRunning[instanceID]; !wasRunning {
			continue
		}

		// Only start if the instance is actually stopped (or stopping)
		canStart := actualState == types.InstanceStateNameStopped || actualState == types.InstanceStateNameStopping
		if !canStart {
			log.Info("skipping instance - not in startable state",
				"instanceId", instanceID,
				"actualState", actualState,
				"wasRunning", true,
			)
			continue
		}

		instancesToStart = append(instancesToStart, instanceID)
		log.Info("instance marked for start",
			"instanceId", instanceID,
			"actualState", actualState,
			"wasRunning", true,
		)
	}

	msg := fmt.Sprintf("started %d EC2 instance(s)", len(instancesToStart))

	if len(instancesToStart) > 0 {
		startedInstances, skippedMissingCount, err := e.startInstancesWithMissingTolerance(ctx, log, client, instancesToStart)
		if err != nil {
			log.Error(err, "failed to start instances")
			return nil, err
		}

		msg = fmt.Sprintf("started %d EC2 instance(s)", len(startedInstances))
		if skippedMissingCount > 0 {
			msg += fmt.Sprintf("; skipped %d missing instance(s)", skippedMissingCount)
		}

		// Wait for instances to reach running state if configured
		if params.AwaitCompletion.Enabled && len(startedInstances) > 0 {
			timeout := params.AwaitCompletion.Timeout
			if timeout == "" {
				timeout = DefaultWaitTimeout
			}
			if err := e.waitForInstancesRunning(ctx, log, client, startedInstances, timeout); err != nil {
				log.Error(err, "timeout waiting for instances to start")
				msg += fmt.Sprintf("; not all instances confirmed running after %s timeout", timeout)
			} else {
				msg += "; all instances confirmed running"
			}
		}
	} else {
		log.Info("no instances to start")
	}

	log.Info("wakeup completed", "instanceCount", len(instancesToStart))

	return &executor.Result{Message: msg}, nil
}

// startInstancesWithMissingTolerance attempts to start all instances in bulk, but if it encounters an InvalidInstanceID.NotFound error, it retries starting each instance individually to tolerate missing instances.
func (e *Executor) startInstancesWithMissingTolerance(ctx context.Context, log logr.Logger, client EC2Client, instanceIDs []string) ([]string, int, error) {
	log.Info("starting instances", "count", len(instanceIDs))
	_, err := client.StartInstances(ctx, &ec2.StartInstancesInput{InstanceIds: instanceIDs})
	if err == nil {
		log.Info("instances started successfully", "count", len(instanceIDs))
		return instanceIDs, 0, nil
	}

	if !isInstanceNotFound(err) {
		return nil, 0, fmt.Errorf("start instances: %w", err)
	}

	log.Info("bulk start encountered missing instance IDs; retrying per instance", "count", len(instanceIDs))

	started := make([]string, 0, len(instanceIDs))
	skippedMissing := 0

	for _, instanceID := range instanceIDs {
		if _, err := client.StartInstances(ctx, &ec2.StartInstancesInput{InstanceIds: []string{instanceID}}); err != nil {
			if isInstanceNotFound(err) {
				skippedMissing++
				log.Info("skipping missing instance", "instanceId", instanceID)
				continue
			}

			return nil, skippedMissing, fmt.Errorf("start instance %s: %w", instanceID, err)
		}

		started = append(started, instanceID)
	}

	log.Info("instances started with missing-instance tolerance",
		"requestedCount", len(instanceIDs),
		"startedCount", len(started),
		"skippedMissingCount", skippedMissing,
	)

	return started, skippedMissing, nil
}

// isInstanceNotFound checks if the error is an InvalidInstanceID.NotFound error,
// which indicates that one or more instance IDs do not exist.
func isInstanceNotFound(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}

	return apiErr.ErrorCode() == "InvalidInstanceID.NotFound"
}

// waitForInstancesStopped waits for all instances to reach stopped state.
func (e *Executor) waitForInstancesStopped(ctx context.Context, log logr.Logger, client EC2Client, instanceIds []string, timeout string) error {
	w, err := waiter.NewWaiter(ctx, log, timeout)
	if err != nil {
		return fmt.Errorf("create waiter: %w", err)
	}

	checkFunc := func() (bool, string, error) {
		resp, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: instanceIds,
		})
		if err != nil {
			return false, "", fmt.Errorf("describe instances: %w", err)
		}

		stopped := 0
		stopping := 0
		other := 0
		for _, reservation := range resp.Reservations {
			for _, instance := range reservation.Instances {
				switch instance.State.Name {
				case types.InstanceStateNameStopped:
					stopped++
				case types.InstanceStateNameStopping:
					stopping++
				default:
					other++
				}
			}
		}

		status := fmt.Sprintf("stopped=%d, stopping=%d, other=%d", stopped, stopping, other)
		done := stopped == len(instanceIds)
		return done, status, nil
	}

	return w.Poll("instances to stop", checkFunc)
}

// waitForInstancesRunning waits for all instances to reach running state.
func (e *Executor) waitForInstancesRunning(ctx context.Context, log logr.Logger, client EC2Client, instanceIds []string, timeout string) error {
	w, err := waiter.NewWaiter(ctx, log, timeout)
	if err != nil {
		return fmt.Errorf("create waiter: %w", err)
	}

	checkFunc := func() (bool, string, error) {
		resp, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: instanceIds,
		})
		if err != nil {
			return false, "", fmt.Errorf("describe instances: %w", err)
		}

		running := 0
		pending := 0
		other := 0
		for _, reservation := range resp.Reservations {
			for _, instance := range reservation.Instances {
				switch instance.State.Name {
				case types.InstanceStateNameRunning:
					running++
				case types.InstanceStateNamePending:
					pending++
				default:
					other++
				}
			}
		}

		status := fmt.Sprintf("running=%d, pending=%d, other=%d", running, pending, other)
		done := running == len(instanceIds)
		return done, status, nil
	}

	return w.Poll("instances to start", checkFunc)
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

func (e *Executor) findInstances(ctx context.Context, client EC2Client, selector Selector) ([]types.Instance, error) {
	input := &ec2.DescribeInstancesInput{}

	// Build filters
	var filters []types.Filter

	// Add tag filters
	tagKeyOnlySelectors := []string{}
	for key, value := range selector.Tags {
		if value == "" {
			tagKeyOnlySelectors = append(tagKeyOnlySelectors, key)
			continue
		}

		filters = append(filters, types.Filter{
			Name:   aws.String(fmt.Sprintf("tag:%s", key)),
			Values: []string{value},
		})
	}

	if len(tagKeyOnlySelectors) > 0 {
		filters = append(filters, types.Filter{
			Name:   aws.String("tag-key"),
			Values: tagKeyOnlySelectors,
		})
	}

	// Add instance ID filter
	if len(selector.InstanceIDs) > 0 {
		input.InstanceIds = selector.InstanceIDs
	}

	// Filter out terminated instances - we only care about active instances
	filters = append(filters, types.Filter{
		Name: aws.String("instance-state-name"),
		Values: []string{
			string(types.InstanceStateNamePending),
			string(types.InstanceStateNameRunning),
			string(types.InstanceStateNameStopping),
			string(types.InstanceStateNameStopped),
		},
	})

	if len(filters) > 0 {
		input.Filters = filters
	}

	result, err := client.DescribeInstances(ctx, input)
	if err != nil {
		return nil, err
	}

	var instances []types.Instance
	for _, reservation := range result.Reservations {
		instances = append(instances, reservation.Instances...)
	}

	// apply exclusions for ASG and Karpenter managed instances
	return awsutil.ApplyExclusions(instances, awsutil.ExcludeByASGManaged, awsutil.ExcludeByKarpenterManaged), nil
}
