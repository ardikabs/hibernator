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
	"sync"
	"sync/atomic"

	"github.com/go-logr/logr"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/ardikabs/hibernator/pkg/k8sutil"
	"github.com/ardikabs/hibernator/pkg/waiter"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

const (
	ExecutorType       = "workloadscaler"
	DefaultWaitTimeout = "5m"
)

// Executor implements workload downscaling using the scale subresource.
type Executor struct {
	clientFactory ClientFactory

	waitinglist  []WorkloadState
	completionWg sync.WaitGroup
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

func (s WorkloadState) GetGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    s.Group,
		Version:  s.Version,
		Resource: s.Resource,
	}
}

func (s WorkloadState) String() string {
	return fmt.Sprintf("%s/%s/%s", s.Namespace, s.Kind, s.Name)
}

// RestoreState holds all workload states for restoration.
type RestoreState struct {
	Items []WorkloadState `json:"items"`
}

type operationOutcome string

const (
	operationOutcomeApplied      operationOutcome = "applied"
	operationOutcomeSkippedStale operationOutcome = "skipped_stale"
)

type operationStats struct {
	processed    int
	applied      int
	skippedStale int
}

func formatShutdownMessage(stats operationStats, namespaceCount int) string {
	msg := fmt.Sprintf("scaled %d workload(s) to zero across %d namespace(s)", stats.applied, namespaceCount)
	return appendCountSegment(msg, "skipped", stats.skippedStale, "stale workload")
}

func formatWakeUpMessage(stats operationStats) string {
	msg := fmt.Sprintf("restored %d workload(s)", stats.applied)
	return appendCountSegment(msg, "skipped", stats.skippedStale, "stale workload")
}

func appendCountSegment(msg, action string, count int, noun string) string {
	if count <= 0 {
		return msg
	}

	return fmt.Sprintf("%s, %s %d %s(s)", msg, action, count, noun)
}

// Shutdown scales down all matched workloads to zero replicas.
func (e *Executor) Shutdown(ctx context.Context, log logr.Logger, spec executor.Spec) (*executor.Result, error) {
	log = log.WithName("workloadscaler").WithValues("target", spec.TargetName, "targetType", spec.TargetType)
	log.Info("executor starting shutdown")
	e.waitinglist = nil

	var params executorparams.WorkloadScalerParameters
	if len(spec.Parameters) > 0 {
		if err := json.Unmarshal(spec.Parameters, &params); err != nil {
			return nil, fmt.Errorf("parse parameters: %w", err)
		}
	}

	log.Info("parameters parsed",
		"hasNamespaceLiterals", len(params.Namespace.Literals) > 0,
		"hasNamespaceSelector", len(params.Namespace.Selector) > 0,
		"awaitCompletion", params.AwaitCompletion.Enabled,
	)

	// Default includedGroups to Deployment if not specified
	includedGroups := params.IncludedGroups
	if len(includedGroups) == 0 {
		includedGroups = []string{"Deployment"}
	}

	// Build clients using injected factory
	client, err := e.clientFactory(ctx, &spec)
	if err != nil {
		return nil, fmt.Errorf("build kubernetes clients: %w", err)
	}

	// Discover target namespaces
	targetNamespaces, err := e.discoverNamespaces(ctx, client, params.Namespace)
	if err != nil {
		return nil, fmt.Errorf("discover namespaces: %w", err)
	}

	if len(targetNamespaces) == 0 {
		return nil, fmt.Errorf("no namespaces found matching selector")
	}

	log.Info("target namespaces discovered", "count", len(targetNamespaces), "namespaces", strings.Join(targetNamespaces, ", "))

	stats := operationStats{}
	for _, ns := range targetNamespaces {
		for _, kind := range includedGroups {
			gvr, err := e.resolveGVR(kind)
			if err != nil {
				return nil, fmt.Errorf("resolve GVR for %s: %w", kind, err)
			}

			counts, err := e.scaleDownWorkloads(ctx, log, client, ns, gvr, params.WorkloadSelector, params, spec.SaveRestoreData)
			if err != nil {
				return nil, fmt.Errorf("scale down %s in namespace %s: %w", kind, ns, err)
			}

			stats.processed += counts.processed
			stats.applied += counts.applied
			stats.skippedStale += counts.skippedStale
		}
	}

	// Wait for all workloads to scale if configured
	msg := formatShutdownMessage(stats, len(targetNamespaces))

	if params.AwaitCompletion.Enabled {
		timeout := params.AwaitCompletion.Timeout
		if timeout == "" {
			timeout = DefaultWaitTimeout
		}

		var timedOut atomic.Int32
		for _, state := range e.waitinglist {
			e.completionWg.Add(1)
			go func(state WorkloadState) {
				defer e.completionWg.Done()
				if err := e.waitForReplicasScaled(ctx, log, client, state.GetGVR(), state.Namespace, state.Name, 0, timeout); err != nil {
					timedOut.Add(1)
					log.Error(err, "wait for workload to scale", "workload", state.String())
				}

			}(state)
		}

		e.completionWg.Wait()

		total := len(e.waitinglist)
		if failed := int(timedOut.Load()); failed > 0 {
			msg += fmt.Sprintf("; %d of %d workload(s) not yet at zero replicas after %s timeout", failed, total, timeout)
		} else {
			msg += "; all workloads confirmed at zero replicas"
		}
	}

	log.Info("shutdown completed",
		"processed", stats.processed,
		"scaled", stats.applied,
		"skippedStale", stats.skippedStale,
	)

	return &executor.Result{Message: msg}, nil
}

