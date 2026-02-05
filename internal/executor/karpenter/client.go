package karpenter

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// Client provides an abstraction over Kubernetes API operations needed by the Karpenter executor.
// It combines dynamic client operations (for custom resources like NodePool) with typed client
// operations (for built-in resources like Node) into a unified interface.
type Client interface {
	// Resource returns a dynamic resource interface for interacting with custom resources
	// (e.g., Karpenter NodePool) identified by their GroupVersionResource.
	Resource(gvr schema.GroupVersionResource) dynamic.NamespaceableResourceInterface

	// ListNode retrieves all Node resources matching the given label selector.
	// This is used to verify that all nodes managed by a NodePool have been deleted
	// during the hibernation process.
	ListNode(ctx context.Context, selector string) (*corev1.NodeList, error)
}

// client is the concrete implementation of the Client interface.
// It wraps both dynamic.Interface and kubernetes.Interface to provide unified access
// to Kubernetes API operations. This dual-client approach allows:
//   - Dynamic client: For Karpenter NodePool CRDs (custom resources)
//   - Typed client: For Node resources (built-in, type-safe operations)
type client struct {
	// Dynamic provides unstructured access to custom resources via the dynamic API.
	Dynamic dynamic.Interface

	// Typed provides strongly-typed access to built-in Kubernetes resources.
	Typed kubernetes.Interface
}

// Resource returns a namespaced dynamic resource interface for the specified GroupVersionResource.
// This method delegates to the underlying dynamic client and is primarily used for operations
// on Karpenter NodePool custom resources (karpenter.sh/v1/nodepools).
func (c *client) Resource(gvr schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return c.Dynamic.Resource(gvr)
}

// ListNode retrieves all Node resources from the cluster that match the given label selector.
// The selector is typically used to filter nodes by their Karpenter NodePool association
// (e.g., "karpenter.sh/nodepool=default"). This method uses the typed Kubernetes client
// for type-safe operations and better performance compared to dynamic client queries.
func (c *client) ListNode(ctx context.Context, selector string) (*corev1.NodeList, error) {
	return c.Typed.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
}
