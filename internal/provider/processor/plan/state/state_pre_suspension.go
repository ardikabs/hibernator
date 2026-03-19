/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"fmt"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	statusprocessor "github.com/ardikabs/hibernator/internal/provider/processor/status"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// preSuspensionState records the current phase in an annotation and queues
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
type preSuspensionState struct {
	*state
}

func (s *preSuspensionState) Handle(ctx context.Context) (StateResult, error) {
	if s.isInExecutingPhase() {
		drained, result, err := s.awaitExecutionDrain(ctx)
		if err != nil {
			return result, err
		}

		if !drained {
			return result, nil
		}
	}

	return s.performSuspension(ctx)
}

func (s *preSuspensionState) OnDeadline(ctx context.Context) (StateResult, error) {
	// Bypass drain on deadline and suspend immediately
	return s.performSuspension(ctx)
}

// isInExecutingPhase checks if the plan is currently executing (mid-shutdown/wakeup)
func (s *preSuspensionState) isInExecutingPhase() bool {
	phase := s.plan().Status.Phase
	return phase == hibernatorv1alpha1.PhaseHibernating || phase == hibernatorv1alpha1.PhaseWakingUp
}

func (s *preSuspensionState) performSuspension(ctx context.Context) (StateResult, error) {
	plan := s.plan()
	orig := plan.DeepCopy()
	if plan.Annotations == nil {
		plan.Annotations = make(map[string]string)
	}

	plan.Annotations[wellknown.AnnotationSuspendedAtPhase] = string(plan.Status.Phase)
	if err := s.patchAndPreserveStatus(ctx, plan, client.MergeFrom(orig)); err != nil {
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
