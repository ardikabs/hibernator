/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
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
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// Handler is the interface each phase-specific handler must implement.
type Handler interface {
	Handle(ctx context.Context)
	OnDeadline(ctx context.Context)
}

// HandlerFunc is an adapter that allows plain functions to satisfy Handler.
type HandlerFunc func(ctx context.Context, onDeadline bool)

func (f HandlerFunc) Handle(ctx context.Context) { f(ctx, false) }

func (f HandlerFunc) OnDeadline(ctx context.Context) { f(ctx, true) }

// Config holds all the context and infrastructure a state handler needs.
// It is constructed fresh on each handle() call from the supervisor's latest cached
// PlanContext. Because PlanCtx is the same pointer stored in s.cachedCtx, mutations
// to PlanCtx.Plan.Status.* propagate back to the supervisor's optimistic view automatically.
type State struct {
	client.Client

	Key     types.NamespacedName
	Log     logr.Logger
	PlanCtx *message.PlanContext

	APIReader            client.Reader
	Clock                clock.Clock
	Scheme               *runtime.Scheme
	Planner              *scheduler.Planner
	Statuses             *message.ControllerStatuses
	RestoreManager       *restore.Manager
	ControlPlaneEndpoint string
	RunnerImage          string
	RunnerServiceAccount string

	// Timer controls — closures over the supervisor's timer methods so that state
	// handlers can drive timing without knowing about PlanSupervisor internals.
	DeadlineAfter  func(time.Duration)
	CancelDeadline func()

	RequeueAfter  func(time.Duration)
	CancelRequeue func()

	// Job-miss safeguard — closures owned by the Worker that track how many
	// consecutive poll cycles a running target's Job has been absent.
	// OnJobMissing increments the counter and returns true when the threshold is
	// reached (job considered lost → reset target to StatePending).
	// OnJobFound resets the counter when the job reappears.
	// Both are nil-safe; passing nil disables the safeguard.
	OnJobMissing func(target string) bool
	OnJobFound   func(target string)
}

func (s *State) Handle(ctx context.Context)     {}
func (s *State) OnDeadline(ctx context.Context) {}

// patchPreservingStatus patches the plan object (typically to update annotations or
// spec fields) while preserving the worker's optimistic in-memory status.
// controller-runtime's Patch deserialises the API server response into the live object,
// which overwrites Status with the server's (potentially stale) version. This helper
// snapshots Status before the patch and restores it afterwards, so that status mutations
// queued via PlanStatuses.Send are never silently reverted.
func (s *State) patchPreservingStatus(ctx context.Context, plan *hibernatorv1alpha1.HibernatePlan, patch client.Patch) error {
	savedStatus := plan.Status.DeepCopy()
	if err := s.Patch(ctx, plan, patch); err != nil {
		return err
	}
	plan.Status = *savedStatus
	return nil
}

// dispatch invokes the handlerFactory to construct the handler for the plan's
// current phase and executes it synchronously within the same handle() call.
// This allows a state to transition to a new phase and immediately drive its
// execution without waiting for the next worker poll cycle.
// (e.g. idleState transitions plan to Hibernating, then dispatches to construct
// and run hibernatingState handler synchronously).
func (s *State) dispatch(ctx context.Context) {
	if st := Factory(s); st != nil {
		st.Handle(ctx)
	}
}

// plan is a convenience shortcut to the current HibernatePlan.
func (b *State) plan() *hibernatorv1alpha1.HibernatePlan {
	return b.PlanCtx.Plan
}

// nextStage moves the plan to the next execution stage.
func (b *State) nextStage(nextStageIndex int) {
	mutate := func(st *hibernatorv1alpha1.HibernatePlanStatus) {
		st.CurrentStageIndex = nextStageIndex
	}

	plan := b.plan()
	mutate(&plan.Status)
	b.Statuses.PlanStatuses.Send(&message.PlanStatusUpdate{
		NamespacedName: b.Key,
		Mutate:         mutate,
	})
}

// setError transitions the plan to PhaseError.
func (b *State) setError(ctx context.Context, phaseErr error) {
	errMsg := "unknown error"
	if phaseErr != nil {
		errMsg = phaseErr.Error()
	}
	b.Log.V(1).Info("transitioning plan to error state", "error", errMsg)

	mutate := func(s *hibernatorv1alpha1.HibernatePlanStatus) {
		s.Phase = hibernatorv1alpha1.PhaseError
		s.LastTransitionTime = ptr.To(metav1.NewTime(b.Clock.Now()))
		s.ErrorMessage = errMsg
	}

	plan := b.plan()
	mutate(&plan.Status)
	b.Statuses.PlanStatuses.Send(&message.PlanStatusUpdate{
		NamespacedName: b.Key,
		Mutate:         mutate,
	})

	b.dispatch(ctx)
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
func (s *State) TransitionToSuspended(ctx context.Context, onDeadline bool) error {
	plan := s.plan()

	if !onDeadline && (plan.Status.Phase == hibernatorv1alpha1.PhaseHibernating || plan.Status.Phase == hibernatorv1alpha1.PhaseWakingUp) {
		drained, err := s.awaitExecutionDrain(ctx)
		if err != nil {
			return err
		}

		if !drained {
			return nil
		}
	}

	orig := plan.DeepCopy()
	if plan.Annotations == nil {
		plan.Annotations = make(map[string]string)
	}

	plan.Annotations[wellknown.AnnotationSuspendedAtPhase] = string(plan.Status.Phase)
	if err := s.patchPreservingStatus(ctx, plan, client.MergeFrom(orig)); err != nil {
		return fmt.Errorf("failed to record suspended-at-phase annotation: %w", err)
	}

	mutate := func(st *hibernatorv1alpha1.HibernatePlanStatus) {
		st.Phase = hibernatorv1alpha1.PhaseSuspended
		st.ErrorMessage = ""
		st.LastTransitionTime = ptr.To(metav1.NewTime(s.Clock.Now()))
	}

	mutate(&plan.Status)
	s.Statuses.PlanStatuses.Send(&message.PlanStatusUpdate{
		NamespacedName: s.Key,
		Mutate:         mutate,
	})

	return nil
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
// supervisor keeps driving the drain.
func (s *State) awaitExecutionDrain(ctx context.Context) (drained bool, err error) {
	log := s.Log.WithName("execution-drain")

	defer s.DeadlineAfter(wellknown.DeadlineTransitionToSuspended)

	plan := s.plan()
	jobs, err := s.getCurrentCycleJobs(ctx, plan)
	if err != nil {
		s.RequeueAfter(wellknown.RequeueIntervalOnExecution)
		return false, fmt.Errorf("failed to get current cycle jobs: %w", err)
	}

	if err := s.updateExecutionStatuses(ctx, log, plan, jobs); err != nil {
		s.RequeueAfter(wellknown.RequeueIntervalOnExecution)
		return false, fmt.Errorf("failed to update execution statuses: %w", err)
	}

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
		s.RequeueAfter(wellknown.RequeueIntervalOnExecution)
		return false, nil
	}

	// All targets are terminal — clean up drain state and cancel timers.
	s.CancelRequeue()
	s.CancelDeadline()
	return true, nil
}
