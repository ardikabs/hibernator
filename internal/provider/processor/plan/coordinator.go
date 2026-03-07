/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package supervisor provides the Coordinator and Worker types that implement
// per-plan goroutine-based execution. Each HibernatePlan is managed by a dedicated
// Worker goroutine (the Actor), orchestrated by a single Coordinator Runnable
// (the Reactor/Factory). This eliminates the sequential HandleSubscription bottleneck
// present in the previous processor-per-phase model.
package plan

import (
	"context"
	"sync"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/telepresenceio/watchable"

	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/internal/metrics"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/pkg/conflate"
)

// workerEntry tracks a live Worker goroutine.
type workerEntry struct {
	cancel context.CancelFunc
	slot   *conflate.Pipeline[*message.PlanContext]
}

// Coordinator is the single controller-runtime Runnable that owns the lifecycle of
// all Worker goroutines. It subscribes to PlanResources, spawns a Worker
// per plan on first delivery, routes subsequent updates through each plan's context slot, and
// despawns workers when plans are deleted.
//
// It implements manager.Runnable and can be registered with mgr.Add().
type Coordinator struct {
	// Infrastructure dependencies — union of all former plan processors.
	Client               client.Client
	APIReader            client.Reader
	Clock                clock.Clock
	Log                  logr.Logger
	Scheme               *runtime.Scheme
	Planner              *scheduler.Planner
	Resources            *message.ControllerResources
	Statuses             *message.ControllerStatuses
	RestoreManager       *restore.Manager
	ControlPlaneEndpoint string
	RunnerImage          string
	RunnerServiceAccount string

	mu      sync.Mutex
	workers map[types.NamespacedName]*workerEntry
}

// NeedLeaderElection returns true — all plan execution operations require leader election.
func (e *Coordinator) NeedLeaderElection() bool { return true }

// Start subscribes to the full PlanResources watchable map and drives per-plan workers.
// It blocks until ctx is cancelled (i.e., the manager shuts down).
func (e *Coordinator) Start(ctx context.Context) error {
	log := e.Log.WithName("coordinator")
	log.Info("starting coordinator")

	sub := e.Resources.PlanResources.Subscribe(ctx)

	message.HandleSubscription(ctx, log, message.Metadata{
		Runner:  "coordinator",
		Message: "plan-resources",
	}, sub, func(update watchable.Update[types.NamespacedName, *message.PlanContext], _ chan error) {
		log.V(1).Info("received plan context update", "key", update.Key, "delete", update.Delete)

		if update.Delete {
			e.despawn(update.Key)
			return
		}

		e.delivery(ctx, update.Key, update.Value)
	})

	e.shutdownAll()
	log.Info("coordinator stopped")
	return nil
}

// delivery sends planCtx to the worker for key, spawning a new one if necessary.
func (e *Coordinator) delivery(ctx context.Context, key types.NamespacedName, planCtx *message.PlanContext) {
	e.mu.Lock()
	entry, exists := e.workers[key]
	if !exists {
		entry = e.spawn(ctx, key)
	}
	e.mu.Unlock()

	entry.slot.Send(planCtx)
}

// spawn creates a new Worker goroutine for key. Caller must hold e.mu.
func (e *Coordinator) spawn(ctx context.Context, key types.NamespacedName) *workerEntry {
	if e.workers == nil {
		e.workers = make(map[types.NamespacedName]*workerEntry)
	}
	workerCtx, cancel := context.WithCancel(ctx)
	slot := conflate.New[*message.PlanContext]()

	s := &Worker{
		key:                  key,
		log:                  e.Log.WithName("worker"),
		Client:               e.Client,
		APIReader:            e.APIReader,
		Clock:                e.Clock,
		Scheme:               e.Scheme,
		Planner:              e.Planner,
		Resources:            e.Resources,
		Statuses:             e.Statuses,
		RestoreManager:       e.RestoreManager,
		ControlPlaneEndpoint: e.ControlPlaneEndpoint,
		RunnerImage:          e.RunnerImage,
		RunnerServiceAccount: e.RunnerServiceAccount,
		slot:                 slot,
		onIdleReap:           func() { e.reap(key) },
	}

	entry := &workerEntry{cancel: cancel, slot: slot}
	e.workers[key] = entry
	metrics.WorkerGoroutinesGauge.Inc()
	go s.run(workerCtx)
	return entry
}

// despawn cancels and removes the worker for key.
func (e *Coordinator) despawn(key types.NamespacedName) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if entry, ok := e.workers[key]; ok {
		entry.cancel()
		delete(e.workers, key)
		metrics.WorkerGoroutinesGauge.Dec()
	}
}

// reap is called by a Worker that has been idle for too long. It removes the worker
// entry and decrements the gauge. Safe to call from the worker's goroutine because
// the cancel is a no-op if the worker is already exiting.
func (e *Coordinator) reap(key types.NamespacedName) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if entry, ok := e.workers[key]; ok {
		entry.cancel()
		delete(e.workers, key)
		metrics.WorkerGoroutinesGauge.Dec()
		e.Log.V(1).Info("reaped idle worker", "key", key)
	}
}

// shutdownAll cancels all live workers. Called on coordinator shutdown.
func (e *Coordinator) shutdownAll() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for key, entry := range e.workers {
		entry.cancel()
		delete(e.workers, key)
		metrics.WorkerGoroutinesGauge.Dec()
	}
}
