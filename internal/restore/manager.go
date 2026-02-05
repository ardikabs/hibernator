/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// LabelPlan is the label key for plan name.
	LabelPlan = "hibernator.ardikabs.com/plan"

	// LabelTarget is the label key for target name.
	LabelTarget = "hibernator.ardikabs.com/target"

	// LabelManagedBy is the label key for managed-by.
	LabelManagedBy = "app.kubernetes.io/managed-by"

	// AnnotationRestoreVersion is the annotation for restore data version.
	AnnotationRestoreVersion = "hibernator.ardikabs.com/restore-version"

	// AnnotationRestoredPrefix is the prefix for per-target restoration tracking annotations.
	AnnotationRestoredPrefix = "hibernator.ardikabs.com/restored-"

	// DataKeyRestore is the ConfigMap data key for restore data.
	DataKeyRestore = "restore.json"

	// MaxConfigMapSize is the maximum size for ConfigMap data (900KB to leave room for overhead).
	MaxConfigMapSize = 900 * 1024
)

// Manager handles restore data persistence using ConfigMaps.
type Manager struct {
	client client.Client
}

// NewManager creates a new restore data manager.
func NewManager(c client.Client) *Manager {
	return &Manager{client: c}
}

// Data represents restore metadata for a target.
type Data struct {
	// Target is the target name.
	Target string `json:"target"`

	// Executor is the executor type.
	Executor string `json:"executor"`

	// Version is a monotonic version for optimistic concurrency.
	Version int64 `json:"version"`

	// CreatedAt is when the restore data was created.
	CreatedAt metav1.Time `json:"createdAt"`

	// IsLive indicates if the restore data was captured from a running/active state.
	// true = high quality (resources were running), false = low quality (resources already shutdown).
	// IsLive resets to false when wakening from hibernation.
	IsLive bool `json:"isLive"`

	// CapturedAt is the ISO8601 timestamp when the restore data was captured by the executor.
	CapturedAt string `json:"capturedAt,omitempty"`

	// State contains executor-specific restore state.
	State map[string]interface{} `json:"state,omitempty"`
}

// configMapName generates the ConfigMap name for a plan's restore data.
func configMapName(planName string) string {
	return fmt.Sprintf("hibernator-restore-%s", planName)
}

// CreateOrSave persists restore data for a target.
func (m *Manager) CreateOrSave(ctx context.Context, namespace, planName, targetName string, data *Data) error {
	cmName := configMapName(planName)

	// Get or create the ConfigMap
	cm := &corev1.ConfigMap{}
	err := m.client.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      cmName,
	}, cm)

	var patch client.Patch
	if errors.IsNotFound(err) {
		// Create new ConfigMap
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cmName,
				Namespace: namespace,
				Labels: map[string]string{
					LabelPlan:      planName,
					LabelManagedBy: "hibernator",
				},
			},
			Data: make(map[string]string),
		}
	} else if err != nil {
		return fmt.Errorf("get restore configmap: %w", err)
	} else {
		patch = client.MergeFrom(cm.DeepCopy())
	}

	// Serialize data
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal restore data: %w", err)
	}

	// Check size
	if len(dataBytes) > MaxConfigMapSize {
		return fmt.Errorf("restore data too large (%d bytes), max %d", len(dataBytes), MaxConfigMapSize)
	}

	// Store with target-specific key
	key := fmt.Sprintf("%s.json", targetName)
	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data[key] = string(dataBytes)

	// Create or update
	if cm.ResourceVersion == "" {
		return m.client.Create(ctx, cm)
	}

	return m.client.Patch(ctx, cm, patch)
}

