package connectorprobe

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/pkg/awsutil"
)

type fakeSTS struct {
	out *sts.GetCallerIdentityOutput
	err error
}

func (f *fakeSTS) GetCallerIdentity(_ context.Context, _ *sts.GetCallerIdentityInput, _ ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return f.out, f.err
}

func TestAWSChecker_CheckAuth(t *testing.T) {
	identity := &sts.GetCallerIdentityOutput{
		Account: aws.String("111111111111"),
		Arn:     aws.String("arn:aws:sts::111111111111:assumed-role/r/s"),
	}

	tests := []struct {
		name         string
		cfg          *awsutil.AWSConnectorConfig
		buildErr     error
		sts          *fakeSTS
		wantReason   string
		wantContains []string
	}{
		{
			name: "success",
			cfg:  &awsutil.AWSConnectorConfig{AccountID: "111111111111"},
			sts:  &fakeSTS{out: identity},
		},
		{
			name: "empty account skips comparison",
			cfg:  &awsutil.AWSConnectorConfig{},
			sts:  &fakeSTS{out: identity},
		},
		{
			name:       "sts error",
			cfg:        &awsutil.AWSConnectorConfig{AccountID: "111111111111"},
			sts:        &fakeSTS{err: errors.New("denied")},
			wantReason: hibernatorv1alpha1.ReasonInvalidCredentials,
		},
		{
			name:       "build config error",
			cfg:        &awsutil.AWSConnectorConfig{},
			buildErr:   errors.New("bad config"),
			sts:        &fakeSTS{out: identity},
			wantReason: hibernatorv1alpha1.ReasonInvalidCredentials,
		},
		{
			name:         "account mismatch",
			cfg:          &awsutil.AWSConnectorConfig{AccountID: "222222222222"},
			sts:          &fakeSTS{out: identity},
			wantReason:   hibernatorv1alpha1.ReasonAccountMismatch,
			wantContains: []string{"111111111111", "222222222222"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &AWSChecker{
				BuildConfig: func(context.Context, *awsutil.AWSConnectorConfig) (aws.Config, error) {
					return aws.Config{Region: "us-east-1"}, tt.buildErr
				},
				NewSTS: func(aws.Config) STSAPI { return tt.sts },
			}
			id, aerr := c.CheckAuth(context.Background(), tt.cfg)
			if tt.wantReason != "" {
				require.NotNil(t, aerr)
				assert.Nil(t, id)
				assert.Equal(t, tt.wantReason, aerr.Reason)
				for _, s := range tt.wantContains {
					assert.True(t, strings.Contains(aerr.Message, s), aerr.Message)
				}
				return
			}
			require.Nil(t, aerr)
			require.NotNil(t, id)
			assert.Equal(t, "111111111111", id.AccountID)
			assert.Equal(t, "arn:aws:sts::111111111111:assumed-role/r/s", id.ARN)
			assert.Equal(t, "us-east-1", id.Config.Region)
		})
	}
}

func TestNewAWSChecker_Wired(t *testing.T) {
	c := NewAWSChecker()
	assert.NotNil(t, c.BuildConfig)
	assert.NotNil(t, c.NewSTS)
	assert.NotNil(t, c.NewIAM)
	assert.NotNil(t, c.Getenv)
}
