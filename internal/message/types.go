/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package message

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/types"

	"github.com/telepresenceio/watchable"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

// ControllerResources contains the watchable maps for provider-to-processor communication.
// Providers write to these maps; processors subscribe and react to changes.
type ControllerResources struct {
	// PlanResources holds enriched PlanContext for each HibernatePlan.
	PlanResources watchable.Map[types.NamespacedName, *PlanContext]

	// ExceptionResources holds raw ScheduleException resources.
	ExceptionResources watchable.Map[types.NamespacedName, *hibernatorv1alpha1.ScheduleException]
}

// ControllerStatuses holds the status update queues for processor-to-status-writer communication.
// Processors call Send() on these queues; a worker pool in the status writer drains them.
type ControllerStatuses struct {
	// PlanStatuses is the queue of pending status updates for HibernatePlans.
	PlanStatuses *StatusQueue[*PlanStatusUpdate]

	// ExceptionStatuses is the queue of pending status updates for ScheduleExceptions.
	ExceptionStatuses *StatusQueue[*ExceptionStatusUpdate]
}

// NewControllerStatuses initialises a ControllerStatuses with ready-to-use queues.
func NewControllerStatuses() *ControllerStatuses {
	return &ControllerStatuses{
		PlanStatuses:      newStatusQueue[*PlanStatusUpdate]("plan"),
		ExceptionStatuses: newStatusQueue[*ExceptionStatusUpdate]("exception"),
	}
}

// PlanContext contains all data needed by processors to make decisions for a single HibernatePlan.
// It is the value stored in PlanResources and represents the provider's enriched view of the plan.
type PlanContext struct {
	// Plan is the HibernatePlan resource fetched from K8s.
	Plan *hibernatorv1alpha1.HibernatePlan

	// Exceptions is the list of ScheduleExceptions associated with this plan.
	Exceptions []hibernatorv1alpha1.ScheduleException

	// ScheduleResult contains the pre-computed schedule evaluation.
	// Nil if schedule evaluation failed or is not applicable.
	ScheduleResult *ScheduleEvaluation

	// HasRestoreData indicates whether restore data exists for this plan.
	HasRestoreData bool

	// ExecutionProgress summarises terminal job counts for the current execution cycle.
	// It serves purely as a change signal for watchable equality — when a job reaches
	// a terminal state the counts change, PlanContext.Equal() returns false, and the
	// watchable map delivers to the worker immediately instead of waiting for the poll timer.
	// Workers never read this field; they perform their own authoritative APIReader.List.
	// Nil when no execution cycle is active.
	ExecutionProgress *ExecutionProgress
}

// ExecutionProgress is a lightweight summary of terminal job outcomes for the current
// execution cycle. It exists solely to break watchable equality when jobs complete or
// fail, giving the worker an event-driven wake-up signal for the finalize step.
type ExecutionProgress struct {
	CycleID   string
	Completed int
	Failed    int
}

// DeepCopy creates a deep copy of PlanContext.
func (pc *PlanContext) DeepCopy() *PlanContext {
	if pc == nil {
		return nil
	}
	result := &PlanContext{
		HasRestoreData: pc.HasRestoreData,
	}
	if pc.Plan != nil {
		result.Plan = pc.Plan.DeepCopy()
	}
	if pc.Exceptions != nil {
		result.Exceptions = make([]hibernatorv1alpha1.ScheduleException, len(pc.Exceptions))
		for i, exc := range pc.Exceptions {
			result.Exceptions[i] = *exc.DeepCopy()
		}
	}
	if pc.ScheduleResult != nil {
		result.ScheduleResult = &ScheduleEvaluation{
			ShouldHibernate: pc.ScheduleResult.ShouldHibernate,
			RequeueAfter:    pc.ScheduleResult.RequeueAfter,
		}
	}
	if pc.ExecutionProgress != nil {
		ep := *pc.ExecutionProgress
		result.ExecutionProgress = &ep
	}
	return result
}

