/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package status

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/pkg/keyedworker"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = hibernatorv1alpha1.AddToScheme(s)
	return s
}

func newFakeWriter(objs ...client.Object) *Writer {
	c := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithStatusSubresource(
			&hibernatorv1alpha1.HibernatePlan{},
			&hibernatorv1alpha1.ScheduleException{},
		).
		WithObjects(objs...).
		Build()

	return &Writer{
		Client:    c,
		APIReader: c,
		Log:       logr.Discard(),
		Statuses:  message.NewControllerStatuses(),
	}
}

func basePlan(name string) *hibernatorv1alpha1.HibernatePlan {
	return &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
	}
}

func baseException(name string) *hibernatorv1alpha1.ScheduleException {
	return &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
	}
}

// ---------------------------------------------------------------------------
// isPlanStatusEqual
// ---------------------------------------------------------------------------

func TestIsPlanStatusEqual_IdenticalStatus_True(t *testing.T) {
	a := hibernatorv1alpha1.HibernatePlanStatus{Phase: hibernatorv1alpha1.PhaseHibernating}
	b := hibernatorv1alpha1.HibernatePlanStatus{Phase: hibernatorv1alpha1.PhaseHibernating}
	assert.True(t, isPlanStatusEqual(a, b))
}

func TestIsPlanStatusEqual_DifferentPhase_False(t *testing.T) {
	a := hibernatorv1alpha1.HibernatePlanStatus{Phase: hibernatorv1alpha1.PhaseHibernating}
	b := hibernatorv1alpha1.HibernatePlanStatus{Phase: hibernatorv1alpha1.PhaseActive}
	assert.False(t, isPlanStatusEqual(a, b))
}

func TestIsPlanStatusEqual_DifferentLastTransitionTime_Ignored(t *testing.T) {
	t1 := metav1.Now()
	t2 := metav1.NewTime(t1.Time.Add(10))
	a := hibernatorv1alpha1.HibernatePlanStatus{
		Phase:              hibernatorv1alpha1.PhaseHibernating,
		LastTransitionTime: &t1,
	}
	b := hibernatorv1alpha1.HibernatePlanStatus{
		Phase:              hibernatorv1alpha1.PhaseHibernating,
		LastTransitionTime: &t2,
	}
	assert.True(t, isPlanStatusEqual(a, b), "LastTransitionTime differences should be ignored")
}

func TestIsPlanStatusEqual_DifferentExecutionState_False(t *testing.T) {
	a := hibernatorv1alpha1.HibernatePlanStatus{
		Executions: []hibernatorv1alpha1.ExecutionStatus{{Target: "t1", State: hibernatorv1alpha1.StateRunning}},
	}
	b := hibernatorv1alpha1.HibernatePlanStatus{
		Executions: []hibernatorv1alpha1.ExecutionStatus{{Target: "t1", State: hibernatorv1alpha1.StateCompleted}},
	}
	assert.False(t, isPlanStatusEqual(a, b))
}

func TestIsPlanStatusEqual_ExecutionTimeDiffers_Ignored(t *testing.T) {
	t1 := metav1.Now()
	t2 := metav1.NewTime(t1.Time.Add(5))
	a := hibernatorv1alpha1.HibernatePlanStatus{
		Executions: []hibernatorv1alpha1.ExecutionStatus{{
			Target:    "t1",
			State:     hibernatorv1alpha1.StateCompleted,
			StartedAt: &t1,
		}},
	}
	b := hibernatorv1alpha1.HibernatePlanStatus{
		Executions: []hibernatorv1alpha1.ExecutionStatus{{
			Target:    "t1",
			State:     hibernatorv1alpha1.StateCompleted,
			StartedAt: &t2,
		}},
	}
	assert.True(t, isPlanStatusEqual(a, b), "StartedAt differences should be ignored")
}

// ---------------------------------------------------------------------------
// isExceptionStatusEqual
// ---------------------------------------------------------------------------

func TestIsExceptionStatusEqual_SameState_True(t *testing.T) {
	a := hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive, Message: "ok"}
	b := hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive, Message: "ok"}
	assert.True(t, isExceptionStatusEqual(a, b))
}

func TestIsExceptionStatusEqual_DifferentState_False(t *testing.T) {
	a := hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive}
	b := hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateExpired}
	assert.False(t, isExceptionStatusEqual(a, b))
}

func TestIsExceptionStatusEqual_DifferentMessage_False(t *testing.T) {
	a := hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive, Message: "foo"}
	b := hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive, Message: "bar"}
	assert.False(t, isExceptionStatusEqual(a, b))
}

func TestIsExceptionStatusEqual_AppliedAtDiffers_Ignored(t *testing.T) {
	t1 := metav1.Now()
	t2 := metav1.NewTime(t1.Time.Add(10))
	a := hibernatorv1alpha1.ScheduleExceptionStatus{
		State:     hibernatorv1alpha1.ExceptionStateActive,
		AppliedAt: &t1,
	}
	b := hibernatorv1alpha1.ScheduleExceptionStatus{
		State:     hibernatorv1alpha1.ExceptionStateActive,
		AppliedAt: &t2,
	}
	assert.True(t, isExceptionStatusEqual(a, b), "AppliedAt differences should be ignored")
}

