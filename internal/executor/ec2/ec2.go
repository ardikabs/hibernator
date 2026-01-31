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

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/awsutil"
	"github.com/ardikabs/hibernator/pkg/executorparams"
)

const ExecutorType = "ec2"

// Parameters is an alias for the shared EC2 parameter type.
type Parameters = executorparams.EC2Parameters

// Selector is an alias for the shared EC2 selector type.
type Selector = executorparams.EC2Selector

// RestoreState holds EC2 restore data.
type RestoreState struct {
	Instances []InstanceState `json:"instances"`
}

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
func (e *Executor) Shutdown(ctx context.Context, spec executor.Spec) (executor.RestoreData, error) {
	params, err := e.parseParams(spec.Parameters)
	if err != nil {
		return executor.RestoreData{}, fmt.Errorf("parse parameters: %w", err)
	}

	cfg, err := e.loadAWSConfig(ctx, spec)
	if err != nil {
		return executor.RestoreData{}, fmt.Errorf("load AWS config: %w", err)
	}

	client := e.ec2Factory(cfg)

	// Find instances
	instances, err := e.findInstances(ctx, client, params.Selector)
	if err != nil {
		return executor.RestoreData{}, fmt.Errorf("find instances: %w", err)
	}

	restoreState := RestoreState{
		Instances: make([]InstanceState, 0, len(instances)),
	}

	var instancesToStop []string
	for _, inst := range instances {
		instanceID := aws.ToString(inst.InstanceId)
		wasRunning := inst.State.Name == types.InstanceStateNameRunning

		restoreState.Instances = append(restoreState.Instances, InstanceState{
			InstanceID: instanceID,
			WasRunning: wasRunning,
		})

		if wasRunning {
			instancesToStop = append(instancesToStop, instanceID)
		}
	}

	// Stop running instances
	if len(instancesToStop) > 0 {
		_, err = client.StopInstances(ctx, &ec2.StopInstancesInput{
			InstanceIds: instancesToStop,
		})
		if err != nil {
			return executor.RestoreData{}, fmt.Errorf("stop instances: %w", err)
		}
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

// WakeUp starts previously running EC2 instances.
func (e *Executor) WakeUp(ctx context.Context, spec executor.Spec, restore executor.RestoreData) error {
	var restoreState RestoreState
	if err := json.Unmarshal(restore.Data, &restoreState); err != nil {
		return fmt.Errorf("unmarshal restore state: %w", err)
	}

	cfg, err := e.loadAWSConfig(ctx, spec)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	client := e.ec2Factory(cfg)

	// Start instances that were previously running
	var instancesToStart []string
	for _, inst := range restoreState.Instances {
		if inst.WasRunning {
			instancesToStart = append(instancesToStart, inst.InstanceID)
		}
	}

	if len(instancesToStart) > 0 {
		_, err = client.StartInstances(ctx, &ec2.StartInstancesInput{
			InstanceIds: instancesToStart,
		})
		if err != nil {
			return fmt.Errorf("start instances: %w", err)
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
	for key, value := range selector.Tags {
		filters = append(filters, types.Filter{
			Name:   aws.String(fmt.Sprintf("tag:%s", key)),
			Values: []string{value},
		})
	}

	// Add instance ID filter
	if len(selector.InstanceIDs) > 0 {
		input.InstanceIds = selector.InstanceIDs
	}

	// Exclude terminated and shutting-down instances
	filters = append(filters, types.Filter{
		Name:   aws.String("instance-state-name"),
		Values: []string{"running", "stopped", "pending", "stopping"},
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

func init() {
	executor.Register(New())
}
