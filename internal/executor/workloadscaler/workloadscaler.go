/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package workloadscaler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/ardikabs/hibernator/pkg/k8sutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// Executor implements workload downscaling using the scale subresource.
type Executor struct {
	clientFactory ClientFactory
}

// ClientFactory is a function type for creating Kubernetes clients.
type ClientFactory func(ctx context.Context, spec *executor.Spec) (dynamic.Interface, kubernetes.Interface, error)

// New creates a new WorkloadScaler executor with real Kubernetes clients.
func New() *Executor {
	return &Executor{
		clientFactory: func(ctx context.Context, spec *executor.Spec) (dynamic.Interface, kubernetes.Interface, error) {
			return k8sutil.BuildClients(ctx, spec.ConnectorConfig.K8S)
		},
	}
}

// NewWithClients creates a new WorkloadScaler executor with injected client factory.
// This is useful for testing with mock clients.
func NewWithClients(clientFactory ClientFactory) *Executor {
	return &Executor{
		clientFactory: clientFactory,
	}
}

// Type returns the executor type.
func (e *Executor) Type() string {
	return "workloadscaler"
}

// Validate validates the executor spec.
func (e *Executor) Validate(spec executor.Spec) error {
	if spec.ConnectorConfig.K8S == nil {
		return fmt.Errorf("K8S connector config is required")
	}

	var params executorparams.WorkloadScalerParameters
	if len(spec.Parameters) > 0 {
		if err := json.Unmarshal(spec.Parameters, &params); err != nil {
			return fmt.Errorf("parse parameters: %w", err)
		}
	}

	// Validate namespace selector
	if len(params.Namespace.Literals) == 0 && len(params.Namespace.Selector) == 0 {
		return fmt.Errorf("namespace must specify either literals or selector")
	}

	// Check mutual exclusivity
	if len(params.Namespace.Literals) > 0 && len(params.Namespace.Selector) > 0 {
		return fmt.Errorf("namespace.literals and namespace.selector are mutually exclusive")
	}

	return nil
}

