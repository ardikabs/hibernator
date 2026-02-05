/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package workloadscaler

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// Client provides an abstraction over Kubernetes API operations needed by the WorkloadScaler executor.
// It combines dynamic client operations (for workload resources and scale subresource) with typed client
// operations (for namespace discovery) into a unified interface.
type Client interface {
	// ListNamespaces retrieves all namespaces matching the given label selector.
	// This is used to discover target namespaces when namespace.selector is specified.
	ListNamespaces(ctx context.Context, selector string) (*corev1.NamespaceList, error)

	// ListWorkloads retrieves all workload resources in the specified namespace matching
	// the given label selector. Returns unstructured objects for flexible resource type handling.
	ListWorkloads(ctx context.Context, gvr schema.GroupVersionResource, namespace string, selector string) (*unstructured.UnstructuredList, error)

	// GetScale retrieves the scale subresource for a specific workload.
	// This is used to read the current replica count before downscaling.
	GetScale(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) (*unstructured.Unstructured, error)

	// UpdateScale updates the scale subresource for a specific workload.
	// This is used to set replicas to 0 during shutdown or restore the original count during wakeup.
	UpdateScale(ctx context.Context, gvr schema.GroupVersionResource, namespace string, scaleObj *unstructured.Unstructured) (*unstructured.Unstructured, error)
}

// client is the concrete implementation of the Client interface.
// It wraps both dynamic.Interface and kubernetes.Interface to provide unified access
// to Kubernetes API operations. This dual-client approach allows:
//   - Dynamic client: For workload resources and scale subresource operations
//   - Typed client: For namespace discovery (built-in, type-safe operations)
type client struct {
	// Dynamic provides unstructured access to workload resources and scale subresource.
	Dynamic dynamic.Interface

	// Typed provides strongly-typed access to built-in Kubernetes resources (namespaces).
	Typed kubernetes.Interface
}

// ListNamespaces retrieves all namespace resources from the cluster that match the given label selector.
// The selector is typically used to filter namespaces by labels (e.g., "environment=staging").
// This method uses the typed Kubernetes client for type-safe operations and better performance.
func (c *client) ListNamespaces(ctx context.Context, selector string) (*corev1.NamespaceList, error) {
	return c.Typed.CoreV1().Namespaces().List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
}

// ListWorkloads retrieves all workload resources in the specified namespace that match the given label selector.
// The GVR parameter determines which workload type to query (Deployment, StatefulSet, etc.).
// Returns unstructured objects to support any resource type that implements the scale subresource.
func (c *client) ListWorkloads(ctx context.Context, gvr schema.GroupVersionResource, namespace string, selector string) (*unstructured.UnstructuredList, error) {
	return c.Dynamic.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
}

// GetScale retrieves the scale subresource for a specific workload in the given namespace.
// This method uses the dynamic client with the "scale" subresource suffix to read the current
// replica count. The returned unstructured object contains spec.replicas and status fields.
func (c *client) GetScale(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) (*unstructured.Unstructured, error) {
	return c.Dynamic.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{}, "scale")
}

// UpdateScale updates the scale subresource for a specific workload in the given namespace.
// This method uses the dynamic client with the "scale" subresource suffix to modify the replica count.
// Typically used to set replicas to 0 during hibernation or restore the original count during wakeup.
func (c *client) UpdateScale(ctx context.Context, gvr schema.GroupVersionResource, namespace string, scaleObj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	return c.Dynamic.Resource(gvr).Namespace(namespace).Update(ctx, scaleObj, metav1.UpdateOptions{}, "scale")
}