// Equal checks if two PlanContext objects are equal.
// Uses reflect.DeepEqual for comparison as a pragmatic choice.
func (pc *PlanContext) Equal(other *PlanContext) bool {
	if pc == other {
		return true
	}
	if pc == nil || other == nil {
		return false
	}
	// Compare fields using basic equality and deep comparison where needed
	if pc.HasRestoreData != other.HasRestoreData {
		return false
	}
	if (pc.Plan == nil) != (other.Plan == nil) {
		return false
	}
	if pc.Plan != nil && !planEqual(pc.Plan, other.Plan) {
		return false
	}
	if len(pc.Exceptions) != len(other.Exceptions) {
		return false
	}
	for i := range pc.Exceptions {
		if !exceptionsEqual(&pc.Exceptions[i], &other.Exceptions[i]) {
			return false
		}
	}
	if (pc.ScheduleResult == nil) != (other.ScheduleResult == nil) {
		return false
	}
	if pc.ScheduleResult != nil {
		if pc.ScheduleResult.ShouldHibernate != other.ScheduleResult.ShouldHibernate {
			return false
		}
		// RequeueAfter is intentionally excluded — it is a time-varying scheduling hint
		// computed from time.Until(), which changes on every reconcile cycle even when
		// nothing meaningful has changed. Including it would cause spurious watchable
		// re-delivery on every provider reconcile.
	}
	if !executionProgressEqual(pc.ExecutionProgress, other.ExecutionProgress) {
		return false
	}
	return true
}

// executionProgressEqual compares two ExecutionProgress pointers for value equality.
func executionProgressEqual(a, b *ExecutionProgress) bool {
	if a == b {
		return true
	}
	if (a == nil) != (b == nil) {
		return false
	}
	return a.CycleID == b.CycleID &&
		a.Completed == b.Completed &&
		a.Failed == b.Failed
}

// Helper functions for equality comparisons
func planEqual(a, b *hibernatorv1alpha1.HibernatePlan) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	// Include ResourceVersion so that any K8s update (spec, annotations, or true status writes)
	// is detected by watchable and causes re-delivery to subscribers.
	//
	// Also include Status.Phase so that in-memory optimistic phase updates (which do NOT bump
	// ResourceVersion) are still detected. Without this, optimistic phase changes would be
	// suppressed by watchable, breaking cross-processor phase routing.
	return a.Name == b.Name && a.Namespace == b.Namespace && a.UID == b.UID &&
		a.ResourceVersion == b.ResourceVersion &&
		a.Status.Phase == b.Status.Phase
}

func exceptionsEqual(a, b *hibernatorv1alpha1.ScheduleException) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Name == b.Name && a.Namespace == b.Namespace && a.UID == b.UID
}

// ScheduleEvaluation contains the result of schedule evaluation by the provider.
type ScheduleEvaluation struct {
	// ShouldHibernate indicates if the plan should currently be hibernating.
	ShouldHibernate bool

	// RequeueAfter is the suggested requeue duration based on schedule evaluation.
	RequeueAfter time.Duration
}

// PhaseTransitionHookFn is a function invoked around a plan status write by the StatusWriter.
//
// For a PreHook the plan reflects the current (pre-mutation) K8s state.
// Returning a non-nil error from a PreHook aborts the transition — nothing is written to K8s.
//
// For a PostHook the plan reflects the successfully-persisted state after the write.
// Errors from a PostHook are logged but do not roll back the transition.
type PhaseTransitionHookFn func(ctx context.Context, plan *hibernatorv1alpha1.HibernatePlan) error

// PlanStatusUpdate represents a status mutation queued for the status writer.
// The Mutate function applies changes to the plan's status.
type PlanStatusUpdate struct {
	// NamespacedName identifies the target HibernatePlan.
	NamespacedName types.NamespacedName

	// Mutate is a function that applies status changes.
	// It receives the current status and modifies it in place.
	Mutate func(*hibernatorv1alpha1.HibernatePlanStatus)

	// PreHook is called once with the current (pre-mutation) plan before Mutate is applied.
	// If it returns a non-nil error the transition is aborted with no write to K8s.
	// Optional.
	PreHook PhaseTransitionHookFn

	// PostHook is called once with the successfully-written plan after the status lands in K8s.
	// Errors are logged but do not roll back the transition.
	// Optional.
	PostHook PhaseTransitionHookFn
}

// ExceptionStatusUpdate represents a status mutation queued for the status writer.
type ExceptionStatusUpdate struct {
	// NamespacedName identifies the target ScheduleException.
	NamespacedName types.NamespacedName

	// Mutate is a function that applies status changes.
	Mutate func(*hibernatorv1alpha1.ScheduleExceptionStatus)
}
