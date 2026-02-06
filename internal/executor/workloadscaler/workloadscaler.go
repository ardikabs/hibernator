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

	"github.com/go-logr/logr"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/ardikabs/hibernator/pkg/k8sutil"
	"github.com/ardikabs/hibernator/pkg/waiter"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	ExecutorType       = "workloadscaler"
	DefaultWaitTimeout = "5m"
)

// Executor implements workload downscaling using the scale subresource.
type Executor struct {
	clientFactory ClientFactory
}

// ClientFactory is a function type for creating Kubernetes clients.
type ClientFactory func(ctx context.Context, spec *executor.Spec) (Client, error)

// New creates a new WorkloadScaler executor with real Kubernetes clients.
func New() *Executor {
	return &Executor{
		clientFactory: func(ctx context.Context, spec *executor.Spec) (Client, error) {
			dynamic, typed, err := k8sutil.BuildClients(ctx, spec.ConnectorConfig.K8S)
			if err != nil {
				return nil, err
			}

			return &client{
				Dynamic: dynamic,
				Typed:   typed,
			}, nil
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
	return ExecutorType
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
func (e *Executor) Shutdown(ctx context.Context, log logr.Logger, spec executor.Spec) error {
	var params executorparams.WorkloadScalerParameters
	if len(spec.Parameters) > 0 {
		if err := json.Unmarshal(spec.Parameters, &params); err != nil {
			return fmt.Errorf("parse parameters: %w", err)
		}
	}

	// Default includedGroups to Deployment if not specified
	includedGroups := params.IncludedGroups
	if len(includedGroups) == 0 {
		includedGroups = []string{"Deployment"}
	}

	// Build clients using injected factory
	client, err := e.clientFactory(ctx, &spec)
	if err != nil {
		return fmt.Errorf("build kubernetes clients: %w", err)
	}

	// Discover target namespaces
	targetNamespaces, err := e.discoverNamespaces(ctx, client, params.Namespace)
	if err != nil {
		return fmt.Errorf("discover namespaces: %w", err)
	}

	if len(targetNamespaces) == 0 {
		return fmt.Errorf("no namespaces found matching selector")
	}

	totalWorkloadscaled := 0
	for _, ns := range targetNamespaces {
		for _, kind := range includedGroups {
			gvr, err := e.resolveGVR(kind)
			if err != nil {
				return fmt.Errorf("resolve GVR for %s: %w", kind, err)
			}

			counts, err := e.scaleDownWorkloads(ctx, log, client, ns, gvr, params.WorkloadSelector, params, spec.SaveRestoreData)
			if err != nil {
				return fmt.Errorf("scale down %s in namespace %s: %w", kind, ns, err)
			}

			// Track if any workload had non-zero replicas
			if counts > 0 {
				totalWorkloadscaled += counts
			}
		}
	}

	log.Info("hibernation completed", "numberOfWorkloadsScaled", totalWorkloadscaled)

	return nil
}

// WakeUp restores all workloads to their previous replica counts.
func (e *Executor) WakeUp(ctx context.Context, log logr.Logger, spec executor.Spec, restore executor.RestoreData) error {
	var params executorparams.WorkloadScalerParameters
	if len(spec.Parameters) > 0 {
		if err := json.Unmarshal(spec.Parameters, &params); err != nil {
			return fmt.Errorf("parse parameters: %w", err)
		}
	}

	if len(restore.Data) == 0 {
		return fmt.Errorf("restore data is required for wake-up")
	}

	// Build clients using injected factory
	client, err := e.clientFactory(ctx, &spec)
	if err != nil {
		return fmt.Errorf("build kubernetes clients: %w", err)
	}

	// Restore each workload
	for workloadKey, stateBytes := range restore.Data {
		var state WorkloadState
		if err := json.Unmarshal(stateBytes, &state); err != nil {
			log.Error(err, "failed to unmarshal workload state", "workload", workloadKey)
			return fmt.Errorf("unmarshal workload state %s: %w", workloadKey, err)
		}

		gvr := schema.GroupVersionResource{
			Group:    state.Group,
			Version:  state.Version,
			Resource: state.Resource,
		}

		if err := e.restoreWorkload(ctx, log, client, state, gvr, params); err != nil {
			return fmt.Errorf("restore %s/%s in namespace %s: %w", state.Kind, state.Name, state.Namespace, err)
		}
	}

	return nil
}

// discoverNamespaces returns the list of target namespaces based on the selector.
func (e *Executor) discoverNamespaces(ctx context.Context, client Client, nsSelector executorparams.NamespaceSelector) ([]string, error) {
	// If literals are specified, use them directly
	if len(nsSelector.Literals) > 0 {
		return nsSelector.Literals, nil
	}

	// If selector is specified, list namespaces by label selector
	if len(nsSelector.Selector) > 0 {
		labelSelector := labels.SelectorFromSet(nsSelector.Selector)
		nsList, err := client.ListNamespaces(ctx, labelSelector.String())
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
// Returns: (statesMap, hadNonZero, error)
// - statesMap: map with key = namespace/kind/name
// - hadNonZero: true if any workload had replicas > 0 before scaling
func (e *Executor) scaleDownWorkloads(ctx context.Context,
	log logr.Logger,
	client Client,
	namespace string,
	gvr schema.GroupVersionResource,
	workloadSelector *executorparams.LabelSelector,
	params executorparams.WorkloadScalerParameters,
	callback executor.SaveRestoreDataFunc) (int, error) {

	// Convert label selector to Kubernetes labels.Selector
	selector := labels.Everything()
	if workloadSelector != nil {
		if len(workloadSelector.MatchLabels) > 0 {
			selector = labels.SelectorFromSet(workloadSelector.MatchLabels)
		}
	}

	// List all resources of this type in the namespace
	list, err := client.ListWorkloads(ctx, gvr, namespace, selector.String())
	if err != nil {
		return 0, fmt.Errorf("list resources: %w", err)
	}

	// Build unified map: key = namespace/kind/name
	statesMap := make(map[string]json.RawMessage)
	counts := 0

	for _, item := range list.Items {
		// Get the scale subresource for this workload
		scaleObj, err := client.GetScale(ctx, gvr, namespace, item.GetName())
		if err != nil {
			// Skip resources that don't support scale subresource
			// TODO: we can set an info log that the corresponding object
			// has no scale subresource, hence ignored.
			continue
		}

		// Get current replica count from scale.spec.replicas
		replicas, found, err := unstructured.NestedInt64(scaleObj.Object, "spec", "replicas")
		if err != nil {
			return 0, fmt.Errorf("get replicas from scale for %s/%s: %w", item.GetKind(), item.GetName(), err)
		}
		if !found {
			// Skip if scale object doesn't have spec.replicas
			continue
		}

		// Track if this workload has non-zero replicas
		if replicas > 0 {
			counts++
		}

		// Store current state with key = namespace/kind/name
		key := fmt.Sprintf("%s/%s/%s", item.GetNamespace(), item.GetKind(), item.GetName())
		state := WorkloadState{
			Group:     gvr.Group,
			Version:   gvr.Version,
			Resource:  gvr.Resource,
			Kind:      item.GetKind(),
			Namespace: item.GetNamespace(),
			Name:      item.GetName(),
			Replicas:  int32(replicas),
		}
		stateBytes, _ := json.Marshal(state)
		statesMap[key] = stateBytes

		// Scale to zero by updating scale.spec.replicas
		if err := unstructured.SetNestedField(scaleObj.Object, int64(0), "spec", "replicas"); err != nil {
			return 0, fmt.Errorf("set replicas to zero in scale for %s/%s: %w", item.GetKind(), item.GetName(), err)
		}

		// Update the scale subresource
		_, err = client.UpdateScale(ctx, gvr, namespace, scaleObj)
		if err != nil {
			return 0, fmt.Errorf("update scale for %s/%s: %w", item.GetKind(), item.GetName(), err)
		}

		// Incremental save: persist this workload's restore data immediately
		if callback != nil {
			if err := callback(key, state, replicas > 0); err != nil {
				log.Error(err, "failed to save restore data incrementally", "workload", key)
				// Continue processing - save at end as fallback
			}
		}

		// Wait for replicas to scale if configured
		if params.WaitConfig.Enabled {
			timeout := params.WaitConfig.Timeout
			if timeout == "" {
				timeout = DefaultWaitTimeout
			}
			if err := e.waitForReplicasScaled(ctx, log, client, gvr, item.GetNamespace(), item.GetName(), 0, timeout); err != nil {
				return 0, fmt.Errorf("wait for %s/%s to scale: %w", item.GetKind(), item.GetName(), err)
			}
		}
	}

	return counts, nil
}

// restoreWorkload restores a single workload to its previous replica count.
func (e *Executor) restoreWorkload(ctx context.Context, log logr.Logger, client Client, state WorkloadState, gvr schema.GroupVersionResource, params executorparams.WorkloadScalerParameters) error {
	// Get the scale subresource
	scaleObj, err := client.GetScale(ctx, gvr, state.Namespace, state.Name)
	if err != nil {
		return fmt.Errorf("get scale subresource: %w", err)
	}

	// Update scale.spec.replicas to restore previous count
	if err := unstructured.SetNestedField(scaleObj.Object, int64(state.Replicas), "spec", "replicas"); err != nil {
		return fmt.Errorf("set replicas in scale: %w", err)
	}

	// Update the scale subresource
	_, err = client.UpdateScale(ctx, gvr, state.Namespace, scaleObj)
	if err != nil {
		return fmt.Errorf("update scale subresource: %w", err)
	}

	// Wait for replicas to scale if configured
	if params.WaitConfig.Enabled {
		timeout := params.WaitConfig.Timeout
		if timeout == "" {
			timeout = DefaultWaitTimeout
		}
		if err := e.waitForReplicasScaled(ctx, log, client, gvr, state.Namespace, state.Name, int64(state.Replicas), timeout); err != nil {
			return fmt.Errorf("wait for %s/%s to scale: %w", state.Kind, state.Name, err)
		}
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

// waitForReplicasScaled waits for a workload's replica count to match the desired count.
func (e *Executor) waitForReplicasScaled(ctx context.Context, log logr.Logger, client Client, gvr schema.GroupVersionResource, namespace, name string, desiredReplicas int64, timeoutStr string) error {
	log.Info("waiting for workload replicas to scale",
		"namespace", namespace,
		"name", name,
		"desiredReplicas", desiredReplicas,
		"timeout", timeoutStr,
	)

	w, err := waiter.NewWaiter(ctx, log, timeoutStr)
	if err != nil {
		return fmt.Errorf("create waiter: %w", err)
	}

	checkFn := func() (bool, string, error) {
		// Get the scale subresource
		scaleObj, err := client.GetScale(ctx, gvr, namespace, name)
		if err != nil {
			return false, "", fmt.Errorf("get scale subresource: %w", err)
		}

		// Check status.replicas (current replica count)
		statusReplicas, found, err := unstructured.NestedInt64(scaleObj.Object, "status", "replicas")
		if err != nil {
			return false, "", fmt.Errorf("get status.replicas: %w", err)
		}
		if !found {
			return false, "status.replicas not available", nil
		}

		if statusReplicas == desiredReplicas {
			return true, fmt.Sprintf("current replicas=%d has been met with desired replicas=%d", statusReplicas, desiredReplicas), nil
		}
		return false, fmt.Sprintf("current replicas=%d; desired replicas=%d (waiting)", statusReplicas, desiredReplicas), nil
	}

	description := fmt.Sprintf("%s/%s in namespace %s to scale to %d replicas", gvr.Resource, name, namespace, desiredReplicas)
	if err := w.Poll(description, checkFn); err != nil {
		return fmt.Errorf("%s/%s: %w", namespace, name, err)
	}

	log.Info("workload scaled successfully",
		"namespace", namespace,
		"name", name,
		"replicas", desiredReplicas,
	)
	return nil
}
