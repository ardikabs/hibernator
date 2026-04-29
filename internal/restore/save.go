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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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

// SaveState saves the reported state from the current shutdown cycle and performs
// staleness housekeeping in a single operation.
//
// State merging uses the Strategy Pattern with the following rules:
//  1. Different cycle: always replace (DifferentCycleStrategy)
//  2. Same cycle, never reported: use new value (FirstReportStrategy)
//  3. Same cycle, reported before, demanded: replace (DemandedStateStrategy)
//  4. Same cycle, reported before, not demanded: preserve (PreserveStateStrategy)
//
// Staleness logic:
//   - Keys not reported: increment StaleCount, evict if >= maxStaleCount.
func (m *Manager) SaveState(ctx context.Context, namespace, planName, targetName string, data *Data, maxStaleCount int, cycleID string) error {
	// Load existing data to merge with
	existing, err := m.Load(ctx, namespace, planName, targetName)
	if err != nil {
		return fmt.Errorf("load existing restore data: %w", err)
	}

	now := metav1.Now()

	// No existing data - save with the provided cycle ID
	if existing == nil {
		return m.saveInitialState(data, cycleID, now, namespace, planName, targetName, ctx)
	}

	// Build merged state and status using strategies
	mergedState := make(map[string]any)
	mergedStatus := make(map[string]ResourceStatus)
	isSameCycle := existing.CycleID == cycleID
	selector := &strategySelector{}

	// Process reported keys from this cycle
	for key, newValue := range data.State {
		mergeCtx := &stateMergeContext{
			key:         key,
			newValue:    newValue,
			existing:    existing,
			isSameCycle: isSameCycle,
			now:         now,
			log:         m.log,
		}

		strategy := selector.selectStrategy(mergeCtx)
		if strategy.shouldUseNewValue(mergeCtx) {
			mergedState[key] = m.normalizeStateValue(key, newValue)
		} else {
			// Preserve existing state
			if existingState, exists := existing.State[key]; exists {
				mergedState[key] = existingState
			}
		}

		mergedStatus[key] = strategy.prepareStatus(mergeCtx)
	}

	// Process existing keys not reported this cycle (staleness handling)
	m.handleStaleResources(existing, data, maxStaleCount, mergedState, mergedStatus)
	return m.saveMergedData(data, existing, cycleID, mergedState, mergedStatus, namespace, planName, targetName, ctx)
}

// saveInitialState handles the first save when no existing data exists.
func (m *Manager) saveInitialState(data *Data, cycleID string, now metav1.Time, namespace, planName, targetName string, ctx context.Context) error {
	data.CycleID = cycleID
	data.Status = make(map[string]ResourceStatus)
	for key := range data.State {
		data.Status[key] = ResourceStatus{LastReportedAt: &now}
	}
	return m.Save(ctx, namespace, planName, targetName, data)
}

// normalizeStateValue ensures the state value is in the correct format.
func (m *Manager) normalizeStateValue(key string, value any) any {
	if _, ok := value.(map[string]any); ok {
		return value
	}
	m.log.Info("WARNING: restore state value is not map[string]any, accumulator may not have converted it properly",
		"key", key,
		"type", fmt.Sprintf("%T", value),
		"value", value,
	)
	return value
}

// handleStaleResources processes resources not reported in this cycle.
func (m *Manager) handleStaleResources(existing, data *Data, maxStaleCount int, mergedState map[string]any, mergedStatus map[string]ResourceStatus) {
	for key, value := range existing.State {
		if _, reported := data.State[key]; !reported {
			staleCount := 0
			if existing.Status != nil {
				staleCount = existing.Status[key].StaleCount
			}

			staleCount++

			if staleCount >= maxStaleCount {
				m.log.V(1).Info("evicting stale resource",
					"key", key,
					"staleCount", staleCount,
					"maxStaleCount", maxStaleCount,
				)
				continue
			}

			mergedState[key] = value
			mergedStatus[key] = ResourceStatus{StaleCount: staleCount}
		}
	}
}

// saveMergedData persists the merged state to storage.
func (m *Manager) saveMergedData(data, existing *Data, cycleID string, mergedState map[string]any, mergedStatus map[string]ResourceStatus, namespace, planName, targetName string, ctx context.Context) error {
	if len(mergedStatus) == 0 {
		mergedStatus = nil
	}

	mergedData := &Data{
		Target:     data.Target,
		Executor:   data.Executor,
		Version:    data.Version,
		IsLive:     true,
		CycleID:    cycleID,
		CreatedAt:  existing.CreatedAt,
		CapturedAt: data.CapturedAt,
		State:      mergedState,
		Status:     mergedStatus,
	}

	return m.Save(ctx, namespace, planName, targetName, mergedData)
}
