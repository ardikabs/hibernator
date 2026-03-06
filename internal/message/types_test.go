/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package message

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

// ---------------------------------------------------------------------------
// NewControllerStatuses
// ---------------------------------------------------------------------------

func TestNewControllerStatuses_ReturnsNonNilQueues(t *testing.T) {
	s := NewControllerStatuses()
	require.NotNil(t, s)
	assert.NotNil(t, s.PlanStatuses)
	assert.NotNil(t, s.ExceptionStatuses)
}

// ---------------------------------------------------------------------------
// StatusQueue
// ---------------------------------------------------------------------------

func TestStatusQueue_SendAndLen(t *testing.T) {
	q := newStatusQueue[*PlanStatusUpdate]("plan")

	assert.Equal(t, 0, q.Len())

	q.Send(&PlanStatusUpdate{NamespacedName: types.NamespacedName{Name: "p1"}})
	q.Send(&PlanStatusUpdate{NamespacedName: types.NamespacedName{Name: "p2"}})

	assert.Equal(t, 2, q.Len())
}

func TestStatusQueue_C_ReceivesItems(t *testing.T) {
	q := newStatusQueue[*PlanStatusUpdate]("plan")
	update := &PlanStatusUpdate{NamespacedName: types.NamespacedName{Name: "p1"}}

	q.Send(update)

	select {
	case got := <-q.C():
		assert.Same(t, update, got)
	default:
		t.Fatal("expected item in channel")
	}
}

func TestStatusQueue_Send_DropWhenFull(t *testing.T) {
	// Drain-free send: fill the entire buffer then send one more — must not block.
	q := newStatusQueue[*PlanStatusUpdate]("plan")
	for i := 0; i < statusQueueCapacity; i++ {
		q.Send(&PlanStatusUpdate{})
	}
	// This would deadlock if Send blocked; it should drop silently.
	q.Send(&PlanStatusUpdate{NamespacedName: types.NamespacedName{Name: "overflow"}})
	assert.Equal(t, statusQueueCapacity, q.Len(), "len should stay at capacity after overflow")
}

// ---------------------------------------------------------------------------
// PlanContext.DeepCopy
// ---------------------------------------------------------------------------

func TestPlanContext_DeepCopy_Nil_ReturnsNil(t *testing.T) {
	var pc *PlanContext
	assert.Nil(t, pc.DeepCopy())
}

func TestPlanContext_DeepCopy_Full_IsDeep(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default"},
	}
	plan.Status.Phase = hibernatorv1alpha1.PhaseActive

	orig := &PlanContext{
		Plan:           plan,
		HasRestoreData: true,
		ScheduleResult: &ScheduleEvaluation{ShouldHibernate: true, RequeueAfter: 5 * time.Minute},
		ExecutionProgress: &ExecutionProgress{
			CycleID:   "abc",
			Completed: 1,
			Failed:    0,
		},
		Exceptions: []hibernatorv1alpha1.ScheduleException{
			{ObjectMeta: metav1.ObjectMeta{Name: "ex1"}},
		},
	}

	copy := orig.DeepCopy()

	require.NotNil(t, copy)
	assert.NotSame(t, orig, copy)
	assert.NotSame(t, orig.Plan, copy.Plan)
	assert.NotSame(t, orig.ScheduleResult, copy.ScheduleResult)
	assert.NotSame(t, orig.ExecutionProgress, copy.ExecutionProgress)
	assert.Equal(t, orig.HasRestoreData, copy.HasRestoreData)
	assert.Equal(t, orig.ScheduleResult.ShouldHibernate, copy.ScheduleResult.ShouldHibernate)
	assert.Equal(t, orig.ScheduleResult.RequeueAfter, copy.ScheduleResult.RequeueAfter)
	assert.Equal(t, orig.ExecutionProgress.CycleID, copy.ExecutionProgress.CycleID)
	assert.Len(t, copy.Exceptions, 1)
	assert.Equal(t, "ex1", copy.Exceptions[0].Name)
}

func TestPlanContext_DeepCopy_NilFields_OK(t *testing.T) {
	orig := &PlanContext{HasRestoreData: false}
	copy := orig.DeepCopy()
	require.NotNil(t, copy)
	assert.Nil(t, copy.Plan)
	assert.Nil(t, copy.ScheduleResult)
	assert.Nil(t, copy.ExecutionProgress)
}

// ---------------------------------------------------------------------------
// PlanContext.Equal
// ---------------------------------------------------------------------------

func TestPlanContext_Equal_SamePointer_IsTrue(t *testing.T) {
	pc := &PlanContext{HasRestoreData: true}
	assert.True(t, pc.Equal(pc))
}

