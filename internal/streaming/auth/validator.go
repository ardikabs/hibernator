/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	authv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// ExpectedAudience is the audience expected in runner tokens.
	ExpectedAudience = "hibernator-control-plane"

	// BearerPrefix is the prefix for bearer tokens.
	BearerPrefix = "Bearer "
)

// TokenValidator validates projected ServiceAccount tokens using TokenReview.
type TokenValidator struct {
	clientset *kubernetes.Clientset
	log       logr.Logger
	audience  string
}

// NewTokenValidator creates a new token validator.
func NewTokenValidator(clientset *kubernetes.Clientset, log logr.Logger) *TokenValidator {
	return &TokenValidator{
		clientset: clientset,
		log:       log.WithName("token-validator"),
		audience:  ExpectedAudience,
	}
}

// ValidationResult contains the result of token validation.
type ValidationResult struct {
	// Valid indicates if the token is valid.
	Valid bool

	// Username is the authenticated user (e.g., system:serviceaccount:ns:name).
	Username string

	// Groups are the user's groups.
	Groups []string

	// Namespace is extracted from the service account.
	Namespace string

	// ServiceAccount is extracted from the username.
	ServiceAccount string

	// Error contains any validation error.
	Error error
}

// ValidateToken validates a bearer token using TokenReview.
func (v *TokenValidator) ValidateToken(ctx context.Context, token string) *ValidationResult {
	result := &ValidationResult{}

	// Strip Bearer prefix if present
	token = strings.TrimPrefix(token, BearerPrefix)
	if token == "" {
		result.Error = fmt.Errorf("empty token")
		return result
	}

	// Create TokenReview
	review := &authv1.TokenReview{
		Spec: authv1.TokenReviewSpec{
			Token:     token,
			Audiences: []string{v.audience},
		},
	}

	// Submit TokenReview
	reviewResult, err := v.clientset.AuthenticationV1().TokenReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		result.Error = fmt.Errorf("token review failed: %w", err)
		v.log.Error(err, "token review API call failed")
		return result
	}

	// Check authentication status
	if !reviewResult.Status.Authenticated {
		result.Error = fmt.Errorf("token not authenticated: %s", reviewResult.Status.Error)
		v.log.Info("token authentication failed", "error", reviewResult.Status.Error)
		return result
	}

	// Verify audience
	if len(reviewResult.Status.Audiences) == 0 {
		result.Error = fmt.Errorf("no audiences in token")
		return result
	}

	audienceValid := false
	for _, aud := range reviewResult.Status.Audiences {
		if aud == v.audience {
			audienceValid = true
			break
		}
	}
	if !audienceValid {
		result.Error = fmt.Errorf("audience mismatch: expected %s, got %v", v.audience, reviewResult.Status.Audiences)
		return result
	}

	// Extract namespace and service account from username
	// Format: system:serviceaccount:<namespace>:<name>
	parts := strings.Split(reviewResult.Status.User.Username, ":")
	if len(parts) == 4 && parts[0] == "system" && parts[1] == "serviceaccount" {
		result.Namespace = parts[2]
		result.ServiceAccount = parts[3]
	}

	result.Valid = true
	result.Username = reviewResult.Status.User.Username
	result.Groups = reviewResult.Status.User.Groups

	v.log.V(1).Info("token validated",
		"username", result.Username,
		"namespace", result.Namespace,
		"serviceAccount", result.ServiceAccount,
	)

	return result
}

// ExtractTokenFromHeader extracts a bearer token from an Authorization header.
func ExtractTokenFromHeader(authHeader string) string {
	if strings.HasPrefix(authHeader, BearerPrefix) {
		return strings.TrimPrefix(authHeader, BearerPrefix)
	}
	return authHeader
}
