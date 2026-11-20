package connectorprobe

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/pkg/awsutil"
)

// STSAPI is the subset of the STS client used for the identity check.
type STSAPI interface {
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// AuthError is a failed authentication check; Reason is a Reason* constant.
type AuthError struct {
	Reason  string
	Message string
}

func (e *AuthError) Error() string { return e.Reason + ": " + e.Message }

// AWSIdentity is the verified caller identity and the config that produced it.
type AWSIdentity struct {
	AccountID string
	ARN       string
	Config    aws.Config
}

// AWSChecker verifies AWS credentials; its fields are injectable for tests.
type AWSChecker struct {
	BuildConfig func(ctx context.Context, cfg *awsutil.AWSConnectorConfig) (aws.Config, error)
	NewSTS      func(aws.Config) STSAPI
	NewIAM      func(aws.Config) IAMAPI
	Getenv      func(string) string
}

// NewAWSChecker returns a checker wired to the real AWS SDK.
func NewAWSChecker() *AWSChecker {
	return &AWSChecker{
		BuildConfig: awsutil.BuildAWSConfig,
		NewSTS:      func(c aws.Config) STSAPI { return sts.NewFromConfig(c) },
		NewIAM:      func(c aws.Config) IAMAPI { return iam.NewFromConfig(c) },
		Getenv:      os.Getenv,
	}
}

// CheckAuth builds the config (applying assume-role) and calls GetCallerIdentity.
func (c *AWSChecker) CheckAuth(ctx context.Context, cfg *awsutil.AWSConnectorConfig) (*AWSIdentity, *AuthError) {
	awsCfg, err := c.BuildConfig(ctx, cfg)
	if err != nil {
		return nil, &AuthError{Reason: hibernatorv1alpha1.ReasonInvalidCredentials, Message: fmt.Sprintf("build AWS config: %v", err)}
	}

	out, err := c.NewSTS(awsCfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, &AuthError{Reason: hibernatorv1alpha1.ReasonInvalidCredentials, Message: fmt.Sprintf("sts:GetCallerIdentity failed: %v", err)}
	}

	id := &AWSIdentity{
		AccountID: aws.ToString(out.Account),
		ARN:       aws.ToString(out.Arn),
		Config:    awsCfg,
	}
	if cfg.AccountID != "" && id.AccountID != cfg.AccountID {
		return nil, &AuthError{
			Reason:  hibernatorv1alpha1.ReasonAccountMismatch,
			Message: fmt.Sprintf("credentials belong to account %s, expected %s", id.AccountID, cfg.AccountID),
		}
	}
	return id, nil
}
