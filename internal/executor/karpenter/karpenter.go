/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package karpenter

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Executor implements hibernation for Karpenter NodePools.
type Executor struct{}

// New creates a new Karpenter executor.
func New() *Executor {
	return &Executor{}
}

// Type returns the executor type.
func (e *Executor) Type() string {
	return "karpenter"
}

// Validate validates the executor spec.
func (e *Executor) Validate(spec executor.Spec) error {
	if spec.ConnectorConfig.K8S == nil {
		return fmt.Errorf("K8S connector config is required")
	}
	if spec.ConnectorConfig.K8S.ClusterName == "" {
		return fmt.Errorf("cluster name is required")
	}
	if spec.ConnectorConfig.K8S.Region == "" {
		return fmt.Errorf("region is required")
	}

	var params struct {
		NodePools []string `json:"nodePools"`
	}
	if err := json.Unmarshal(spec.Parameters, &params); err != nil {
		return fmt.Errorf("parse parameters: %w", err)
	}

	if len(params.NodePools) == 0 {
		return fmt.Errorf("at least one NodePool must be specified")
	}

	return nil
}

// Shutdown scales Karpenter NodePools to zero by setting disruption budgets and resource limits.
func (e *Executor) Shutdown(ctx context.Context, spec executor.Spec) (executor.RestoreData, error) {
	var params struct {
		NodePools []string `json:"nodePools"`
	}
	if err := json.Unmarshal(spec.Parameters, &params); err != nil {
		return executor.RestoreData{}, fmt.Errorf("parse parameters: %w", err)
	}

	// Build clients
	_, dynamicClient, err := e.buildKubernetesClient(ctx, &spec)
	if err != nil {
		return executor.RestoreData{}, fmt.Errorf("build kubernetes client: %w", err)
	}

	// Store original state
	nodePoolStates := make(map[string]NodePoolState)

	// Process each NodePool
	for _, nodePoolName := range params.NodePools {
		state, err := e.scaleDownNodePool(ctx, dynamicClient, nodePoolName)
		if err != nil {
			return executor.RestoreData{}, fmt.Errorf("scale down NodePool %s: %w", nodePoolName, err)
		}
		nodePoolStates[nodePoolName] = state
	}

	// Build restore data
	stateBytes, err := json.Marshal(nodePoolStates)
	if err != nil {
		return executor.RestoreData{}, fmt.Errorf("marshal restore data: %w", err)
	}

	return executor.RestoreData{
		Type: e.Type(),
		Data: stateBytes,
	}, nil
}

// WakeUp restores Karpenter NodePools from hibernation.
func (e *Executor) WakeUp(ctx context.Context, spec executor.Spec, restore executor.RestoreData) error {
	if len(restore.Data) == 0 {
		return fmt.Errorf("restore data is required for wake-up")
	}

	var nodePoolStates map[string]NodePoolState
	if err := json.Unmarshal(restore.Data, &nodePoolStates); err != nil {
		return fmt.Errorf("unmarshal restore data: %w", err)
	}

	// Build clients
	_, dynamicClient, err := e.buildKubernetesClient(ctx, &spec)
	if err != nil {
		return fmt.Errorf("build kubernetes client: %w", err)
	}

	// Restore each NodePool
	for nodePoolName, state := range nodePoolStates {
		if err := e.restoreNodePool(ctx, dynamicClient, nodePoolName, state); err != nil {
			return fmt.Errorf("restore NodePool %s: %w", nodePoolName, err)
		}
	}

	return nil
}

// NodePoolState stores the original state of a NodePool before hibernation.
type NodePoolState struct {
	Name              string                 `json:"name"`
	DisruptionBudgets interface{}            `json:"disruptionBudgets,omitempty"`
	Limits            map[string]interface{} `json:"limits,omitempty"`
}

// scaleDownNodePool scales a NodePool to prevent new nodes from being created.
func (e *Executor) scaleDownNodePool(ctx context.Context, client dynamic.Interface, nodePoolName string) (NodePoolState, error) {
	nodePoolGVR := schema.GroupVersionResource{
		Group:    "karpenter.sh",
		Version:  "v1",
		Resource: "nodepools",
	}

	// Get the NodePool
	nodePool, err := client.Resource(nodePoolGVR).Get(ctx, nodePoolName, metav1.GetOptions{})
	if err != nil {
		return NodePoolState{}, fmt.Errorf("get NodePool: %w", err)
	}

	// Save original state
	spec, found, err := unstructured.NestedMap(nodePool.Object, "spec")
	if err != nil || !found {
		return NodePoolState{}, fmt.Errorf("get NodePool spec: %w", err)
	}

	state := NodePoolState{Name: nodePoolName}

	// Save disruption budgets
	if budgets, found, _ := unstructured.NestedFieldCopy(spec, "disruption"); found {
		state.DisruptionBudgets = budgets
	}

	// Save limits
	if limits, found, _ := unstructured.NestedMap(spec, "limits"); found {
		state.Limits = limits
	}

	// Update NodePool to prevent new nodes: set limits to zero
	if err := unstructured.SetNestedMap(nodePool.Object, map[string]interface{}{
		"cpu":    "0",
		"memory": "0Gi",
	}, "spec", "limits", "resources"); err != nil {
		return NodePoolState{}, fmt.Errorf("set resource limits: %w", err)
	}

	// Update the NodePool
	if _, err := client.Resource(nodePoolGVR).Update(ctx, nodePool, metav1.UpdateOptions{}); err != nil {
		return NodePoolState{}, fmt.Errorf("update NodePool: %w", err)
	}

	return state, nil
}

