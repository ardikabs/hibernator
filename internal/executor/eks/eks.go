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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/go-logr/logr"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/awsutil"
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/ardikabs/hibernator/pkg/k8sutil"
	"github.com/ardikabs/hibernator/pkg/waiter"
)

const (
	ExecutorType       = "eks"
	DefaultWaitTimeout = "10m"
)

// Parameters is an alias for the shared EKS parameter type.
type Parameters = executorparams.EKSParameters

// NodeGroup is an alias for the shared EKS node group type.
type NodeGroup = executorparams.EKSNodeGroup

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
	k8sFactory      K8SClientFactory
	awsConfigLoader AWSConfigLoader

	waitinglist []string
	wg          sync.WaitGroup
}

// EKSClientFactory is a function type for creating EKS clients.
type EKSClientFactory func(cfg aws.Config) EKSClient

// STSClientFactory is a function type for creating STS clients.
type STSClientFactory func(cfg aws.Config) STSClient

// K8SClientFactory is a function type for creating K8S clients.
type K8SClientFactory func(ctx context.Context, spec *executor.Spec) (K8SClient, error)

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
		k8sFactory: func(ctx context.Context, spec *executor.Spec) (K8SClient, error) {
			_, typed, err := k8sutil.BuildClients(ctx, spec.ConnectorConfig.K8S)
			if err != nil {
				return nil, err
			}

			return &k8sClient{
				Typed: typed,
			}, nil
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
func (e *Executor) Shutdown(ctx context.Context, log logr.Logger, spec executor.Spec) error {
	log.Info("EKS executor starting shutdown",
		"target", spec.TargetName,
		"targetType", spec.TargetType,
	)

	params, err := e.parseParams(spec.Parameters)
	if err != nil {
		log.Error(err, "failed to parse parameters")
		return fmt.Errorf("parse parameters: %w", err)
	}

	log.Info("parameters parsed",
		"clusterName", params.ClusterName,
		"nodeGroupCount", len(params.NodeGroups),
		"isAllNodeGroups", len(params.NodeGroups) == 0,
	)

	cfg, err := e.loadAWSConfig(ctx, spec)
	if err != nil {
		log.Error(err, "failed to load AWS config")
		return fmt.Errorf("load AWS config: %w", err)
	}

	eksClient := e.eksFactory(cfg)
	clusterName := params.ClusterName

	// Retrieve cluster information and setup K8S client
	k8sClient, err := e.setupK8SClient(ctx, log, eksClient, cfg, &spec, clusterName)
	if err != nil {
		return fmt.Errorf("setup Kubernetes client: %w", err)
	}

	// Determine target node groups
	targetNodeGroups, err := e.determineTargetNodeGroups(ctx, log, eksClient, clusterName, params)
	if err != nil {
		return fmt.Errorf("determine target node groups: %w", err)
	}

	// Scale each node group to zero
	for _, ngName := range targetNodeGroups {
		log.Info("scaling node group to zero",
			"clusterName", clusterName,
			"nodeGroup", ngName,
		)

		if err := e.scaleNodeGroupToZero(ctx, log, eksClient, k8sClient, clusterName, ngName, params, spec.SaveRestoreData); err != nil {
			log.Error(err, "failed to scale node group",
				"clusterName", clusterName,
				"nodeGroup", ngName,
			)
			return fmt.Errorf("scale node group %s: %w", ngName, err)
		}
	}

	// Wait for all node groups to complete scaling down if configured
	if params.AwaitCompletion.Enabled {
		timeout := params.AwaitCompletion.Timeout
		if timeout == "" {
			timeout = DefaultWaitTimeout
		}

		for _, ngName := range e.waitinglist {
			e.wg.Add(1)

			go func(nodegroup string) {
				defer e.wg.Done()
				if err := e.waitForNodesDeleted(ctx, log, k8sClient, clusterName, nodegroup, timeout); err != nil {
					log.Error(err, "error while waiting for nodes to be deleted", "nodeGroup", ngName)
				}
			}(ngName)
		}

		e.wg.Wait()
	}

	log.Info("EKS shutdown completed successfully",
		"clusterName", clusterName,
		"nodeGroupCount", len(targetNodeGroups),
	)

	return nil
}

// WakeUp restores EKS Managed Node Groups to their original scaling configuration.
func (e *Executor) WakeUp(ctx context.Context, log logr.Logger, spec executor.Spec, restore executor.RestoreData) error {
	log.Info("EKS executor starting wakeup",
		"target", spec.TargetName,
		"targetType", spec.TargetType,
	)

	// Parse parameters
	params, err := e.parseParams(spec.Parameters)
	if err != nil {
		log.Error(err, "failed to parse parameters")
		return fmt.Errorf("parse parameters: %w", err)
	}

	log.Info("restore state loaded", "nodeGroupCount", len(restore.Data))

	cfg, err := e.loadAWSConfig(ctx, spec)
	if err != nil {
		log.Error(err, "failed to load AWS config")
		return fmt.Errorf("load AWS config: %w", err)
	}

	eksClient := e.eksFactory(cfg)
	clusterName := params.ClusterName

	// Restore each node group
	for ngName, stateBytes := range restore.Data {
		var state NodeGroupState
		if err := json.Unmarshal(stateBytes, &state); err != nil {
			log.Error(err, "failed to unmarshal node group state", "nodeGroup", ngName)
			return fmt.Errorf("unmarshal node group state %s: %w", ngName, err)
		}

		log.Info("restoring node group",
			"clusterName", clusterName,
			"nodeGroup", ngName,
			"desiredSize", state.DesiredSize,
			"minSize", state.MinSize,
			"maxSize", state.MaxSize,
		)

		if err := e.restoreNodeGroup(ctx, log, eksClient, clusterName, ngName, state, params); err != nil {
			log.Error(err, "failed to restore node group",
				"clusterName", clusterName,
				"nodeGroup", ngName,
			)
			return fmt.Errorf("restore node group %s: %w", ngName, err)
		}
	}

	// Wait for all node groups to become active if configured
	if params.AwaitCompletion.Enabled {
		timeout := params.AwaitCompletion.Timeout
		if timeout == "" {
			timeout = DefaultWaitTimeout
		}

		for _, ngName := range e.waitinglist {
			e.wg.Add(1)

			go func(nodegroup string) {
				defer e.wg.Done()
				if err := e.waitForNodeGroupActive(ctx, log, eksClient, clusterName, nodegroup, timeout); err != nil {
					log.Error(err, "error while waiting for node group to become active", "nodeGroup", ngName)
				}
			}(ngName)
		}

		e.wg.Wait()
	}

	log.Info("EKS wakeup completed successfully",
		"clusterName", clusterName,
		"nodeGroupCount", len(restore.Data),
	)
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

func (e *Executor) scaleNodeGroupToZero(ctx context.Context, log logr.Logger, eksClient EKSClient, k8sClient K8SClient, clusterName, ngName string, params Parameters, callback executor.SaveRestoreDataFunc) error {
	// Get current state
	desc, err := eksClient.DescribeNodegroup(ctx, &eks.DescribeNodegroupInput{
		ClusterName:   aws.String(clusterName),
		NodegroupName: aws.String(ngName),
	})
	if err != nil {
		return err
	}

	state := NodeGroupState{
		DesiredSize: aws.ToInt32(desc.Nodegroup.ScalingConfig.DesiredSize),
		MinSize:     aws.ToInt32(desc.Nodegroup.ScalingConfig.MinSize),
		MaxSize:     aws.ToInt32(desc.Nodegroup.ScalingConfig.MaxSize),
	}

	// Scale to zero
	if _, err = eksClient.UpdateNodegroupConfig(ctx, &eks.UpdateNodegroupConfigInput{
		ClusterName:   aws.String(clusterName),
		NodegroupName: aws.String(ngName),
		ScalingConfig: &types.NodegroupScalingConfig{
			MinSize:     aws.Int32(0),
			DesiredSize: aws.Int32(0),
			MaxSize:     aws.Int32(state.MaxSize), // Keep max
		},
	}); err != nil {
		return err
	}

	// Add to waiting list for awaiting completion if configured
	if params.AwaitCompletion.Enabled {
		e.waitinglist = append(e.waitinglist, ngName)
	}
	log.Info("node group scaled successfully",
		"nodeGroup", ngName,
		"previousDesired", state.DesiredSize,
		"previousMin", state.MinSize,
		"previousMax", state.MaxSize,
	)

	// Incremental save: persist this node group's restore data immediately
	if callback != nil {
		if err := callback(ngName, state, state.DesiredSize > 0); err != nil {
			log.Error(err, "failed to save restore data incrementally", "nodeGroup", ngName)
			// Continue processing - save at end as fallback
		}
	}

	return nil
}

func (e *Executor) restoreNodeGroup(ctx context.Context, log logr.Logger, client EKSClient, clusterName, ngName string, state NodeGroupState, params Parameters) error {
	_, err := client.UpdateNodegroupConfig(ctx, &eks.UpdateNodegroupConfigInput{
		ClusterName:   aws.String(clusterName),
		NodegroupName: aws.String(ngName),
		ScalingConfig: &types.NodegroupScalingConfig{
			MinSize:     aws.Int32(state.MinSize),
			DesiredSize: aws.Int32(state.DesiredSize),
			MaxSize:     aws.Int32(state.MaxSize),
		},
	})
	if err != nil {
		return err
	}

	// Add to waiting list for awaiting completion if configured
	if params.AwaitCompletion.Enabled {
		e.waitinglist = append(e.waitinglist, ngName)
	}

	log.Info("node group restored successfully",
		"nodeGroup", ngName,
		"desiredSize", state.DesiredSize,
		"minSize", state.MinSize,
		"maxSize", state.MaxSize,
	)

	return nil
}

// waitForNodesDeleted waits for all Nodes managed by the ManagedNodeGroup to be deleted.
func (e *Executor) waitForNodesDeleted(ctx context.Context, log logr.Logger, client K8SClient, clusterName, ngName, timeout string) error {
	log.Info("waiting for ManagedNodeGroup nodes to be deleted",
		"clusterName", clusterName,
		"managedNodeGroup", ngName,
		"timeout", timeout,
	)

	w, err := waiter.NewWaiter(ctx, log, timeout)
	if err != nil {
		return fmt.Errorf("create waiter: %w", err)
	}

	// List Nodes with the ManagedNodeGroup label
	labelSelector := fmt.Sprintf("eks.amazonaws.com/nodegroup=%s", ngName)

	if err := w.Poll(fmt.Sprintf("ManagedNodeGroup %s nodes to be deleted", ngName), func() (bool, string, error) {
		// List all nodes with the nodepool label
		nodes, err := client.ListNode(ctx, labelSelector)
		if err != nil {
			return false, "", fmt.Errorf("list nodes: %w", err)
		}

		nodeCount := len(nodes.Items)
		if nodeCount == 0 {
			return true, "all nodes deleted", nil
		}

		return false, fmt.Sprintf("%d node(s) still exist, waiting for deletion", nodeCount), nil
	}); err != nil {
		return err
	}

	log.Info("All ManagedNodeGroup nodes deleted successfully", "nodeGroup", ngName)
	return nil
}

// waitForNodeGroupActive polls the node group status until it reaches ACTIVE state.
func (e *Executor) waitForNodeGroupActive(ctx context.Context, log logr.Logger, eksClient EKSClient, clusterName, ngName, timeout string) error {
	log.Info("waiting for ManagedNodeGroup to be active",
		"clusterName", clusterName,
		"managedNodeGroup", ngName,
		"timeout", timeout,
	)

	w, err := waiter.NewWaiter(ctx, log, timeout)
	if err != nil {
		return err
	}

	if err := w.Poll(fmt.Sprintf("node group %s/%s to become active", clusterName, ngName), func() (bool, string, error) {
		desc, err := eksClient.DescribeNodegroup(ctx, &eks.DescribeNodegroupInput{
			ClusterName:   aws.String(clusterName),
			NodegroupName: aws.String(ngName),
		})
		if err != nil {
			return false, "", err
		}

		status := string(desc.Nodegroup.Status)
		currentSize := aws.ToInt32(desc.Nodegroup.ScalingConfig.DesiredSize)
		desiredSize := aws.ToInt32(desc.Nodegroup.ScalingConfig.DesiredSize)

		statusStr := fmt.Sprintf("status=%s, current=%d, desired=%d", status, currentSize, desiredSize)

		if desc.Nodegroup.Status == types.NodegroupStatusActive {
			return true, statusStr, nil
		}

		return false, statusStr, nil
	}); err != nil {
		return err
	}

	log.Info("Node group is now active", "nodeGroup", ngName)
	return nil
}

// setupK8SClient retrieves cluster information from EKS and creates a Kubernetes client.
// This method fetches the cluster endpoint and CA certificate, then initializes a K8S client
// that can be used to monitor node deletion during hibernation.
func (e *Executor) setupK8SClient(ctx context.Context, log logr.Logger, eksClient EKSClient, cfg aws.Config, spec *executor.Spec, clusterName string) (K8SClient, error) {
	log.Info("retrieving EKS cluster information", "clusterName", clusterName)

	clusterInfo, err := e.getClusterInfo(ctx, eksClient, clusterName)
	if err != nil {
		return nil, err
	}

	// Setup K8S connector config with cluster credentials
	spec.ConnectorConfig.K8S = &executor.K8SConnectorConfig{
		ClusterName:     clusterName,
		Region:          cfg.Region,
		ClusterEndpoint: clusterInfo.Endpoint,
		ClusterCAData:   clusterInfo.CAData,
		UseEKSToken:     true,
		AWS:             spec.ConnectorConfig.AWS,
	}

	k8sClient, err := e.k8sFactory(ctx, spec)
	if err != nil {
		log.Error(err, "failed to create Kubernetes client")
		return nil, fmt.Errorf("create Kubernetes client: %w", err)
	}

	log.Info("Kubernetes client created successfully")
	return k8sClient, nil
}

// clusterInfo holds essential EKS cluster information.
type clusterInfo struct {
	Endpoint string
	CAData   []byte
}

// getClusterInfo retrieves and validates essential cluster information from EKS.
func (e *Executor) getClusterInfo(ctx context.Context, eksClient EKSClient, clusterName string) (*clusterInfo, error) {
	output, err := eksClient.DescribeCluster(ctx, &eks.DescribeClusterInput{
		Name: aws.String(clusterName),
	})
	if err != nil {
		return nil, fmt.Errorf("describe cluster %s: %w", clusterName, err)
	}

	if output.Cluster == nil {
		return nil, fmt.Errorf("cluster %s not found", clusterName)
	}

	endpoint := aws.ToString(output.Cluster.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("cluster endpoint not available")
	}

	caDataEncoded := aws.ToString(output.Cluster.CertificateAuthority.Data)
	if caDataEncoded == "" {
		return nil, fmt.Errorf("cluster certificate authority data missing")
	}

	caData, err := base64.StdEncoding.DecodeString(caDataEncoded)
	if err != nil {
		return nil, fmt.Errorf("decode certificate authority data: %w", err)
	}

	return &clusterInfo{
		Endpoint: endpoint,
		CAData:   caData,
	}, nil
}

// determineTargetNodeGroups resolves which node groups should be scaled based on parameters.
// If no specific node groups are provided in parameters, it will discover all node groups in the cluster.
func (e *Executor) determineTargetNodeGroups(ctx context.Context, log logr.Logger, eksClient EKSClient, clusterName string, params Parameters) ([]string, error) {
	var targetNodeGroups []string

	if len(params.NodeGroups) == 0 {
		// Empty means all node groups in the cluster
		log.Info("discovering all node groups in cluster", "clusterName", clusterName)
		nodeGroups, err := e.listNodeGroups(ctx, eksClient, clusterName)
		if err != nil {
			log.Error(err, "failed to list node groups", "clusterName", clusterName)
			return nil, fmt.Errorf("list node groups: %w", err)
		}
		targetNodeGroups = nodeGroups
	} else {
		// Use explicitly specified node groups
		for _, ng := range params.NodeGroups {
			targetNodeGroups = append(targetNodeGroups, ng.Name)
		}
	}

	log.Info("target node groups determined", "count", len(targetNodeGroups))

	if len(targetNodeGroups) == 0 {
		log.Error(nil, "no node groups found", "clusterName", clusterName)
		return nil, fmt.Errorf("no node groups found in cluster %s", clusterName)
	}

	return targetNodeGroups, nil
}