// ---------------------------------------------------------------------------
// handlePlanStatusUpdate
// ---------------------------------------------------------------------------

func TestHandlePlanStatusUpdate_AppliesMutation(t *testing.T) {
	plan := basePlan("test-plan")
	w := newFakeWriter(plan)

	update := &message.PlanStatusUpdate{
		NamespacedName: types.NamespacedName{Name: "test-plan", Namespace: "default"},
		Mutate: func(st *hibernatorv1alpha1.HibernatePlanStatus) {
			st.Phase = hibernatorv1alpha1.PhaseHibernating
		},
	}

	err := w.handlePlanStatusUpdate(context.Background(), logr.Discard(), update)
	require.NoError(t, err)

	result := &hibernatorv1alpha1.HibernatePlan{}
	require.NoError(t, w.Get(context.Background(),
		types.NamespacedName{Name: "test-plan", Namespace: "default"}, result))
	assert.Equal(t, hibernatorv1alpha1.PhaseHibernating, result.Status.Phase)
}

func TestHandlePlanStatusUpdate_NoChange_Skips(t *testing.T) {
	plan := basePlan("test-plan")
	plan.Status.Phase = hibernatorv1alpha1.PhaseHibernating
	w := newFakeWriter(plan)

	callCount := 0
	update := &message.PlanStatusUpdate{
		NamespacedName: types.NamespacedName{Name: "test-plan", Namespace: "default"},
		Mutate: func(st *hibernatorv1alpha1.HibernatePlanStatus) {
			callCount++
			st.Phase = hibernatorv1alpha1.PhaseHibernating // identical — no-op
		},
	}

	require.NoError(t, w.handlePlanStatusUpdate(context.Background(), logr.Discard(), update))
	assert.Equal(t, 1, callCount)
}

func TestHandlePlanStatusUpdate_PreHookRejection_AbortsWrite(t *testing.T) {
	plan := basePlan("test-plan")
	w := newFakeWriter(plan)

	update := &message.PlanStatusUpdate{
		NamespacedName: types.NamespacedName{Name: "test-plan", Namespace: "default"},
		PreHook: func(_ context.Context, _ *hibernatorv1alpha1.HibernatePlan) error {
			return assert.AnError
		},
		Mutate: func(st *hibernatorv1alpha1.HibernatePlanStatus) {
			st.Phase = hibernatorv1alpha1.PhaseHibernating
		},
	}

	err := w.handlePlanStatusUpdate(context.Background(), logr.Discard(), update)
	require.Error(t, err, "pre-hook rejection must be propagated")

	// Status must not have changed.
	result := &hibernatorv1alpha1.HibernatePlan{}
	_ = w.Get(context.Background(), types.NamespacedName{Name: "test-plan", Namespace: "default"}, result)
	assert.Empty(t, result.Status.Phase)
}

func TestHandlePlanStatusUpdate_PostHookCalled(t *testing.T) {
	plan := basePlan("test-plan")
	w := newFakeWriter(plan)

	postCalled := false
	update := &message.PlanStatusUpdate{
		NamespacedName: types.NamespacedName{Name: "test-plan", Namespace: "default"},
		Mutate: func(st *hibernatorv1alpha1.HibernatePlanStatus) {
			st.Phase = hibernatorv1alpha1.PhaseHibernating
		},
		PostHook: func(_ context.Context, p *hibernatorv1alpha1.HibernatePlan) error {
			postCalled = true
			assert.Equal(t, hibernatorv1alpha1.PhaseHibernating, p.Status.Phase)
			return nil
		},
	}

	require.NoError(t, w.handlePlanStatusUpdate(context.Background(), logr.Discard(), update))
	assert.True(t, postCalled)
}

func TestHandlePlanStatusUpdate_NotFound_ReturnsError(t *testing.T) {
	w := newFakeWriter() // no plan registered

	update := &message.PlanStatusUpdate{
		NamespacedName: types.NamespacedName{Name: "ghost", Namespace: "default"},
		Mutate:         func(st *hibernatorv1alpha1.HibernatePlanStatus) {},
	}

	require.Error(t, w.handlePlanStatusUpdate(context.Background(), logr.Discard(), update))
}

// ---------------------------------------------------------------------------
// handleExceptionStatusUpdate
// ---------------------------------------------------------------------------

