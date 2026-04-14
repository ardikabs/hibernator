/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	sr := &message.ScheduleEvaluation{ShouldHibernate: true}
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
	sr := &message.ScheduleEvaluation{ShouldHibernate: false}
	st := newIdleState(plan, sr, false)
	h := &idleState{state: st}

	h.Handle(context.Background())

	assert.Equal(t, hibernatorv1alpha1.PhaseActive, plan.Status.Phase)
	assert.Zero(t, planStatuses(st).Len())
}

func TestIdleState_Handle_HibernatedNoRestoreData_NoWakeUp(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
	sr := &message.ScheduleEvaluation{ShouldHibernate: false}
	st := newIdleState(plan, sr, false /* no restore data */)
	h := &idleState{state: st}

	h.Handle(context.Background())

	// No restore data → no wakeup even though schedule says so.
	assert.Equal(t, hibernatorv1alpha1.PhaseHibernated, plan.Status.Phase)
	assert.Zero(t, planStatuses(st).Len())
}

func TestIdleState_Handle_HibernatedShouldStayHibernated(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
	sr := &message.ScheduleEvaluation{ShouldHibernate: true}
	st := newIdleState(plan, sr, true)
	h := &idleState{state: st}

	h.Handle(context.Background())

	assert.Equal(t, hibernatorv1alpha1.PhaseHibernated, plan.Status.Phase)
	assert.Zero(t, planStatuses(st).Len())
}

func TestIdleState_TransitionToHibernating_StartNotificationUsesMutatedPendingTargets(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds"},
		{Name: "app", Type: "eks"},
	}
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "db", Executor: "rds", State: hibernatorv1alpha1.StateCompleted},
		{Target: "app", Executor: "eks", State: hibernatorv1alpha1.StateCompleted},
	}

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)
	spy := &spyNotifier{}
	st.Notifier = spy
	st.PlanCtx.Notifications = []hibernatorv1alpha1.HibernateNotification{{
		ObjectMeta: metav1.ObjectMeta{Name: "n1", Namespace: "default"},
		Spec: hibernatorv1alpha1.HibernateNotificationSpec{
			OnEvents: []hibernatorv1alpha1.NotificationEvent{hibernatorv1alpha1.EventStart},
			Sinks: []hibernatorv1alpha1.NotificationSink{{
				Name:      "slack",
				Type:      hibernatorv1alpha1.SinkSlack,
				SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s1"},
			}},
		},
	}}

	h := &idleState{state: st}
	_, err := h.transitionToHibernating(st.Log)
	require.NoError(t, err)

	upd := <-planStatuses(st).C()
	require.NotNil(t, upd.PostHook)
	require.NoError(t, upd.PostHook(context.Background(), plan))

	require.Len(t, spy.requests, 1)
	req := spy.requests[0]
	assert.Equal(t, string(hibernatorv1alpha1.EventStart), req.Payload.Event)
	assert.Equal(t, string(hibernatorv1alpha1.PhaseHibernating), req.Payload.Phase)
	assert.Equal(t, string(hibernatorv1alpha1.OperationHibernate), req.Payload.Operation)
	require.Len(t, req.Payload.Targets, 2)
	assert.Equal(t, "Pending", req.Payload.Targets[0].State)
	assert.Equal(t, "Pending", req.Payload.Targets[1].State)
}

func TestIdleState_TransitionToWakingUp_StartNotificationUsesMutatedPendingTargets(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds"},
		{Name: "app", Type: "eks"},
	}
	plan.Status.CurrentCycleID = "cycle-001"
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "db", Executor: "rds", State: hibernatorv1alpha1.StateCompleted},
		{Target: "app", Executor: "eks", State: hibernatorv1alpha1.StateCompleted},
	}

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)
	spy := &spyNotifier{}
	st.Notifier = spy
	st.PlanCtx.Notifications = []hibernatorv1alpha1.HibernateNotification{{
		ObjectMeta: metav1.ObjectMeta{Name: "n1", Namespace: "default"},
		Spec: hibernatorv1alpha1.HibernateNotificationSpec{
			OnEvents: []hibernatorv1alpha1.NotificationEvent{hibernatorv1alpha1.EventStart},
			Sinks: []hibernatorv1alpha1.NotificationSink{{
				Name:      "slack",
				Type:      hibernatorv1alpha1.SinkSlack,
				SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s1"},
			}},
		},
	}}

	h := &idleState{state: st}
	_, err := h.transitionToWakingUp(st.Log)
	require.NoError(t, err)

	upd := <-planStatuses(st).C()
	require.NotNil(t, upd.PostHook)
	require.NoError(t, upd.PostHook(context.Background(), plan))

	require.Len(t, spy.requests, 1)
	req := spy.requests[0]
	assert.Equal(t, string(hibernatorv1alpha1.EventStart), req.Payload.Event)
	assert.Equal(t, string(hibernatorv1alpha1.PhaseWakingUp), req.Payload.Phase)
	assert.Equal(t, string(hibernatorv1alpha1.OperationWakeUp), req.Payload.Operation)
	require.Len(t, req.Payload.Targets, 2)
	assert.Equal(t, "Pending", req.Payload.Targets[0].State)
	assert.Equal(t, "Pending", req.Payload.Targets[1].State)
}
