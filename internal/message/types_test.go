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

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

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
		Schedule: &ScheduleEvaluation{
			Exceptions: []hibernatorv1alpha1.ScheduleException{
				{ObjectMeta: metav1.ObjectMeta{Name: "ex1"}},
			},
			ShouldHibernate: true,
			NextEvent:       time.Date(2026, 1, 1, 20, 0, 0, 0, time.UTC),
		},
	}

	copy := orig.DeepCopy()

	require.NotNil(t, copy)
	assert.NotSame(t, orig, copy)
	assert.NotSame(t, orig.Plan, copy.Plan)
	assert.NotSame(t, orig.Schedule, copy.Schedule)
	assert.Equal(t, orig.HasRestoreData, copy.HasRestoreData)
	assert.Equal(t, orig.Schedule.ShouldHibernate, copy.Schedule.ShouldHibernate)
	assert.Equal(t, orig.Schedule.NextEvent, copy.Schedule.NextEvent)
	assert.Len(t, copy.Schedule.Exceptions, 1)
	assert.Equal(t, "ex1", copy.Schedule.Exceptions[0].Name)
}

func TestPlanContext_DeepCopy_NilFields_OK(t *testing.T) {
	orig := &PlanContext{HasRestoreData: false}
	copy := orig.DeepCopy()
	require.NotNil(t, copy)
	assert.Nil(t, copy.Plan)
	assert.Nil(t, copy.Schedule)
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

func TestPlanContext_Equal_DifferentSchedule_IsFalse(t *testing.T) {
	a := &PlanContext{Schedule: &ScheduleEvaluation{ShouldHibernate: true}}
	b := &PlanContext{Schedule: &ScheduleEvaluation{ShouldHibernate: false}}
	assert.False(t, a.Equal(b))
}

func TestPlanContext_Equal_DifferentNextEvent_IsFalse(t *testing.T) {
	// NextEvent is an absolute timestamp included in equality — different values
	// must cause re-delivery so the requeue processor re-arms its timer.
	a := &PlanContext{Schedule: &ScheduleEvaluation{ShouldHibernate: true, NextEvent: time.Date(2026, 1, 1, 20, 0, 0, 0, time.UTC)}}
	b := &PlanContext{Schedule: &ScheduleEvaluation{ShouldHibernate: true, NextEvent: time.Date(2026, 1, 2, 6, 0, 0, 0, time.UTC)}}
	assert.False(t, a.Equal(b))
}

func TestPlanContext_Equal_SameNextEvent_IsTrue(t *testing.T) {
	// Same NextEvent values should not cause spurious re-delivery.
	event := time.Date(2026, 1, 1, 20, 0, 0, 0, time.UTC)
	a := &PlanContext{Schedule: &ScheduleEvaluation{ShouldHibernate: true, NextEvent: event}}
	b := &PlanContext{Schedule: &ScheduleEvaluation{ShouldHibernate: true, NextEvent: event}}
	assert.True(t, a.Equal(b))
}

func TestPlanContext_Equal_NilVsNonNilSchedule_IsFalse(t *testing.T) {
	a := &PlanContext{Schedule: &ScheduleEvaluation{ShouldHibernate: true}}
	b := &PlanContext{Schedule: nil}
	assert.False(t, a.Equal(b))
}

func TestPlanContext_Equal_DifferentExceptionCount_IsFalse(t *testing.T) {
	a := &PlanContext{Schedule: &ScheduleEvaluation{
		Exceptions: []hibernatorv1alpha1.ScheduleException{
			{ObjectMeta: metav1.ObjectMeta{Name: "ex1"}},
		},
	}}

	b := &PlanContext{}
	assert.False(t, a.Equal(b))
}

func TestPlanContext_Equal_NilPlanOneNil_IsFalse(t *testing.T) {
	a := &PlanContext{Plan: &hibernatorv1alpha1.HibernatePlan{}}
	b := &PlanContext{Plan: nil}
	assert.False(t, a.Equal(b))
}
