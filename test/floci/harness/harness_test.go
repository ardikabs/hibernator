//go:build floci

package harness

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/stretchr/testify/require"
)

func TestSetup_ConnectsToFloci(t *testing.T) {
	c := Setup(t)
	require.Equal(t, "us-east-1", c.Region)
	out, err := c.STS.GetCallerIdentity(context.Background(), &sts.GetCallerIdentityInput{})
	require.NoError(t, err)
	require.NotNil(t, out.Account)
}
