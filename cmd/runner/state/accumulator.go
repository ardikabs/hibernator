/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/internal/restore"
)

// Accumulator batches incremental saves in memory before flushing to ConfigMap.
// This reduces Kubernetes API calls from N to 1 (where N = number of resources).
type Accumulator struct {
	mu         sync.Mutex
	state      map[string]any // Accumulated state captured from live API calls
	log        logr.Logger
	k8sClient  client.Client
	namespace  string
	plan       string
	target     string
	targetType string
	cycleID    string // Current execution cycle ID for intent tracking
}

// NewReportStateHandlers creates an internal accumulator and returns the corresponding ReportStateCallback
// and flush functions. The callback accumulates saves in memory; flush writes accumulated data in a single API call.
// The cycleID parameter is used to track which execution cycle first captured each resource's intent.
func NewReportStateHandlers(ctx context.Context, k8sClient client.Client, log logr.Logger, namespace, plan, target, targetType, cycleID string) (executor.ReportStateCallback, func() error) {
	acc := &Accumulator{
		state:      make(map[string]any),
		log:        log,
		k8sClient:  k8sClient,
		namespace:  namespace,
		plan:       plan,
		target:     target,
		targetType: targetType,
		cycleID:    cycleID,
	}

	callback := func(key string, value any) error {
		return acc.add(key, value)
	}

	flush := func() error {
		return acc.flush(ctx)
	}

	return callback, flush
}

// add accumulates a key-value pair in memory.
// Converts struct values to map[string]any to ensure compatibility with restore manager.
func (a *Accumulator) add(key string, value any) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Convert struct to map[string]any if needed
	// This ensures compatibility with restore.Manager.SaveState() which expects map[string]any
	mapValue, err := normalizeStateValue(value)
	if err != nil {
		a.log.Error(err, "when converting state, therefore we keep it as-is",
			"target", a.target,
			"targetType", a.targetType,
			"key", key,
			"type", fmt.Sprintf("%T", value),
			"value", value)
		a.state[key] = value
	} else {
		a.state[key] = mapValue
	}

	a.log.V(1).Info("restore data accumulated in memory",
		"key", key,
		"totalKeys", len(a.state),
	)
	return nil
}

// normalizeStateValue converts a value to map[string]any for consistent state representation.
// If the value is already a map[string]any, it returns it as-is.
// Otherwise, it marshals to JSON and unmarshals to map[string]any.
func normalizeStateValue(value any) (map[string]any, error) {
	// Fast path: already a map[string]any
	if m, ok := value.(map[string]any); ok {
		return m, nil
	}

	// Convert via JSON marshal/unmarshal
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal value: %w", err)
	}

	var result map[string]any
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		return nil, fmt.Errorf("unmarshal to map: %w", err)
	}

	return result, nil
}

// flush saves all accumulated data to ConfigMap via SaveState, which performs
// merge and staleness housekeeping in a single API call.
//
// If no data was accumulated (executor performed a no-op shutdown), an empty-state
// restore point is written so that a subsequent wakeup can proceed without error.
func (a *Accumulator) flush(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	log := a.log.WithValues("plan", fmt.Sprintf("%s/%s", a.namespace, a.plan), "target", a.target)

	const maxStaleCount = 3

	now := metav1.Now()
	data := &restore.Data{
		Target:     a.target,
		Executor:   a.targetType,
		Version:    1,
		CreatedAt:  now,
		IsLive:     true,
		CapturedAt: &now,
		State:      a.state, // may be nil/empty for no-op shutdown
	}

	rm := restore.NewManager(a.k8sClient, log)
	if err := rm.SaveState(ctx, a.namespace, a.plan, a.target, data, maxStaleCount, a.cycleID); err != nil {
		return fmt.Errorf("save state to ConfigMap: %w", err)
	}

	log.Info("restore data flushed to ConfigMap",
		"reportedKeys", len(a.state),
	)

	return nil
}
