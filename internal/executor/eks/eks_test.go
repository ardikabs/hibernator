/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package eks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/internal/executor/eks/mocks"
)

func TestNew(t *testing.T) {
	e := New()
	assert.NotNil(t, e)
	assert.NotNil(t, e.eksFactory)
	assert.NotNil(t, e.stsFactory)
}

func TestNewWithClients(t *testing.T) {
	mockEKS := &mocks.EKSClient{}
	mockSTS := &mocks.STSClient{}
	mockK8S := &mocks.K8SClient{}

	eksFactory := func(cfg aws.Config) EKSClient { return mockEKS }
	stsFactory := func(cfg aws.Config) STSClient { return mockSTS }
	k8sFactory := func(ctx context.Context, spec *executor.Spec) (K8SClient, error) { return mockK8S, nil }

	e := NewWithClients(eksFactory, stsFactory, nil)
	e.k8sFactory = k8sFactory

	assert.NotNil(t, e)
	assert.NotNil(t, e.k8sFactory)
}

func TestExecutorType(t *testing.T) {
	e := New()
	assert.Equal(t, "eks", e.Type())
	assert.Equal(t, ExecutorType, e.Type())
}

func TestValidate_MissingAWSConfig(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName:      "test-cluster",
		TargetType:      "eks",
		Parameters:      json.RawMessage(`{"clusterName": "my-cluster"}`),
		ConnectorConfig: executor.ConnectorConfig{},
	}
	err := e.Validate(spec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "AWS connector config required")
}

func TestValidate_MissingRegion(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
		Parameters: json.RawMessage(`{"clusterName": "my-cluster"}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{},
		},
	}
	err := e.Validate(spec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "region is required")
}

func TestValidate_MissingClusterName(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
		Parameters: json.RawMessage(`{}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}
	err := e.Validate(spec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "clusterName is required")
}

func TestValidate_Valid(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
		Parameters: json.RawMessage(`{"clusterName": "my-cluster"}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}
	err := e.Validate(spec)
	assert.NoError(t, err)
}

func TestValidate_WithNodeGroups(t *testing.T) {
	e := New()
	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
		Parameters: json.RawMessage(`{"clusterName": "my-cluster", "nodeGroups": [{"name": "ng-1"}, {"name": "ng-2"}]}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}
	err := e.Validate(spec)
	assert.NoError(t, err)
}

