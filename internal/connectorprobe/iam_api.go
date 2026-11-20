package connectorprobe

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/iam"
)

// IAMAPI is the subset of the IAM client used for policy simulation.
type IAMAPI interface {
	SimulatePrincipalPolicy(ctx context.Context, params *iam.SimulatePrincipalPolicyInput, optFns ...func(*iam.Options)) (*iam.SimulatePrincipalPolicyOutput, error)
}
