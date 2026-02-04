package rds

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// RDSClient is the interface for AWS RDS operations used by the executor.
type RDSClient interface {
	DescribeDBInstances(
		ctx context.Context,
		params *rds.DescribeDBInstancesInput,
		optFns ...func(*rds.Options),
	) (*rds.DescribeDBInstancesOutput, error)

	DescribeDBClusters(
		ctx context.Context,
		params *rds.DescribeDBClustersInput,
		optFns ...func(*rds.Options),
	) (*rds.DescribeDBClustersOutput, error)

	CreateDBSnapshot(
		ctx context.Context,
		params *rds.CreateDBSnapshotInput,
		optFns ...func(*rds.Options),
	) (*rds.CreateDBSnapshotOutput, error)

	DescribeDBSnapshots(
		ctx context.Context,
		params *rds.DescribeDBSnapshotsInput,
		optFns ...func(*rds.Options),
	) (*rds.DescribeDBSnapshotsOutput, error)

	CreateDBClusterSnapshot(
		ctx context.Context,
		params *rds.CreateDBClusterSnapshotInput,
		optFns ...func(*rds.Options),
	) (*rds.CreateDBClusterSnapshotOutput, error)

	DescribeDBClusterSnapshots(
		ctx context.Context,
		params *rds.DescribeDBClusterSnapshotsInput,
		optFns ...func(*rds.Options),
	) (*rds.DescribeDBClusterSnapshotsOutput, error)

	StopDBInstance(
		ctx context.Context,
		params *rds.StopDBInstanceInput,
		optFns ...func(*rds.Options),
	) (*rds.StopDBInstanceOutput, error)

	StartDBInstance(
		ctx context.Context,
		params *rds.StartDBInstanceInput,
		optFns ...func(*rds.Options),
	) (*rds.StartDBInstanceOutput, error)

	StopDBCluster(
		ctx context.Context,
		params *rds.StopDBClusterInput,
		optFns ...func(*rds.Options),
	) (*rds.StopDBClusterOutput, error)

	StartDBCluster(
		ctx context.Context,
		params *rds.StartDBClusterInput,
		optFns ...func(*rds.Options),
	) (*rds.StartDBClusterOutput, error)

	ListTagsForResource(
		ctx context.Context,
		params *rds.ListTagsForResourceInput,
		optFns ...func(*rds.Options),
	) (*rds.ListTagsForResourceOutput, error)
}

// STSClient is the interface for AWS STS operations used for role assumption.
type STSClient interface {
	AssumeRole(
		ctx context.Context,
		params *sts.AssumeRoleInput,
		optFns ...func(*sts.Options),
	) (*sts.AssumeRoleOutput, error)
}
