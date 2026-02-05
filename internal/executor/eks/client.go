package eks

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// K8SClient provides Kubernetes API operations needed by the EKS executor.
// This interface is used to monitor and verify the state of nodes managed by EKS
// Managed Node Groups during the hibernation lifecycle.
//
// The primary use case is waiting for nodes to be deleted after scaling a node group
// to zero during the shutdown phase. This ensures that the hibernation process has
// fully completed before marking the operation as successful.
type K8SClient interface {
	// ListNode retrieves all Node resources matching the given label selector.
	// The selector typically targets nodes with the "eks.amazonaws.com/nodegroup" label
	// to identify nodes belonging to a specific EKS Managed Node Group.
	ListNode(ctx context.Context, selector string) (*corev1.NodeList, error)
}

type k8sClient struct {
	Typed kubernetes.Interface
}

func (c *k8sClient) ListNode(ctx context.Context, selector string) (*corev1.NodeList, error) {
	return c.Typed.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
}

// EKSClient is the interface for AWS EKS operations.
// It defines the minimal set of EKS API methods needed by the executor.
type EKSClient interface {
	DescribeCluster(ctx context.Context,
		params *eks.DescribeClusterInput,
		optFns ...func(*eks.Options),
	) (*eks.DescribeClusterOutput, error)

	// ListNodegroups lists all node groups in an EKS cluster.
	ListNodegroups(
		ctx context.Context,
		params *eks.ListNodegroupsInput,
		optFns ...func(*eks.Options),
	) (*eks.ListNodegroupsOutput, error)

	// DescribeNodegroup describes a specific node group.
	DescribeNodegroup(
		ctx context.Context,
		params *eks.DescribeNodegroupInput,
		optFns ...func(*eks.Options),
	) (*eks.DescribeNodegroupOutput, error)

	// UpdateNodegroupConfig updates a node group's scaling config.
	UpdateNodegroupConfig(
		ctx context.Context,
		params *eks.UpdateNodegroupConfigInput,
		optFns ...func(*eks.Options),
	) (*eks.UpdateNodegroupConfigOutput, error)
}

// STSClient is the interface for AWS STS operations used for role assumption.
type STSClient interface {
	// AssumeRole returns temporary credentials for a role.
	AssumeRole(
		ctx context.Context,
		params *sts.AssumeRoleInput,
		optFns ...func(*sts.Options),
	) (*sts.AssumeRoleOutput, error)

	// GetCallerIdentity returns information about the AWS account and caller.
	GetCallerIdentity(
		ctx context.Context,
		params *sts.GetCallerIdentityInput,
		optFns ...func(*sts.Options),
	) (*sts.GetCallerIdentityOutput, error)
}
