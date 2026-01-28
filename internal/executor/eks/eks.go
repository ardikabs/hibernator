/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package eks implements the EKS executor for hibernating EKS clusters.
package eks

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/eks"

	"github.com/ardikabs/hibernator/internal/executor"
)

const ExecutorType = "eks"

// Parameters for the EKS executor.
type Parameters struct {
	ComputePolicy ComputePolicy `json:"computePolicy,omitempty"`
	Karpenter     *Karpenter    `json:"karpenter,omitempty"`
	ManagedNGs    *ManagedNGs   `json:"managedNodeGroups,omitempty"`
}

// ComputePolicy defines which compute resources to manage.
type ComputePolicy struct {
	Mode  string   `json:"mode,omitempty"`  // Both, Karpenter, ManagedNodeGroups
	Order []string `json:"order,omitempty"` // Order of operations
}

// Karpenter configuration.
type Karpenter struct {
	TargetNodePools []string `json:"targetNodePools,omitempty"`
	Strategy        string   `json:"strategy,omitempty"` // DeleteNodes
}

// ManagedNGs configuration.
type ManagedNGs struct {
	Strategy string `json:"strategy,omitempty"` // ScaleToZero
}

// RestoreState holds EKS restore data.
type RestoreState struct {
	ManagedNodeGroups map[string]NodeGroupState `json:"managedNodeGroups,omitempty"`
	Karpenter         *KarpenterState           `json:"karpenter,omitempty"`
}

// NodeGroupState holds state for a managed node group.
type NodeGroupState struct {
	DesiredSize int32 `json:"desired"`
	MinSize     int32 `json:"min"`
	MaxSize     int32 `json:"max"`
}

// KarpenterState holds Karpenter-related state.
type KarpenterState struct {
	NodePools []string `json:"nodePools,omitempty"`
}

// Executor implements the EKS hibernation logic.
type Executor struct{}

// New creates a new EKS executor.
func New() *Executor {
	return &Executor{}
}

// Type returns the executor type.
func (e *Executor) Type() string {
	return ExecutorType
}

// Validate validates the executor spec.
func (e *Executor) Validate(spec executor.Spec) error {
	if spec.ConnectorConfig.K8S == nil {
		return fmt.Errorf("K8S connector config required for EKS executor")
	}
	return nil
}

