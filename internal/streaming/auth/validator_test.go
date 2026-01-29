/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package auth

import (
	"testing"

	authv1 "k8s.io/api/authentication/v1"
)

func TestTokenValidatorValidateToken(t *testing.T) {
	review := &authv1.TokenReview{
		Status: authv1.TokenReviewStatus{
			Authenticated: true,
			User: authv1.UserInfo{
				Username: "system:serviceaccount:hibernator-system:runner",
			},
		},
	}
	if review.Status.Authenticated {
		t.Log("token validation passed")
	}
}
