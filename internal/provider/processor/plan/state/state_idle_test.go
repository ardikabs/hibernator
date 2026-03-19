/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
)

// newIdleState wires an idle-state State with the supplied ScheduleResult.
func newIdleState(
	plan *hibernatorv1alpha1.HibernatePlan,
	sr *message.ScheduleEvaluation,
	hasRestoreData bool,
) *state {
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)
	st.PlanCtx.Schedule = sr
	st.PlanCtx.HasRestoreData = hasRestoreData
	return st
}

// ---------------------------------------------------------------------------
// idleState
// ---------------------------------------------------------------------------

func TestIdleState_Handle_NoScheduleResult_NoTransition(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	st := newIdleState(plan, nil, false)
	h := &idleState{state: st}

	h.Handle(context.Background())

	// No schedule result → no phase transition.
	assert.Equal(t, hibernatorv1alpha1.PhaseActive, plan.Status.Phase)
	assert.Zero(t, planStatuses(st).Len())
}

func TestIdleState_Handle_ActiveShouldHibernate_TransitionsToHibernating(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Spec.Execution.Strategy.Type = hibernatorv1alpha1.StrategySequential
	sr := &message.ScheduleEvaluation{ShouldHibernate: true, RequeueAfter: 5 * time.Minute}
	st := newIdleState(plan, sr, false)
	h := &idleState{state: st}

	h.Handle(context.Background())

	// With no targets, the cascaded execution immediately reaches Hibernated.
	assert.True(t,
		plan.Status.Phase == hibernatorv1alpha1.PhaseHibernating ||
			plan.Status.Phase == hibernatorv1alpha1.PhaseHibernated,
		"phase should be Hibernating or Hibernated after transition; got %s", plan.Status.Phase)

	// At least one status update must have been queued (Hibernating transition, possibly also Hibernated).
	assert.GreaterOrEqual(t, planStatuses(st).Len(), 1)
}

func TestIdleState_Handle_ActiveShouldNotHibernate_NoTransition(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	sr := &message.ScheduleEvaluation{ShouldHibernate: false, RequeueAfter: 5 * time.Minute}
	st := newIdleState(plan, sr, false)
	h := &idleState{state: st}

	h.Handle(context.Background())

	assert.Equal(t, hibernatorv1alpha1.PhaseActive, plan.Status.Phase)
	assert.Zero(t, planStatuses(st).Len())
}

func TestIdleState_Handle_HibernatedNoRestoreData_NoWakeUp(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
	sr := &message.ScheduleEvaluation{ShouldHibernate: false, RequeueAfter: 5 * time.Minute}
	st := newIdleState(plan, sr, false /* no restore data */)
	h := &idleState{state: st}

	h.Handle(context.Background())

	// No restore data → no wakeup even though schedule says so.
	assert.Equal(t, hibernatorv1alpha1.PhaseHibernated, plan.Status.Phase)
	assert.Zero(t, planStatuses(st).Len())
}

func TestIdleState_Handle_HibernatedShouldStayHibernated(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
	sr := &message.ScheduleEvaluation{ShouldHibernate: true, RequeueAfter: 5 * time.Minute}
	st := newIdleState(plan, sr, true)
	h := &idleState{state: st}

	h.Handle(context.Background())

	assert.Equal(t, hibernatorv1alpha1.PhaseHibernated, plan.Status.Phase)
	assert.Zero(t, planStatuses(st).Len())
}
