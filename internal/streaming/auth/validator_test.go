/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package auth

import (
	"strings"
	"testing"
)

func TestExpectedAudience(t *testing.T) {
	if ExpectedAudience != "hibernator-control-plane" {
		t.Errorf("expected audience 'hibernator-control-plane', got %s", ExpectedAudience)
	}
}

func TestBearerPrefix(t *testing.T) {
	if BearerPrefix != "Bearer " {
		t.Errorf("expected prefix 'Bearer ', got %s", BearerPrefix)
	}
}

func TestExtractTokenFromHeader(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{
			name:   "with bearer prefix",
			header: "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
			want:   "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
		},
		{
			name:   "without bearer prefix",
			header: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
			want:   "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
		},
		{
			name:   "empty header",
			header: "",
			want:   "",
		},
		{
			name:   "only bearer prefix",
			header: "Bearer ",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractTokenFromHeader(tt.header)
			if got != tt.want {
				t.Errorf("ExtractTokenFromHeader() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidationResult(t *testing.T) {
	result := ValidationResult{
		Valid:          true,
		Username:       "system:serviceaccount:hibernator-system:runner",
		Groups:         []string{"system:serviceaccounts"},
		Namespace:      "hibernator-system",
		ServiceAccount: "runner",
	}

	if !result.Valid {
		t.Error("expected Valid to be true")
	}
	if result.Username != "system:serviceaccount:hibernator-system:runner" {
		t.Errorf("unexpected username: %s", result.Username)
	}
	if result.Namespace != "hibernator-system" {
		t.Errorf("unexpected namespace: %s", result.Namespace)
	}
	if result.ServiceAccount != "runner" {
		t.Errorf("unexpected service account: %s", result.ServiceAccount)
	}
	if len(result.Groups) != 1 {
		t.Errorf("expected 1 group, got %d", len(result.Groups))
	}
}

func TestValidationResult_Error(t *testing.T) {
	result := ValidationResult{
		Valid: false,
		Error: nil,
	}

	if result.Valid {
		t.Error("expected Valid to be false")
	}
}

func TestParseServiceAccountFromUsername(t *testing.T) {
	tests := []struct {
		username  string
		namespace string
		sa        string
	}{
		{
			username:  "system:serviceaccount:hibernator-system:runner",
			namespace: "hibernator-system",
			sa:        "runner",
		},
		{
			username:  "system:serviceaccount:default:test-sa",
			namespace: "default",
			sa:        "test-sa",
		},
	}

	for _, tt := range tests {
		t.Run(tt.username, func(t *testing.T) {
			parts := strings.Split(tt.username, ":")
			if len(parts) == 4 && parts[0] == "system" && parts[1] == "serviceaccount" {
				if parts[2] != tt.namespace {
					t.Errorf("expected namespace %s, got %s", tt.namespace, parts[2])
				}
				if parts[3] != tt.sa {
					t.Errorf("expected SA %s, got %s", tt.sa, parts[3])
				}
			} else {
				t.Error("failed to parse username")
			}
		})
	}
}
