/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// DataKeyRestore is the ConfigMap data key for restore data.
	DataKeyRestore = "restore.json"

	// MaxConfigMapSize is the maximum size for ConfigMap data (900KB to leave room for overhead).
	MaxConfigMapSize = 900 * 1024
)

// Manager handles restore data persistence using ConfigMaps.
type Manager struct {
	client client.Client
	log    logr.Logger
}

// NewManager creates a new restore data manager.
func NewManager(c client.Client, log logr.Logger) *Manager {
	if log.GetSink() == nil {
		log = logr.Discard()
	}
	return &Manager{client: c, log: log}
}

// ResourceStatus tracks per-resource metadata for staleness tracking and future extensions.
type ResourceStatus struct {
	// StaleCount tracks consecutive hibernation cycles where this resource was not reported.
	// When this reaches maxStaleCount, the resource is evicted from State.
	StaleCount int `json:"staleCount,omitempty"`

	// LastReportedAt records when this resource was last reported by the executor.
	// Used for same-cycle restart detection: if nil, resource hasn't been reported in this cycle.
	// If set, the existing state is preserved during restart unless the new state is demanded.
	LastReportedAt *metav1.Time `json:"lastReportedAt,omitempty"`
}

// Data represents restore metadata for a target.
type Data struct {
	// Target is the target name.
	Target string `json:"target"`

	// Executor is the executor type.
	Executor string `json:"executor"`

	// Version is a monotonic version for optimistic concurrency.
	Version int64 `json:"version"`

	// IsLive indicates if the restore data was captured from a running/active state.
	// true = high quality (resources were running), false = low quality (resources already shutdown).
	// IsLive resets to false when wakening from hibernation.
	IsLive bool `json:"isLive"`

	// CycleID identifies the hibernation execution cycle that created this restore data.
	// This is set during transitionToHibernating and cleared when all targets are restored.
	// It enables idempotent restart - when a runner restarts, the same cycle ID is reused
	// from existing live restore data, allowing ManagedByCycleIDs comparisons to work correctly.
	CycleID string `json:"cycleID,omitempty"`

	// CreatedAt is when the restore data was created.
	// This is set once when the restore data entry is first created and never changes.
	CreatedAt metav1.Time `json:"createdAt"`

	// CapturedAt is when the state was last captured/updated by the executor.
	// This changes each time the state is updated during a hibernation cycle.
	// When a target is first initialized without state, this will be nil.
	CapturedAt *metav1.Time `json:"capturedAt,omitempty"`

	// State contains executor-specific restore state.
	// Key is the resource identifier (e.g., "instance:i-123", "default/Deployment/app").
	State map[string]any `json:"state,omitempty"`

	// Status contains per-resource metadata for staleness tracking and other attributes.
	// Key matches State keys. This separation allows State to contain only executor-specific
	// data while Status holds hibernator-internal metadata.
	Status map[string]ResourceStatus `json:"status,omitempty"`
}

// configMapName generates the ConfigMap name for a plan's restore data.
func configMapName(planName string) string {
	return fmt.Sprintf("hibernator-restore-%s", planName)
}

func GetRestoreConfigMap(planName string) string {
	return configMapName(planName)
}

// PrepareRestorePoint ensures a clean ConfigMap exists for storing restore data for the plan.
func (m *Manager) PrepareRestorePoint(ctx context.Context, namespace, planName string) error {
	cmName := configMapName(planName)

	// Check if ConfigMap exists
	cm := &corev1.ConfigMap{}
	err := m.client.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      cmName,
	}, cm)

	if apierrors.IsNotFound(err) {
		// Create new ConfigMap
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cmName,
				Namespace: namespace,
				Labels: map[string]string{
					wellknown.LabelPlan: planName,
				},
			},
			Data: make(map[string]string),
		}
		return m.client.Create(ctx, cm)
	}

	if err != nil {
		return fmt.Errorf("get restore configmap: %w", err)
	}

	// Otherwise exists - clear restore point
	// Assuming plan reset means reset all live data to not live, and snapshot the previous state as-is in the annotation
	if cm.Annotations == nil {
		cm.Annotations = make(map[string]string)
	}

	previous, err := json.Marshal(cm.Data)
	if err == nil {
		if len(previous) == 0 {
			previous = []byte("n/a")
		}
		cm.Annotations[wellknown.AnnotationPreviousRestoreState] = string(previous)
	}

	for key, val := range cm.Data {
		state := &Data{}
		if err := json.Unmarshal([]byte(val), state); err == nil {
			// Reset IsLive flag
			state.IsLive = false
		}

		stateBytes, err := json.Marshal(state)
		if err == nil {
			cm.Data[key] = string(stateBytes)
		}
	}

	return m.client.Update(ctx, cm)
}

