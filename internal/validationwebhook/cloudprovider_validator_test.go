/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package validationwebhook

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/go-logr/logr"
)

func TestCloudProviderValidator_ValidateCreate(t *testing.T) {
	validator := NewCloudProviderValidator(logr.Discard())

	tests := []struct {
		name     string
		provider *hibernatorv1alpha1.CloudProvider
		wantErr  bool
		errMsg   string
	}{
		{
			name: "valid - IRSA only",
			provider: &hibernatorv1alpha1.CloudProvider{
				ObjectMeta: metav1.ObjectMeta{Name: "aws-irsa", Namespace: "default"},
				Spec: hibernatorv1alpha1.CloudProviderSpec{
					Type: hibernatorv1alpha1.CloudProviderAWS,
					AWS: &hibernatorv1alpha1.AWSConfig{
						AccountId: "123456789012",
						Region:    "us-east-1",
						Auth: hibernatorv1alpha1.AWSAuth{
							ServiceAccount: &hibernatorv1alpha1.ServiceAccountAuth{},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid - IRSA with AssumeRoleArn",
			provider: &hibernatorv1alpha1.CloudProvider{
				ObjectMeta: metav1.ObjectMeta{Name: "aws-irsa-assume", Namespace: "default"},
				Spec: hibernatorv1alpha1.CloudProviderSpec{
					Type: hibernatorv1alpha1.CloudProviderAWS,
					AWS: &hibernatorv1alpha1.AWSConfig{
						AccountId:     "123456789012",
						Region:        "us-east-1",
						AssumeRoleArn: "arn:aws:iam::123456789012:role/hibernator-target",
						Auth: hibernatorv1alpha1.AWSAuth{
							ServiceAccount: &hibernatorv1alpha1.ServiceAccountAuth{},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid - static credentials only",
			provider: &hibernatorv1alpha1.CloudProvider{
				ObjectMeta: metav1.ObjectMeta{Name: "aws-static", Namespace: "default"},
				Spec: hibernatorv1alpha1.CloudProviderSpec{
					Type: hibernatorv1alpha1.CloudProviderAWS,
					AWS: &hibernatorv1alpha1.AWSConfig{
						AccountId: "123456789012",
						Region:    "us-east-1",
						Auth: hibernatorv1alpha1.AWSAuth{
							Static: &hibernatorv1alpha1.StaticAuth{
								SecretRef: hibernatorv1alpha1.SecretReference{
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
			provider: &hibernatorv1alpha1.CloudProvider{
				ObjectMeta: metav1.ObjectMeta{Name: "aws-static-assume", Namespace: "default"},
				Spec: hibernatorv1alpha1.CloudProviderSpec{
					Type: hibernatorv1alpha1.CloudProviderAWS,
					AWS: &hibernatorv1alpha1.AWSConfig{
						AccountId:     "123456789012",
						Region:        "us-east-1",
						AssumeRoleArn: "arn:aws:iam::123456789012:role/hibernator-target",
						Auth: hibernatorv1alpha1.AWSAuth{
							Static: &hibernatorv1alpha1.StaticAuth{
								SecretRef: hibernatorv1alpha1.SecretReference{
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
			provider: &hibernatorv1alpha1.CloudProvider{
				ObjectMeta: metav1.ObjectMeta{Name: "aws-both", Namespace: "default"},
				Spec: hibernatorv1alpha1.CloudProviderSpec{
					Type: hibernatorv1alpha1.CloudProviderAWS,
					AWS: &hibernatorv1alpha1.AWSConfig{
						AccountId: "123456789012",
						Region:    "us-east-1",
						Auth: hibernatorv1alpha1.AWSAuth{
							ServiceAccount: &hibernatorv1alpha1.ServiceAccountAuth{},
							Static: &hibernatorv1alpha1.StaticAuth{
								SecretRef: hibernatorv1alpha1.SecretReference{
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
			provider: &hibernatorv1alpha1.CloudProvider{
				ObjectMeta: metav1.ObjectMeta{Name: "aws-no-auth", Namespace: "default"},
				Spec: hibernatorv1alpha1.CloudProviderSpec{
					Type: hibernatorv1alpha1.CloudProviderAWS,
					AWS: &hibernatorv1alpha1.AWSConfig{
						AccountId: "123456789012",
						Region:    "us-east-1",
						Auth:      hibernatorv1alpha1.AWSAuth{},
					},
				},
			},
			wantErr: true,
			errMsg:  "at least one authentication method must be specified",
		},
		{
			name: "invalid - AWS config missing when type is aws",
			provider: &hibernatorv1alpha1.CloudProvider{
				ObjectMeta: metav1.ObjectMeta{Name: "aws-no-config", Namespace: "default"},
				Spec: hibernatorv1alpha1.CloudProviderSpec{
					Type: hibernatorv1alpha1.CloudProviderAWS,
					AWS:  nil,
				},
			},
			wantErr: true,
			errMsg:  "spec.aws is required when type is 'aws'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validator.ValidateCreate(context.Background(), runtime.Object(tt.provider))
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCreate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errMsg != "" {
				if err == nil {
					t.Errorf("ValidateCreate() expected error containing %q, got nil", tt.errMsg)
				} else if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateCreate() error = %q, want error containing %q", err.Error(), tt.errMsg)
				}
			}
		})
	}
}

func TestCloudProviderValidator_ValidateCreate_WrongType(t *testing.T) {
	validator := NewCloudProviderValidator(logr.Discard())
	wrongType := &hibernatorv1alpha1.HibernatePlan{}
	_, err := validator.ValidateCreate(context.Background(), runtime.Object(wrongType))
	if err == nil {
		t.Error("ValidateCreate() expected error for wrong type, got nil")
	}
}

func TestCloudProviderValidator_ValidateUpdate(t *testing.T) {
	validator := NewCloudProviderValidator(logr.Discard())

	oldProvider := &hibernatorv1alpha1.CloudProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "aws-test", Namespace: "default"},
		Spec: hibernatorv1alpha1.CloudProviderSpec{
			Type: hibernatorv1alpha1.CloudProviderAWS,
			AWS: &hibernatorv1alpha1.AWSConfig{
				AccountId: "123456789012",
				Region:    "us-east-1",
				Auth: hibernatorv1alpha1.AWSAuth{
					ServiceAccount: &hibernatorv1alpha1.ServiceAccountAuth{},
				},
			},
		},
	}

	newProvider := &hibernatorv1alpha1.CloudProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "aws-test", Namespace: "default"},
		Spec: hibernatorv1alpha1.CloudProviderSpec{
			Type: hibernatorv1alpha1.CloudProviderAWS,
			AWS: &hibernatorv1alpha1.AWSConfig{
				AccountId: "123456789012",
				Region:    "us-west-2",
				Auth: hibernatorv1alpha1.AWSAuth{
					ServiceAccount: &hibernatorv1alpha1.ServiceAccountAuth{},
				},
			},
		},
	}

	_, err := validator.ValidateUpdate(context.Background(), runtime.Object(oldProvider), runtime.Object(newProvider))
	if err != nil {
		t.Errorf("ValidateUpdate() unexpected error = %v", err)
	}
}

func TestCloudProviderValidator_ValidateDelete(t *testing.T) {
	validator := NewCloudProviderValidator(logr.Discard())
	provider := &hibernatorv1alpha1.CloudProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "aws-test", Namespace: "default"},
	}
	_, err := validator.ValidateDelete(context.Background(), runtime.Object(provider))
	if err != nil {
		t.Errorf("ValidateDelete() unexpected error = %v", err)
	}
}
