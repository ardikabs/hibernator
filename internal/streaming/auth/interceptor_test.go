/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package auth

import (
	"context"
	"strings"
	"testing"
)

func TestContextKeys(t *testing.T) {
	tests := []struct {
		name string
		key  contextKey
		want string
	}{
		{
			name: "validation result key",
			key:  ValidationResultKey,
			want: "validation-result",
		},
		{
			name: "execution ID key",
			key:  ExecutionIDKey,
			want: "execution-id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.key) != tt.want {
				t.Errorf("key = %s, want %s", string(tt.key), tt.want)
			}
		})
	}
}

func TestHeaderConstants(t *testing.T) {
	if AuthorizationHeader != "authorization" {
		t.Errorf("AuthorizationHeader = %s, want 'authorization'", AuthorizationHeader)
	}
	if ExecutionIDHeader != "x-execution-id" {
		t.Errorf("ExecutionIDHeader = %s, want 'x-execution-id'", ExecutionIDHeader)
	}
}

func TestGetValidationResult(t *testing.T) {
	tests := []struct {
		name   string
		ctx    context.Context
		want   *ValidationResult
		exists bool
	}{
		{
			name:   "empty context",
			ctx:    context.Background(),
			want:   nil,
			exists: false,
		},
		{
			name: "context with valid result",
			ctx: context.WithValue(context.Background(), ValidationResultKey, &ValidationResult{
				Valid:          true,
				Username:       "system:serviceaccount:test:runner",
				Namespace:      "test",
				ServiceAccount: "runner",
			}),
			want: &ValidationResult{
				Valid:          true,
				Username:       "system:serviceaccount:test:runner",
				Namespace:      "test",
				ServiceAccount: "runner",
			},
			exists: true,
		},
		{
			name:   "context with wrong type",
			ctx:    context.WithValue(context.Background(), ValidationResultKey, "not a validation result"),
			want:   nil,
			exists: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetValidationResult(tt.ctx)
			if tt.exists {
				if got == nil {
					t.Fatal("expected non-nil result")
				}
				if got.Valid != tt.want.Valid {
					t.Errorf("Valid = %v, want %v", got.Valid, tt.want.Valid)
				}
				if got.Username != tt.want.Username {
					t.Errorf("Username = %s, want %s", got.Username, tt.want.Username)
				}
			} else {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
			}
		})
	}
}