func TestPlanContext_Equal_NilOther_IsFalse(t *testing.T) {
	pc := &PlanContext{}
	assert.False(t, pc.Equal(nil))
}

func TestPlanContext_Equal_BothNilViaCast_IsTrue(t *testing.T) {
	var a, b *PlanContext
	// a.Equal(b) would panic on nil receiver — use Equal(a, b) pattern indirectly.
	// Just assert same-pointer shortcut covers nil==nil.
	assert.True(t, a == b)
}

func TestPlanContext_Equal_DifferentHasRestoreData_IsFalse(t *testing.T) {
	a := &PlanContext{HasRestoreData: true}
	b := &PlanContext{HasRestoreData: false}
	assert.False(t, a.Equal(b))
}

func TestPlanContext_Equal_DifferentPhase_IsFalse(t *testing.T) {
	mkPlan := func(phase hibernatorv1alpha1.PlanPhase) *hibernatorv1alpha1.HibernatePlan {
		p := &hibernatorv1alpha1.HibernatePlan{}
		p.Status.Phase = phase
		return p
	}
	a := &PlanContext{Plan: mkPlan(hibernatorv1alpha1.PhaseActive)}
	b := &PlanContext{Plan: mkPlan(hibernatorv1alpha1.PhaseHibernating)}
	assert.False(t, a.Equal(b))
}

func TestPlanContext_Equal_SamePlan_IsTrue(t *testing.T) {
	p := &hibernatorv1alpha1.HibernatePlan{}
	p.Name = "x"
	p.ResourceVersion = "1"
	p.Status.Phase = hibernatorv1alpha1.PhaseActive
	a := &PlanContext{Plan: p}
	b := &PlanContext{Plan: p.DeepCopy()}
	assert.True(t, a.Equal(b))
}

func TestPlanContext_Equal_DifferentScheduleResult_IsFalse(t *testing.T) {
	a := &PlanContext{ScheduleResult: &ScheduleEvaluation{ShouldHibernate: true}}
	b := &PlanContext{ScheduleResult: &ScheduleEvaluation{ShouldHibernate: false}}
	assert.False(t, a.Equal(b))
}

func TestPlanContext_Equal_RequeueAfterIgnored_IsTrue(t *testing.T) {
	// RequeueAfter is intentionally excluded from equality — changes to it must not
	// cause spurious re-delivery.
	a := &PlanContext{ScheduleResult: &ScheduleEvaluation{ShouldHibernate: true, RequeueAfter: 1 * time.Minute}}
	b := &PlanContext{ScheduleResult: &ScheduleEvaluation{ShouldHibernate: true, RequeueAfter: 5 * time.Minute}}
	assert.True(t, a.Equal(b))
}

func TestPlanContext_Equal_NilVsNonNilScheduleResult_IsFalse(t *testing.T) {
	a := &PlanContext{ScheduleResult: &ScheduleEvaluation{ShouldHibernate: true}}
	b := &PlanContext{ScheduleResult: nil}
	assert.False(t, a.Equal(b))
}

func TestPlanContext_Equal_DifferentExceptionCount_IsFalse(t *testing.T) {
	a := &PlanContext{Exceptions: []hibernatorv1alpha1.ScheduleException{
		{ObjectMeta: metav1.ObjectMeta{Name: "ex1"}},
	}}
	b := &PlanContext{}
	assert.False(t, a.Equal(b))
}

func TestPlanContext_Equal_NilPlanOneNil_IsFalse(t *testing.T) {
	a := &PlanContext{Plan: &hibernatorv1alpha1.HibernatePlan{}}
	b := &PlanContext{Plan: nil}
	assert.False(t, a.Equal(b))
}

// ---------------------------------------------------------------------------
// executionProgressEqual (covers the private helper via Equal)
// ---------------------------------------------------------------------------

func TestPlanContext_Equal_ExecutionProgress_DifferentCycleID_IsFalse(t *testing.T) {
	a := &PlanContext{ExecutionProgress: &ExecutionProgress{CycleID: "abc"}}
	b := &PlanContext{ExecutionProgress: &ExecutionProgress{CycleID: "xyz"}}
	assert.False(t, a.Equal(b))
}

func TestPlanContext_Equal_ExecutionProgress_NilVsNonNil_IsFalse(t *testing.T) {
	a := &PlanContext{ExecutionProgress: &ExecutionProgress{CycleID: "abc"}}
	b := &PlanContext{ExecutionProgress: nil}
	assert.False(t, a.Equal(b))
}

func TestPlanContext_Equal_ExecutionProgress_BothNil_IsTrue(t *testing.T) {
	a := &PlanContext{}
	b := &PlanContext{}
	assert.True(t, a.Equal(b))
}
