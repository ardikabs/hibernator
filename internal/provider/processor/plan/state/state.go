/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
	statusprocessor "github.com/ardikabs/hibernator/internal/provider/processor/status"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// StateResult carries the timer directives a handler returns to the worker.
// The worker applies these after each Handle or OnDeadline call.
//
// Timer contract:
//
//	Requeue        — cancels BOTH timers and immediately re-invokes handle().
//	                 Signals "this phase is done": any active watchdog (TimeoutAfter)
//	                 is no longer relevant and must not fire on the next phase.
//	                 The next phase arms its own timers via its first Handle() call.
//
//	RequeueAfter   — resets the poll timer to fire after d.
//	                 Zero cancels the poll timer (no future poll tick).
//
//	TimeoutAfter   — arms a one-shot max-lifetime timer FROM THE FIRST CALL (idempotent).
//	                 Safe to return on every poll tick — only the first value takes effect
//	                 while the timer is already running. Scoped to the current phase:
//	                 cancelled automatically when Requeue is returned.
//	                 Zero cancels any active timeout timer.
//
//	DeadlineAfter  — sets or resets the deadline timer to fire after d from now, always
//	                 replacing any previously active deadline. Use for precise
//	                 schedule-driven wake-ups where the target time is recomputed on
//	                 each delivery (e.g. suspend-until).
//	                 Zero cancels any active deadline timer.
//
//	All zero       — cancels ALL timers. The handler is signalling "go quiet":
//	                 no further polling or deadline will fire until the next slot delivery.
type StateResult struct {
	// Requeue cancels both timers and immediately re-invokes handle().
	// Signals "this phase is done" — the active watchdog (TimeoutAfter) is cancelled
	// so it cannot fire unexpectedly on the next phase, which arms its own timers.
	Requeue bool

	// RequeueAfter resets the poll timer to fire after d.
	// Zero cancels the poll timer — no future poll tick will fire.
	RequeueAfter time.Duration

	// TimeoutAfter arms a one-shot max-lifetime timer if not already active
	// (idempotent / arm-once). Safe to include on every poll tick.
	// Zero cancels any active timeout timer.
	TimeoutAfter time.Duration

	// DeadlineAfter sets (or resets) the deadline timer to fire after d from now,
	// always replacing any previously set deadline.
	// Zero cancels any active deadline timer.
	DeadlineAfter time.Duration
}

// PlanError wraps an error to indicate a plan-level failure. When a handler
// returns a PlanError from Handle or OnDeadline, the default OnError
// implementation transitions the plan to PhaseError. Use AsPlanError to
// create one; detection is done with errors.As.
type PlanError struct{ cause error }

func (e *PlanError) Error() string { return e.cause.Error() }
func (e *PlanError) Unwrap() error { return e.cause }

// AsPlanError wraps err as a plan-level error. OnError will ingest it into
// the Plan's status and move the plan to PhaseError. Use this in Handle or
// OnDeadline when the failure reflects a problem with the plan itself, not
// a transient infrastructure issue.
func AsPlanError(err error) error {
	if err == nil {
		return nil
	}
	return &PlanError{cause: err}
}

// Handler is the interface each phase-specific handler must implement.
type Handler interface {
	// Handle is invoked on every normal reconcile tick: slot delivery and poll
	// timer fire. It drives the plan forward for its current phase and returns
	// a StateResult that tells the worker how to schedule the next wake-up.
	// A non-nil error is forwarded to OnError after the StateResult is applied.
	Handle(ctx context.Context) (StateResult, error)

	// OnDeadline is invoked when the deadline timer fires. Semantics are
	// identical to Handle but signal that a time-bound operation (e.g. a
	// suspend-until window or an execution drain timeout) has expired.
	// Handlers that do not care about deadlines inherit a no-op from *state.
	OnDeadline(ctx context.Context) (StateResult, error)

	// OnError is called by the worker as a side-effect when Handle or OnDeadline
	// returns a non-nil error. It decides only what to write to the Plan (e.g.
	// transition to PhaseError for a PlanError, or just log for transient failures).
	// Timer/requeue directives are owned by the StateResult already returned from
	// Handle or OnDeadline — OnError does not influence them.
	OnError(ctx context.Context, err error) StateResult
}

// HandlerFunc is an adapter that allows plain functions to satisfy Handler.
type HandlerFunc func(ctx context.Context, onDeadline bool) (StateResult, error)

func (f HandlerFunc) Handle(ctx context.Context) (StateResult, error) { return f(ctx, false) }

func (f HandlerFunc) OnDeadline(ctx context.Context) (StateResult, error) { return f(ctx, true) }

// OnError implements Handler for HandlerFunc. HandlerFunc shims are thin
// infrastructure wrappers whose callers already log the error inside the
// closure before returning it, so no further action is needed here.
func (f HandlerFunc) OnError(_ context.Context, _ error) StateResult { return StateResult{} }