func TestHandleExceptionStatusUpdate_AppliesMutation(t *testing.T) {
	ex := baseException("test-exception")
	w := newFakeWriter(ex)

	update := &message.ExceptionStatusUpdate{
		NamespacedName: types.NamespacedName{Name: "test-exception", Namespace: "default"},
		Mutate: func(st *hibernatorv1alpha1.ScheduleExceptionStatus) {
			st.State = hibernatorv1alpha1.ExceptionStateActive
			st.Message = "activated"
		},
	}

	require.NoError(t, w.handleExceptionStatusUpdate(context.Background(), logr.Discard(), update))

	result := &hibernatorv1alpha1.ScheduleException{}
	require.NoError(t, w.Get(context.Background(),
		types.NamespacedName{Name: "test-exception", Namespace: "default"}, result))
	assert.Equal(t, hibernatorv1alpha1.ExceptionStateActive, result.Status.State)
	assert.Equal(t, "activated", result.Status.Message)
}

func TestHandleExceptionStatusUpdate_NoChange_Skips(t *testing.T) {
	ex := baseException("test-exception")
	ex.Status.State = hibernatorv1alpha1.ExceptionStateActive
	ex.Status.Message = "already active"
	w := newFakeWriter(ex)

	callCount := 0
	update := &message.ExceptionStatusUpdate{
		NamespacedName: types.NamespacedName{Name: "test-exception", Namespace: "default"},
		Mutate: func(st *hibernatorv1alpha1.ScheduleExceptionStatus) {
			callCount++
			st.State = hibernatorv1alpha1.ExceptionStateActive
			st.Message = "already active"
		},
	}

	require.NoError(t, w.handleExceptionStatusUpdate(context.Background(), logr.Discard(), update))
	assert.Equal(t, 1, callCount)
}

func TestHandleExceptionStatusUpdate_NotFound_ReturnsError(t *testing.T) {
	w := newFakeWriter()

	update := &message.ExceptionStatusUpdate{
		NamespacedName: types.NamespacedName{Name: "ghost", Namespace: "default"},
		Mutate:         func(st *hibernatorv1alpha1.ScheduleExceptionStatus) {},
	}

	require.Error(t, w.handleExceptionStatusUpdate(context.Background(), logr.Discard(), update))
}

// ---------------------------------------------------------------------------
// Writer.NeedLeaderElection
// ---------------------------------------------------------------------------

func TestWriter_NeedLeaderElection_ReturnsTrue(t *testing.T) {
	w := newFakeWriter()
	assert.True(t, w.NeedLeaderElection())
}

// ---------------------------------------------------------------------------
// Writer.Start — exits cleanly when context is cancelled
// ---------------------------------------------------------------------------

func TestWriter_Start_ExitsOnContextCancel(t *testing.T) {
	w := newFakeWriter()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := w.Start(ctx)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Writer.drain — flushes buffered updates when queues are non-empty
// ---------------------------------------------------------------------------

func TestWriter_Drain_EmptyQueues_IsNoop(t *testing.T) {
	w := newFakeWriter()
	planPool := keyedworker.New[types.NamespacedName, *message.PlanStatusUpdate]()
	excPool := keyedworker.New[types.NamespacedName, *message.ExceptionStatusUpdate]()
	// Should return immediately without panic.
	w.drain(logr.Discard(), planPool, excPool)
}

func TestWriter_Drain_ProcessesPlanUpdates(t *testing.T) {
	plan := basePlan("drain-plan")
	w := newFakeWriter(plan)

	// Pre-load two plan status updates into the queue.
	key := types.NamespacedName{Name: plan.Name, Namespace: plan.Namespace}
	w.Statuses.PlanStatuses.Send(&message.PlanStatusUpdate{
		NamespacedName: key,
		Mutate:         func(st *hibernatorv1alpha1.HibernatePlanStatus) {},
	})
	w.Statuses.PlanStatuses.Send(&message.PlanStatusUpdate{
		NamespacedName: key,
		Mutate:         func(st *hibernatorv1alpha1.HibernatePlanStatus) {},
	})

	require.Equal(t, 2, w.Statuses.PlanStatuses.Len())

	planPool := keyedworker.New[types.NamespacedName, *message.PlanStatusUpdate]()
	excPool := keyedworker.New[types.NamespacedName, *message.ExceptionStatusUpdate]()
	w.drain(logr.Discard(), planPool, excPool)

	assert.Equal(t, 0, w.Statuses.PlanStatuses.Len())
}

func TestWriter_Drain_ProcessesExceptionUpdates(t *testing.T) {
	exc := baseException("drain-exc")
	w := newFakeWriter(exc)

	key := types.NamespacedName{Name: exc.Name, Namespace: exc.Namespace}
	w.Statuses.ExceptionStatuses.Send(&message.ExceptionStatusUpdate{
		NamespacedName: key,
		Mutate:         func(st *hibernatorv1alpha1.ScheduleExceptionStatus) {},
	})

	require.Equal(t, 1, w.Statuses.ExceptionStatuses.Len())

	planPool := keyedworker.New[types.NamespacedName, *message.PlanStatusUpdate]()
	excPool := keyedworker.New[types.NamespacedName, *message.ExceptionStatusUpdate]()
	w.drain(logr.Discard(), planPool, excPool)

	assert.Equal(t, 0, w.Statuses.ExceptionStatuses.Len())
}
