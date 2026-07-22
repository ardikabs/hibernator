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
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// ---------------------------------------------------------------------------
// revertState
// ---------------------------------------------------------------------------

func TestRevertState_Handle_LiveRestoreData_TransitionsToWakingUp(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseError)
	plan.Annotations = map[string]string{
		wellknown.AnnotationRevert: "true",
	}
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds"},
		{Name: "cache", Type: "ec2"},
	}
	plan.Status.CurrentCycleID = "cycle-001"
	plan.Spec.Execution.Strategy.Type = hibernatorv1alpha1.StrategySequential

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	// Save live restore data for "db" only.
	err := st.RestoreManager.Save(context.Background(), plan.Namespace, plan.Name, "db", &restore.Data{
		Target:   "db",
		Executor: "rds",
		IsLive:   true,
		State: map[string]any{
			"instance:db-1": map[string]any{"wasRunning": true},
		},
	})
	require.NoError(t, err)

	h := &revertState{state: st}
	result, err := h.Handle(context.Background())
	require.NoError(t, err)
	assert.True(t, result.Requeue, "revertState should request requeue")

	// Verify the queued status update transitions to WakingUp.
	upd := <-planStatuses(st).C()
	require.NotNil(t, upd.Mutator)
	testPlan := plan.DeepCopy()
	upd.Mutator.Mutate(testPlan)
	assert.Equal(t, hibernatorv1alpha1.PhaseWakingUp, testPlan.Status.Phase)
	assert.Equal(t, hibernatorv1alpha1.OperationWakeUp, testPlan.Status.CurrentOperation)
	assert.Len(t, testPlan.Status.Executions, 2)
	assert.Equal(t, "db", testPlan.Status.Executions[0].Target)
	assert.Equal(t, "cache", testPlan.Status.Executions[1].Target)
	assert.Equal(t, hibernatorv1alpha1.StatePending, testPlan.Status.Executions[0].State)
	assert.Equal(t, hibernatorv1alpha1.StatePending, testPlan.Status.Executions[1].State)

	// Revert annotation must be preserved so idleState can consume it after wakeup.
	var updatedPlan hibernatorv1alpha1.HibernatePlan
	err = c.Get(context.Background(), client.ObjectKeyFromObject(plan), &updatedPlan)
	require.NoError(t, err)
	assert.Equal(t, "true", updatedPlan.Annotations[wellknown.AnnotationRevert])
}

func TestRevertState_Handle_NoLiveRestoreData_StaysErrorAndRemovesAnnotation(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseError)
	plan.Annotations = map[string]string{
		wellknown.AnnotationRevert: "true",
	}
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds"},
	}
	plan.Status.CurrentCycleID = "cycle-001"

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := &revertState{state: st}
	result, err := h.Handle(context.Background())
	require.NoError(t, err)
	assert.True(t, result.Requeue, "no-op revert should still requeue")

	// Verify the queued status update keeps the plan in Error with a clear message.
	upd := <-planStatuses(st).C()
	require.NotNil(t, upd.Mutator)
	testPlan := plan.DeepCopy()
	upd.Mutator.Mutate(testPlan)
	assert.Equal(t, hibernatorv1alpha1.PhaseError, testPlan.Status.Phase)
	assert.Contains(t, testPlan.Status.ErrorMessage, "no targets have live restore data")

	// Verify the revert annotation is removed so we don't loop.
	var updatedPlan hibernatorv1alpha1.HibernatePlan
	err = c.Get(context.Background(), client.ObjectKeyFromObject(plan), &updatedPlan)
	require.NoError(t, err)
	assert.Empty(t, updatedPlan.Annotations[wellknown.AnnotationRevert])
}

func TestRevertState_Handle_UsesPlanSnapshotTargets(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseError)
	plan.Annotations = map[string]string{
		wellknown.AnnotationRevert: "true",
	}
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds"},
		{Name: "cache", Type: "ec2"},
	}
	plan.Status.CurrentCycleID = "cycle-001"
	plan.Status.PlanSnapshot = &hibernatorv1alpha1.PlanSnapshot{
		CycleID: "cycle-001",
		Targets: []hibernatorv1alpha1.Target{
			{Name: "snapshot-db", Type: "rds"},
		},
	}

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	// Save live restore data for the snapshot target, not the live target.
	err := st.RestoreManager.Save(context.Background(), plan.Namespace, plan.Name, "snapshot-db", &restore.Data{
		Target:   "snapshot-db",
		Executor: "rds",
		IsLive:   true,
	})
	require.NoError(t, err)

	h := &revertState{state: st}
	_, err = h.Handle(context.Background())
	require.NoError(t, err)

	upd := <-planStatuses(st).C()
	require.NotNil(t, upd.Mutator)
	testPlan := plan.DeepCopy()
	upd.Mutator.Mutate(testPlan)

	assert.Equal(t, hibernatorv1alpha1.PhaseWakingUp, testPlan.Status.Phase)
	assert.Len(t, testPlan.Status.Executions, 1)
	assert.Equal(t, "snapshot-db", testPlan.Status.Executions[0].Target)
}

func TestRevertState_Handle_MissingAnnotation_IsNoop(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseError)
	plan.Annotations = map[string]string{}
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds"},
	}

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := &revertState{state: st}
	result, err := h.Handle(context.Background())
	require.NoError(t, err)
	assert.False(t, result.Requeue)
	assert.Zero(t, planStatuses(st).Len())
	assert.Equal(t, hibernatorv1alpha1.PhaseError, plan.Status.Phase)
}
