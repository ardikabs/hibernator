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
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

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
	WasScaled   bool  `json:"wasScaled"` // true if scaled down by hibernator, false if already at 0
}

type operationOutcome string

const (
	operationOutcomeApplied      operationOutcome = "applied"
	operationOutcomeSkippedStale operationOutcome = "skipped_stale"
)

type operationStats struct {
	processed    int
	applied      int
	skippedStale int
}

func formatShutdownMessage(clusterName string, stats operationStats) string {
	msg := fmt.Sprintf("scaled %d node group(s) to zero in EKS cluster %s", stats.applied, clusterName)
	return appendCountSegment(msg, "skipped", stats.skippedStale, "stale node group")
}

func formatWakeUpMessage(clusterName string, stats operationStats) string {
	msg := fmt.Sprintf("restored %d node group(s) in EKS cluster %s", stats.applied, clusterName)
	return appendCountSegment(msg, "skipped", stats.skippedStale, "stale node group")
}

func appendCountSegment(msg, action string, count int, noun string) string {
	if count <= 0 {
		return msg
	}

	return fmt.Sprintf("%s, %s %d %s(s)", msg, action, count, noun)
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
func (e *Executor) Shutdown(ctx context.Context, log logr.Logger, spec executor.Spec) (*executor.Result, error) {
	log = log.WithName("eks").WithValues("target", spec.TargetName, "targetType", spec.TargetType)
	log.Info("executor starting shutdown")
	e.waitinglist = nil

	params, err := e.parseParams(spec.Parameters)
	if err != nil {
		return nil, fmt.Errorf("parse parameters: %w", err)
	}

	log.Info("parameters parsed",
		"clusterName", params.ClusterName,
		"nodeGroupCount", len(params.NodeGroups),
		"isAllNodeGroups", len(params.NodeGroups) == 0,
	)

	cfg, err := e.loadAWSConfig(ctx, spec)
	if err != nil {
		log.Error(err, "failed to load AWS config")
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	eksClient := e.eksFactory(cfg)
	clusterName := params.ClusterName

	// Retrieve cluster information and setup K8S client
	k8sClient, err := e.setupK8SClient(ctx, log, eksClient, cfg, &spec, clusterName)
	if err != nil {
		return nil, fmt.Errorf("setup Kubernetes client: %w", err)
	}

	// Determine target node groups
	targetNodeGroups, err := e.determineTargetNodeGroups(ctx, log, eksClient, clusterName, params)
	if err != nil {
		return nil, fmt.Errorf("determine target node groups: %w", err)
	}

	stats := operationStats{processed: len(targetNodeGroups)}

	// Scale each node group to zero
	for _, ngName := range targetNodeGroups {
		log.Info("scaling node group to zero",
			"clusterName", clusterName,
			"nodeGroup", ngName,
		)

		outcome, err := e.scaleNodeGroupToZero(ctx, log, eksClient, k8sClient, clusterName, ngName, params, spec.ReportStateCallback)
		if err != nil {
			log.Error(err, "failed to scale node group",
				"clusterName", clusterName,
				"nodeGroup", ngName,
			)
			return nil, fmt.Errorf("scale node group %s: %w", ngName, err)
		}

		switch outcome {
		case operationOutcomeApplied:
			stats.applied++
		case operationOutcomeSkippedStale:
			stats.skippedStale++
		}
	}

	// Wait for all node groups to complete scaling down if configured
	msg := formatShutdownMessage(clusterName, stats)

	if params.AwaitCompletion.Enabled {
		timeout := params.AwaitCompletion.Timeout
		if timeout == "" {
			timeout = DefaultWaitTimeout
		}

		var timedOut atomic.Int32
		for _, ngName := range e.waitinglist {
			e.wg.Add(1)

			go func(nodegroup string) {
				defer e.wg.Done()
				if err := e.waitForNodesDeleted(ctx, log, k8sClient, clusterName, nodegroup, timeout); err != nil {
					timedOut.Add(1)
					log.Error(err, "error while waiting for nodes to be deleted", "nodeGroup", nodegroup)
				}
			}(ngName)
		}

		e.wg.Wait()

		total := len(e.waitinglist)
		if failed := int(timedOut.Load()); failed > 0 {
			msg += fmt.Sprintf("; %d of %d node group(s) still have nodes after %s timeout", failed, total, timeout)
		} else {
			msg += "; all nodes terminated"
		}
	}

	log.Info("shutdown completed",
		"clusterName", clusterName,
		"processed", stats.processed,
		"scaled", stats.applied,
		"skippedStale", stats.skippedStale,
	)

	return &executor.Result{Message: msg}, nil
}

// WakeUp restores EKS Managed Node Groups to their original scaling configuration.
func (e *Executor) WakeUp(ctx context.Context, log logr.Logger, spec executor.Spec, restore executor.RestoreData) (*executor.Result, error) {
	log = log.WithName("eks").WithValues("target", spec.TargetName, "targetType", spec.TargetType)
	log.Info("executor starting wakeup")
	e.waitinglist = nil

	if len(restore.Data) == 0 {
		log.Info("no restore data available, wakeup operation is no-op")
		return &executor.Result{Message: "wakeup completed for EKS (no restore data)"}, nil
	}

	// Parse parameters
	params, err := e.parseParams(spec.Parameters)
	if err != nil {
		log.Error(err, "failed to parse parameters")
		return nil, fmt.Errorf("parse parameters: %w", err)
	}

	log.Info("restore state loaded", "nodeGroupCount", len(restore.Data))

	cfg, err := e.loadAWSConfig(ctx, spec)
	if err != nil {
		log.Error(err, "failed to load AWS config")
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	eksClient := e.eksFactory(cfg)
	clusterName := params.ClusterName
	stats := operationStats{processed: len(restore.Data)}

	// Restore each node group
	for ngName, stateBytes := range restore.Data {
		var state NodeGroupState
		if err := json.Unmarshal(stateBytes, &state); err != nil {
			log.Error(err, "failed to unmarshal node group state", "nodeGroup", ngName)
			return nil, fmt.Errorf("unmarshal node group state %s: %w", ngName, err)
		}

		if !state.WasScaled {
			stats.skippedStale++
			log.Info("node group was already at zero before hibernation, skipping restore",
				"clusterName", clusterName,
				"nodeGroup", ngName,
			)
			continue
		}

		log.Info("restoring node group",
			"clusterName", clusterName,
			"nodeGroup", ngName,
			"desiredSize", state.DesiredSize,
			"minSize", state.MinSize,
			"maxSize", state.MaxSize,
		)

		outcome, err := e.restoreNodeGroup(ctx, log, eksClient, clusterName, ngName, state, params)
		if err != nil {
			log.Error(err, "failed to restore node group",
				"clusterName", clusterName,
				"nodeGroup", ngName,
			)
			return nil, fmt.Errorf("restore node group %s: %w", ngName, err)
		}

		switch outcome {
		case operationOutcomeApplied:
			stats.applied++
		case operationOutcomeSkippedStale:
			stats.skippedStale++
		}
	}

	// Wait for all node groups to become active if configured
	msg := formatWakeUpMessage(clusterName, stats)

	if params.AwaitCompletion.Enabled {
		timeout := params.AwaitCompletion.Timeout
		if timeout == "" {
			timeout = DefaultWaitTimeout
		}

		var timedOut atomic.Int32
		for _, ngName := range e.waitinglist {
			e.wg.Add(1)

			go func(nodegroup string) {
				defer e.wg.Done()
				if err := e.waitForNodeGroupActive(ctx, log, eksClient, clusterName, nodegroup, timeout); err != nil {
					timedOut.Add(1)
					log.Error(err, "error while waiting for node group to become active", "nodeGroup", nodegroup)
				}
			}(ngName)
		}

		e.wg.Wait()

		total := len(e.waitinglist)
		if failed := int(timedOut.Load()); failed > 0 {
			msg += fmt.Sprintf("; %d of %d node group(s) not yet active after %s timeout", failed, total, timeout)
		} else {
			msg += "; all node groups active"
		}
	}

	log.Info("wakeup completed",
		"clusterName", clusterName,
		"processed", stats.processed,
		"restored", stats.applied,
		"skippedStale", stats.skippedStale,
	)
	return &executor.Result{Message: msg}, nil
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

func (e *Executor) scaleNodeGroupToZero(ctx context.Context, log logr.Logger, eksClient EKSClient, k8sClient K8SClient, clusterName, ngName string, params Parameters, callback executor.ReportStateCallback) (operationOutcome, error) {
	// Get current state
	desc, err := eksClient.DescribeNodegroup(ctx, &eks.DescribeNodegroupInput{
		ClusterName:   aws.String(clusterName),
		NodegroupName: aws.String(ngName),
	})
	if err != nil {
		var notFoundErr *types.ResourceNotFoundException
		if errors.As(err, &notFoundErr) {
			log.Info("node group not found, skipping stale shutdown entry",
				"clusterName", clusterName,
				"nodeGroup", ngName,
			)
			return operationOutcomeSkippedStale, nil
		}
		return "", err
	}

	desiredSize := aws.ToInt32(desc.Nodegroup.ScalingConfig.DesiredSize)
	minSize := aws.ToInt32(desc.Nodegroup.ScalingConfig.MinSize)
	maxSize := aws.ToInt32(desc.Nodegroup.ScalingConfig.MaxSize)

	// Determine if this is a voluntary action (already at 0) or needs scaling
	wasScaled := desiredSize > 0

	state := NodeGroupState{
		DesiredSize: desiredSize,
		MinSize:     minSize,
		MaxSize:     maxSize,
		WasScaled:   wasScaled,
	}

	// Scale to zero only if not already at zero
	if wasScaled {
		if _, err = eksClient.UpdateNodegroupConfig(ctx, &eks.UpdateNodegroupConfigInput{
			ClusterName:   aws.String(clusterName),
			NodegroupName: aws.String(ngName),
			ScalingConfig: &types.NodegroupScalingConfig{
				MinSize:     aws.Int32(0),
				DesiredSize: aws.Int32(0),
				MaxSize:     aws.Int32(maxSize), // Keep max
			},
		}); err != nil {
			var notFoundErr *types.ResourceNotFoundException
			if errors.As(err, &notFoundErr) {
				log.Info("node group not found during scale update, skipping stale shutdown entry",
					"clusterName", clusterName,
					"nodeGroup", ngName,
				)
				return operationOutcomeSkippedStale, nil
			}
			return "", err
		}

		// Add to waiting list for awaiting completion if configured
		if params.AwaitCompletion.Enabled {
			e.waitinglist = append(e.waitinglist, ngName)
		}
		log.Info("node group scaled to zero",
			"nodeGroup", ngName,
			"previousDesired", desiredSize,
			"previousMin", minSize,
			"previousMax", maxSize,
		)
	} else {
		log.Info("node group already at zero, skipping scale down",
			"nodeGroup", ngName,
			"desiredSize", desiredSize,
		)
	}

	// Incremental save: persist this node group's restore data immediately.
	if callback != nil {
		if err := callback(ngName, state); err != nil {
			log.Error(err, "failed to save restore data incrementally", "nodeGroup", ngName)
			// Continue processing - save at end as fallback
		}
	}

	return operationOutcomeApplied, nil
}

func (e *Executor) restoreNodeGroup(ctx context.Context, log logr.Logger, client EKSClient, clusterName, ngName string, state NodeGroupState, params Parameters) (operationOutcome, error) {
	_, err := client.DescribeNodegroup(ctx, &eks.DescribeNodegroupInput{
		ClusterName:   aws.String(clusterName),
		NodegroupName: aws.String(ngName),
	})
	if err != nil {
		var notFoundErr *types.ResourceNotFoundException
		if errors.As(err, &notFoundErr) {
			log.Info("node group not found, skipping stale restore entry",
				"clusterName", clusterName,
				"nodeGroup", ngName,
			)
			return operationOutcomeSkippedStale, nil
		}

		return "", err
	}

	_, err = client.UpdateNodegroupConfig(ctx, &eks.UpdateNodegroupConfigInput{
		ClusterName:   aws.String(clusterName),
		NodegroupName: aws.String(ngName),
		ScalingConfig: &types.NodegroupScalingConfig{
			MinSize:     aws.Int32(state.MinSize),
			DesiredSize: aws.Int32(state.DesiredSize),
			MaxSize:     aws.Int32(state.MaxSize),
		},
	})
	if err != nil {
		var notFoundErr *types.ResourceNotFoundException
		if errors.As(err, &notFoundErr) {
			log.Info("node group not found during restore update, skipping stale restore entry",
				"clusterName", clusterName,
				"nodeGroup", ngName,
			)
			return operationOutcomeSkippedStale, nil
		}

		return "", err
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

	return operationOutcomeApplied, nil
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