func TestShutdown_WithSpecificNodeGroups(t *testing.T) {
	ctx := context.Background()

	mockEKS := &mocks.EKSClient{}
	mockK8S := &mocks.K8SClient{}

	caDataEncoded := base64.StdEncoding.EncodeToString([]byte("test-ca-data"))

	// Setup expectation for DescribeCluster (for K8S client setup)
	mockEKS.On("DescribeCluster", mock.Anything, &eks.DescribeClusterInput{
		Name: aws.String("my-cluster"),
	}).Return(&eks.DescribeClusterOutput{
		Cluster: &types.Cluster{
			Endpoint: aws.String("https://eks.example.com"),
			CertificateAuthority: &types.Certificate{
				Data: aws.String(caDataEncoded),
			},
		},
	}, nil)

	// Setup expectations for DescribeNodegroup calls
	mockEKS.On("DescribeNodegroup", mock.Anything, &eks.DescribeNodegroupInput{
		ClusterName:   aws.String("my-cluster"),
		NodegroupName: aws.String("ng-1"),
	}).Return(&eks.DescribeNodegroupOutput{
		Nodegroup: &types.Nodegroup{
			ScalingConfig: &types.NodegroupScalingConfig{
				DesiredSize: aws.Int32(3),
				MinSize:     aws.Int32(1),
				MaxSize:     aws.Int32(5),
			},
		},
	}, nil)

	// Setup expectations for UpdateNodegroupConfig calls
	mockEKS.On("UpdateNodegroupConfig", mock.Anything, mock.MatchedBy(func(input *eks.UpdateNodegroupConfigInput) bool {
		return aws.ToString(input.ClusterName) == "my-cluster" &&
			aws.ToString(input.NodegroupName) == "ng-1" &&
			aws.ToInt32(input.ScalingConfig.DesiredSize) == 0
	})).Return(&eks.UpdateNodegroupConfigOutput{}, nil)

	eksFactory := func(cfg aws.Config) EKSClient { return mockEKS }
	stsFactory := func(cfg aws.Config) STSClient { return &mocks.STSClient{} }
	k8sFactory := func(ctx context.Context, spec *executor.Spec) (K8SClient, error) { return mockK8S, nil }

	e := NewWithClients(eksFactory, stsFactory, nil)
	e.k8sFactory = k8sFactory

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
		Parameters: json.RawMessage(`{"clusterName": "my-cluster", "nodeGroups": [{"name": "ng-1"}]}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)

	mockEKS.AssertExpectations(t)
}

func TestShutdown_WithListAllNodeGroups(t *testing.T) {
	ctx := context.Background()

	mockEKS := &mocks.EKSClient{}
	mockK8S := &mocks.K8SClient{}

	caDataEncoded := base64.StdEncoding.EncodeToString([]byte("test-ca-data"))

	// Setup expectation for DescribeCluster (for K8S client setup)
	mockEKS.On("DescribeCluster", mock.Anything, &eks.DescribeClusterInput{
		Name: aws.String("my-cluster"),
	}).Return(&eks.DescribeClusterOutput{
		Cluster: &types.Cluster{
			Endpoint: aws.String("https://eks.example.com"),
			CertificateAuthority: &types.Certificate{
				Data: aws.String(caDataEncoded),
			},
		},
	}, nil)

	// Setup expectation for ListNodegroups
	mockEKS.On("ListNodegroups", mock.Anything, &eks.ListNodegroupsInput{
		ClusterName: aws.String("my-cluster"),
	}).Return(&eks.ListNodegroupsOutput{
		Nodegroups: []string{"ng-1", "ng-2"},
	}, nil)

	// Setup expectations for DescribeNodegroup calls for both node groups
	mockEKS.On("DescribeNodegroup", mock.Anything, mock.MatchedBy(func(input *eks.DescribeNodegroupInput) bool {
		return aws.ToString(input.ClusterName) == "my-cluster"
	})).Return(&eks.DescribeNodegroupOutput{
		Nodegroup: &types.Nodegroup{
			ScalingConfig: &types.NodegroupScalingConfig{
				DesiredSize: aws.Int32(3),
				MinSize:     aws.Int32(1),
				MaxSize:     aws.Int32(5),
			},
		},
	}, nil)

	// Setup expectations for UpdateNodegroupConfig calls
	mockEKS.On("UpdateNodegroupConfig", mock.Anything, mock.MatchedBy(func(input *eks.UpdateNodegroupConfigInput) bool {
		return aws.ToString(input.ClusterName) == "my-cluster" &&
			aws.ToInt32(input.ScalingConfig.DesiredSize) == 0
	})).Return(&eks.UpdateNodegroupConfigOutput{}, nil)

	eksFactory := func(cfg aws.Config) EKSClient { return mockEKS }
	stsFactory := func(cfg aws.Config) STSClient { return &mocks.STSClient{} }
	k8sFactory := func(ctx context.Context, spec *executor.Spec) (K8SClient, error) { return mockK8S, nil }

	e := NewWithClients(eksFactory, stsFactory, nil)
	e.k8sFactory = k8sFactory

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
		Parameters: json.RawMessage(`{"clusterName": "my-cluster"}`), // Empty nodeGroups means all
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)

	mockEKS.AssertExpectations(t)
}

func TestShutdown_DescribeNodegroupError(t *testing.T) {
	ctx := context.Background()

	mockEKS := &mocks.EKSClient{}
	mockK8S := &mocks.K8SClient{}

	caDataEncoded := base64.StdEncoding.EncodeToString([]byte("test-ca-data"))

	// Setup expectation for DescribeCluster (for K8S client setup)
	mockEKS.On("DescribeCluster", mock.Anything, &eks.DescribeClusterInput{
		Name: aws.String("my-cluster"),
	}).Return(&eks.DescribeClusterOutput{
		Cluster: &types.Cluster{
			Endpoint: aws.String("https://eks.example.com"),
			CertificateAuthority: &types.Certificate{
				Data: aws.String(caDataEncoded),
			},
		},
	}, nil)

	// Setup expectation to return error
	mockEKS.On("DescribeNodegroup", mock.Anything, mock.Anything).
		Return(nil, errors.New("access denied"))

	eksFactory := func(cfg aws.Config) EKSClient { return mockEKS }
	stsFactory := func(cfg aws.Config) STSClient { return &mocks.STSClient{} }
	k8sFactory := func(ctx context.Context, spec *executor.Spec) (K8SClient, error) { return mockK8S, nil }

	e := NewWithClients(eksFactory, stsFactory, nil)
	e.k8sFactory = k8sFactory

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
		Parameters: json.RawMessage(`{"clusterName": "my-cluster", "nodeGroups": [{"name": "ng-1"}]}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access denied")
}

func TestWakeUp_RestoreNodeGroups(t *testing.T) {
	ctx := context.Background()

	mockEKS := &mocks.EKSClient{}

	// Setup expectation for UpdateNodegroupConfig call during restore
	mockEKS.On("UpdateNodegroupConfig", mock.Anything, mock.MatchedBy(func(input *eks.UpdateNodegroupConfigInput) bool {
		return aws.ToString(input.ClusterName) == "my-cluster" &&
			aws.ToString(input.NodegroupName) == "ng-1" &&
			aws.ToInt32(input.ScalingConfig.DesiredSize) == 3 // Restored to original
	})).Return(&eks.UpdateNodegroupConfigOutput{}, nil)

	eksFactory := func(cfg aws.Config) EKSClient { return mockEKS }
	stsFactory := func(cfg aws.Config) STSClient { return &mocks.STSClient{} }

	e := NewWithClients(eksFactory, stsFactory, nil)

	// Create per-nodegroup restore data (key = nodegroup name)
	nodeGroupState, _ := json.Marshal(NodeGroupState{DesiredSize: 3, MinSize: 1, MaxSize: 5})

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
		Parameters: json.RawMessage(`{"clusterName": "my-cluster"}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore := executor.RestoreData{
		Type: "eks",
		Data: map[string]json.RawMessage{
			"ng-1": nodeGroupState,
		},
	}

	err := e.WakeUp(ctx, logr.Discard(), spec, restore)
	assert.NoError(t, err)

	mockEKS.AssertExpectations(t)
}

func TestWakeUp_InvalidRestoreData(t *testing.T) {
	e := New()
	ctx := context.Background()

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore := executor.RestoreData{
		Type: "eks",
		Data: map[string]json.RawMessage{
			"invalid": json.RawMessage(`{invalid json}`),
		},
	}

	err := e.WakeUp(ctx, logr.Discard(), spec, restore)
	assert.Error(t, err)
}

func TestShutdown_InvalidParameters(t *testing.T) {
	e := New()
	ctx := context.Background()

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
		Parameters: json.RawMessage(`{invalid json}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.Error(t, err)
}

// ============================================================================
// Data type tests
// ============================================================================

func TestRestoreState_JSON(t *testing.T) {
	state := RestoreState{
		ClusterName: "my-cluster",
		NodeGroups: map[string]NodeGroupState{
			"ng-1": {DesiredSize: 3, MinSize: 1, MaxSize: 5},
			"ng-2": {DesiredSize: 5, MinSize: 2, MaxSize: 10},
		},
	}

	data, err := json.Marshal(state)
	assert.NoError(t, err)

	var decoded RestoreState
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, "my-cluster", decoded.ClusterName)
	assert.Equal(t, 2, len(decoded.NodeGroups))
	assert.Equal(t, int32(3), decoded.NodeGroups["ng-1"].DesiredSize)
	assert.Equal(t, int32(5), decoded.NodeGroups["ng-2"].DesiredSize)
}

// ============================================================================
// K8S client integration tests
// ============================================================================

func TestWaitForNodesDeleted_Success(t *testing.T) {
	ctx := context.Background()
	mockK8S := &mocks.K8SClient{}

	// First call: 2 nodes exist
	// Second call: 0 nodes exist (deleted)
	callCount := 0
	mockK8S.On("ListNode", mock.Anything, "eks.amazonaws.com/nodegroup=ng-1").Return(
		func(ctx context.Context, selector string) *corev1.NodeList {
			callCount++
			if callCount == 1 {
				return &corev1.NodeList{
					Items: []corev1.Node{
						{}, {}, // 2 nodes
					},
				}
			}
			return &corev1.NodeList{Items: []corev1.Node{}} // No nodes
		},
		func(ctx context.Context, selector string) error {
			return nil
		},
	)

	e := New()

	err := e.waitForNodesDeleted(ctx, logr.Discard(), mockK8S, "my-cluster", "ng-1", "30s")
	assert.NoError(t, err)

	mockK8S.AssertExpectations(t)
}

func TestWaitForNodesDeleted_ListNodeError(t *testing.T) {
	ctx := context.Background()
	mockK8S := &mocks.K8SClient{}

	mockK8S.On("ListNode", mock.Anything, mock.Anything).Return(
		nil, errors.New("API error"),
	)

	e := New()

	err := e.waitForNodesDeleted(ctx, logr.Discard(), mockK8S, "my-cluster", "ng-1", "5s")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "list nodes")
}

