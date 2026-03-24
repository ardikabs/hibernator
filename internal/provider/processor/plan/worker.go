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
	statusprocessor "github.com/ardikabs/hibernator/internal/provider/processor/status"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/pkg/keyedworker"
)

const (
	// consecutiveJobMissThreshold is the number of consecutive poll cycles a running
	// target's Job must be absent before it is considered genuinely lost and the target
	// is reset to StatePending for re-dispatch.
	consecutiveJobMissThreshold = 3

	// workerIdleTimeout is the duration after which a Worker that has received no slot
	// delivery and no timer event self-terminates. The Coordinator re-spawns it on
	// the next delivery, keeping the goroutine pool bounded.
	workerIdleTimeout = 30 * time.Minute

	// maxHandleDepth is the maximum allowed depth of recursive handle() calls due to Requeue=true results.
	// This prevents unbounded recursion in the case of a misbehaving handler that always returns Requeue=true.
	// If this depth is exceeded, the worker will log an error and stop processing further events for the current plan
	// until the next slot delivery or timer event, which resets the depth counter.
	maxHandleDepth = 5
)

// Worker is a long-lived goroutine that manages every phase of a single
// HibernatePlan's lifecycle. It receives updates from the Coordinator via a
// latest-wins slot (keyedworker.Slot[*message.PlanContext]) and manages internal
// timers for re-driving execution as needed.
//
//   - requeueTimer  — re-drives execution while runner Jobs are in-flight, and
//     fires after exponential backoff when the plan is in PhaseError.
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
	Statuses             *statusprocessor.ControllerStatuses
	RestoreManager       *restore.Manager
	ControlPlaneEndpoint string
	RunnerImage          string
	RunnerServiceAccount string

	// cachedCtx is the most-recent PlanContext delivered by the Coordinator.
	// Mutated in-place for optimistic local updates.
	cachedCtx *message.PlanContext

	// Inbound context slot from the Coordinator — latest-wins delivery.
	slot keyedworker.Slot[*message.PlanContext]

	// Timers — nil means inactive.
	requeueTimer  clock.Timer
	deadlineTimer clock.Timer

	// consecutiveJobMisses tracks how many consecutive poll cycles each target's
	// runner Job has been absent while still in StateRunning. Lazily initialised
	// on the first execution-phase handle() call. Resets when the job reappears.
	consecutiveJobMisses map[string]int
}

// run is the worker's event loop. It blocks on five event sources:
//
//   - slot.ready    — a new PlanContext was delivered by the coordinator (latest-wins).
//   - requeueTimer  — re-drives the current phase while runner Jobs are in-flight, and
//     fires after exponential backoff when the plan is in PhaseError.
//   - deadlineTimer — fires when a suspend-until deadline expires.
//   - idleTimer     — fires when the worker has been idle for workerIdleTimeout.
//
// On each event the worker builds a fresh planState for the current phase and calls
// Handle(ctx). A nil timer channel never fires, so inactive timers are represented as nil.
func (s *Worker) run(ctx context.Context) {
	defer s.cleanup()

	workerIdleTimer := s.Clock.NewTimer(workerIdleTimeout)
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

		case <-workerIdleTimer.C():
			s.log.V(1).Info("worker idle timeout reached, self-terminating", "plan", s.key)
			return
		}
	}
}

// buildConfig assembles a *state.Config from the worker's infrastructure
// dependencies and timer-control closures. A fresh Config is constructed on
// every handle() call so handlers are fully stateless with respect to the worker.
func (s *Worker) buildConfig() *state.Config {
	return &state.Config{
		Log:                  s.log,
		Client:               s.Client,
		APIReader:            s.APIReader,
		Clock:                s.Clock,
		Scheme:               s.Scheme,
		Planner:              s.Planner,
		Resources:            s.Resources,
		Statuses:             s.Statuses,
		RestoreManager:       s.RestoreManager,
		ControlPlaneEndpoint: s.ControlPlaneEndpoint,
		RunnerImage:          s.RunnerImage,
		RunnerServiceAccount: s.RunnerServiceAccount,
		OnJobMissing:         s.trackConsecutiveJobMiss,
		OnJobFound:           s.resetConsecutiveJobMiss,
	}
}

