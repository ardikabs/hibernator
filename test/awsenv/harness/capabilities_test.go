//go:build awsenv

package harness

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsUnsupportedMatchesUnknownOperations(t *testing.T) {
	require.True(t, IsUnsupported(errors.New("UnknownOperationException: stop")))
	require.True(t, IsUnsupported(errors.New("NotImplemented: UpdateNodegroupConfig")))
	require.True(t, IsUnsupported(errors.New("OperationNotSupported")))
	// The emulator S3-fallthrough shape is deliberately NOT global: it is
	// scoped to UpdateNodegroupConfig via isEmulatorFallthrough.
	require.False(t, IsUnsupported(errors.New("operation error EKS: UpdateNodegroupConfig, https response error StatusCode: 400, deserialization failed, failed to decode response body, invalid character '<' looking for beginning of value")))
	require.False(t, IsUnsupported(errors.New("operation error RDS: StopDBInstance, https response error StatusCode: 500, <html>Internal Error</html>")))
	require.False(t, IsUnsupported(errors.New("unexpected end of JSON input")))
	require.False(t, IsUnsupported(errors.New("AccessDenied: not authorized")))
	require.False(t, IsUnsupported(errors.New("DBInstanceNotFound")))
	require.False(t, IsUnsupported(nil))
}

func TestEmulatorFallthroughScopedToUpdateNodegroupConfig(t *testing.T) {
	fallthroughErr := errors.New("operation error EKS: UpdateNodegroupConfig, https response error StatusCode: 400, RequestID: 037043aa-6dd2-4e51-a5b7-6ac19c22d8dd, deserialization failed, failed to decode response body, invalid character '<' looking for beginning of value")
	require.True(t, isEmulatorFallthrough("UpdateNodegroupConfig", fallthroughErr))
	require.False(t, isEmulatorFallthrough("StopDBInstance", fallthroughErr))
	require.False(t, isEmulatorFallthrough("UpdateNodegroupConfig", errors.New("operation error EKS: UpdateNodegroupConfig, https response error StatusCode: 400, <html>proxy error</html>")))
	require.False(t, isEmulatorFallthrough("UpdateNodegroupConfig", errors.New("unexpected end of JSON input")))
	require.False(t, isEmulatorFallthrough("UpdateNodegroupConfig", nil))
}

func TestRequireNoErrorOrSkipPassesThroughNil(t *testing.T) {
	RequireNoErrorOrSkip(t, CapabilityEC2Lifecycle, "StopInstances", nil)
}

func TestResolveProviderDefaultsAndRejects(t *testing.T) {
	t.Setenv("AWSENV_PROVIDER", "")
	t.Setenv("AWSENV_ENDPOINT_URL", "")
	provider, err := resolveProvider()
	require.NoError(t, err)
	require.Equal(t, "floci", provider)

	t.Setenv("AWSENV_PROVIDER", "real-aws")
	_, err = resolveProvider()
	require.ErrorContains(t, err, "unsupported AWSENV_PROVIDER")
}

func TestResolveProviderEndpointFidelity(t *testing.T) {
	t.Setenv("AWSENV_PROVIDER", "")

	t.Setenv("AWSENV_ENDPOINT_URL", "")
	provider, err := resolveProvider()
	require.NoError(t, err)
	require.Equal(t, "floci", provider)

	t.Setenv("AWSENV_ENDPOINT_URL", "http://localhost:4566")
	provider, err = resolveProvider()
	require.NoError(t, err)
	require.Equal(t, "floci", provider)

	t.Setenv("AWSENV_ENDPOINT_URL", "http://custom:4566")
	_, err = resolveProvider()
	require.ErrorContains(t, err, "AWSENV_PROVIDER")
}
