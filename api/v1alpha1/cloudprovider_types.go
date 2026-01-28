/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CloudProviderType defines supported cloud providers.
// +kubebuilder:validation:Enum=aws
type CloudProviderType string

const (
	CloudProviderAWS CloudProviderType = "aws"
)

// AWSAuth defines AWS authentication configuration.
type AWSAuth struct {
	// ServiceAccount configures IRSA-based authentication.
	// +optional
	ServiceAccount *ServiceAccountAuth `json:"serviceAccount,omitempty"`

	// Static configures static credential-based authentication.
	// +optional
	Static *StaticAuth `json:"static,omitempty"`
}

// ServiceAccountAuth configures IRSA.
type ServiceAccountAuth struct {
	// AssumeRoleArn is the IAM role ARN to assume.
	// +kubebuilder:validation:Required
	AssumeRoleArn string `json:"assumeRoleArn"`
}

// StaticAuth configures static credentials.
type StaticAuth struct {
	// SecretRef references a Secret containing credentials.
	SecretRef SecretReference `json:"secretRef"`
}

// SecretReference is a reference to a Secret.
type SecretReference struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// AWSConfig holds AWS-specific configuration.
type AWSConfig struct {
	// AccountId is the AWS account ID.
	// +kubebuilder:validation:Required
	AccountId string `json:"accountId"`

	// Region is the AWS region.
	// +kubebuilder:validation:Required
	Region string `json:"region"`

	// Auth configures authentication.
	// +kubebuilder:validation:Required
	Auth AWSAuth `json:"auth"`
}

// CloudProviderSpec defines the desired state of CloudProvider.
type CloudProviderSpec struct {
	// Type of cloud provider.
	// +kubebuilder:validation:Required
	Type CloudProviderType `json:"type"`

	// AWS holds AWS-specific configuration (required when Type=aws).
	// +optional
	AWS *AWSConfig `json:"aws,omitempty"`
}

// CloudProviderStatus defines the observed state of CloudProvider.
type CloudProviderStatus struct {
	// Ready indicates if the provider is ready to use.
	Ready bool `json:"ready,omitempty"`

	// Message provides status details.
	// +optional
	Message string `json:"message,omitempty"`

	// LastValidated is when credentials were last validated.
	// +optional
	LastValidated *metav1.Time `json:"lastValidated,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CloudProvider is the Schema for the cloudproviders API.
type CloudProvider struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CloudProviderSpec   `json:"spec,omitempty"`
	Status CloudProviderStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CloudProviderList contains a list of CloudProvider.
type CloudProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CloudProvider `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CloudProvider{}, &CloudProviderList{})
}
