/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// K8SClusterType defines supported Kubernetes cluster types.
// +kubebuilder:validation:Enum=eks;gke;k8s
type K8SClusterType string

const (
	ClusterTypeEKS K8SClusterType = "eks"
	ClusterTypeGKE K8SClusterType = "gke"
	ClusterTypeK8S K8SClusterType = "k8s"
)

// ProviderRef references a CloudProvider.
type ProviderRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// EKSConfig holds EKS-specific configuration.
type EKSConfig struct {
	// Name is the EKS cluster name.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Region is the AWS region.
	// +kubebuilder:validation:Required
	Region string `json:"region"`
}

// GKEConfig holds GKE-specific configuration.
type GKEConfig struct {
	// Name is the GKE cluster name.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Project is the GCP project.
	// +kubebuilder:validation:Required
	Project string `json:"project"`

	// Zone or region of the cluster.
	// +kubebuilder:validation:Required
	Location string `json:"location"`
}

// KubeconfigRef references a kubeconfig Secret.
type KubeconfigRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// K8SAccessConfig holds Kubernetes API access configuration.
type K8SAccessConfig struct {
	// KubeconfigRef references a Secret containing kubeconfig.
	// +optional
	KubeconfigRef *KubeconfigRef `json:"kubeconfigRef,omitempty"`

	// InCluster uses in-cluster config (for self-management).
	// +optional
	InCluster bool `json:"inCluster,omitempty"`
}

// K8SClusterSpec defines the desired state of K8SCluster.
type K8SClusterSpec struct {
	// ProviderRef references the CloudProvider (optional for generic k8s).
	// +optional
	ProviderRef *ProviderRef `json:"providerRef,omitempty"`

	// EKS holds EKS-specific configuration.
	// +optional
	EKS *EKSConfig `json:"eks,omitempty"`

	// GKE holds GKE-specific configuration.
	// +optional
	GKE *GKEConfig `json:"gke,omitempty"`

	// K8S holds generic Kubernetes access configuration.
	// +optional
	K8S *K8SAccessConfig `json:"k8s,omitempty"`
}

// K8SClusterStatus defines the observed state of K8SCluster.
type K8SClusterStatus struct {
	// Ready indicates if the cluster is reachable.
	Ready bool `json:"ready,omitempty"`

	// Message provides status details.
	// +optional
	Message string `json:"message,omitempty"`

	// LastValidated is when connectivity was last validated.
	// +optional
	LastValidated *metav1.Time `json:"lastValidated,omitempty"`

	// ClusterType is the detected cluster type.
	// +optional
	ClusterType K8SClusterType `json:"clusterType,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.status.clusterType`
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// K8SCluster is the Schema for the k8sclusters API.
type K8SCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   K8SClusterSpec   `json:"spec,omitempty"`
	Status K8SClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// K8SClusterList contains a list of K8SCluster.
type K8SClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []K8SCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&K8SCluster{}, &K8SClusterList{})
}
