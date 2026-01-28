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

	// State contains executor-specific restore state.
	State map[string]interface{} `json:"state,omitempty"`
}

// configMapName generates the ConfigMap name for a plan's restore data.
func configMapName(planName string) string {
	return fmt.Sprintf("hibernator-restore-%s", planName)
}

// Save persists restore data for a target.
func (m *Manager) Save(ctx context.Context, namespace, planName, targetName string, data *Data) error {
	cmName := configMapName(planName)

	// Get or create the ConfigMap
	var cm corev1.ConfigMap
	err := m.client.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      cmName,
	}, &cm)

	if errors.IsNotFound(err) {
		// Create new ConfigMap
		cm = corev1.ConfigMap{
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
		return m.client.Create(ctx, &cm)
	}
	return m.client.Update(ctx, &cm)
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

// LoadAll retrieves all restore data for a plan.
func (m *Manager) LoadAll(ctx context.Context, namespace, planName string) (map[string]*Data, error) {
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

	result := make(map[string]*Data)
	for key, dataStr := range cm.Data {
		// Skip non-JSON keys
		if len(key) < 6 || key[len(key)-5:] != ".json" {
			continue
		}

		var data Data
		if err := json.Unmarshal([]byte(dataStr), &data); err != nil {
			continue // Skip malformed entries
		}

		targetName := key[:len(key)-5] // Remove .json suffix
		result[targetName] = &data
	}

	return result, nil
}

// Delete removes restore data for a target.
func (m *Manager) Delete(ctx context.Context, namespace, planName, targetName string) error {
	cmName := configMapName(planName)

	var cm corev1.ConfigMap
	err := m.client.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      cmName,
	}, &cm)

	if errors.IsNotFound(err) {
		return nil // Already gone
	}
	if err != nil {
		return fmt.Errorf("get restore configmap: %w", err)
	}

	key := fmt.Sprintf("%s.json", targetName)
	delete(cm.Data, key)

	// If empty, delete the entire ConfigMap
	if len(cm.Data) == 0 {
		return m.client.Delete(ctx, &cm)
	}

	return m.client.Update(ctx, &cm)
}

// DeleteAll removes all restore data for a plan.
func (m *Manager) DeleteAll(ctx context.Context, namespace, planName string) error {
	cmName := configMapName(planName)

	var cm corev1.ConfigMap
	err := m.client.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      cmName,
	}, &cm)

	if errors.IsNotFound(err) {
		return nil // Already gone
	}
	if err != nil {
		return fmt.Errorf("get restore configmap: %w", err)
	}

	return m.client.Delete(ctx, &cm)
}

// GetConfigMapRef returns the ConfigMap reference for a plan's restore data.
func (m *Manager) GetConfigMapRef(planName string) string {
	return configMapName(planName)
}
