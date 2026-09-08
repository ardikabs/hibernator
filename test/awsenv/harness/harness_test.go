//go:build awsenv

package harness

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/stretchr/testify/require"
)

func TestSetup_ConnectsToBackend(t *testing.T) {
	c := Setup(t)
	require.Equal(t, "us-east-1", c.Clients.Region)
	out, err := c.Clients.STS.GetCallerIdentity(context.Background(), &sts.GetCallerIdentityInput{})
	require.NoError(t, err)
	require.NotNil(t, out.Account)
}