// Load retrieves restore data for a target.
func (m *Manager) Load(ctx context.Context, namespace, planName, targetName string) (*Data, error) {
	cmName := configMapName(planName)

	var cm corev1.ConfigMap
	err := m.client.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      cmName,
	}, &cm)

	if apierrors.IsNotFound(err) {
		return nil, nil // No restore data
	}
	if err != nil {
		return nil, fmt.Errorf("get restore configmap: %w", err)
	}

	key := fmt.Sprintf("%s.json", targetName)
	dataStr, ok := cm.Data[key]
	if !ok {
		return nil, nil // No restore data for this target
	}

	var data Data
	if err := json.Unmarshal([]byte(dataStr), &data); err != nil {
		return nil, fmt.Errorf("unmarshal restore data: %w", err)
	}

	return &data, nil
}

// isDemandedState checks if a resource is in demanded state (wasRunning=true or wasScaled=true)
func isDemandedState(state map[string]any) bool {
	if wasRunning, ok := state["wasRunning"].(bool); ok && wasRunning {
		return true
	}
	if wasScaled, ok := state["wasScaled"].(bool); ok && wasScaled {
		return true
	}
	return false
}

// MarkTargetRestored marks a target as successfully restored.
// Sets annotation: hibernator.ardikabs.com/restored-{targetName}: "true"
func (m *Manager) MarkTargetRestored(ctx context.Context, namespace, planName, targetName string) error {
	cmName := configMapName(planName)

	var cm corev1.ConfigMap
	err := m.client.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      cmName,
	}, &cm)

	if apierrors.IsNotFound(err) {
		// ConfigMap doesn't exist - nothing to mark
		return nil
	}
	if err != nil {
		return fmt.Errorf("get restore configmap: %w", err)
	}

	// Set annotation
	if cm.Annotations == nil {
		cm.Annotations = make(map[string]string)
	}
	annotationKey := wellknown.AnnotationRestoredPrefix + targetName
	cm.Annotations[annotationKey] = "true"

	// Reset IsLive flag and clear CycleID for this target's data after successful restore
	key := fmt.Sprintf("%s.json", targetName)
	if val, ok := cm.Data[key]; ok {
		var data Data
		if err := json.Unmarshal([]byte(val), &data); err == nil {
			// Mark data as consumed - next hibernation should capture fresh live state
			data.IsLive = false
			if dataBytes, err := json.Marshal(&data); err == nil {
				cm.Data[key] = string(dataBytes)
			}
		}
	}

	return m.client.Update(ctx, &cm)
}

// MarkAllTargetsRestored checks if all targets have been restored.
func (m *Manager) MarkAllTargetsRestored(ctx context.Context, namespace, planName string, targetNames []string) (bool, error) {
	cmName := configMapName(planName)

	var cm corev1.ConfigMap
	err := m.client.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      cmName,
	}, &cm)

	if apierrors.IsNotFound(err) {
		// No ConfigMap means no restore data, consider all restored
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("get restore configmap: %w", err)
	}

	// Check if all targets have restored annotation
	for _, targetName := range targetNames {
		annotationKey := wellknown.AnnotationRestoredPrefix + targetName
		if cm.Annotations[annotationKey] != "true" {
			return false, nil
		}
	}

	return true, nil
}

// UnlockRestoreData clears all restored-* annotations and resets CycleID for all targets.
// This unlocks the restore data for the next hibernation cycle.
func (m *Manager) UnlockRestoreData(ctx context.Context, namespace, planName string) error {
	cmName := configMapName(planName)

	cm := &corev1.ConfigMap{}
	err := m.client.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      cmName,
	}, cm)

	if apierrors.IsNotFound(err) {
		// No ConfigMap to unlock
		return nil
	}
	if err != nil {
		return fmt.Errorf("get restore configmap: %w", err)
	}

	// Remove all restored-* annotations
	if cm.Annotations != nil {
		for key := range cm.Annotations {
			if len(key) > len(wellknown.AnnotationRestoredPrefix) && key[:len(wellknown.AnnotationRestoredPrefix)] == wellknown.AnnotationRestoredPrefix {
				delete(cm.Annotations, key)
			}
		}
	}

	// Clear CycleID from all target data to mark restoration as complete
	for key, val := range cm.Data {
		var data Data
		if err := json.Unmarshal([]byte(val), &data); err == nil && data.CycleID != "" {
			m.log.V(1).Info("clearing CycleID after successful restoration",
				"target", data.Target,
				"clearedCycleID", data.CycleID,
			)
			data.CycleID = ""
			if dataBytes, err := json.Marshal(&data); err == nil {
				cm.Data[key] = string(dataBytes)
			}
		}
	}

	return m.client.Update(ctx, cm)
}

// HasRestoreData checks if restore ConfigMap exists for the plan, and at least have eligible restore point,
// as indicated by `.isLive=true`
func (m *Manager) HasRestoreData(ctx context.Context, namespace, planName string) (bool, error) {
	cmName := configMapName(planName)

	var cm corev1.ConfigMap
	err := m.client.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      cmName,
	}, &cm)

	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get restore configmap: %w", err)
	}

	for _, val := range cm.Data {
		var data Data
		if err := json.Unmarshal([]byte(val), &data); err != nil {
			return false, nil
		}

		// Return early if any data is live
		// Runner will take care of restore point staleness
		if data.IsLive {
			return true, nil
		}
	}

	return len(cm.Data) > 0, nil
}