// WorkloadState holds the restore state for a single workload.
type WorkloadState struct {
	Group     string `json:"group"`
	Version   string `json:"version"`
	Resource  string `json:"resource"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Replicas  int32  `json:"replicas"`
}

// RestoreState holds all workload states for restoration.
type RestoreState struct {
	Items []WorkloadState `json:"items"`
}

// Shutdown scales down all matched workloads to zero replicas.
func (e *Executor) Shutdown(ctx context.Context, spec executor.Spec) (executor.RestoreData, error) {
	var params executorparams.WorkloadScalerParameters
	if len(spec.Parameters) > 0 {
		if err := json.Unmarshal(spec.Parameters, &params); err != nil {
			return executor.RestoreData{}, fmt.Errorf("parse parameters: %w", err)
		}
	}

	// Default includedGroups to Deployment if not specified
	includedGroups := params.IncludedGroups
	if len(includedGroups) == 0 {
		includedGroups = []string{"Deployment"}
	}

	// Build clients using injected factory
	dynamicClient, k8sClient, err := e.clientFactory(ctx, &spec)
	if err != nil {
		return executor.RestoreData{}, fmt.Errorf("build kubernetes clients: %w", err)
	}

	// Discover target namespaces
	targetNamespaces, err := e.discoverNamespaces(ctx, k8sClient, params.Namespace)
	if err != nil {
		return executor.RestoreData{}, fmt.Errorf("discover namespaces: %w", err)
	}

	if len(targetNamespaces) == 0 {
		return executor.RestoreData{}, fmt.Errorf("no namespaces found matching selector")
	}

	// Discover workloads across all target namespaces
	var restoreStates []WorkloadState
	for _, ns := range targetNamespaces {
		for _, kind := range includedGroups {
			gvr, err := e.resolveGVR(kind)
			if err != nil {
				return executor.RestoreData{}, fmt.Errorf("resolve GVR for %s: %w", kind, err)
			}

			states, err := e.scaleDownWorkloads(ctx, dynamicClient, ns, gvr, params.WorkloadSelector)
			if err != nil {
				return executor.RestoreData{}, fmt.Errorf("scale down %s in namespace %s: %w", kind, ns, err)
			}

			restoreStates = append(restoreStates, states...)
		}
	}

	// Build restore data
	restore := RestoreState{Items: restoreStates}
	stateBytes, err := json.Marshal(restore)
	if err != nil {
		return executor.RestoreData{}, fmt.Errorf("marshal restore data: %w", err)
	}

	return executor.RestoreData{
		Type: e.Type(),
		Data: stateBytes,
	}, nil
}

// WakeUp restores all workloads to their previous replica counts.
func (e *Executor) WakeUp(ctx context.Context, spec executor.Spec, restore executor.RestoreData) error {
	if len(restore.Data) == 0 {
		return fmt.Errorf("restore data is required for wake-up")
	}

	var restoreState RestoreState
	if err := json.Unmarshal(restore.Data, &restoreState); err != nil {
		return fmt.Errorf("unmarshal restore data: %w", err)
	}

	// Build clients using injected factory
	dynamicClient, _, err := e.clientFactory(ctx, &spec)
	if err != nil {
		return fmt.Errorf("build kubernetes clients: %w", err)
	}

	// Restore each workload
	for _, state := range restoreState.Items {
		gvr := schema.GroupVersionResource{
			Group:    state.Group,
			Version:  state.Version,
			Resource: state.Resource,
		}

		if err := e.restoreWorkload(ctx, dynamicClient, state, gvr); err != nil {
			return fmt.Errorf("restore %s/%s in namespace %s: %w", state.Kind, state.Name, state.Namespace, err)
		}
	}

	return nil
}

// discoverNamespaces returns the list of target namespaces based on the selector.
func (e *Executor) discoverNamespaces(ctx context.Context, k8sClient kubernetes.Interface, nsSelector executorparams.NamespaceSelector) ([]string, error) {
	// If literals are specified, use them directly
	if len(nsSelector.Literals) > 0 {
		return nsSelector.Literals, nil
	}

	// If selector is specified, list namespaces by label selector
	if len(nsSelector.Selector) > 0 {
		labelSelector := labels.SelectorFromSet(nsSelector.Selector)
		nsList, err := k8sClient.CoreV1().Namespaces().List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector.String(),
		})
		if err != nil {
			return nil, fmt.Errorf("list namespaces: %w", err)
		}

		namespaces := make([]string, len(nsList.Items))
		for i, ns := range nsList.Items {
			namespaces[i] = ns.Name
		}
		return namespaces, nil
	}

	return nil, fmt.Errorf("namespace selector must specify either literals or selector")
}

// scaleDownWorkloads scales down all matching workloads in a namespace and returns their states.
func (e *Executor) scaleDownWorkloads(ctx context.Context, dynamicClient dynamic.Interface, namespace string, gvr schema.GroupVersionResource, workloadSelector *executorparams.LabelSelector) ([]WorkloadState, error) {
	// Convert label selector to Kubernetes labels.Selector
	selector := labels.Everything()
	if workloadSelector != nil {
		if len(workloadSelector.MatchLabels) > 0 {
			selector = labels.SelectorFromSet(workloadSelector.MatchLabels)
		}
	}

	// List all resources of this type in the namespace
	list, err := dynamicClient.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}

	var states []WorkloadState
	for _, item := range list.Items {
		// Get the scale subresource for this workload
		scaleObj, err := dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, item.GetName(), metav1.GetOptions{}, "scale")
		if err != nil {
			// Skip resources that don't support scale subresource
			// TODO: we can set an info log that the corresponding object
			// has no scale subresource, hence ignored.
			continue
		}

		// Get current replica count from scale.spec.replicas
		replicas, found, err := unstructured.NestedInt64(scaleObj.Object, "spec", "replicas")
		if err != nil {
			return nil, fmt.Errorf("get replicas from scale for %s/%s: %w", item.GetKind(), item.GetName(), err)
		}
		if !found {
			// Skip if scale object doesn't have spec.replicas
			continue
		}

		// Store current state
		states = append(states, WorkloadState{
			Group:     gvr.Group,
			Version:   gvr.Version,
			Resource:  gvr.Resource,
			Kind:      item.GetKind(),
			Namespace: item.GetNamespace(),
			Name:      item.GetName(),
			Replicas:  int32(replicas),
		})

		// Scale to zero by updating scale.spec.replicas
		if err := unstructured.SetNestedField(scaleObj.Object, int64(0), "spec", "replicas"); err != nil {
			return nil, fmt.Errorf("set replicas to zero in scale for %s/%s: %w", item.GetKind(), item.GetName(), err)
		}

		// Update the scale subresource
		_, err = dynamicClient.Resource(gvr).Namespace(namespace).Update(ctx, scaleObj, metav1.UpdateOptions{}, "scale")
		if err != nil {
			return nil, fmt.Errorf("update scale for %s/%s: %w", item.GetKind(), item.GetName(), err)
		}
	}

	return states, nil
}

// restoreWorkload restores a single workload to its previous replica count.
func (e *Executor) restoreWorkload(ctx context.Context, dynamicClient dynamic.Interface, state WorkloadState, gvr schema.GroupVersionResource) error {
	// Get the scale subresource
	scaleObj, err := dynamicClient.Resource(gvr).Namespace(state.Namespace).Get(ctx, state.Name, metav1.GetOptions{}, "scale")
	if err != nil {
		return fmt.Errorf("get scale subresource: %w", err)
	}

	// Update scale.spec.replicas to restore previous count
	if err := unstructured.SetNestedField(scaleObj.Object, int64(state.Replicas), "spec", "replicas"); err != nil {
		return fmt.Errorf("set replicas in scale: %w", err)
	}

	// Update the scale subresource
	_, err = dynamicClient.Resource(gvr).Namespace(state.Namespace).Update(ctx, scaleObj, metav1.UpdateOptions{}, "scale")
	if err != nil {
		return fmt.Errorf("update scale subresource: %w", err)
	}

	return nil
}

// resolveGVR resolves a kind to its GroupVersionResource.
// It supports two mechanisms:
//  1. Hardcoded mappings for common Kubernetes resources (Deployment, StatefulSet, etc.)
//  2. Dynamic parsing for custom CRDs using format: group/version/resource
//     Example: "argoproj.io/v1alpha1/rollouts" → Group: argoproj.io, Version: v1alpha1, Resource: rollouts
func (e *Executor) resolveGVR(kind string) (schema.GroupVersionResource, error) {
	// Map common kinds to their GVRs (built-in resources)
	gvrMap := map[string]schema.GroupVersionResource{
		"Deployment": {
			Group:    "apps",
			Version:  "v1",
			Resource: "deployments",
		},
		"StatefulSet": {
			Group:    "apps",
			Version:  "v1",
			Resource: "statefulsets",
		},
		"ReplicaSet": {
			Group:    "apps",
			Version:  "v1",
			Resource: "replicasets",
		},
	}

	// First, try to find in hardcoded mappings
	if gvr, ok := gvrMap[kind]; ok {
		return gvr, nil
	}

	// If not found, try to parse as custom CRD format: group/version/resource
	return parseCRDFormat(kind)
}

// parseCRDFormat parses a CRD specification string in the format "group/version/resource".
// Example: "argoproj.io/v1alpha1/rollouts" → {Group: "argoproj.io", Version: "v1alpha1", Resource: "rollouts"}
// This enables support for custom CRDs without requiring code changes.
func parseCRDFormat(spec string) (schema.GroupVersionResource, error) {
	parts := strings.Split(spec, "/")
	if len(parts) != 3 {
		return schema.GroupVersionResource{}, fmt.Errorf(
			"kind %q is not a supported common resource; use 'group/version/resource' format for custom CRDs", spec,
		)
	}

	group, version, resource := parts[0], parts[1], parts[2]

	// Validate non-empty parts
	if group == "" || version == "" || resource == "" {
		return schema.GroupVersionResource{}, fmt.Errorf(
			"CRD format 'group/version/resource' has empty parts: group=%q, version=%q, resource=%q",
			group, version, resource,
		)
	}

	return schema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resource,
	}, nil
}
