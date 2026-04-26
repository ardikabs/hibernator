/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ardikabs/hibernator/internal/wellknown"
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

	// IsLive indicates if the restore data was captured from a running/active state.
	// true = high quality (resources were running), false = low quality (resources already shutdown).
	// IsLive resets to false when wakening from hibernation.
	IsLive bool `json:"isLive"`

	// CreatedAt is when the restore data was created.
	// This is set once when the restore data entry is first created and never changes.
	CreatedAt metav1.Time `json:"createdAt"`

	// CapturedAt is when the state was last captured/updated by the executor.
	// This changes each time the state is updated during a hibernation cycle.
	// When a target is first initialized without state, this will be nil.
	CapturedAt *metav1.Time `json:"capturedAt,omitempty"`

	// State contains executor-specific restore state.
	State map[string]any `json:"state,omitempty"`

	// StaleCounts tracks consecutive hibernation cycles where a resource was not reported by the executor.
	StaleCounts map[string]int `json:"staleCounts,omitempty"`
}

// UnmarshalJSON implements custom JSON unmarshaling for backward compatibility.
// It handles both old string format and new metav1.Time format for CapturedAt.
func (d *Data) UnmarshalJSON(data []byte) error {
	type DataAlias Data
	aux := &struct {
		CapturedAt interface{} `json:"capturedAt,omitempty"`
		*DataAlias
	}{
		DataAlias: (*DataAlias)(d),
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Handle CapturedAt backward compatibility
	if aux.CapturedAt != nil {
		switch v := aux.CapturedAt.(type) {
		case string:
			// Old format: parse string timestamp
			if v != "" {
				t, err := time.Parse(time.RFC3339, v)
				if err != nil {
					// Try alternative formats
					t, err = time.Parse("2006-01-02T15:04:05Z", v)
					if err != nil {
						return fmt.Errorf("parse capturedAt string: %w", err)
					}
				}
				mt := metav1.NewTime(t)
				d.CapturedAt = &mt
			}
		case map[string]interface{}:
			// New format: metav1.Time object
			if sec, ok := v["seconds"].(float64); ok {
				t := time.Unix(int64(sec), 0)
				mt := metav1.NewTime(t)
				d.CapturedAt = &mt
			}
		}
	}

	return nil
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

// Save persists restore data for a target.
func (m *Manager) Save(ctx context.Context, namespace, planName, targetName string, data *Data) error {
	cmName := configMapName(planName)

	// Get or create the ConfigMap
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
	} else if err != nil {
		return fmt.Errorf("get restore configmap: %w", err)
	}

	patch := client.MergeFrom(cm.DeepCopy())

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

// SaveState saves the reported state from the current shutdown cycle and performs
// staleness housekeeping in a single operation.
//
// Logic:
//  1. Keys present in reportedState: update their value and clear any StaleCount.
//  2. Existing keys missing from reportedState: increment their StaleCount.
//     If StaleCount >= maxStaleCount, the key is evicted from the state entirely.
//  3. The result is saved in a single API call.
//
// This replaces the previous SaveOrPreserve + HousekeepStaleResources two-step pattern.
func (m *Manager) SaveState(ctx context.Context, namespace, planName, targetName string, data *Data, maxStaleCount int) error {
	// Load existing data to merge with
	existing, err := m.Load(ctx, namespace, planName, targetName)
	if err != nil {
		return fmt.Errorf("load existing restore data: %w", err)
	}

	if existing == nil {
		// No existing data, save as-is
		return m.Save(ctx, namespace, planName, targetName, data)
	}

	// Build merged state
	mergedState := make(map[string]any)
	staleCounts := make(map[string]int)

	// Copy existing stale counts
	for k, v := range existing.StaleCounts {
		staleCounts[k] = v
	}

	// Process reported keys: update value and clear stale count
	for key, value := range data.State {
		mergedState[key] = value
		delete(staleCounts, key)
	}

	// Process existing keys not reported this cycle
	for key, value := range existing.State {
		if _, reported := data.State[key]; !reported {
			// Not reported this cycle — increment stale counter
			staleCounts[key]++
			if staleCounts[key] >= maxStaleCount {
				// Evict: exceeded grace period
				delete(staleCounts, key)
			} else {
				// Keep existing value until eviction
				mergedState[key] = value
			}
		}
	}

	// Clean up empty staleCounts map
	if len(staleCounts) == 0 {
		staleCounts = nil
	}

	mergedData := &Data{
		Target:      data.Target,
		Executor:    data.Executor,
		Version:     data.Version,
		IsLive:      true,
		CreatedAt:   existing.CreatedAt, // Preserve original creation time
		CapturedAt:  data.CapturedAt,    // Update to latest capture time
		State:       mergedState,
		StaleCounts: staleCounts,
	}

	return m.Save(ctx, namespace, planName, targetName, mergedData)
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

// UnlockRestoreData clears all restored-* annotations without deleting ConfigMap data.
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
