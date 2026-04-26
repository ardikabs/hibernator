/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"fmt"
	"sync"
	"time"

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
}

// NewReportStateHandlers creates an internal accumulator and returns the corresponding ReportStateCallback
// and flush functions. The callback accumulates saves in memory; flush writes accumulated data in a single API call.
func NewReportStateHandlers(ctx context.Context, k8sClient client.Client, log logr.Logger, namespace, plan, target, targetType string) (executor.ReportStateCallback, func() error) {
	acc := &Accumulator{
		state:      make(map[string]any),
		log:        log,
		k8sClient:  k8sClient,
		namespace:  namespace,
		plan:       plan,
		target:     target,
		targetType: targetType,
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
func (a *Accumulator) add(key string, value any) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.state[key] = value

	a.log.V(1).Info("restore data accumulated in memory",
		"key", key,
		"totalKeys", len(a.state),
	)
	return nil
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

	data := &restore.Data{
		Target:     a.target,
		Executor:   a.targetType,
		Version:    1,
		CreatedAt:  metav1.Now(),
		IsLive:     true,
		CapturedAt: time.Now().Format(time.RFC3339),
		State:      a.state, // may be nil/empty for no-op shutdown
	}

	rm := restore.NewManager(a.k8sClient)
	if err := rm.SaveState(ctx, a.namespace, a.plan, a.target, data, maxStaleCount); err != nil {
		return fmt.Errorf("save state to ConfigMap: %w", err)
	}

	log.Info("restore data flushed to ConfigMap",
		"reportedKeys", len(a.state),
	)

	return nil
}
