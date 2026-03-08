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

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/telepresenceio/watchable"

	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/internal/metrics"
	statusprocessor "github.com/ardikabs/hibernator/internal/provider/processor/status"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/pkg/keyedworker"
)

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
	Statuses             *statusprocessor.ControllerStatuses
	RestoreManager       *restore.Manager
	ControlPlaneEndpoint string
	RunnerImage          string
	RunnerServiceAccount string
}

// NeedLeaderElection returns true — all plan execution operations require leader election.
func (e *Coordinator) NeedLeaderElection() bool { return true }

// Start subscribes to the full PlanResources watchable map and drives per-plan workers.
// It blocks until ctx is cancelled (i.e., the manager shuts down).
func (e *Coordinator) Start(ctx context.Context) error {
	log := e.Log.WithName("coordinator")
	log.Info("starting coordinator")

	pool := keyedworker.New(
		keyedworker.WithSlotFactory[types.NamespacedName](keyedworker.LatestWinsSlot[*message.PlanContext]()),
		keyedworker.WithOnSpawnCallback[types.NamespacedName, *message.PlanContext](func(_ types.NamespacedName) { metrics.WorkerGoroutinesGauge.Inc() }),
		keyedworker.WithOnRemoveCallback[types.NamespacedName, *message.PlanContext](func(_ types.NamespacedName) { metrics.WorkerGoroutinesGauge.Dec() }),
		keyedworker.WithLogger[types.NamespacedName, *message.PlanContext](log.WithName("pool")),
	)
	pool.Register(ctx, e.workerFactory)

	sub := e.Resources.PlanResources.Subscribe(ctx)

	message.HandleSubscription(ctx, log, message.Metadata{
		Runner:  "coordinator",
		Message: "plan-resources",
	}, sub, func(update watchable.Update[types.NamespacedName, *message.PlanContext], _ chan error) {
		log.V(1).Info("received plan context update", "key", update.Key, "delete", update.Delete)

		if update.Delete {
			pool.Remove(update.Key)
			return
		}

		pool.Deliver(update.Key, update.Value)
	})

	pool.Stop()
	log.Info("coordinator stopped")
	return nil
}

// workerFactory constructs the goroutine body for a single plan key.
// The Pool calls this once per new entry and owns the resulting goroutine's lifecycle.
func (e *Coordinator) workerFactory(key types.NamespacedName, slot keyedworker.Slot[*message.PlanContext]) func(context.Context) {
	w := &Worker{
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
	}
	return w.run
}
