/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package provider

import (
	"sync"
	"sync/atomic"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/internal/metrics"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// dependencyNonceMap tracks per-plan monotonic counters that increment whenever a
// dependent resource — external to the HibernatePlan state itself — undergoes a
// significant state transition that the plan execution must react to (e.g., a Job
// reaching a terminal state). The current counter value is embedded into
// PlanContext.DeliveryNonce on each Reconcile call, ensuring watchable.Map.Store()
// detects a meaningful change and re-delivers the context to subscribers even when
// no HibernatePlan field has changed. This bridges the gap between dependent-resource
// events and plan-level watchable notifications, enabling near real-time reaction
// without polling.
type dependencyNonceMap struct {
	m sync.Map
}

// Add atomically adds delta to the counter for the given plan key and returns the new value.
func (dn *dependencyNonceMap) Add(key types.NamespacedName, delta int64) int64 {
	val, _ := dn.m.LoadOrStore(key, &atomic.Int64{})
	return val.(*atomic.Int64).Add(delta)
}

// Inc atomically increments the counter for the given plan key by 1 and returns the new value.
func (dn *dependencyNonceMap) Inc(key types.NamespacedName) int64 {
	return dn.Add(key, 1)
}

// Get returns the current counter value for the given plan key, or 0 if not set.
func (dn *dependencyNonceMap) Get(key types.NamespacedName) int64 {
	val, ok := dn.m.Load(key)
	if !ok {
		return 0
	}
	return val.(*atomic.Int64).Load()
}

// Set atomically sets the counter for the given plan key to n.
func (dn *dependencyNonceMap) Set(key types.NamespacedName, n int64) {
	val, _ := dn.m.LoadOrStore(key, &atomic.Int64{})
	val.(*atomic.Int64).Store(n)
}

// Delete removes the counter entry for the given plan key.
// Should be called when a plan is deleted to prevent unbounded memory growth.
func (dn *dependencyNonceMap) Delete(key types.NamespacedName) {
	dn.m.Delete(key)
}

// notificationBindingTracker tracks which NotificationResources binding keys have
// been written by each plan. This is needed because watchable.Map has no Range or
// LoadAll operation, so we must remember keys ourselves in order to delete stale
// entries when a notification disappears or a plan is deleted.
type notificationBindingTracker struct {
	mu sync.Mutex
	m  map[types.NamespacedName]map[message.NotificationBindingKey]struct{}
}

// Reconcile updates the set of binding keys for a plan. It calls deleteFn for any
// key from the previous reconcile that is absent from currentKeys (stale binding).
func (t *notificationBindingTracker) Reconcile(planKey types.NamespacedName, currentKeys map[message.NotificationBindingKey]struct{}, deleteFn func(message.NotificationBindingKey)) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.m == nil {
		t.m = make(map[types.NamespacedName]map[message.NotificationBindingKey]struct{})
	}

	prev := t.m[planKey]
	for key := range prev {
		if _, ok := currentKeys[key]; !ok {
			deleteFn(key)
		}
	}
	t.m[planKey] = currentKeys
}

// DeletePlan removes all binding keys for a deleted plan, calling deleteFn for each.
func (t *notificationBindingTracker) DeletePlan(planKey types.NamespacedName, deleteFn func(message.NotificationBindingKey)) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.m == nil {
		return
	}

	for key := range t.m[planKey] {
		deleteFn(key)
	}
	delete(t.m, planKey)
}

// channelEnqueuer implements message.PlanEnqueuer by sending GenericEvents to a channel
// that is registered as a WatchesRawSource on the PlanReconciler.
type channelEnqueuer struct {
	logger logr.Logger
	ch     chan<- event.GenericEvent
}

func (e *channelEnqueuer) Enqueue(key types.NamespacedName) {
	obj := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      key.Name,
			Namespace: key.Namespace,
		},
	}

	select {
	case e.ch <- event.GenericEvent{Object: obj}:
	default:
		e.logger.V(4).Info("enqueue channel is full, skipping event", "plan", key)
		metrics.EnqueueDropTotal.WithLabelValues(key.String()).Inc()
	}
}