func (s *Worker) handle(ctx context.Context, planCtx *message.PlanContext, onDeadline bool) {
	s.handleWithDepth(ctx, planCtx, onDeadline, 0)
}

// handle dispatches the plan to the appropriate state handler based on its current phase.
func (s *Worker) handleWithDepth(ctx context.Context, planCtx *message.PlanContext, onDeadline bool, depth int) {
	if planCtx == nil || planCtx.Plan == nil {
		return
	}

	plan := planCtx.Plan
	planName := plan.Name
	phaseBefore := string(plan.Status.Phase)

	if depth >= maxHandleDepth {
		s.log.Error(nil, "handle() recursion depth exceeded; possible phase loop",
			"plan", s.key,
			"phase", phaseBefore)
		return
	}

	start := time.Now()
	handler := state.New(s.key, planCtx, s.buildConfig())
	if handler == nil {
		s.log.V(1).Info("unrecognised phase, skipping", "phase", phaseBefore)
		return
	}

	var (
		result state.StateResult
		err    error
	)

	status := "success"
	if onDeadline {
		result, err = handler.OnDeadline(ctx)
	} else {
		result, err = handler.Handle(ctx)
	}
	if err != nil {
		status = "error"
		result = handler.OnError(ctx, err)
	}

	duration := time.Since(start).Seconds()
	phaseAfter := string(plan.Status.Phase)

	// ReconcileTotal / ReconcileDuration — one observation per handle() call.
	metrics.ReconcileTotal.WithLabelValues(planName, phaseBefore, status).Inc()
	metrics.ReconcileDuration.WithLabelValues(planName, phaseBefore).Observe(duration)

	// ActivePlanGauge — update on phase transition.
	if phaseBefore != phaseAfter {
		if phaseBefore != "" {
			metrics.ActivePlanGauge.WithLabelValues(phaseBefore).Dec()
		}
		metrics.ActivePlanGauge.WithLabelValues(phaseAfter).Inc()
	}

	// Apply timer directives from StateResult.
	if result.Requeue {
		// Phase transition: cancel all timers then immediately re-evaluate.
		// The next phase arms its own timers via its first Handle() call.
		s.stopRequeueTimer()
		s.stopDeadlineTimer()
		s.handleWithDepth(ctx, planCtx, false, depth+1)
		return
	}

	s.applyTimers(result)
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
// producer for that plan's status mutations.
//
// The reconciler is the source of truth for status correction. The Provider
// reconciles on every schedule tick and object change, delivering a fresh informer
// snapshot on each cycle. Once the StatusWriter successfully persists a status
// mutation, the next delivery carries the correct persisted status — naturally
// collapsing any optimistic-vs-persisted divergence window. On operator restart,
// cachedCtx is nil and the first delivery unconditionally adopts the informer
// snapshot, so cold-start divergence cannot occur. Residual divergence risk on
// StatusWriter failure is accepted and bounded by the StatusWriter's retry logic.
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

// applyTimers applies the non-Requeue timer directives from a StateResult.
// Zero RequeueAfter cancels the poll timer; zero TimeoutAfter and DeadlineAfter
// cancel the deadline timer. See StateResult for the full contract.
func (s *Worker) applyTimers(result state.StateResult) {
	if result.RequeueAfter > 0 {
		s.setRequeueTimer(result.RequeueAfter)
	} else {
		s.stopRequeueTimer()
	}

	switch {
	case result.TimeoutAfter > 0:
		s.setDeadlineTimer(result.TimeoutAfter) // arm-once: no-op if already running
	case result.DeadlineAfter > 0:
		s.stopDeadlineTimer()
		s.deadlineTimer = s.Clock.NewTimer(result.DeadlineAfter) // always-override
	default:
		s.stopDeadlineTimer()
	}
}

func (s *Worker) setRequeueTimer(d time.Duration) {
	s.stopRequeueTimer()
	s.requeueTimer = s.Clock.NewTimer(d)
}

func (s *Worker) stopRequeueTimer() {
	if s.requeueTimer != nil {
		s.requeueTimer.Stop()
		s.requeueTimer = nil
	}
}

func (s *Worker) setDeadlineTimer(d time.Duration) {
	if s.deadlineTimer == nil {
		s.deadlineTimer = s.Clock.NewTimer(d)
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
func timerChan(t clock.Timer) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C()
}