func TestNodeGroupState_JSON(t *testing.T) {
	state := NodeGroupState{
		DesiredSize: 3,
		MinSize:     1,
		MaxSize:     5,
	}

	data, _ := json.Marshal(state)
	var decoded NodeGroupState
	json.Unmarshal(data, &decoded)

	assert.Equal(t, int32(3), decoded.DesiredSize)
	assert.Equal(t, int32(1), decoded.MinSize)
	assert.Equal(t, int32(5), decoded.MaxSize)
}

func TestParameters_JSON(t *testing.T) {
	params := Parameters{
		ClusterName: "my-cluster",
		NodeGroups: []NodeGroup{
			{Name: "ng-1"},
			{Name: "ng-2"},
		},
	}

	data, _ := json.Marshal(params)
	var decoded Parameters
	json.Unmarshal(data, &decoded)

	assert.Equal(t, "my-cluster", decoded.ClusterName)
	assert.Equal(t, 2, len(decoded.NodeGroups))
}

func TestExecutorType_Constant(t *testing.T) {
	assert.Equal(t, "eks", ExecutorType)
}

// ============================================================================
// Helper method tests
// ============================================================================

func TestGetClusterInfo_Success(t *testing.T) {
	ctx := context.Background()
	mockEKS := &mocks.EKSClient{}

	caDataEncoded := base64.StdEncoding.EncodeToString([]byte("test-ca-data"))

	mockEKS.On("DescribeCluster", mock.Anything, &eks.DescribeClusterInput{
		Name: aws.String("my-cluster"),
	}).Return(&eks.DescribeClusterOutput{
		Cluster: &types.Cluster{
			Endpoint: aws.String("https://eks.example.com"),
			CertificateAuthority: &types.Certificate{
				Data: aws.String(caDataEncoded),
			},
		},
	}, nil)

	eksFactory := func(cfg aws.Config) EKSClient { return mockEKS }
	e := NewWithClients(eksFactory, nil, nil)

	info, err := e.getClusterInfo(ctx, mockEKS, "my-cluster")
	assert.NoError(t, err)
	assert.NotNil(t, info)
	assert.Equal(t, "https://eks.example.com", info.Endpoint)
	assert.Equal(t, []byte("test-ca-data"), info.CAData)

	mockEKS.AssertExpectations(t)
}

