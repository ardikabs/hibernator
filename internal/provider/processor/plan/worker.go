/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package plan

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	k8sutil "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/internal/metrics"
	"github.com/ardikabs/hibernator/internal/provider/processor/plan/state"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/pkg/conflate"
)

// consecutiveJobMissThreshold is the number of consecutive poll cycles a running
// target's Job must be absent before it is considered genuinely lost and the target
// is reset to StatePending for re-dispatch.
const consecutiveJobMissThreshold = 3

// workerIdleTimeout is the duration after which a Worker that has received no slot
// delivery and no timer event self-terminates. The Coordinator re-spawns it on
// the next delivery, keeping the goroutine pool bounded.
const workerIdleTimeout = 30 * time.Minute

// Worker is a long-lived goroutine that manages every phase of a single
// HibernatePlan's lifecycle. It receives updates from the Coordinator via a
// latest-wins planContextSlot and manages internal timers for re-driving execution as needed.
//
//   - pollTimer   — re-drives execution while runner Jobs are in-flight.
//   - retryTimer  — fires after exponential backoff when in PhaseError.
//   - deadlineTimer — fires when a suspend-until deadline expires.
//
// Optimistic state: the worker mutates s.cachedCtx.Plan.Status.* in-place so that
// subsequent timer-driven handle() calls always see the latest in-memory state without
// waiting for a K8s roundtrip or a watchable re-delivery.
type Worker struct {
	key types.NamespacedName
	log logr.Logger

	// Infrastructure dependencies — same union as Coordinator.
	client.Client
	APIReader            client.Reader
	Clock                clock.Clock
	Scheme               *k8sutil.Scheme
	Planner              *scheduler.Planner
	Resources            *message.ControllerResources
	Statuses             *message.ControllerStatuses
	RestoreManager       *restore.Manager
	ControlPlaneEndpoint string
	RunnerImage          string
	RunnerServiceAccount string

	// cachedCtx is the most-recent PlanContext delivered by the engine.
	// Mutated in-place for optimistic local updates.
	cachedCtx *message.PlanContext

	// Inbound context slot from the engine — latest-wins delivery.
	slot *conflate.Pipeline[*message.PlanContext]

	// Timers — nil means inactive.
	requeueTimer  *time.Timer
	deadlineTimer *time.Timer

	// consecutiveJobMisses tracks how many consecutive poll cycles each target's
	// runner Job has been absent while still in StateRunning. Lazily initialised
	// on the first execution-phase handle() call. Resets when the job reappears.
	consecutiveJobMisses map[string]int

	// cachedState is re-used across handle() calls to reduce per-call allocation.
	// Updated in-place before each handle() via refreshState().
	cachedState *state.State

	// onIdleReap is a callback to the Coordinator's reap() method.
	// Called when the idle timer fires so the coordinator can clean up the entry.
	onIdleReap func()
}

// run is the worker's event loop. It blocks on five event sources:
//
//   - slot.ready     — a new PlanContext was delivered by the coordinator (latest-wins).
//   - pollTimer      — re-drives the current phase while runner Jobs are in-flight.
//   - retryTimer     — fires after exponential backoff when the plan is in PhaseError.
//   - deadlineTimer  — fires when a suspend-until deadline expires.
//   - idleTimer      — fires when the worker has been idle for workerIdleTimeout.
//
// On each event the worker builds a fresh planState for the current phase and calls
// Handle(ctx). A nil timer channel never fires, so inactive timers are represented as nil.
func (s *Worker) run(ctx context.Context) {
	defer s.cleanup()

	workerIdleTimer := time.NewTimer(workerIdleTimeout)
	defer workerIdleTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-s.slot.C():
			planCtx := s.slot.Recv()
			if planCtx == nil {
				continue
			}
			s.mergeIncoming(planCtx)
			s.handle(ctx, s.cachedCtx, false)
			workerIdleTimer.Reset(workerIdleTimeout)

		case <-timerChan(s.requeueTimer):
			s.requeueTimer = nil
			if s.cachedCtx != nil {
				s.handle(ctx, s.cachedCtx, false)
			}
			workerIdleTimer.Reset(workerIdleTimeout)

		case <-timerChan(s.deadlineTimer):
			s.deadlineTimer = nil
			if s.cachedCtx != nil {
				s.handle(ctx, s.cachedCtx, true)
			}
			workerIdleTimer.Reset(workerIdleTimeout)

		case <-workerIdleTimer.C:
			s.log.V(1).Info("worker idle timeout reached, self-terminating", "plan", s.key)
			if s.onIdleReap != nil {
				s.onIdleReap()
			}
			return
		}
	}
}

// buildState constructs or refreshes the cached state.State wired to this worker.
// The Config is allocated once and reused across handle() calls to reduce GC pressure (M5).
// Only the per-call field (PlanCtx) is updated on each invocation.
func (s *Worker) buildState() *state.State {
	if s.cachedState == nil {
		s.cachedState = &state.State{
			Key: s.key,
			Log: s.log,

			Client:               s.Client,
			APIReader:            s.APIReader,
			Clock:                s.Clock,
			Scheme:               s.Scheme,
			Planner:              s.Planner,
			Statuses:             s.Statuses,
			RestoreManager:       s.RestoreManager,
			ControlPlaneEndpoint: s.ControlPlaneEndpoint,
			RunnerImage:          s.RunnerImage,
			RunnerServiceAccount: s.RunnerServiceAccount,

			OnJobMissing: s.trackConsecutiveJobMiss,
			OnJobFound:   s.resetConsecutiveJobMiss,

			RequeueAfter:   s.setRequeueTimer,
			CancelRequeue:  s.stopRequeueTimer,
			DeadlineAfter:  s.setDeadlineTimer,
			CancelDeadline: s.stopDeadlineTimer,
		}
	}

	// Update the per-call field.
	s.cachedState.PlanCtx = s.cachedCtx
	return s.cachedState
}

