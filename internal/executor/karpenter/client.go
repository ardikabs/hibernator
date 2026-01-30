package karpenter

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// K8sClient is the interface for Kubernetes dynamic API operations.
// It defines the minimal set of Kubernetes API methods needed by the Karpenter executor.
type K8sClient interface {
	// For Karpenter NodePool resources
	// Resource returns a namespaced dynamic resource interface for a given GroupVersionResource.
	Resource(gvr schema.GroupVersionResource) dynamic.NamespaceableResourceInterface
}

// K8sResourceInterface is the interface for Kubernetes resource operations.
// It wraps the namespaced dynamic resource interface.
type K8sResourceInterface interface {
	// Namespace returns a namespaced dynamic resource interface.
	Namespace(namespace string) dynamic.ResourceInterface
}

// K8sResourceClient is the interface for CRUD operations on a specific Kubernetes resource.
// This mirrors the essential methods from dynamic.ResourceInterface used by the executor.
type K8sResourceClient interface {
	// Create creates a new resource.
	Create(ctx context.Context, obj *unstructured.Unstructured, opts ...interface{}) (*unstructured.Unstructured, error)

	// Update updates an existing resource.
	Update(ctx context.Context, obj *unstructured.Unstructured, opts ...interface{}) (*unstructured.Unstructured, error)

	// Get retrieves a resource by name.
	Get(ctx context.Context, name string, opts ...interface{}) (*unstructured.Unstructured, error)

	// List lists resources in a namespace.
	List(ctx context.Context, opts ...interface{}) (*unstructured.UnstructuredList, error)

	// Delete deletes a resource by name.
	Delete(ctx context.Context, name string, opts ...interface{}) error
}