func TestGetClusterInfo_ClusterNotFound(t *testing.T) {
	ctx := context.Background()
	mockEKS := &mocks.EKSClient{}

	mockEKS.On("DescribeCluster", mock.Anything, mock.Anything).Return(
		&eks.DescribeClusterOutput{Cluster: nil}, nil,
	)

	eksFactory := func(cfg aws.Config) EKSClient { return mockEKS }
	e := NewWithClients(eksFactory, nil, nil)

	_, err := e.getClusterInfo(ctx, mockEKS, "my-cluster")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGetClusterInfo_MissingEndpoint(t *testing.T) {
	ctx := context.Background()
	mockEKS := &mocks.EKSClient{}

	mockEKS.On("DescribeCluster", mock.Anything, mock.Anything).Return(
		&eks.DescribeClusterOutput{
			Cluster: &types.Cluster{
				Endpoint: nil, // Missing endpoint
				CertificateAuthority: &types.Certificate{
					Data: aws.String("dGVzdA=="),
				},
			},
		}, nil,
	)

	eksFactory := func(cfg aws.Config) EKSClient { return mockEKS }
	e := NewWithClients(eksFactory, nil, nil)

	_, err := e.getClusterInfo(ctx, mockEKS, "my-cluster")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "endpoint not available")
}

