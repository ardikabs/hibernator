/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package status

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/telepresenceio/watchable"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/pkg/keyedworker"
)

// Writer is the dedicated status writer processor.
// It is the ONLY component that writes status sub-resources to the K8s API.
//
// Processors queue status mutations via ControllerStatuses watchable maps.
// A small dispatcher pool routes each update to a per-plan/exception KeyedWorkerPool,
// which serialises writes for the same resource and reaps idle goroutines after
// workerIdleTTL of inactivity.
//
// APIReader must be the uncached API reader (mgr.GetAPIReader()) so that Get calls
// inside RetryOnConflict always fetch the true server state rather than a potentially
// stale informer-cache snapshot. Using the cache here would cause isPlanStatusEqual
// to compare against a lagging baseline, producing incorrect skip decisions.
type Writer struct {
	client.Client
	APIReader client.Reader
	Log       logr.Logger
	Statuses  *message.ControllerStatuses
	// Resources is used to subscribe to plan existence events.
	// When a plan is deleted, its per-key worker entry is removed from the pool.
	// Optional: if nil, pool entries are only removed when the per-key goroutine
	// is idle-reaped.
	Resources *message.ControllerResources
}

// NeedLeaderElection returns true since this processor writes status.
func (w *Writer) NeedLeaderElection() bool {
	return true
}

// planStatusDispatchers is the number of goroutines routing plan status updates
// from the global StatusQueue into the per-plan KeyedWorkerPool.
// A single goroutine is sufficient: dispatching is a non-blocking channel read +
// pool.Send(), so it can keep pace with any realistic producer rate. Real
// parallelism lives inside the KeyedWorkerPool (one goroutine per plan key).
// exceptionStatusDispatchers mirrors this for ScheduleExceptions.

// workerIdleTTL is how long a per-key worker goroutine waits with no work before
// it exits. The next status update for that key restarts it on demand.
const workerIdleTTL = 30 * time.Minute

// drainTimeout is the maximum duration the writer waits to flush buffered status
// updates after the manager context is cancelled. This gives in-flight updates a
// chance to reach the API server before the process exits.
const drainTimeout = 5 * time.Second

// Start implements manager.Runnable. It wires two KeyedWorkerPools (one for
// HibernatePlans, one for ScheduleExceptions) behind a single dispatcher goroutine
// each that routes updates from the global StatusQueues into the per-key pools.
//
// Architecture:
//
//	StatusQueue.C() ──► dispatcher goroutine (1) ──► KeyedWorkerPool
//	                                                        │
//	                                              per-key goroutine (1 per plan/exception)
//	                                              FIFO, idle-reaped after 30m
//
// A single dispatcher is sufficient because dispatching is O(1): a non-blocking
// channel receive followed by pool.Send() (map lock + buffered channel write).
// All real parallelism lives inside the KeyedWorkerPool.
//
// Plan/exception deletions are detected via resource subscriptions and trigger
// pool.Remove(key) to reclaim goroutine and channel memory immediately.
func (w *Writer) Start(ctx context.Context) error {
	log := w.Log.WithName("writer")
	log.Info("starting status writer processor", "workerIdleTTL", workerIdleTTL.String())

	// Per-plan pool: serialises writes per plan, reaps idle goroutines.
	planPool := keyedworker.New(
		keyedworker.WithIdleTTL[types.NamespacedName, *message.PlanStatusUpdate](workerIdleTTL),
		keyedworker.WithLogger[types.NamespacedName, *message.PlanStatusUpdate](log.WithName("plan-pool")),
	)
	planPool.Start(ctx, func(ctx context.Context, update *message.PlanStatusUpdate) error {
		return w.handlePlanStatusUpdate(ctx, log, update)
	})

	// Per-exception pool: same guarantees for ScheduleExceptions.
	exceptionPool := keyedworker.New(
		keyedworker.WithIdleTTL[types.NamespacedName, *message.ExceptionStatusUpdate](workerIdleTTL),
		keyedworker.WithLogger[types.NamespacedName, *message.ExceptionStatusUpdate](log.WithName("exception-pool")),
	)
	exceptionPool.Start(ctx, func(ctx context.Context, update *message.ExceptionStatusUpdate) error {
		return w.handleExceptionStatusUpdate(ctx, log, update)
	})

	var wg sync.WaitGroup

	// Plan status dispatcher: reads from the global queue and routes to the plan pool.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case update := <-w.Statuses.PlanStatuses.C():
				if update == nil {
					continue
				}
				log.V(1).Info("received plan status update", "plan", update.NamespacedName.String())
				planPool.Send(update.NamespacedName, update)
			}
		}
	}()

	// Exception status dispatcher: reads from the global queue and routes to the exception pool.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case update := <-w.Statuses.ExceptionStatuses.C():
				if update == nil {
					continue
				}
				log.V(1).Info("received exception status update", "exception", update.NamespacedName.String())
				exceptionPool.Send(update.NamespacedName, update)
			}
		}
	}()

	// Resource deletion subscriptions: remove per-key pool entries when plans or exceptions
	// are deleted from K8s so their goroutine and channel memory are reclaimed promptly.
	if w.Resources != nil {
		// Plan deletion subscription.
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub := w.Resources.PlanResources.Subscribe(ctx)
			message.HandleSubscription(
				ctx, log,
				message.Metadata{Runner: "status.writer", Message: "plan-resources"},
				sub,
				func(update watchable.Update[types.NamespacedName, *message.PlanContext], _ chan error) {
					if update.Delete {
						log.V(1).Info("plan deleted, removing pool entry", "plan", update.Key.String())
						planPool.Remove(update.Key)
					}
				},
			)
		}()

		// Exception deletion subscription.
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub := w.Resources.ExceptionResources.Subscribe(ctx)
			message.HandleSubscription(
				ctx, log,
				message.Metadata{Runner: "status.writer", Message: "exception-resources"},
				sub,
				func(update watchable.Update[types.NamespacedName, *hibernatorv1alpha1.ScheduleException], _ chan error) {
					if update.Delete {
						log.V(1).Info("exception deleted, removing pool entry", "exception", update.Key.String())
						exceptionPool.Remove(update.Key)
					}
				},
			)
		}()
	}

	wg.Wait()

	// Graceful drain: flush status updates still buffered in the global queues.
	w.drain(log, planPool, exceptionPool)

	log.Info("status writer processor stopped")
	return nil
}

