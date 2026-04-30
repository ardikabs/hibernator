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
	data.CapturedAt = &now

	// Initialize Status map if nil (backward compatibility)
	// When called from accumulator, Status will be pre-populated with LastReportedAt
	// When called directly (tests, legacy code), we initialize with current time
	if data.Status == nil {
		data.Status = make(map[string]ResourceStatus)
	}
	// Ensure all keys in State have a corresponding Status entry
	for key := range data.State {
		if _, exists := data.Status[key]; !exists {
			data.Status[key] = ResourceStatus{LastReportedAt: &now}
		}
	}

	// No existing data - save with the provided cycle ID
	if existing == nil {
		return m.saveNewState(data, cycleID, namespace, planName, targetName, ctx)
	}

	// Build merged state and status using strategies
	mergedState := make(map[string]any)
	mergedStatus := make(map[string]ResourceStatus)
	isSameCycle := existing.CycleID == cycleID
	selector := &strategySelector{}

	// Process reported keys from this cycle
	for key, newValue := range data.State {
		mergeCtx := &stateMergeContext{
			key:            key,
			newValue:       newValue,
			existing:       existing,
			incomingStatus: data.Status, // Pass the incoming status from accumulator
			isSameCycle:    isSameCycle,
			now:            now,
			log:            m.log,
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

		mergedStatus[key] = strategy.setStatus(mergeCtx)
	}

	// Process existing keys not reported this cycle (staleness handling)
	m.handleStaleResources(existing, data, maxStaleCount, mergedState, mergedStatus)
	return m.saveMergedData(data, existing, cycleID, mergedState, mergedStatus, namespace, planName, targetName, ctx)
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

// saveNewState handles the first save when no existing data exists.
// Data should already have Status with LastReportedAt and CapturedAt set by the caller.
func (m *Manager) saveNewState(data *Data, cycleID string, namespace, planName, targetName string, ctx context.Context) error {
	data.CycleID = cycleID

	// Status and CapturedAt should already be set by the accumulator
	return m.Save(ctx, namespace, planName, targetName, data)
}

// saveMergedData persists the merged state to storage.
// CapturedAt should already be set by the caller (accumulator) to indicate
// when the data was ready to be persisted.
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
		CapturedAt: data.CapturedAt, // Set by accumulator when flush is called
		State:      mergedState,
		Status:     mergedStatus,
	}

	return m.Save(ctx, namespace, planName, targetName, mergedData)
}