func TestGetClusterInfo_InvalidCAData(t *testing.T) {
	ctx := context.Background()
	mockEKS := &mocks.EKSClient{}

	mockEKS.On("DescribeCluster", mock.Anything, mock.Anything).Return(
		&eks.DescribeClusterOutput{
			Cluster: &types.Cluster{
				Endpoint: aws.String("https://eks.example.com"),
				CertificateAuthority: &types.Certificate{
					Data: aws.String("invalid-base64!!!"),
				},
			},
		}, nil,
	)

	eksFactory := func(cfg aws.Config) EKSClient { return mockEKS }
	e := NewWithClients(eksFactory, nil, nil)

	_, err := e.getClusterInfo(ctx, mockEKS, "my-cluster")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "decode certificate authority")
}

func TestDetermineTargetNodeGroups_SpecificNodeGroups(t *testing.T) {
	ctx := context.Background()
	mockEKS := &mocks.EKSClient{}

	eksFactory := func(cfg aws.Config) EKSClient { return mockEKS }
	e := NewWithClients(eksFactory, nil, nil)

	params := Parameters{
		ClusterName: "my-cluster",
		NodeGroups: []NodeGroup{
			{Name: "ng-1"},
			{Name: "ng-2"},
		},
	}

	result, err := e.determineTargetNodeGroups(ctx, logr.Discard(), mockEKS, "my-cluster", params)
	assert.NoError(t, err)
	assert.Equal(t, []string{"ng-1", "ng-2"}, result)

	// Should not call ListNodegroups when specific node groups are provided
	mockEKS.AssertNotCalled(t, "ListNodegroups")
}

func TestDetermineTargetNodeGroups_AllNodeGroups(t *testing.T) {
	ctx := context.Background()
	mockEKS := &mocks.EKSClient{}

	mockEKS.On("ListNodegroups", mock.Anything, &eks.ListNodegroupsInput{
		ClusterName: aws.String("my-cluster"),
	}).Return(&eks.ListNodegroupsOutput{
		Nodegroups: []string{"ng-a", "ng-b", "ng-c"},
	}, nil)

	eksFactory := func(cfg aws.Config) EKSClient { return mockEKS }
	e := NewWithClients(eksFactory, nil, nil)

	params := Parameters{
		ClusterName: "my-cluster",
		NodeGroups:  []NodeGroup{}, // Empty means all
	}

	result, err := e.determineTargetNodeGroups(ctx, logr.Discard(), mockEKS, "my-cluster", params)
	assert.NoError(t, err)
	assert.Equal(t, []string{"ng-a", "ng-b", "ng-c"}, result)

	mockEKS.AssertExpectations(t)
}

func TestDetermineTargetNodeGroups_NoNodeGroupsFound(t *testing.T) {
	ctx := context.Background()
	mockEKS := &mocks.EKSClient{}

	mockEKS.On("ListNodegroups", mock.Anything, mock.Anything).Return(
		&eks.ListNodegroupsOutput{Nodegroups: []string{}}, nil,
	)

	eksFactory := func(cfg aws.Config) EKSClient { return mockEKS }
	e := NewWithClients(eksFactory, nil, nil)

	params := Parameters{
		ClusterName: "my-cluster",
		NodeGroups:  []NodeGroup{}, // Empty means all
	}

	_, err := e.determineTargetNodeGroups(ctx, logr.Discard(), mockEKS, "my-cluster", params)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no node groups found")
}

