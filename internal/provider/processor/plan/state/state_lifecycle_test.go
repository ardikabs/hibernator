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
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// ---------------------------------------------------------------------------
// lifecycleState — init path
// ---------------------------------------------------------------------------

func TestLifecycleState_HandleInit_AddsFinalizerWhenMissing(t *testing.T) {
	plan := basePlanForState("p", "")
	// No finalizer present — handler should add it.
	c := newHandlerFakeClient(plan)
	state := newHandlerState(plan, c, &timerTracker{})

	h := &lifecycleState{State: state}
	h.Handle(context.Background())

	// Verify the finalizer was persisted via the fake client.
	updated := &hibernatorv1alpha1.HibernatePlan{}
	err := c.Get(context.Background(), types.NamespacedName{Name: "p", Namespace: "default"}, updated)
	require.NoError(t, err)
	assert.Contains(t, updated.Finalizers, wellknown.PlanFinalizerName)
}

func TestLifecycleState_HandleInit_SetsActivePhaseWhenFinalizerPresent(t *testing.T) {
	plan := basePlanForState("p", "")
	plan.Finalizers = []string{wellknown.PlanFinalizerName}
	c := newHandlerFakeClient(plan)
	state := newHandlerState(plan, c, &timerTracker{})

	h := &lifecycleState{State: state}
	h.Handle(context.Background())

	// In-memory plan should be Active; queue should have a status update.
	assert.Equal(t, hibernatorv1alpha1.PhaseActive, state.plan().Status.Phase)
	assert.GreaterOrEqual(t, state.Statuses.PlanStatuses.Len(), 1)
}

// ---------------------------------------------------------------------------
// lifecycleState — delete path
// ---------------------------------------------------------------------------

func TestLifecycleState_HandleDelete_RemovesFinalizer(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Finalizers = []string{wellknown.PlanFinalizerName}
	now := metav1.NewTime(time.Now())
	plan.DeletionTimestamp = &now
	c := newHandlerFakeClient(plan)
	state := newHandlerState(plan, c, &timerTracker{})

	h := &lifecycleState{State: state, delete: true}
	h.Handle(context.Background())

	// After removing the last finalizer the fake client may GC the object.
	updated := &hibernatorv1alpha1.HibernatePlan{}
	err := c.Get(context.Background(), types.NamespacedName{Name: "p", Namespace: "default"}, updated)
	if err == nil {
		assert.NotContains(t, updated.Finalizers, wellknown.PlanFinalizerName)
	} else {
		// Object was garbage-collected after finalizer removal — that's expected.
		require.NoError(t, client.IgnoreNotFound(err), "unexpected error after finalizer removal: %v", err)
	}
}

func TestLifecycleState_HandleDelete_DeletesOwnerJobs(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernating)
	plan.Finalizers = []string{wellknown.PlanFinalizerName}
	now := metav1.NewTime(time.Now())
	plan.DeletionTimestamp = &now

	// Create a Job that belongs to this plan.
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runner-job",
			Namespace: "default",
			Labels: map[string]string{
				wellknown.LabelPlan: "p",
			},
		},
	}

	c := newHandlerFakeClient(plan, job)
	state := newHandlerState(plan, c, &timerTracker{})

	h := &lifecycleState{State: state, delete: true}
	h.Handle(context.Background())

	// Job should be deleted.
	remainingJob := &batchv1.Job{}
	err := c.Get(context.Background(), types.NamespacedName{Name: "runner-job", Namespace: "default"}, remainingJob)
	assert.Error(t, err, "job should have been deleted during finalizer cleanup")
}