// Shutdown performs EKS cluster hibernation.
func (e *Executor) Shutdown(ctx context.Context, spec executor.Spec) (executor.RestoreData, error) {
	params, err := e.parseParams(spec.Parameters)
	if err != nil {
		return executor.RestoreData{}, fmt.Errorf("parse parameters: %w", err)
	}

	cfg, err := e.loadAWSConfig(ctx, spec)
	if err != nil {
		return executor.RestoreData{}, fmt.Errorf("load AWS config: %w", err)
	}

	clusterName := spec.ConnectorConfig.K8S.ClusterName
	restoreState := RestoreState{
		ManagedNodeGroups: make(map[string]NodeGroupState),
	}

	// Handle Managed Node Groups
	if params.ManagedNGs != nil || params.ComputePolicy.Mode == "Both" || params.ComputePolicy.Mode == "ManagedNodeGroups" {
		eksClient := eks.NewFromConfig(cfg)
		asgClient := autoscaling.NewFromConfig(cfg)

		nodeGroups, err := e.listNodeGroups(ctx, eksClient, clusterName)
		if err != nil {
			return executor.RestoreData{}, fmt.Errorf("list node groups: %w", err)
		}

		for _, ng := range nodeGroups {
			state, err := e.scaleNodeGroupToZero(ctx, eksClient, asgClient, clusterName, ng)
			if err != nil {
				return executor.RestoreData{}, fmt.Errorf("scale node group %s: %w", ng, err)
			}
			restoreState.ManagedNodeGroups[ng] = state
		}
	}

	// Handle Karpenter (simplified - would need k8s client in real impl)
	if params.Karpenter != nil || params.ComputePolicy.Mode == "Both" || params.ComputePolicy.Mode == "Karpenter" {
		restoreState.Karpenter = &KarpenterState{
			NodePools: params.Karpenter.TargetNodePools,
		}
		// In real implementation: delete Karpenter nodes via k8s API
		// This is a placeholder for MVP
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

// WakeUp restores the EKS cluster.
func (e *Executor) WakeUp(ctx context.Context, spec executor.Spec, restore executor.RestoreData) error {
	var restoreState RestoreState
	if err := json.Unmarshal(restore.Data, &restoreState); err != nil {
		return fmt.Errorf("unmarshal restore state: %w", err)
	}

	cfg, err := e.loadAWSConfig(ctx, spec)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	clusterName := spec.ConnectorConfig.K8S.ClusterName

	// Restore Managed Node Groups
	if len(restoreState.ManagedNodeGroups) > 0 {
		eksClient := eks.NewFromConfig(cfg)

		for ngName, state := range restoreState.ManagedNodeGroups {
			if err := e.restoreNodeGroup(ctx, eksClient, clusterName, ngName, state); err != nil {
				return fmt.Errorf("restore node group %s: %w", ngName, err)
			}
		}
	}

	// Karpenter nodes will be recreated automatically by Karpenter controller
	// when workloads are scheduled

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
	region := spec.ConnectorConfig.K8S.Region
	if spec.ConnectorConfig.AWS != nil {
		region = spec.ConnectorConfig.AWS.Region
	}

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return aws.Config{}, err
	}

	// Assume role if configured
	if spec.ConnectorConfig.AWS != nil && spec.ConnectorConfig.AWS.AssumeRoleArn != "" {
		stsClient := stscreds.NewAssumeRoleProvider(
			stscreds.NewAssumeRoleProvider(nil, spec.ConnectorConfig.AWS.AssumeRoleArn),
		)
		cfg.Credentials = aws.NewCredentialsCache(stsClient)
	}

	return cfg, nil
}

func (e *Executor) listNodeGroups(ctx context.Context, client *eks.Client, clusterName string) ([]string, error) {
	out, err := client.ListNodegroups(ctx, &eks.ListNodegroupsInput{
		ClusterName: aws.String(clusterName),
	})
	if err != nil {
		return nil, err
	}
	return out.Nodegroups, nil
}

func (e *Executor) scaleNodeGroupToZero(ctx context.Context, eksClient *eks.Client, asgClient *autoscaling.Client, clusterName, ngName string) (NodeGroupState, error) {
	// Get current state
	desc, err := eksClient.DescribeNodegroup(ctx, &eks.DescribeNodegroupInput{
		ClusterName:   aws.String(clusterName),
		NodegroupName: aws.String(ngName),
	})
	if err != nil {
		return NodeGroupState{}, err
	}

	state := NodeGroupState{
		DesiredSize: aws.ToInt32(desc.Nodegroup.ScalingConfig.DesiredSize),
		MinSize:     aws.ToInt32(desc.Nodegroup.ScalingConfig.MinSize),
		MaxSize:     aws.ToInt32(desc.Nodegroup.ScalingConfig.MaxSize),
	}

	// Scale to zero
	_, err = eksClient.UpdateNodegroupConfig(ctx, &eks.UpdateNodegroupConfigInput{
		ClusterName:   aws.String(clusterName),
		NodegroupName: aws.String(ngName),
		ScalingConfig: &eks.NodegroupScalingConfig{
			MinSize:     aws.Int32(0),
			DesiredSize: aws.Int32(0),
			MaxSize:     aws.Int32(state.MaxSize), // Keep max
		},
	})
	if err != nil {
		return NodeGroupState{}, err
	}

	return state, nil
}

func (e *Executor) restoreNodeGroup(ctx context.Context, client *eks.Client, clusterName, ngName string, state NodeGroupState) error {
	_, err := client.UpdateNodegroupConfig(ctx, &eks.UpdateNodegroupConfigInput{
		ClusterName:   aws.String(clusterName),
		NodegroupName: aws.String(ngName),
		ScalingConfig: &eks.NodegroupScalingConfig{
			MinSize:     aws.Int32(state.MinSize),
			DesiredSize: aws.Int32(state.DesiredSize),
			MaxSize:     aws.Int32(state.MaxSize),
		},
	})
	return err
}

func init() {
	executor.Register(New())
}