// New creates a Handler for the given plan context. It constructs a fresh state
// from the provided configuration and dispatches to the phase-appropriate handler.
// Returns nil for unrecognised phases.
//
// The caller (Worker) is responsible for supplying a fresh Config on every
// handle() call. Because planCtx.Plan is the same pointer stored in the worker's
// cachedCtx, mutations to Plan.Status.* inside handlers propagate back to the
// worker's optimistic view automatically.
func New(key types.NamespacedName, planCtx *message.PlanContext, cfg *Config) Handler {
	if planCtx == nil || planCtx.Plan == nil {
		return nil
	}
	s := newState(key, planCtx, cfg)
	return selectHandler(s)
}

// newState constructs a private state value from the given key, plan context, and config.
func newState(key types.NamespacedName, planCtx *message.PlanContext, cfg *Config) *state {
	return &state{
		Key:                  key,
		PlanCtx:              planCtx,
		Log:                  cfg.Log,
		Client:               cfg.Client,
		APIReader:            cfg.APIReader,
		Clock:                cfg.Clock,
		Scheme:               cfg.Scheme,
		Planner:              cfg.Planner,
		Resources:            cfg.Resources,
		Statuses:             cfg.Statuses,
		RestoreManager:       cfg.RestoreManager,
		ControlPlaneEndpoint: cfg.ControlPlaneEndpoint,
		RunnerImage:          cfg.RunnerImage,
		RunnerServiceAccount: cfg.RunnerServiceAccount,
		OnJobMissing:         cfg.OnJobMissing,
		OnJobFound:           cfg.OnJobFound,
	}
}

// state holds all context and infrastructure a handler needs for a single
// handle() invocation. It is constructed fresh on each call via newState() and
// is not cached between calls.
//
// Fields are capitalised so they are accessible within the package (e.g. from
// handler types and tests). The type itself is unexported — callers outside the
// package interact only through the Handler interface and the New() factory.
type state struct {
	client.Client

	Key     types.NamespacedName
	Log     logr.Logger
	PlanCtx *message.PlanContext

	APIReader            client.Reader
	Clock                clock.Clock
	Scheme               *runtime.Scheme
	Planner              *scheduler.Planner
	Resources            *message.ControllerResources
	Statuses             *statusprocessor.ControllerStatuses
	RestoreManager       *restore.Manager
	ControlPlaneEndpoint string
	RunnerImage          string
	RunnerServiceAccount string

	OnJobMissing func(target string) bool
	OnJobFound   func(target string)
}

// Handle is a no-op so that handler types embedding *state inherit a default
// implementation for the Handle method. Handler types with specific logic override it.
func (s *state) Handle(ctx context.Context) (StateResult, error) { return StateResult{}, nil }

// OnDeadline is a no-op so that handler types that do not handle deadline events
// (all except suspendedState) inherit a default do-nothing implementation.
func (s *state) OnDeadline(ctx context.Context) (StateResult, error) { return StateResult{}, nil }

// OnError is the default error handler inherited by all phase handler types that
// embed *state. It classifies the error and acts accordingly:
//   - PlanError: ingested into Plan status via setError; plan transitions to PhaseError.
//   - Any other error: treated as transient; logged only. The caller's StateResult
//     (returned from Handle or OnDeadline) governs whether and when to requeue.
func (s *state) OnError(ctx context.Context, err error) StateResult {
	var pe *PlanError
	if errors.As(err, &pe) {
		s.Log.Error(err, "plan-level error, transitioning to PhaseError", "plan", s.Key)
		s.setError(ctx, err)
		return StateResult{Requeue: true}
	}

	s.Log.Error(err, "transient error during handler", "plan", s.Key)
	return StateResult{RequeueAfter: wellknown.RequeueIntervalOnTransientError}
}

// patchPreservingStatus patches the plan object (typically to update annotations or
// spec fields) while preserving the worker's optimistic in-memory status.
// controller-runtime's Patch deserialises the API server response into the live object,
// which overwrites Status with the server's (potentially stale) version. This helper
// snapshots Status before the patch and restores it afterwards, so that status mutations
// queued via PlanStatuses.Send are never silently reverted.
func (s *state) patchPreservingStatus(ctx context.Context, plan *hibernatorv1alpha1.HibernatePlan, patch client.Patch) error {
	savedStatus := plan.Status.DeepCopy()
	if err := s.Patch(ctx, plan, patch); err != nil {
		return err
	}
	plan.Status = *savedStatus
	return nil
}

// plan is a convenience shortcut to the current HibernatePlan.
func (b *state) plan() *hibernatorv1alpha1.HibernatePlan {
	return b.PlanCtx.Plan
}