// WakeUp restores all workloads to their previous replica counts.
func (e *Executor) WakeUp(ctx context.Context, log logr.Logger, spec executor.Spec, restore executor.RestoreData) (*executor.Result, error) {
	log = log.WithName("workloadscaler").WithValues("target", spec.TargetName, "targetType", spec.TargetType)
	log.Info("executor starting wakeup")
	e.waitinglist = nil

	var params executorparams.WorkloadScalerParameters

	if len(restore.Data) == 0 {
		log.Info("no restore data available, wakeup operation is no-op")
		return &executor.Result{Message: "wakeup completed for workloadscaler (no restore data)"}, nil
	}

	if len(spec.Parameters) > 0 {
		if err := json.Unmarshal(spec.Parameters, &params); err != nil {
			return nil, fmt.Errorf("parse parameters: %w", err)
		}
	}

	log.Info("restore state loaded", "workloadCount", len(restore.Data))

	// Build clients using injected factory
	client, err := e.clientFactory(ctx, &spec)
	if err != nil {
		return nil, fmt.Errorf("build kubernetes clients: %w", err)
	}

	stats := operationStats{processed: len(restore.Data)}

	// Restore each workload
	for workloadKey, stateBytes := range restore.Data {
		var state WorkloadState
		if err := json.Unmarshal(stateBytes, &state); err != nil {
			log.Error(err, "failed to unmarshal workload state", "workload", workloadKey)
			return nil, fmt.Errorf("unmarshal workload state %s: %w", workloadKey, err)
		}

		outcome, err := e.restoreWorkload(ctx, log, client, state, params)
		if err != nil {
			return nil, fmt.Errorf("restore %s/%s in namespace %s: %w", state.Kind, state.Name, state.Namespace, err)
		}

		switch outcome {
		case operationOutcomeApplied:
			stats.applied++
		case operationOutcomeSkippedStale:
			stats.skippedStale++
		}
	}

	// Wait for all workloads to scale if configured
	msg := formatWakeUpMessage(stats)

	if params.AwaitCompletion.Enabled {
		timeout := params.AwaitCompletion.Timeout
		if timeout == "" {
			timeout = DefaultWaitTimeout
		}

		var timedOut atomic.Int32
		for _, state := range e.waitinglist {
			e.completionWg.Add(1)
			go func(state WorkloadState) {
				defer e.completionWg.Done()
				if err := e.waitForReplicasScaled(ctx, log, client, state.GetGVR(), state.Namespace, state.Name, int64(state.Replicas), timeout); err != nil {
					timedOut.Add(1)
					log.Error(err, "wait for workload to scale", "workload", state.String())
				}

			}(state)
		}

		e.completionWg.Wait()

		total := len(e.waitinglist)
		if failed := int(timedOut.Load()); failed > 0 {
			msg += fmt.Sprintf("; %d of %d workload(s) not yet at desired replicas after %s timeout", failed, total, timeout)
		} else {
			msg += "; all workloads confirmed at desired replicas"
		}
	}

	log.Info("wakeup completed",
		"processed", stats.processed,
		"restored", stats.applied,
		"skippedStale", stats.skippedStale,
	)

	return &executor.Result{Message: msg}, nil
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
// It returns the count of workloads that had non-zero replicas before scaling down, and any error encountered.
func (e *Executor) scaleDownWorkloads(ctx context.Context,
	log logr.Logger,
	client Client,
	namespace string,
	gvr schema.GroupVersionResource,
	workloadSelector *metav1.LabelSelector,
	params executorparams.WorkloadScalerParameters,
	callback executor.SaveRestoreDataFunc) (operationStats, error) {

	// Convert label selector to Kubernetes labels.Selector
	selector, err := metav1.LabelSelectorAsSelector(workloadSelector)
	if err != nil {
		return operationStats{}, fmt.Errorf("invalid label selector: %w", err)
	}

	log.Info("scaling down workloads",
		"namespace", namespace,
		"resource", gvr.Resource,
		"selector", selector.String(),
	)

	// List all resources of this type in the namespace
	list, err := client.ListWorkloads(ctx, gvr, namespace, selector.String())
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("no resources found, skipping", "namespace", namespace, "resource", gvr.Resource)
			return operationStats{}, nil
		}

		return operationStats{}, fmt.Errorf("list resources: %w", err)
	}

	// Build unified map: key = namespace/kind/name
	statesMap := make(map[string]json.RawMessage)
	stats := operationStats{}

	for _, item := range list.Items {
		stats.processed++

		// Get the scale subresource for this workload
		scaleObj, err := client.GetScale(ctx, gvr, namespace, item.GetName())
		if err != nil {
			if apierrors.IsNotFound(err) {
				stats.skippedStale++
			}
			log.Info("failed to get scale subresource, skipping", "namespace", namespace, "name", item.GetName(), "kind", item.GetKind())
			continue
		}

		// Get current replica count from scale.spec.replicas
		replicas, found, err := unstructured.NestedInt64(scaleObj.Object, "spec", "replicas")
		if err != nil {
			return operationStats{}, fmt.Errorf("get replicas from scale for %s/%s: %w", item.GetKind(), item.GetName(), err)
		}
		if !found {
			// Skip if scale object doesn't have spec.replicas
			continue
		}

		// Track if this workload has non-zero replicas
		shouldCountAsApplied := replicas > 0

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
			return operationStats{}, fmt.Errorf("set replicas to zero in scale for %s/%s: %w", item.GetKind(), item.GetName(), err)
		}

		// Update the scale subresource
		_, err = client.UpdateScale(ctx, gvr, namespace, scaleObj)
		if err != nil {
			if apierrors.IsNotFound(err) {
				log.Info("resource not found, skipping", "namespace", namespace, "name", item.GetName(), "kind", item.GetKind())
				stats.skippedStale++

				// Skip resources that no longer exist
				continue
			}

			return operationStats{}, fmt.Errorf("update scale for %s/%s: %w", item.GetKind(), item.GetName(), err)
		}

		if shouldCountAsApplied {
			stats.applied++
		}

		// Add to waiting list if awaitCompletion is configured
		if params.AwaitCompletion.Enabled {
			e.waitinglist = append(e.waitinglist, state)
		}

		// Incremental save: persist this workload's restore data immediately
		if callback != nil {
			if err := callback(key, state, replicas > 0); err != nil {
				log.Error(err, "failed to save restore data incrementally", "workload", key)
				// Continue processing - save at end as fallback
			}
		}
	}

	return stats, nil
}

