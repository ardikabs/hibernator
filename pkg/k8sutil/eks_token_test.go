/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package k8sutil

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ardikabs/hibernator/pkg/awsutil"
)

// TestNewEKSTokenSource_Success validates successful token source creation.
func TestNewEKSTokenSource_Success(t *testing.T) {
	cfg := &K8SConnectorConfig{
		ClusterName: "test-cluster",
		Region:      "us-east-1",
		AWS: &awsutil.AWSConnectorConfig{
			Region:          "us-east-1",
			AccountID:       "123456789012",
			AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
			SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		},
	}

	ctx := context.Background()
	source, err := newEKSTokenSource(ctx, cfg)
	require.NoError(t, err)
	assert.NotNil(t, source)
	assert.Equal(t, "test-cluster", source.clusterName)
	assert.Equal(t, "us-east-1", source.region)
}

// TestNewEKSTokenSource_MissingClusterName validates error when cluster name is missing.
func TestNewEKSTokenSource_MissingClusterName(t *testing.T) {
	cfg := &K8SConnectorConfig{
		ClusterName: "",
		Region:      "us-east-1",
		AWS: &awsutil.AWSConnectorConfig{
			Region:          "us-east-1",
			AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
			SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		},
	}

	ctx := context.Background()
	source, err := newEKSTokenSource(ctx, cfg)
	assert.Error(t, err)
	assert.Nil(t, source)
	assert.Contains(t, err.Error(), "clusterName is required")
}

// TestNewEKSTokenSource_MissingRegion validates error when region is missing.
func TestNewEKSTokenSource_MissingRegion(t *testing.T) {
	cfg := &K8SConnectorConfig{
		ClusterName: "test-cluster",
		Region:      "",
		AWS: &awsutil.AWSConnectorConfig{
			Region:          "us-east-1",
			AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
			SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		},
	}

	ctx := context.Background()
	source, err := newEKSTokenSource(ctx, cfg)
	assert.Error(t, err)
	assert.Nil(t, source)
	assert.Contains(t, err.Error(), "region is required")
}

// TestNewEKSTokenSource_MissingAWSConfig validates error when AWS config is missing.
func TestNewEKSTokenSource_MissingAWSConfig(t *testing.T) {
	cfg := &K8SConnectorConfig{
		ClusterName: "test-cluster",
		Region:      "us-east-1",
		AWS:         nil,
	}

	ctx := context.Background()
	source, err := newEKSTokenSource(ctx, cfg)
	assert.Error(t, err)
	assert.Nil(t, source)
	assert.Contains(t, err.Error(), "AWS connector config is required")
}

// TestGetToken_CachesValidTokens validates that tokens are cached until 3-minute buffer.
func TestGetToken_CachesValidTokens(t *testing.T) {
	source := &eksTokenSource{
		clusterName: "test-cluster",
		region:      "us-east-1",
		token:       "k8s-aws-v1.test-token",
		expiration:  time.Now().Add(10 * time.Minute),
	}

	ctx := context.Background()
	retrievedToken, err := source.getToken(ctx)
	require.NoError(t, err)
	assert.Equal(t, "k8s-aws-v1.test-token", retrievedToken)
}

// TestGeneratePresignedToken_TokenFormat validates the token format.
func TestGeneratePresignedToken_TokenFormat(t *testing.T) {
	cfg := &K8SConnectorConfig{
		ClusterName: "test-cluster",
		Region:      "us-east-1",
		AWS: &awsutil.AWSConnectorConfig{
			Region:          "us-east-1",
			AccountID:       "123456789012",
			AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
			SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		},
	}

	ctx := context.Background()
	source, err := newEKSTokenSource(ctx, cfg)
	require.NoError(t, err)

	generatedToken, expiration, err := source.generatePresignedToken(ctx)
	require.NoError(t, err)

	// Token format: k8s-aws-v1.<base64-encoded-url>
	assert.True(t, strings.HasPrefix(generatedToken, "k8s-aws-v1."))
	parts := strings.Split(generatedToken, ".")
	assert.Len(t, parts, 2)
	assert.Equal(t, "k8s-aws-v1", parts[0])

	// Verify base64 part is valid
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	assert.NotEmpty(t, string(decoded))

	// Verify decoded content looks like a URL
	assert.True(t, strings.HasPrefix(string(decoded), "https://"))

	// Verify expiration is set (should be ~15 minutes from now)
	now := time.Now()
	assert.True(t, expiration.After(now.Add(14*time.Minute)))
	assert.True(t, expiration.Before(now.Add(16*time.Minute)))
}