// handle dispatches the plan to the appropriate state handler based on its current phase.
func (s *Worker) handle(ctx context.Context, planCtx *message.PlanContext, onDeadline bool) {
	if planCtx == nil || planCtx.Plan == nil {
		return
	}

	plan := planCtx.Plan
	planName := plan.Name
	phaseBefore := string(plan.Status.Phase)

	start := time.Now()
	handler := state.Factory(s.buildState())
	if handler == nil {
		s.log.V(1).Info("unrecognised phase, skipping", "phase", phaseBefore)
		return
	}

	if onDeadline {
		handler.OnDeadline(ctx)
	} else {
		handler.Handle(ctx)
	}

	duration := time.Since(start).Seconds()
	phaseAfter := string(plan.Status.Phase)

	// ReconcileTotal / ReconcileDuration — one observation per handle() call.
	metrics.ReconcileTotal.WithLabelValues(planName, phaseBefore, "success").Inc()
	metrics.ReconcileDuration.WithLabelValues(planName, phaseBefore).Observe(duration)

	// ActivePlanGauge — update on phase transition.
	if phaseBefore != phaseAfter {
		if phaseBefore != "" {
			metrics.ActivePlanGauge.WithLabelValues(phaseBefore).Dec()
		}
		metrics.ActivePlanGauge.WithLabelValues(phaseAfter).Inc()
	}
}

// trackConsecutiveJobMiss increments the consecutive-miss counter for target and returns
// true once the counter reaches consecutiveJobMissThreshold, signalling that the job
// is genuinely gone and the target should be reset to StatePending for re-dispatch.
// Only active during PhaseHibernating and PhaseWakingUp; returns false for all other phases.
func (s *Worker) trackConsecutiveJobMiss(target string) bool {
	if s.cachedCtx == nil {
		return false
	}
	phase := s.cachedCtx.Plan.Status.Phase
	if phase != hibernatorv1alpha1.PhaseHibernating && phase != hibernatorv1alpha1.PhaseWakingUp {
		return false
	}
	if s.consecutiveJobMisses == nil {
		s.consecutiveJobMisses = make(map[string]int)
	}
	s.consecutiveJobMisses[target]++
	if s.consecutiveJobMisses[target] >= consecutiveJobMissThreshold {
		delete(s.consecutiveJobMisses, target)
		return true
	}
	return false
}

// resetConsecutiveJobMiss clears the miss counter for target when its job reappears,
// preventing a false threshold crossing caused by transient informer-cache lag.
func (s *Worker) resetConsecutiveJobMiss(target string) {
	if s.consecutiveJobMisses != nil {
		delete(s.consecutiveJobMisses, target)
	}
}

// mergeIncoming accepts a fresh PlanContext from the watchable-map delivery and
// merges it into the worker's cached state. The incoming delivery carries fresh Spec,
// Annotations, Labels, and provider-computed fields (ScheduleResult, HasRestoreData,
// Exceptions) from the informer cache, but its Status may be stale — the informer
// only reflects what the API server has persisted, which lags behind the worker's
// optimistic in-memory mutations by at least one StatusWriter round-trip.
//
// To prevent the stale informer status from clobbering optimistic mutations
// (e.g. Active→Hibernating transition that hasn't been persisted yet), we carry
// forward the worker's in-memory Status onto the incoming plan whenever there is
// a prior cached context. This is safe because the StatusWriter is the ONLY
// component that writes to the status sub-resource, and the worker is the sole
// producer for that plan's status mutations. reconcileTruth() provides the
// correction path if the optimistic status ever genuinely diverges from the
// persisted state.
func (s *Worker) mergeIncoming(incoming *message.PlanContext) {
	if s.cachedCtx == nil || s.cachedCtx.Plan == nil {
		// First delivery — no optimistic state to preserve.
		s.cachedCtx = incoming
		return
	}

	// Carry the optimistic status forward onto the fresh plan object.
	// Everything else (Spec, ObjectMeta, provider-derived fields) comes from
	// the incoming delivery since those are authoritative from the informer.
	status := s.cachedCtx.Plan.Status

	s.cachedCtx = incoming
	s.cachedCtx.Plan.Status = status
}

// ---------------------------------------------------------------------------
// Timer helpers
// ---------------------------------------------------------------------------
func (s *Worker) setRequeueTimer(d time.Duration) {
	s.stopRequeueTimer()
	s.requeueTimer = time.NewTimer(d)
}

func (s *Worker) stopRequeueTimer() {
	if s.requeueTimer != nil {
		s.requeueTimer.Stop()
		s.requeueTimer = nil
	}
}

func (s *Worker) setDeadlineTimer(d time.Duration) {
	if s.deadlineTimer == nil {
		s.deadlineTimer = time.NewTimer(d)
	}
}

func (s *Worker) stopDeadlineTimer() {
	if s.deadlineTimer != nil {
		s.deadlineTimer.Stop()
		s.deadlineTimer = nil
	}
}

// cleanup cancels all active timers when the worker exits.
func (s *Worker) cleanup() {
	s.stopRequeueTimer()
	s.stopDeadlineTimer()
}

// timerChan returns the channel of t, or nil if t is nil.
// A nil channel never selects, so inactive timers effectively disable their case.
func timerChan(t *time.Timer) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}
