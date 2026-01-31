/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package k8sutil

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"k8s.io/client-go/rest"

	"github.com/ardikabs/hibernator/pkg/awsutil"
)

// eksTokenSource manages EKS authentication tokens with caching and expiration.
type eksTokenSource struct {
	mu          sync.Mutex
	clusterName string
	region      string
	awsConfig   aws.Config
	token       string
	expiration  time.Time
}

// newEKSTokenSource creates a new EKS token source.
// It validates required configuration (clusterName, AWS credentials) and builds
// the AWS config needed for presigned URL signing.
func newEKSTokenSource(ctx context.Context, cfg *K8SConnectorConfig) (*eksTokenSource, error) {
	if cfg.ClusterName == "" {
		return nil, fmt.Errorf("clusterName is required for EKS token generation")
	}
	if cfg.AWS == nil {
		return nil, fmt.Errorf("AWS connector config is required for EKS token generation")
	}
	if cfg.Region == "" {
		return nil, fmt.Errorf("region is required for EKS token generation")
	}

	// Build AWS config
	awsConfig, err := awsutil.BuildAWSConfig(ctx, cfg.AWS)
	if err != nil {
		return nil, fmt.Errorf("build AWS config: %w", err)
	}

	return &eksTokenSource{
		clusterName: cfg.ClusterName,
		region:      cfg.Region,
		awsConfig:   awsConfig,
	}, nil
}

// getToken retrieves a valid EKS authentication token.
// Tokens are cached until 3 minutes before expiration.
// It generates a presigned STS GetCallerIdentity request and encodes it as a bearer token.
func (s *eksTokenSource) getToken(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Return cached token if still valid (with 3 minute buffer)
	if s.token != "" && time.Now().Before(s.expiration.Add(-3*time.Minute)) {
		return s.token, nil
	}

	// Generate new token
	token, expiration, err := s.generatePresignedToken(ctx)
	if err != nil {
		return "", fmt.Errorf("generate presigned token: %w", err)
	}

	s.token = token
	s.expiration = expiration
	return token, nil
}

// generatePresignedToken creates a presigned STS GetCallerIdentity request
// and encodes it as an EKS authentication token in the format: k8s-aws-v1.<base64-url>
func (s *eksTokenSource) generatePresignedToken(ctx context.Context) (string, time.Time, error) {
	const emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	presignDuration := 15 * time.Minute
	signTime := time.Now().UTC()
	expiresSeconds := strconv.FormatInt(int64(presignDuration/time.Second), 10)

	// Build STS GetCallerIdentity request
	stsEndpoint := fmt.Sprintf("https://sts.%s.amazonaws.com/", s.region)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, stsEndpoint, nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("create request: %w", err)
	}

	// Set query parameters for GetCallerIdentity + expiry
	query := req.URL.Query()
	query.Set("Action", "GetCallerIdentity")
	query.Set("X-Amz-Expires", expiresSeconds)
	req.URL.RawQuery = query.Encode()

	// Add custom header for cluster identification
	req.Header.Set("x-k8s-aws-id", s.clusterName)

	// Get AWS credentials from config
	creds, err := s.awsConfig.Credentials.Retrieve(ctx)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("retrieve AWS credentials: %w", err)
	}

	// Initialize SigV4 signer
	signer := v4.NewSigner()

	// Presign the request using query parameters
	signedURL, _, err := signer.PresignHTTP(ctx, creds, req, emptyPayloadHash, "sts", s.region, signTime)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("presign request: %w", err)
	}

	// Encode presigned URL as base64-url and format as EKS token
	encodedURL := base64.RawURLEncoding.EncodeToString([]byte(signedURL))
	token := fmt.Sprintf("k8s-aws-v1.%s", encodedURL)

	return token, signTime.Add(presignDuration), nil
}

// eksTokenRoundTripper wraps an HTTP transport to inject EKS bearer tokens.
type eksTokenRoundTripper struct {
	base   http.RoundTripper
	source *eksTokenSource
}

// RoundTrip implements http.RoundTripper by injecting a bearer token.
// If token generation fails, the error is returned immediately (fail-fast).
func (rt *eksTokenRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = clone.Header.Clone()

	tokenValue, err := rt.source.getToken(req.Context())
	if err != nil {
		// Fail-fast: return error immediately without retries
		return nil, fmt.Errorf("EKS token generation failed: %w", err)
	}

	clone.Header.Set("Authorization", "Bearer "+tokenValue)
	return rt.base.RoundTrip(clone)
}

// wrapTokenTransport chains the EKS token round-tripper into the REST config's transport.
func wrapTokenTransport(restConfig *rest.Config, source *eksTokenSource) {
	wrap := func(rt http.RoundTripper) http.RoundTripper {
		if rt == nil {
			rt = http.DefaultTransport
		}
		return &eksTokenRoundTripper{base: rt, source: source}
	}

	if restConfig.WrapTransport == nil {
		restConfig.WrapTransport = wrap
		return
	}

	prev := restConfig.WrapTransport
	restConfig.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		return wrap(prev(rt))
	}
}
