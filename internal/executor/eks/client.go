package eks

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// EKSClient is the interface for AWS EKS operations.
// It defines the minimal set of EKS API methods needed by the executor.
type EKSClient interface {
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