// restoreNodePool restores a NodePool to its original configuration.
func (e *Executor) restoreNodePool(ctx context.Context, client dynamic.Interface, nodePoolName string, state NodePoolState) error {
	nodePoolGVR := schema.GroupVersionResource{
		Group:    "karpenter.sh",
		Version:  "v1",
		Resource: "nodepools",
	}

	// Get the current NodePool
	nodePool, err := client.Resource(nodePoolGVR).Get(ctx, nodePoolName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get NodePool: %w", err)
	}

	// Restore disruption budgets
	if state.DisruptionBudgets != nil {
		if err := unstructured.SetNestedField(nodePool.Object, state.DisruptionBudgets, "spec", "disruption"); err != nil {
			return fmt.Errorf("restore disruption: %w", err)
		}
	}

	// Restore limits
	if state.Limits != nil {
		if err := unstructured.SetNestedMap(nodePool.Object, state.Limits, "spec", "limits"); err != nil {
			return fmt.Errorf("restore limits: %w", err)
		}
	}

	// Update the NodePool
	if _, err := client.Resource(nodePoolGVR).Update(ctx, nodePool, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update NodePool: %w", err)
	}

	return nil
}

// buildKubernetesClient builds a Kubernetes client for the EKS cluster.
func (e *Executor) buildKubernetesClient(ctx context.Context, spec *executor.Spec) (*kubernetes.Clientset, dynamic.Interface, error) {
	// Build AWS config
	cfg, err := e.buildAWSConfig(ctx, spec)
	if err != nil {
		return nil, nil, fmt.Errorf("build AWS config: %w", err)
	}

	// Get EKS cluster info
	eksClient := eks.NewFromConfig(cfg)
	clusterOutput, err := eksClient.DescribeCluster(ctx, &eks.DescribeClusterInput{
		Name: aws.String(spec.ConnectorConfig.K8S.ClusterName),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("describe EKS cluster: %w", err)
	}

	if clusterOutput.Cluster == nil || clusterOutput.Cluster.Endpoint == nil {
		return nil, nil, fmt.Errorf("EKS cluster endpoint not available")
	}

	// Build kubeconfig from EKS cluster endpoint and CA
	endpoint := aws.ToString(clusterOutput.Cluster.Endpoint)
	caCertB64 := aws.ToString(clusterOutput.Cluster.CertificateAuthority.Data)

	if endpoint == "" || caCertB64 == "" {
		return nil, nil, fmt.Errorf("EKS cluster endpoint or CA certificate missing")
	}

	// Build kubeconfig YAML from EKS cluster info
	kubeconfig := fmt.Sprintf(`apiVersion: v1
clusters:
- cluster:
    server: %s
    certificate-authority-data: %s
  name: eks-cluster
contexts:
- context:
    cluster: eks-cluster
    user: eks-user
  name: eks-context
current-context: eks-context
kind: Config
preferences: {}
users:
- name: eks-user
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: aws
      args:
        - eks
        - get-token
        - --cluster-name
        - %s
        - --region
        - %s
`, endpoint, caCertB64, spec.ConnectorConfig.K8S.ClusterName, spec.ConnectorConfig.AWS.Region)

	// Parse kubeconfig to get REST config
	clientConfig, err := clientcmd.NewClientConfigFromBytes([]byte(kubeconfig))
	if err != nil {
		return nil, nil, fmt.Errorf("parse generated kubeconfig: %w", err)
	}

	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("build rest config from kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("create kubernetes clientset: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("create dynamic client: %w", err)
	}

	return clientset, dynamicClient, nil
}

// buildAWSConfig builds AWS SDK config with optional role assumption.
func (e *Executor) buildAWSConfig(ctx context.Context, spec *executor.Spec) (aws.Config, error) {
	var opts []func(*config.LoadOptions) error

	// Set region
	if spec.ConnectorConfig.K8S != nil && spec.ConnectorConfig.K8S.Region != "" {
		opts = append(opts, config.WithRegion(spec.ConnectorConfig.K8S.Region))
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("load AWS config: %w", err)
	}

	// Assume role if specified
	if spec.ConnectorConfig.AWS != nil && spec.ConnectorConfig.AWS.AssumeRoleArn != "" {
		stsClient := sts.NewFromConfig(cfg)
		creds := stscreds.NewAssumeRoleProvider(stsClient, spec.ConnectorConfig.AWS.AssumeRoleArn)
		cfg.Credentials = creds
	}

	return cfg, nil
}
