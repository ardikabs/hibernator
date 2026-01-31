package ec2

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// EC2Client is the interface for AWS EC2 operations.
// It defines the minimal set of EC2 API methods needed by the executor.
type EC2Client interface {
	// DescribeInstances describes one or more EC2 instances.
	DescribeInstances(
		ctx context.Context,
		params *ec2.DescribeInstancesInput,
		optFns ...func(*ec2.Options),
	) (*ec2.DescribeInstancesOutput, error)

	// StopInstances stops one or more running EC2 instances.
	StopInstances(
		ctx context.Context,
		params *ec2.StopInstancesInput,
		optFns ...func(*ec2.Options),
	) (*ec2.StopInstancesOutput, error)

	// StartInstances starts one or more stopped EC2 instances.
	StartInstances(
		ctx context.Context,
		params *ec2.StartInstancesInput,
		optFns ...func(*ec2.Options),
	) (*ec2.StartInstancesOutput, error)
}