func TestSetupK8SClient_Success(t *testing.T) {
	ctx := context.Background()
	mockEKS := &mocks.EKSClient{}
	mockK8S := &mocks.K8SClient{}

	caDataEncoded := base64.StdEncoding.EncodeToString([]byte("test-ca-data"))

	mockEKS.On("DescribeCluster", mock.Anything, &eks.DescribeClusterInput{
		Name: aws.String("my-cluster"),
	}).Return(&eks.DescribeClusterOutput{
		Cluster: &types.Cluster{
			Endpoint: aws.String("https://eks.example.com"),
			CertificateAuthority: &types.Certificate{
				Data: aws.String(caDataEncoded),
			},
		},
	}, nil)

	eksFactory := func(cfg aws.Config) EKSClient { return mockEKS }
	k8sFactory := func(ctx context.Context, spec *executor.Spec) (K8SClient, error) {
		return mockK8S, nil
	}

	e := NewWithClients(eksFactory, nil, nil)
	e.k8sFactory = k8sFactory

	spec := executor.Spec{
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	cfg := aws.Config{Region: "us-east-1"}

	client, err := e.setupK8SClient(ctx, logr.Discard(), mockEKS, cfg, &spec, "my-cluster")
	assert.NoError(t, err)
	assert.NotNil(t, client)
	assert.NotNil(t, spec.ConnectorConfig.K8S)
	assert.Equal(t, "my-cluster", spec.ConnectorConfig.K8S.ClusterName)
	assert.Equal(t, "https://eks.example.com", spec.ConnectorConfig.K8S.ClusterEndpoint)
	assert.Equal(t, []byte("test-ca-data"), spec.ConnectorConfig.K8S.ClusterCAData)
	assert.True(t, spec.ConnectorConfig.K8S.UseEKSToken)

	mockEKS.AssertExpectations(t)
}

func TestSetupK8SClient_ClusterInfoError(t *testing.T) {
	ctx := context.Background()
	mockEKS := &mocks.EKSClient{}

	mockEKS.On("DescribeCluster", mock.Anything, mock.Anything).Return(
		nil, errors.New("cluster not found"),
	)

	eksFactory := func(cfg aws.Config) EKSClient { return mockEKS }
	e := NewWithClients(eksFactory, nil, nil)

	spec := executor.Spec{
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	cfg := aws.Config{Region: "us-east-1"}

	_, err := e.setupK8SClient(ctx, logr.Discard(), mockEKS, cfg, &spec, "my-cluster")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cluster not found")
}

func TestSetupK8SClient_K8SFactoryError(t *testing.T) {
	ctx := context.Background()
	mockEKS := &mocks.EKSClient{}

	caDataEncoded := base64.StdEncoding.EncodeToString([]byte("test-ca-data"))

	mockEKS.On("DescribeCluster", mock.Anything, mock.Anything).Return(
		&eks.DescribeClusterOutput{
			Cluster: &types.Cluster{
				Endpoint: aws.String("https://eks.example.com"),
				CertificateAuthority: &types.Certificate{
					Data: aws.String(caDataEncoded),
				},
			},
		}, nil,
	)

	eksFactory := func(cfg aws.Config) EKSClient { return mockEKS }
	k8sFactory := func(ctx context.Context, spec *executor.Spec) (K8SClient, error) {
		return nil, errors.New("k8s client creation failed")
	}

	e := NewWithClients(eksFactory, nil, nil)
	e.k8sFactory = k8sFactory

	spec := executor.Spec{
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	cfg := aws.Config{Region: "us-east-1"}

	_, err := e.setupK8SClient(ctx, logr.Discard(), mockEKS, cfg, &spec, "my-cluster")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create Kubernetes client")
}