// TestGeneratePresignedToken_ContainsActionParameter validates Action parameter.
func TestGeneratePresignedToken_ContainsActionParameter(t *testing.T) {
	cfg := &K8SConnectorConfig{
		ClusterName: "test-cluster",
		Region:      "eu-west-1",
		AWS: &awsutil.AWSConnectorConfig{
			Region:          "eu-west-1",
			AccountID:       "123456789012",
			AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
			SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		},
	}

	ctx := context.Background()
	source, err := newEKSTokenSource(ctx, cfg)
	require.NoError(t, err)

	generatedToken, _, err := source.generatePresignedToken(ctx)
	require.NoError(t, err)

	// Decode token to verify structure
	parts := strings.Split(generatedToken, ".")
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)

	presignedURL := string(decoded)
	parsedURL, err := url.Parse(presignedURL)
	require.NoError(t, err)

	// Verify Action parameter is present
	assert.Equal(t, "GetCallerIdentity", parsedURL.Query().Get("Action"))
}

// TestGeneratePresignedToken_ContainsCorrectRegion validates correct region in URL.
func TestGeneratePresignedToken_ContainsCorrectRegion(t *testing.T) {
	testRegions := []string{"us-east-1", "ap-southeast-2", "eu-central-1"}

	for _, region := range testRegions {
		t.Run(fmt.Sprintf("region_%s", region), func(t *testing.T) {
			cfg := &K8SConnectorConfig{
				ClusterName: "test-cluster",
				Region:      region,
				AWS: &awsutil.AWSConnectorConfig{
					Region:          region,
					AccountID:       "123456789012",
					AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
					SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
				},
			}

			ctx := context.Background()
			source, err := newEKSTokenSource(ctx, cfg)
			require.NoError(t, err)

			generatedToken, _, err := source.generatePresignedToken(ctx)
			require.NoError(t, err)

			// Decode and verify region
			parts := strings.Split(generatedToken, ".")
			decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
			require.NoError(t, err)

			presignedURL := string(decoded)
			assert.True(t, strings.Contains(presignedURL, fmt.Sprintf("sts.%s.amazonaws.com", region)))
		})
	}
}

// TestTokenCaching_MultipleCallsReuseToken validates that multiple calls reuse cached token.
func TestTokenCaching_MultipleCallsReuseToken(t *testing.T) {
	cfg := &K8SConnectorConfig{
		ClusterName: "test-cluster",
		Region:      "us-east-1",
		AWS: &awsutil.AWSConnectorConfig{
			Region:          "us-east-1",
			AccountID:       "123456789012",
			AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
			SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		},
	}

	ctx := context.Background()
	source, err := newEKSTokenSource(ctx, cfg)
	require.NoError(t, err)

	// Set a valid cached token
	source.token = "k8s-aws-v1.cached-token"
	source.expiration = time.Now().Add(10 * time.Minute)

	// Multiple calls should return the same cached token
	token1, err := source.getToken(ctx)
	require.NoError(t, err)

	token2, err := source.getToken(ctx)
	require.NoError(t, err)

	assert.Equal(t, token1, token2)
	assert.Equal(t, "k8s-aws-v1.cached-token", token1)
}

// TestTokenExpiration_RefreshesNearBuffer validates token refresh behavior near 3-minute buffer.
func TestTokenExpiration_RefreshesNearBuffer(t *testing.T) {
	cfg := &K8SConnectorConfig{
		ClusterName: "test-cluster",
		Region:      "us-east-1",
		AWS: &awsutil.AWSConnectorConfig{
			Region:          "us-east-1",
			AccountID:       "123456789012",
			AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
			SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		},
	}

	ctx := context.Background()
	source, err := newEKSTokenSource(ctx, cfg)
	require.NoError(t, err)

	// Test boundary conditions for 3-minute buffer
	testCases := []struct {
		name           string
		exprMins       float64 // minutes from now
		shouldUseCache bool
	}{
		{"Just within cache", 3.5, true},
		{"Just at buffer edge", 3.0, false},
		{"Just before buffer", 2.9, false},
		{"Well before buffer", 5.0, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			source.token = "k8s-aws-v1.test-token"
			source.expiration = time.Now().Add(time.Duration(tc.exprMins*60) * time.Second)

			retrievedToken, err := source.getToken(ctx)

			if tc.shouldUseCache {
				// Should return cached token successfully
				assert.NoError(t, err)
				assert.Equal(t, "k8s-aws-v1.test-token", retrievedToken)
			} else {
				// Should generate a new token
				assert.NoError(t, err)
				assert.True(t, strings.HasPrefix(retrievedToken, "k8s-aws-v1."))
				assert.NotEqual(t, "k8s-aws-v1.test-token", retrievedToken)
			}
		})
	}
}
