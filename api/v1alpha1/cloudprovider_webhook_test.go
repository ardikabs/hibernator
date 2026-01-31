/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package v1alpha1

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestCloudProvider_ValidateCreate(t *testing.T) {
	tests := []struct {
		name     string
		provider *CloudProvider
		wantErr  bool
		errMsg   string
	}{
		{
			name: "valid - IRSA only",
			provider: &CloudProvider{
				ObjectMeta: metav1.ObjectMeta{Name: "aws-irsa", Namespace: "default"},
				Spec: CloudProviderSpec{
					Type: CloudProviderAWS,
					AWS: &AWSConfig{
						AccountId: "123456789012",
						Region:    "us-east-1",
						Auth: AWSAuth{
							ServiceAccount: &ServiceAccountAuth{},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid - IRSA with AssumeRoleArn",
			provider: &CloudProvider{
				ObjectMeta: metav1.ObjectMeta{Name: "aws-irsa-assume", Namespace: "default"},
				Spec: CloudProviderSpec{
					Type: CloudProviderAWS,
					AWS: &AWSConfig{
						AccountId:     "123456789012",
						Region:        "us-east-1",
						AssumeRoleArn: "arn:aws:iam::123456789012:role/hibernator-target",
						Auth: AWSAuth{
							ServiceAccount: &ServiceAccountAuth{},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid - static credentials only",
			provider: &CloudProvider{
				ObjectMeta: metav1.ObjectMeta{Name: "aws-static", Namespace: "default"},
				Spec: CloudProviderSpec{
					Type: CloudProviderAWS,
					AWS: &AWSConfig{
						AccountId: "123456789012",
						Region:    "us-east-1",
						Auth: AWSAuth{
							Static: &StaticAuth{
								SecretRef: SecretReference{
									Name:      "aws-creds",
									Namespace: "default",
								},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid - static credentials with AssumeRoleArn",
			provider: &CloudProvider{
				ObjectMeta: metav1.ObjectMeta{Name: "aws-static-assume", Namespace: "default"},
				Spec: CloudProviderSpec{
					Type: CloudProviderAWS,
					AWS: &AWSConfig{
						AccountId:     "123456789012",
						Region:        "us-east-1",
						AssumeRoleArn: "arn:aws:iam::123456789012:role/hibernator-target",
						Auth: AWSAuth{
							Static: &StaticAuth{
								SecretRef: SecretReference{
									Name:      "aws-creds",
									Namespace: "default",
								},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid - both auth methods specified",
			provider: &CloudProvider{
				ObjectMeta: metav1.ObjectMeta{Name: "aws-both", Namespace: "default"},
				Spec: CloudProviderSpec{
					Type: CloudProviderAWS,
					AWS: &AWSConfig{
						AccountId: "123456789012",
						Region:    "us-east-1",
						Auth: AWSAuth{
							ServiceAccount: &ServiceAccountAuth{},
							Static: &StaticAuth{
								SecretRef: SecretReference{
									Name:      "aws-creds",
									Namespace: "default",
								},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid - no auth method specified",
			provider: &CloudProvider{
				ObjectMeta: metav1.ObjectMeta{Name: "aws-no-auth", Namespace: "default"},
				Spec: CloudProviderSpec{
					Type: CloudProviderAWS,
					AWS: &AWSConfig{
						AccountId: "123456789012",
						Region:    "us-east-1",
						Auth:      AWSAuth{},
					},
				},
			},
			wantErr: true,
			errMsg:  "at least one authentication method must be specified",
		},
		{
			name: "invalid - AWS config missing when type is aws",
			provider: &CloudProvider{
				ObjectMeta: metav1.ObjectMeta{Name: "aws-no-config", Namespace: "default"},
				Spec: CloudProviderSpec{
					Type: CloudProviderAWS,
					AWS:  nil,
				},
			},
			wantErr: true,
			errMsg:  "spec.aws is required when type is 'aws'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.provider.ValidateCreate(context.Background(), runtime.Object(tt.provider))
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCreate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errMsg != "" {
				if err == nil {
					t.Errorf("ValidateCreate() expected error containing %q, got nil", tt.errMsg)
				} else if err.Error() != "" && len(tt.errMsg) > 0 {
					// Simple substring check
					errStr := err.Error()
					found := false
					for i := 0; i <= len(errStr)-len(tt.errMsg); i++ {
						if errStr[i:i+len(tt.errMsg)] == tt.errMsg {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("ValidateCreate() error = %q, want error containing %q", errStr, tt.errMsg)
					}
				}
			}
		})
	}
}

func TestCloudProvider_ValidateCreate_WrongType(t *testing.T) {
	wrongType := &HibernatePlan{}
	provider := &CloudProvider{}
	_, err := provider.ValidateCreate(context.Background(), runtime.Object(wrongType))
	if err == nil {
		t.Error("ValidateCreate() expected error for wrong type, got nil")
	}
}

func TestCloudProvider_ValidateUpdate(t *testing.T) {
	oldProvider := &CloudProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "aws-test", Namespace: "default"},
		Spec: CloudProviderSpec{
			Type: CloudProviderAWS,
			AWS: &AWSConfig{
				AccountId: "123456789012",
				Region:    "us-east-1",
				Auth: AWSAuth{
					ServiceAccount: &ServiceAccountAuth{},
				},
			},
		},
	}

	newProvider := &CloudProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "aws-test", Namespace: "default"},
		Spec: CloudProviderSpec{
			Type: CloudProviderAWS,
			AWS: &AWSConfig{
				AccountId: "123456789012",
				Region:    "us-west-2",
				Auth: AWSAuth{
					ServiceAccount: &ServiceAccountAuth{},
				},
			},
		},
	}

	_, err := newProvider.ValidateUpdate(context.Background(), runtime.Object(oldProvider), runtime.Object(newProvider))
	if err != nil {
		t.Errorf("ValidateUpdate() unexpected error = %v", err)
	}
}