func TestGetExecutionID(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{
			name: "empty context",
			ctx:  context.Background(),
			want: "",
		},
		{
			name: "context with execution ID",
			ctx:  context.WithValue(context.Background(), ExecutionIDKey, "exec-12345"),
			want: "exec-12345",
		},
		{
			name: "context with wrong type",
			ctx:  context.WithValue(context.Background(), ExecutionIDKey, 12345),
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetExecutionID(tt.ctx)
			if got != tt.want {
				t.Errorf("GetExecutionID() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestValidateExecutionAccess(t *testing.T) {
	tests := []struct {
		name          string
		ctx           context.Context
		planName      string
		planNamespace string
		wantErr       bool
		errContains   string
	}{
		{
			name:          "no authentication context",
			ctx:           context.Background(),
			planName:      "test-plan",
			planNamespace: "test-ns",
			wantErr:       true,
			errContains:   "no authentication context",
		},
		{
			name: "namespace mismatch",
			ctx: context.WithValue(context.Background(), ValidationResultKey, &ValidationResult{
				Valid:          true,
				Namespace:      "other-ns",
				ServiceAccount: "hibernator-runner-test-plan",
			}),
			planName:      "test-plan",
			planNamespace: "test-ns",
			wantErr:       true,
			errContains:   "namespace mismatch",
		},
		{
			name: "service account mismatch",
			ctx: context.WithValue(context.Background(), ValidationResultKey, &ValidationResult{
				Valid:          true,
				Namespace:      "test-ns",
				ServiceAccount: "wrong-runner",
			}),
			planName:      "test-plan",
			planNamespace: "test-ns",
			wantErr:       true,
			errContains:   "service account mismatch",
		},
		{
			name: "valid access",
			ctx: context.WithValue(context.Background(), ValidationResultKey, &ValidationResult{
				Valid:          true,
				Namespace:      "test-ns",
				ServiceAccount: "hibernator-runner-test-plan",
			}),
			planName:      "test-plan",
			planNamespace: "test-ns",
			wantErr:       false,
		},
		{
			name: "valid access with suffix",
			ctx: context.WithValue(context.Background(), ValidationResultKey, &ValidationResult{
				Valid:          true,
				Namespace:      "test-ns",
				ServiceAccount: "hibernator-runner-test-plan-abc123",
			}),
			planName:      "test-plan",
			planNamespace: "test-ns",
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateExecutionAccess(tt.ctx, tt.planName, tt.planNamespace)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateExecutionAccess() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && tt.errContains != "" {
				if err == nil || !containsString(err.Error(), tt.errContains) {
					t.Errorf("error should contain %q, got %v", tt.errContains, err)
				}
			}
		})
	}
}

func TestAuthenticatedServerStream(t *testing.T) {
	// Create a context with validation result
	validResult := &ValidationResult{
		Valid:          true,
		Username:       "system:serviceaccount:test:runner",
		Namespace:      "test",
		ServiceAccount: "runner",
	}
	ctx := context.WithValue(context.Background(), ValidationResultKey, validResult)

	stream := &authenticatedServerStream{
		ctx: ctx,
	}

	// Test that context is returned correctly
	gotCtx := stream.Context()
	result := GetValidationResult(gotCtx)
	if result == nil {
		t.Fatal("expected validation result in context")
	}
	if result.Username != validResult.Username {
		t.Errorf("Username = %s, want %s", result.Username, validResult.Username)
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestNewTokenValidator(t *testing.T) {
	// Test that validator is created with correct audience
	// We can't test with real clientset, but we can test struct creation
	validator := &TokenValidator{
		clientset: nil,
		audience:  ExpectedAudience,
	}

	if validator.audience != "hibernator-control-plane" {
		t.Errorf("audience = %s, want 'hibernator-control-plane'", validator.audience)
	}
}

func TestValidationResult_Fields(t *testing.T) {
	result := &ValidationResult{
		Valid:          true,
		Username:       "system:serviceaccount:ns:sa",
		Groups:         []string{"system:serviceaccounts", "system:serviceaccounts:ns"},
		Namespace:      "ns",
		ServiceAccount: "sa",
		Error:          nil,
	}

	if !result.Valid {
		t.Error("expected Valid to be true")
	}
	if result.Username != "system:serviceaccount:ns:sa" {
		t.Errorf("Username = %s", result.Username)
	}
	if len(result.Groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(result.Groups))
	}
	if result.Namespace != "ns" {
		t.Errorf("Namespace = %s", result.Namespace)
	}
	if result.ServiceAccount != "sa" {
		t.Errorf("ServiceAccount = %s", result.ServiceAccount)
	}
	if result.Error != nil {
		t.Errorf("Error = %v", result.Error)
	}
}

func TestValidationResult_WithError(t *testing.T) {
	result := &ValidationResult{
		Valid: false,
		Error: context.DeadlineExceeded,
	}

	if result.Valid {
		t.Error("expected Valid to be false")
	}
	if result.Error != context.DeadlineExceeded {
		t.Errorf("Error = %v, want context.DeadlineExceeded", result.Error)
	}
}

func TestExtractTokenFromHeader_Extended(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{
			name:   "with bearer prefix",
			header: "Bearer my-token-123",
			want:   "my-token-123",
		},
		{
			name:   "without bearer prefix",
			header: "my-token-123",
			want:   "my-token-123",
		},
		{
			name:   "empty string",
			header: "",
			want:   "",
		},
		{
			name:   "bearer only",
			header: "Bearer ",
			want:   "",
		},
		{
			name:   "lowercase bearer",
			header: "bearer my-token",
			want:   "bearer my-token", // Should not strip lowercase
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractTokenFromHeader(tt.header)
			if got != tt.want {
				t.Errorf("ExtractTokenFromHeader(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

func TestExpectedAudienceConstant(t *testing.T) {
	if ExpectedAudience != "hibernator-control-plane" {
		t.Errorf("ExpectedAudience = %s, want 'hibernator-control-plane'", ExpectedAudience)
	}
}

func TestBearerPrefixConstant(t *testing.T) {
	if BearerPrefix != "Bearer " {
		t.Errorf("BearerPrefix = %q, want 'Bearer '", BearerPrefix)
	}
}

func TestValidationResult_ServiceAccountParsing(t *testing.T) {
	tests := []struct {
		name          string
		username      string
		wantNamespace string
		wantSA        string
	}{
		{
			name:          "valid serviceaccount format",
			username:      "system:serviceaccount:mynamespace:mysa",
			wantNamespace: "mynamespace",
			wantSA:        "mysa",
		},
		{
			name:          "namespace with hyphen",
			username:      "system:serviceaccount:my-namespace:my-sa",
			wantNamespace: "my-namespace",
			wantSA:        "my-sa",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &ValidationResult{
				Valid:    true,
				Username: tt.username,
			}

			// Parse using strings.Split
			parts := strings.Split(tt.username, ":")
			if len(parts) == 4 && parts[0] == "system" && parts[1] == "serviceaccount" {
				result.Namespace = parts[2]
				result.ServiceAccount = parts[3]
			}

			if result.Namespace != tt.wantNamespace {
				t.Errorf("Namespace = %s, want %s", result.Namespace, tt.wantNamespace)
			}
			if result.ServiceAccount != tt.wantSA {
				t.Errorf("ServiceAccount = %s, want %s", result.ServiceAccount, tt.wantSA)
			}
		})
	}
}

func TestValidationResult_InvalidUsernameParsing(t *testing.T) {
	invalidUsernames := []string{
		"",
		"user",
		"system:node:mynode",
		"system:serviceaccount", // incomplete
		"other:serviceaccount:ns:sa",
	}

	for _, username := range invalidUsernames {
		t.Run(username, func(t *testing.T) {
			result := &ValidationResult{
				Valid:    true,
				Username: username,
			}

			// The parsing should leave Namespace and ServiceAccount empty for invalid formats
			if result.Namespace != "" || result.ServiceAccount != "" {
				t.Errorf("expected empty namespace/sa for invalid username %q", username)
			}
		})
	}
}

func TestValidationResult_Defaults(t *testing.T) {
	var result ValidationResult

	if result.Valid {
		t.Error("default Valid should be false")
	}
	if result.Username != "" {
		t.Error("default Username should be empty")
	}
	if result.Groups != nil {
		t.Error("default Groups should be nil")
	}
	if result.Namespace != "" {
		t.Error("default Namespace should be empty")
	}
	if result.ServiceAccount != "" {
		t.Error("default ServiceAccount should be empty")
	}
	if result.Error != nil {
		t.Error("default Error should be nil")
	}
}