// nextStage moves the plan to the next execution stage.
func (b *state) nextStage(nextStageIndex int) {
	plan := b.plan()
	b.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: b.Key,
		Resource:       plan,
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.CurrentStageIndex = nextStageIndex
		}),
	})
}

// setError transitions the plan to PhaseError.
func (b *state) setError(ctx context.Context, phaseErr error) {
	errMsg := "unknown error"
	if phaseErr != nil {
		errMsg = phaseErr.Error()
	}
	b.Log.V(1).Info("transitioning plan to error state", "error", errMsg)

	plan := b.plan()
	b.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: b.Key,
		Resource:       plan,
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.Phase = hibernatorv1alpha1.PhaseError
			p.Status.LastTransitionTime = ptr.To(metav1.NewTime(b.Clock.Now()))
			p.Status.ErrorMessage = errMsg
		}),
	})
}

// TransitionToSuspended records the current phase in an annotation and queues
// a PhaseSuspended status update, then returns.
//
// Graceful drain: if the plan is mid-execution (PhaseHibernating or PhaseWakingUp)
// and has Running targets, the actual transition is deferred — this method reschedules
// a poll tick and returns nil. PhaseSuspended is only written once all in-flight
// targets reach a terminal state. This ensures the execution bookmark held in
// status (CycleID, StageIndex, Executions) is accurate at suspension time, enabling
// resumeFromExecution to resume or route correctly on resume.
//
// Note: there is no drain timeout. If a Job never reaches a terminal state the plan
// holds at the current execution phase indefinitely. Use the Job TTL or manual Job
// deletion to unblock. When the deadline fires (onDeadline=true) the drain is
// bypassed and the suspension is written immediately.
func (s *state) TransitionToSuspended(ctx context.Context, onDeadline bool) (StateResult, error) {
	plan := s.plan()

	if !onDeadline && (plan.Status.Phase == hibernatorv1alpha1.PhaseHibernating || plan.Status.Phase == hibernatorv1alpha1.PhaseWakingUp) {
		drained, result, err := s.awaitExecutionDrain(ctx)
		if err != nil {
			return result, err
		}

		if !drained {
			return result, nil
		}
	}

	orig := plan.DeepCopy()
	if plan.Annotations == nil {
		plan.Annotations = make(map[string]string)
	}

	plan.Annotations[wellknown.AnnotationSuspendedAtPhase] = string(plan.Status.Phase)
	if err := s.patchPreservingStatus(ctx, plan, client.MergeFrom(orig)); err != nil {
		return StateResult{}, fmt.Errorf("failed to record suspended-at-phase annotation: %w", err)
	}

	s.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: s.Key,
		Resource:       plan,
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.Phase = hibernatorv1alpha1.PhaseSuspended
			p.Status.ErrorMessage = ""
			p.Status.LastTransitionTime = ptr.To(metav1.NewTime(s.Clock.Now()))
		}),
	})

	return StateResult{Requeue: true}, nil
}

// awaitExecutionDrain blocks the transition to PhaseSuspended until all in-flight
// targets reach a terminal state. A target is considered in-flight when LogsRef is
// set (assigned at Job dispatch time) and FinishedAt is nil.
//
// On the first call it arms the deadline timer and stashes a sentinel so subsequent
// poll-driven calls skip the arm step. It returns (false, nil) while targets are
// still running (caller should re-queue) or on the first arm cycle, and (true, nil)
// once all targets are terminal (caller should proceed with the suspension write).
// Any API error is returned directly; the poll timer is always reset on error so the
// worker keeps driving the drain.
func (s *state) awaitExecutionDrain(ctx context.Context) (drained bool, result StateResult, err error) {
	log := s.Log.WithName("execution-drain")

	plan := s.plan()
	jobs, err := s.getCurrentCycleJobs(ctx, plan)
	if err != nil {
		return false, StateResult{}, fmt.Errorf("failed to get current cycle jobs: %w", err)
	}

	s.updateExecutionStatuses(ctx, log, plan, jobs)

	hasActiveExecution := false
	countActiveExecution := 0
	for _, exec := range plan.Status.Executions {
		if exec.LogsRef != "" && exec.FinishedAt == nil {
			log.V(1).Info("found active execution", "target", exec.Target)
			hasActiveExecution = true
			countActiveExecution++
		}
	}

	if hasActiveExecution {
		log.Info("suspension requested but executions are still active, waiting for terminal state before suspending",
			"phase", plan.Status.Phase,
			"executions", countActiveExecution,
		)
		return false, StateResult{
			RequeueAfter: wellknown.RequeueIntervalOnExecution,
			TimeoutAfter: wellknown.TimeoutTransitionToSuspended,
		}, nil
	}

	// All targets are terminal — drain complete.
	return true, StateResult{}, nil
}