// drain flushes status updates still buffered in the global StatusQueues after
// the manager context is cancelled. Per-key pool buffers are intentionally not
// drained here — the next reconciliation cycle will re-push any missed updates.
func (w *Writer) drain(log logr.Logger, planPool *keyedworker.Pool[types.NamespacedName, *message.PlanStatusUpdate], exceptionPool *keyedworker.Pool[types.NamespacedName, *message.ExceptionStatusUpdate]) {
	planRemaining := w.Statuses.PlanStatuses.Len()
	exceptionRemaining := w.Statuses.ExceptionStatuses.Len()
	if planRemaining == 0 && exceptionRemaining == 0 {
		return
	}

	log.Info("draining buffered status updates",
		"planRemaining", planRemaining,
		"exceptionRemaining", exceptionRemaining,
		"timeout", drainTimeout.String())

	ctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()

	drained := 0
	for {
		select {
		case <-ctx.Done():
			log.Info("drain timeout reached", "drained", drained)
			return
		case update, ok := <-w.Statuses.PlanStatuses.C():
			if !ok || update == nil {
				continue
			}
			planPool.Send(update.NamespacedName, update)
			drained++
		case update, ok := <-w.Statuses.ExceptionStatuses.C():
			if !ok || update == nil {
				continue
			}
			exceptionPool.Send(update.NamespacedName, update)
			drained++
		default:
			// Both channels empty — drain complete.
			log.Info("drain complete", "drained", drained)
			return
		}
	}
}