// Load retrieves restore data for a target.
func (m *Manager) Load(ctx context.Context, namespace, planName, targetName string) (*Data, error) {
	cmName := configMapName(planName)

	var cm corev1.ConfigMap
	err := m.client.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      cmName,
	}, &cm)

	if errors.IsNotFound(err) {
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

// SaveOrPreserve saves restore data with quality-aware preservation and merge logic.
// Quality rules:
//   - If existing data has IsLive=true and new has IsLive=false, preserves existing keys
//   - If same quality or new is better, merges new keys into existing state
//
// Merge logic:
//   - New keys are added to existing State map
//   - Existing keys are preserved if existing IsLive=true and new IsLive=false
//   - Existing keys are overwritten if new IsLive=true or same quality
func (m *Manager) SaveOrPreserve(ctx context.Context, namespace, planName, targetName string, data *Data) error {
	// Check if restore data already exists
	existing, err := m.Load(ctx, namespace, planName, targetName)
	if err != nil {
		return fmt.Errorf("check existing restore data: %w", err)
	}

	if existing == nil {
		// No existing data, save as-is
		return m.CreateOrSave(ctx, namespace, planName, targetName, data)
	}

	// Merge logic: combine existing and new state maps
	existingIsLive := existing.IsLive
	newIsLive := data.IsLive

	// Determine which quality to use for final result
	finalIsLive := existingIsLive || newIsLive // Best quality wins

	// Merge State maps
	mergedState := make(map[string]interface{})

	// First, copy all existing state
	for key, value := range existing.State {
		mergedState[key] = value
	}

	// Then, merge new state based on quality rules
	for key, newValue := range data.State {
		existingValue, existsInOld := existing.State[key]

		if !existsInOld {
			// New key not in existing → always add
			mergedState[key] = newValue
		} else if existingIsLive && !newIsLive {
			// Existing key is high-quality, new is low-quality → preserve existing
			mergedState[key] = existingValue
		} else {
			// New is same/better quality → overwrite
			mergedState[key] = newValue
		}
	}

	// Create merged data with best quality and combined state
	mergedData := &Data{
		Target:     data.Target,
		Executor:   data.Executor,
		Version:    data.Version,
		CreatedAt:  data.CreatedAt,
		IsLive:     finalIsLive,
		CapturedAt: data.CapturedAt,
		State:      mergedState,
	}

	// Save merged data
	return m.CreateOrSave(ctx, namespace, planName, targetName, mergedData)
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

	if errors.IsNotFound(err) {
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
	annotationKey := AnnotationRestoredPrefix + targetName
	cm.Annotations[annotationKey] = "true"

	// Reset IsLive flag for this target's data after successful restore
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

	if errors.IsNotFound(err) {
		// No ConfigMap means no restore data, consider all restored
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("get restore configmap: %w", err)
	}

	// Check if all targets have restored annotation
	for _, targetName := range targetNames {
		annotationKey := AnnotationRestoredPrefix + targetName
		if cm.Annotations[annotationKey] != "true" {
			return false, nil
		}
	}

	return true, nil
}

// UnlockRestoreData clears all restored-* annotations without deleting ConfigMap data.
// This unlocks the restore data for the next hibernation cycle.
func (m *Manager) UnlockRestoreData(ctx context.Context, namespace, planName string) error {
	cmName := configMapName(planName)

	cm := &corev1.ConfigMap{}
	err := m.client.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      cmName,
	}, cm)

	if errors.IsNotFound(err) {
		// No ConfigMap to unlock
		return nil
	}
	if err != nil {
		return fmt.Errorf("get restore configmap: %w", err)
	}

	// Remove all restored-* annotations
	if cm.Annotations != nil {
		for key := range cm.Annotations {
			if len(key) > len(AnnotationRestoredPrefix) && key[:len(AnnotationRestoredPrefix)] == AnnotationRestoredPrefix {
				delete(cm.Annotations, key)
			}
		}
	}

	return m.client.Update(ctx, cm)
}

// HasRestoreData checks if restore ConfigMap exists for the plan.
func (m *Manager) HasRestoreData(ctx context.Context, namespace, planName string) (bool, error) {
	cmName := configMapName(planName)

	var cm corev1.ConfigMap
	err := m.client.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      cmName,
	}, &cm)

	if errors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get restore configmap: %w", err)
	}

	return len(cm.Data) > 0, nil
}
