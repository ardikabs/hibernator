/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package eks implements the EKS executor for hibernating EKS Managed Node Groups.
// This executor uses AWS API to scale node groups to zero.
// For Karpenter NodePools, use the separate Karpenter executor.
package eks

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/awsutil"
	"github.com/ardikabs/hibernator/pkg/executorparams"
)

const ExecutorType = "eks"

// Parameters is an alias for the shared EKS parameter type.
type Parameters = executorparams.EKSParameters

// NodeGroup is an alias for the shared EKS node group type.
type NodeGroup = executorparams.EKSNodeGroup

// RestoreState holds EKS restore data for managed node groups.
type RestoreState struct {
	ClusterName string                    `json:"clusterName"`
	NodeGroups  map[string]NodeGroupState `json:"nodeGroups"`
}

// NodeGroupState holds state for a managed node group.
type NodeGroupState struct {
	DesiredSize int32 `json:"desired"`
	MinSize     int32 `json:"min"`
	MaxSize     int32 `json:"max"`
}

// Executor implements the EKS hibernation logic for Managed Node Groups.
type Executor struct {
	eksFactory      EKSClientFactory
	stsFactory      STSClientFactory
	awsConfigLoader AWSConfigLoader
}

// EKSClientFactory is a function type for creating EKS clients.
type EKSClientFactory func(cfg aws.Config) EKSClient

// STSClientFactory is a function type for creating STS clients.
type STSClientFactory func(cfg aws.Config) STSClient

// AWSConfigLoader is a function type for loading AWS config.
type AWSConfigLoader func(ctx context.Context, spec executor.Spec) (aws.Config, error)

// New creates a new EKS executor with real AWS clients.
func New() *Executor {
	return &Executor{
		eksFactory: func(cfg aws.Config) EKSClient {
			return eks.NewFromConfig(cfg)
		},
		stsFactory: func(cfg aws.Config) STSClient {
			return sts.NewFromConfig(cfg)
		},
	}
}

// NewWithClients creates a new EKS executor with injected client factories.
// This is useful for testing with mock clients.
func NewWithClients(eksFactory EKSClientFactory, stsFactory STSClientFactory, awsConfigLoader AWSConfigLoader) *Executor {
	return &Executor{
		eksFactory:      eksFactory,
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
		return fmt.Errorf("AWS connector config required for EKS executor")
	}
	if spec.ConnectorConfig.AWS.Region == "" {
		return fmt.Errorf("AWS region is required")
	}

	var params Parameters
	if len(spec.Parameters) > 0 {
		if err := json.Unmarshal(spec.Parameters, &params); err != nil {
			return fmt.Errorf("parse parameters: %w", err)
		}
	}

	if params.ClusterName == "" {
		return fmt.Errorf("clusterName is required")
	}

	return nil
}

// Shutdown performs EKS Managed Node Group hibernation by scaling to zero.
func (e *Executor) Shutdown(ctx context.Context, spec executor.Spec) (executor.RestoreData, error) {
	params, err := e.parseParams(spec.Parameters)
	if err != nil {
		return executor.RestoreData{}, fmt.Errorf("parse parameters: %w", err)
	}

	cfg, err := e.loadAWSConfig(ctx, spec)
	if err != nil {
		return executor.RestoreData{}, fmt.Errorf("load AWS config: %w", err)
	}

	eksClient := e.eksFactory(cfg)
	clusterName := params.ClusterName

	restoreState := RestoreState{
		ClusterName: clusterName,
		NodeGroups:  make(map[string]NodeGroupState),
	}

	// Determine target node groups
	var targetNodeGroups []string
	if len(params.NodeGroups) == 0 {
		// Empty means all node groups in the cluster
		targetNodeGroups, err = e.listNodeGroups(ctx, eksClient, clusterName)
		if err != nil {
			return executor.RestoreData{}, fmt.Errorf("list node groups: %w", err)
		}
	} else {
		for _, ng := range params.NodeGroups {
			targetNodeGroups = append(targetNodeGroups, ng.Name)
		}
	}

	if len(targetNodeGroups) == 0 {
		return executor.RestoreData{}, fmt.Errorf("no node groups found in cluster %s", clusterName)
	}

	// Scale each node group to zero
	for _, ngName := range targetNodeGroups {
		state, err := e.scaleNodeGroupToZero(ctx, eksClient, clusterName, ngName)
		if err != nil {
			return executor.RestoreData{}, fmt.Errorf("scale node group %s: %w", ngName, err)
		}
		restoreState.NodeGroups[ngName] = state
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

// WakeUp restores EKS Managed Node Groups to their original scaling configuration.
func (e *Executor) WakeUp(ctx context.Context, spec executor.Spec, restore executor.RestoreData) error {
	var restoreState RestoreState
	if err := json.Unmarshal(restore.Data, &restoreState); err != nil {
		return fmt.Errorf("unmarshal restore state: %w", err)
	}

	cfg, err := e.loadAWSConfig(ctx, spec)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	eksClient := e.eksFactory(cfg)

	// Restore each node group
	for ngName, state := range restoreState.NodeGroups {
		if err := e.restoreNodeGroup(ctx, eksClient, restoreState.ClusterName, ngName, state); err != nil {
			return fmt.Errorf("restore node group %s: %w", ngName, err)
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

func (e *Executor) listNodeGroups(ctx context.Context, client EKSClient, clusterName string) ([]string, error) {
	out, err := client.ListNodegroups(ctx, &eks.ListNodegroupsInput{
		ClusterName: aws.String(clusterName),
	})
	if err != nil {
		return nil, err
	}
	return out.Nodegroups, nil
}

func (e *Executor) scaleNodeGroupToZero(ctx context.Context, eksClient EKSClient, clusterName, ngName string) (NodeGroupState, error) {
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
		ScalingConfig: &types.NodegroupScalingConfig{
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

func (e *Executor) restoreNodeGroup(ctx context.Context, client EKSClient, clusterName, ngName string, state NodeGroupState) error {
	_, err := client.UpdateNodegroupConfig(ctx, &eks.UpdateNodegroupConfigInput{
		ClusterName:   aws.String(clusterName),
		NodegroupName: aws.String(ngName),
		ScalingConfig: &types.NodegroupScalingConfig{
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