// restoreWorkload restores a single workload to its previous replica count.
func (e *Executor) restoreWorkload(ctx context.Context, log logr.Logger, client Client, state WorkloadState, params executorparams.WorkloadScalerParameters) (operationOutcome, error) {
	gvr := state.GetGVR()

	log.Info("scaling up workloads",
		"name", state.Name,
		"kind", state.Kind,
		"namespace", state.Namespace,
		"replicas", state.Replicas,
	)

	// Get the scale subresource
	scaleObj, err := client.GetScale(ctx, gvr, state.Namespace, state.Name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("resource not found, skipping", "namespace", state.Namespace, "name", state.Name, "kind", state.Kind)
			return operationOutcomeSkippedStale, nil
		}

		return "", fmt.Errorf("get scale subresource: %w", err)
	}

	// Update scale.spec.replicas to restore previous count
	if err := unstructured.SetNestedField(scaleObj.Object, int64(state.Replicas), "spec", "replicas"); err != nil {
		return "", fmt.Errorf("set replicas in scale: %w", err)
	}

	// Update the scale subresource
	_, err = client.UpdateScale(ctx, gvr, state.Namespace, scaleObj)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("resource not found, skipping", "namespace", state.Namespace, "name", state.Name, "kind", state.Kind)
			return operationOutcomeSkippedStale, nil
		}

		return "", fmt.Errorf("update scale subresource: %w", err)
	}

	// Add to waiting list if awaitCompletion is configured
	if params.AwaitCompletion.Enabled {
		e.waitinglist = append(e.waitinglist, state)
	}

	return operationOutcomeApplied, nil
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
func (e *Executor) waitForReplicasScaled(ctx context.Context, log logr.Logger, client Client, gvr schema.GroupVersionResource, namespace, name string, desiredReplicas int64, timeout string) error {
	log.Info("waiting for workload replicas to scale",
		"namespace", namespace,
		"name", name,
		"desiredReplicas", desiredReplicas,
		"timeout", timeout,
	)

	w, err := waiter.NewWaiter(ctx, log, timeout)
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