// handlePlanStatusUpdate fetches a fresh copy of the plan, applies the mutation, and writes status.
//
// Hook execution order:
//  1. PreHook (if set) — called once with the current K8s plan before any mutation.
//     A non-nil error aborts the transition entirely; nothing is written to K8s.
//  2. Mutate + RetryOnConflict write — applies the mutation with conflict-safe retries.
//  3. PostHook (if set) — called once with the successfully-persisted plan.
//     Errors are logged but do not roll back the transition.
func (w *Writer) handlePlanStatusUpdate(ctx context.Context, log logr.Logger, update *message.PlanStatusUpdate) error {
	log = log.WithValues("plan", update.NamespacedName.String())

	// PreHook: validate or prepare before any mutation is applied.
	// Fetches the current plan state so the hook sees truth, not a stale cache.
	if update.PreHook != nil {
		current := &hibernatorv1alpha1.HibernatePlan{}
		if err := w.APIReader.Get(ctx, update.NamespacedName, current); err != nil {
			if apierrors.IsNotFound(err) {
				return fmt.Errorf("plan %s not found: %w", update.NamespacedName.String(), err)
			}

			return fmt.Errorf("get plan for pre-hook: %w", err)
		}

		log.V(1).Info("executing pre-transition hook", "phase", current.Status.Phase)
		if err := update.PreHook(ctx, current); err != nil {
			return fmt.Errorf("plan %s: pre-transition hook rejected: %w", update.NamespacedName.String(), err)
		}
	}

	// Write the mutation with conflict-safe retries.
	var written *hibernatorv1alpha1.HibernatePlan
	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		plan := new(hibernatorv1alpha1.HibernatePlan)
		if err := w.APIReader.Get(ctx, update.NamespacedName, plan); err != nil {
			return fmt.Errorf("get plan for status update: %w", err)
		}

		// Snapshot status before applying the mutation so we can detect a no-op.
		before := plan.Status

		// Apply the mutation.
		update.Mutate(&plan.Status)

		// Skip the write if nothing meaningful changed — avoids an unnecessary
		// K8s API call on poll-timer re-drives or duplicate deliveries.
		if isPlanStatusEqual(before, plan.Status) {
			log.V(1).Info("plan status unchanged, skipping write", "phase", plan.Status.Phase)
			return nil
		}

		if err := w.Status().Update(ctx, plan); err != nil {
			return fmt.Errorf("write plan status: %w", err)
		}

		written = plan

		if log.GetSink() != nil && log.GetSink().Enabled(1) {
			log = log.V(1).WithValues("diff", planStatusDiff(before, plan.Status))
		}

		log.Info("plan status updated", "previousPhase", before.Phase, "phase", plan.Status.Phase)

		return nil
	}); err != nil {
		return err
	}

	// PostHook: notify or audit after the transition is confirmed in K8s.
	// Errors are logged but do not roll back the already-persisted state.
	if update.PostHook != nil && written != nil {
		log.V(1).Info("executing post-transition hook", "phase", written.Status.Phase)

		if err := update.PostHook(ctx, written); err != nil {
			log.Error(err, "post-transition hook failed (non-fatal)", "phase", written.Status.Phase)
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Status equality helpers
// ---------------------------------------------------------------------------

// planStatusDiff returns a human-readable unified diff between two HibernatePlanStatus
// values, applying the same ignore options as isPlanStatusEqual so that pure
// bookkeeping timestamps are excluded from the output.
func planStatusDiff(a, b hibernatorv1alpha1.HibernatePlanStatus) string {
	return cmp.Diff(a, b,
		cmpopts.IgnoreFields(hibernatorv1alpha1.HibernatePlanStatus{}, "LastTransitionTime", "LastRetryTime"),
		cmpopts.IgnoreFields(hibernatorv1alpha1.ExecutionStatus{}, "StartedAt", "FinishedAt"),
		cmpopts.IgnoreFields(hibernatorv1alpha1.ExceptionReference{}, "AppliedAt", "ExpiredAt"),
	)
}

// isPlanStatusEqual returns true when two HibernatePlanStatus values are semantically
// identical. Pure-timestamp fields that change on every write are excluded from
// comparison so that a mutation that only bumps a timestamp does not trigger an
// unnecessary K8s API call.
func isPlanStatusEqual(a, b hibernatorv1alpha1.HibernatePlanStatus) bool {
	return cmp.Equal(a, b,
		// Phase-transition and retry timestamps — changed by almost every mutation;
		// excluding them lets us detect whether the actual state content changed.
		cmpopts.IgnoreFields(hibernatorv1alpha1.HibernatePlanStatus{}, "LastTransitionTime", "LastRetryTime"),
		// Per-target execution timing — semantic state (State, Message, JobRef) is
		// what matters; start/finish times are bookkeeping.
		cmpopts.IgnoreFields(hibernatorv1alpha1.ExecutionStatus{}, "StartedAt", "FinishedAt"),
		// Exception reference bookkeeping timestamps — ValidFrom/ValidUntil are
		// semantic and are intentionally NOT ignored.
		cmpopts.IgnoreFields(hibernatorv1alpha1.ExceptionReference{}, "AppliedAt", "ExpiredAt"),
	)
}

// isExceptionStatusEqual returns true when two ScheduleExceptionStatus values are
// semantically identical, ignoring pure bookkeeping timestamps.
func isExceptionStatusEqual(a, b hibernatorv1alpha1.ScheduleExceptionStatus) bool {
	return cmp.Equal(a, b,
		cmpopts.IgnoreFields(hibernatorv1alpha1.ScheduleExceptionStatus{}, "AppliedAt", "ExpiredAt"),
	)
}

// handleExceptionStatusUpdate fetches a fresh copy of the exception, applies the mutation, and writes status.
func (w *Writer) handleExceptionStatusUpdate(ctx context.Context, log logr.Logger, update *message.ExceptionStatusUpdate) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		log = log.WithValues("exception", update.NamespacedName.String())

		exception := &hibernatorv1alpha1.ScheduleException{}
		nn := types.NamespacedName{
			Name:      update.NamespacedName.Name,
			Namespace: update.NamespacedName.Namespace,
		}
		if err := w.APIReader.Get(ctx, nn, exception); err != nil {
			if apierrors.IsNotFound(err) {
				return fmt.Errorf("exception %s not found: %w", update.NamespacedName.String(), err)
			}

			return fmt.Errorf("get exception for status update: %w", err)
		}

		// Snapshot before mutation.
		before := exception.Status

		// Apply the mutation.
		update.Mutate(&exception.Status)

		// Skip write if nothing meaningful changed.
		if isExceptionStatusEqual(before, exception.Status) {
			log.V(1).Info("exception status unchanged, skipping write", "state", exception.Status.State)
			return nil
		}

		if err := w.Status().Update(ctx, exception); err != nil {
			return fmt.Errorf("write exception status: %w", err)
		}

		log.Info("exception status updated", "state", exception.Status.State)
		return nil
	})
}
