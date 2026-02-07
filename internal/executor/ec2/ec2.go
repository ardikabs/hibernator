/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package ec2 implements the EC2 executor for hibernating EC2 instances.
package ec2

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
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
func (e *Executor) Shutdown(ctx context.Context, log logr.Logger, spec executor.Spec) error {
	log.Info("EC2 executor starting shutdown",
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
		"hasInstanceIDs", len(params.Selector.InstanceIDs) > 0,
		"tagCount", len(params.Selector.Tags),
		"instanceIDCount", len(params.Selector.InstanceIDs),
	)

	cfg, err := e.loadAWSConfig(ctx, spec)
	if err != nil {
		log.Error(err, "failed to load AWS config")
		return fmt.Errorf("load AWS config: %w", err)
	}

	client := e.ec2Factory(cfg)

	// Find instances
	log.Info("discovering EC2 instances matching selector")
	instances, err := e.findInstances(ctx, client, params.Selector)
	if err != nil {
		log.Error(err, "failed to find instances")
		return fmt.Errorf("find instances: %w", err)
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
		wasRunning := inst.State.Name == types.InstanceStateNameRunning

		// Store state with instanceID as key
		state := InstanceState{
			InstanceID: instanceID,
			WasRunning: wasRunning,
		}

		log.Info("instance state captured",
			"instanceId", instanceID,
			"state", inst.State.Name,
			"willStop", wasRunning,
		)

		// Add to stop list if running
		if wasRunning {
			instancesToStop = append(instancesToStop, instanceID)
		}

		// Incremental save: persist this instance's restore data immediately
		if spec.SaveRestoreData != nil {
			if err := spec.SaveRestoreData(instanceID, state, wasRunning); err != nil {
				log.Error(err, "failed to save restore data incrementally", "instanceId", instanceID)
				// Continue processing - save at end as fallback
			}
		}
	}

	// Stop running instances
	if len(instancesToStop) > 0 {
		log.Info("stopping running instances", "count", len(instancesToStop))
		_, err = client.StopInstances(ctx, &ec2.StopInstancesInput{
			InstanceIds: instancesToStop,
		})
		if err != nil {
			log.Error(err, "failed to stop instances")
			return fmt.Errorf("stop instances: %w", err)
		}
		log.Info("instances stopped successfully", "count", len(instancesToStop))

		// Wait for instances to reach stopped state if configured
		if params.WaitConfig.Enabled {
			timeout := params.WaitConfig.Timeout
			if timeout == "" {
				timeout = DefaultWaitTimeout
			}
			if err := e.waitForInstancesStopped(ctx, log, client, instancesToStop, timeout); err != nil {
				return fmt.Errorf("wait for instances stopped: %w", err)
			}
		}
	} else {
		log.Info("no running instances to stop, all already at desired state")
	}

	log.Info("EC2 shutdown completed successfully",
		"totalInstances", len(instances),
		"stoppedInstances", len(instancesToStop),
		"isLive", hasRunningInstances,
	)

	return nil
}

// WakeUp starts previously running EC2 instances.
func (e *Executor) WakeUp(ctx context.Context, log logr.Logger, spec executor.Spec, restore executor.RestoreData) error {
	log.Info("EC2 executor starting wakeup",
		"target", spec.TargetName,
		"targetType", spec.TargetType,
	)

	params, err := e.parseParams(spec.Parameters)
	if err != nil {
		log.Error(err, "failed to parse parameters")
		return fmt.Errorf("parse parameters: %w", err)
	}

	log.Info("restore state loaded", "totalInstances", len(restore.Data))

	cfg, err := e.loadAWSConfig(ctx, spec)
	if err != nil {
		log.Error(err, "failed to load AWS config")
		return fmt.Errorf("load AWS config: %w", err)
	}

	client := e.ec2Factory(cfg)

	// Start instances that were previously running
	var instancesToStart []string
	for instanceID, stateBytes := range restore.Data {
		var inst InstanceState
		if err := json.Unmarshal(stateBytes, &inst); err != nil {
			log.Error(err, "failed to unmarshal instance state", "instanceId", instanceID)
			return fmt.Errorf("unmarshal instance state %s: %w", instanceID, err)
		}

		if inst.WasRunning {
			instancesToStart = append(instancesToStart, inst.InstanceID)
			log.Info("instance marked for start",
				"instanceId", inst.InstanceID,
				"wasRunning", inst.WasRunning,
			)
		}
	}

	if len(instancesToStart) > 0 {
		log.Info("starting instances", "count", len(instancesToStart))
		_, err = client.StartInstances(ctx, &ec2.StartInstancesInput{
			InstanceIds: instancesToStart,
		})
		if err != nil {
			log.Error(err, "failed to start instances")
			return fmt.Errorf("start instances: %w", err)
		}
		log.Info("instances started successfully", "count", len(instancesToStart))

		// Wait for instances to reach running state if configured
		if params.WaitConfig.Enabled {
			timeout := params.WaitConfig.Timeout
			if timeout == "" {
				timeout = DefaultWaitTimeout
			}
			if err := e.waitForInstancesRunning(ctx, log, client, instancesToStart, timeout); err != nil {
				return fmt.Errorf("wait for instances running: %w", err)
			}
		}
	} else {
		log.Info("no instances to start")
	}

	log.Info("EC2 wakeup completed successfully")
	return nil
}

// waitForInstancesStopped waits for all instances to reach stopped state.
func (e *Executor) waitForInstancesStopped(ctx context.Context, log logr.Logger, client EC2Client, instanceIDs []string, timeout string) error {
	w, err := waiter.NewWaiter(ctx, log, timeout)
	if err != nil {
		return fmt.Errorf("create waiter: %w", err)
	}

	checkFunc := func() (bool, string, error) {
		resp, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: instanceIDs,
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
		done := stopped == len(instanceIDs)
		return done, status, nil
	}

	return w.Poll("instances to stop", checkFunc)
}

// waitForInstancesRunning waits for all instances to reach running state.
func (e *Executor) waitForInstancesRunning(ctx context.Context, log logr.Logger, client EC2Client, instanceIDs []string, timeout string) error {
	w, err := waiter.NewWaiter(ctx, log, timeout)
	if err != nil {
		return fmt.Errorf("create waiter: %w", err)
	}

	checkFunc := func() (bool, string, error) {
		resp, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: instanceIDs,
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
		done := running == len(instanceIDs)
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

	// Exclude terminated and shutting-down instances
	filters = append(filters, types.Filter{
		Name:   aws.String("instance-state-name"),
		Values: []string{"running"},
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

	return instances, nil
}
