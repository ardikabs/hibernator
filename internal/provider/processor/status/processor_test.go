/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package status

import (
	"context"
	"errors"
	"testing"
	"time"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = hibernatorv1alpha1.AddToScheme(s)
	return s
}

func newTestFakeClient(objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(objs...).
		WithStatusSubresource(
			&hibernatorv1alpha1.HibernatePlan{},
			&hibernatorv1alpha1.ScheduleException{},
		).
		Build()
}

func basePlan(name, namespace string, phase hibernatorv1alpha1.PlanPhase) *hibernatorv1alpha1.HibernatePlan {
	return &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			Phase: phase,
		},
	}
}

func newPlanProcessor(objs ...client.Object) *UpdateProcessor[*hibernatorv1alpha1.HibernatePlan] {
	c := newTestFakeClient(objs...)
	return NewUpdateProcessor[*hibernatorv1alpha1.HibernatePlan](logr.Discard(), c, c)
}

// ---------------------------------------------------------------------------
// MutatorFunc
// ---------------------------------------------------------------------------

func TestMutatorFunc_Mutate_CallsInnerFunction(t *testing.T) {
	plan := basePlan("p", "default", hibernatorv1alpha1.PhaseActive)

	var called bool
	fn := MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
		called = true
		p.Status.Phase = hibernatorv1alpha1.PhaseHibernating
	})

	fn.Mutate(plan)

	assert.True(t, called)
	assert.Equal(t, hibernatorv1alpha1.PhaseHibernating, plan.Status.Phase)
}

func TestMutatorFunc_Mutate_NilFunc_IsNoop(t *testing.T) {
	plan := basePlan("p", "default", hibernatorv1alpha1.PhaseActive)

	var fn MutatorFunc[*hibernatorv1alpha1.HibernatePlan]
	// Must not panic
	fn.Mutate(plan)

	assert.Equal(t, hibernatorv1alpha1.PhaseActive, plan.Status.Phase)
}

// ---------------------------------------------------------------------------
// isStatusEqual — HibernatePlan
// ---------------------------------------------------------------------------

func TestIsStatusEqual_HibernatePlan_IdenticalStatus_ReturnsTrue(t *testing.T) {
	a := &hibernatorv1alpha1.HibernatePlan{
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			Phase: hibernatorv1alpha1.PhaseActive,
		},
	}
	b := a.DeepCopy()

	assert.True(t, isStatusEqual(a, b))
}

func TestIsStatusEqual_HibernatePlan_DifferentPhase_ReturnsFalse(t *testing.T) {
	a := &hibernatorv1alpha1.HibernatePlan{
		Status: hibernatorv1alpha1.HibernatePlanStatus{Phase: hibernatorv1alpha1.PhaseActive},
	}
	b := &hibernatorv1alpha1.HibernatePlan{
		Status: hibernatorv1alpha1.HibernatePlanStatus{Phase: hibernatorv1alpha1.PhaseHibernating},
	}

	assert.False(t, isStatusEqual(a, b))
}

func TestIsStatusEqual_HibernatePlan_DifferentExecutionStartedAt_ReturnsTrue(t *testing.T) {
	now := metav1.Now()
	later := metav1.NewTime(now.Add(time.Hour))

	a := &hibernatorv1alpha1.HibernatePlan{
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			Phase: hibernatorv1alpha1.PhaseHibernating,
			Executions: []hibernatorv1alpha1.ExecutionStatus{
				{Target: "db", State: "Running", StartedAt: &now},
			},
		},
	}
	b := a.DeepCopy()
	b.Status.Executions[0].StartedAt = &later

	assert.True(t, isStatusEqual(a, b))
}

func TestIsStatusEqual_HibernatePlan_DifferentExecutionFinishedAt_ReturnsTrue(t *testing.T) {
	now := metav1.Now()
	later := metav1.NewTime(now.Add(time.Hour))

	a := &hibernatorv1alpha1.HibernatePlan{
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			Phase: hibernatorv1alpha1.PhaseHibernated,
			Executions: []hibernatorv1alpha1.ExecutionStatus{
				{Target: "db", State: "Completed", FinishedAt: &now},
			},
		},
	}
	b := a.DeepCopy()
	b.Status.Executions[0].FinishedAt = &later

	assert.True(t, isStatusEqual(a, b))
}

func TestIsStatusEqual_HibernatePlan_DifferentExecutionMessage_ReturnsFalse(t *testing.T) {
	a := &hibernatorv1alpha1.HibernatePlan{
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			Executions: []hibernatorv1alpha1.ExecutionStatus{
				{Target: "db", State: "Failed", Message: "timeout"},
			},
		},
	}
	b := a.DeepCopy()
	b.Status.Executions[0].Message = "connection refused"

	assert.False(t, isStatusEqual(a, b))
}

func TestIsStatusEqual_HibernatePlan_DifferentExceptionAppliedAt_ReturnsTrue(t *testing.T) {
	now := metav1.Now()
	later := metav1.NewTime(now.Add(time.Hour))
	validFrom := metav1.NewTime(now.Add(-time.Hour))
	validUntil := metav1.NewTime(now.Add(2 * time.Hour))

	a := &hibernatorv1alpha1.HibernatePlan{
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			ActiveExceptions: []hibernatorv1alpha1.ExceptionReference{
				{
					Name:       "exc-1",
					Type:       hibernatorv1alpha1.ExceptionSuspend,
					ValidFrom:  validFrom,
					ValidUntil: validUntil,
					State:      hibernatorv1alpha1.ExceptionStateActive,
					AppliedAt:  &now,
				},
			},
		},
	}
	b := a.DeepCopy()
	b.Status.ActiveExceptions[0].AppliedAt = &later

	assert.True(t, isStatusEqual(a, b))
}

func TestIsStatusEqual_HibernatePlan_DifferentExceptionExpiredAt_ReturnsTrue(t *testing.T) {
	now := metav1.Now()
	later := metav1.NewTime(now.Add(time.Hour))
	validFrom := metav1.NewTime(now.Add(-2 * time.Hour))
	validUntil := metav1.NewTime(now.Add(-time.Hour))

	a := &hibernatorv1alpha1.HibernatePlan{
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			ActiveExceptions: []hibernatorv1alpha1.ExceptionReference{
				{
					Name:       "exc-1",
					Type:       hibernatorv1alpha1.ExceptionSuspend,
					ValidFrom:  validFrom,
					ValidUntil: validUntil,
					State:      hibernatorv1alpha1.ExceptionStateExpired,
					ExpiredAt:  &now,
				},
			},
		},
	}
	b := a.DeepCopy()
	b.Status.ActiveExceptions[0].ExpiredAt = &later

	assert.True(t, isStatusEqual(a, b))
}

// ---------------------------------------------------------------------------
// isStatusEqual — ScheduleException
// ---------------------------------------------------------------------------

func TestIsStatusEqual_ScheduleException_IdenticalStatus_ReturnsTrue(t *testing.T) {
	a := &hibernatorv1alpha1.ScheduleException{
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{
			State:   hibernatorv1alpha1.ExceptionStateActive,
			Message: "active",
		},
	}
	b := a.DeepCopy()

	assert.True(t, isStatusEqual(a, b))
}

func TestIsStatusEqual_ScheduleException_DifferentState_ReturnsFalse(t *testing.T) {
	a := &hibernatorv1alpha1.ScheduleException{
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{
			State: hibernatorv1alpha1.ExceptionStateActive,
		},
	}
	b := &hibernatorv1alpha1.ScheduleException{
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{
			State: hibernatorv1alpha1.ExceptionStateExpired,
		},
	}

	assert.False(t, isStatusEqual(a, b))
}

func TestIsStatusEqual_ScheduleException_DifferentAppliedAt_ReturnsTrue(t *testing.T) {
	now := metav1.Now()
	later := metav1.NewTime(now.Add(time.Hour))

	a := &hibernatorv1alpha1.ScheduleException{
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{
			State:     hibernatorv1alpha1.ExceptionStateActive,
			AppliedAt: &now,
		},
	}
	b := a.DeepCopy()
	b.Status.AppliedAt = &later

	assert.True(t, isStatusEqual(a, b))
}

func TestIsStatusEqual_ScheduleException_DifferentExpiredAt_ReturnsTrue(t *testing.T) {
	now := metav1.Now()
	later := metav1.NewTime(now.Add(time.Hour))

	a := &hibernatorv1alpha1.ScheduleException{
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{
			State:     hibernatorv1alpha1.ExceptionStateExpired,
			ExpiredAt: &now,
		},
	}
	b := a.DeepCopy()
	b.Status.ExpiredAt = &later

	assert.True(t, isStatusEqual(a, b))
}

func TestIsStatusEqual_ScheduleException_DifferentMessage_ReturnsFalse(t *testing.T) {
	a := &hibernatorv1alpha1.ScheduleException{
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{
			State:   hibernatorv1alpha1.ExceptionStateActive,
			Message: "applied",
		},
	}
	b := a.DeepCopy()
	b.Status.Message = "processing"

	assert.False(t, isStatusEqual(a, b))
}

// ---------------------------------------------------------------------------
// isStatusEqual — unknown type
// ---------------------------------------------------------------------------

func TestIsStatusEqual_UnknownType_ReturnsFalse(t *testing.T) {
	type unknownObj struct{}
	assert.False(t, isStatusEqual(&unknownObj{}, &unknownObj{}))
}

// ---------------------------------------------------------------------------
// UpdateProcessor.apply
// ---------------------------------------------------------------------------

func TestApply_NilMutator_Skips(t *testing.T) {
	ctx := context.Background()
	plan := basePlan("p1", "default", hibernatorv1alpha1.PhaseActive)
	proc := newPlanProcessor(plan)

	update := Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: types.NamespacedName{Name: "p1", Namespace: "default"},
		Resource:       plan,
		Mutator:        nil,
	}

	err := proc.apply(ctx, update)
	require.NoError(t, err)

	// Status must remain unchanged.
	fresh := &hibernatorv1alpha1.HibernatePlan{}
	require.NoError(t, proc.apiReader.Get(ctx, update.NamespacedName, fresh))
	assert.Equal(t, hibernatorv1alpha1.PhaseActive, fresh.Status.Phase)
}

func TestApply_ObjectNotFound_Skips(t *testing.T) {
	ctx := context.Background()
	proc := newPlanProcessor() // no objects seeded

	update := Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: types.NamespacedName{Name: "missing", Namespace: "default"},
		Resource:       basePlan("missing", "default", hibernatorv1alpha1.PhaseActive),
		Mutator: MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.Phase = hibernatorv1alpha1.PhaseHibernating
		}),
	}

	err := proc.apply(ctx, update)
	require.NoError(t, err)
}

func TestApply_MutationApplied_WritesStatus(t *testing.T) {
	ctx := context.Background()
	plan := basePlan("p1", "default", hibernatorv1alpha1.PhaseActive)
	proc := newPlanProcessor(plan)

	update := Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: types.NamespacedName{Name: "p1", Namespace: "default"},
		Resource:       plan,
		Mutator: MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.Phase = hibernatorv1alpha1.PhaseHibernating
		}),
	}

	err := proc.apply(ctx, update)
	require.NoError(t, err)

	fresh := &hibernatorv1alpha1.HibernatePlan{}
	require.NoError(t, proc.apiReader.Get(ctx, update.NamespacedName, fresh))
	assert.Equal(t, hibernatorv1alpha1.PhaseHibernating, fresh.Status.Phase)
}

func TestApply_StatusUnchanged_SkipsWrite(t *testing.T) {
	ctx := context.Background()
	plan := basePlan("p1", "default", hibernatorv1alpha1.PhaseActive)
	proc := newPlanProcessor(plan)

	// Mutator sets the same phase already stored — no-op write.
	update := Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: types.NamespacedName{Name: "p1", Namespace: "default"},
		Resource:       plan,
		Mutator: MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.Phase = hibernatorv1alpha1.PhaseActive
		}),
	}

	err := proc.apply(ctx, update)
	require.NoError(t, err)
}

func TestApply_PreHook_ObjectNotFound_Skips(t *testing.T) {
	ctx := context.Background()
	proc := newPlanProcessor() // no objects seeded

	var preHookCalled bool
	update := Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: types.NamespacedName{Name: "missing", Namespace: "default"},
		Resource:       basePlan("missing", "default", hibernatorv1alpha1.PhaseActive),
		PreHook: func(_ context.Context, _ *hibernatorv1alpha1.HibernatePlan) error {
			preHookCalled = true
			return nil
		},
		Mutator: MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.Phase = hibernatorv1alpha1.PhaseHibernating
		}),
	}

	err := proc.apply(ctx, update)
	require.NoError(t, err)
	assert.False(t, preHookCalled, "pre-hook must not be called when object is not found")
}

func TestApply_PreHook_Error_AbortsUpdate(t *testing.T) {
	ctx := context.Background()
	plan := basePlan("p1", "default", hibernatorv1alpha1.PhaseActive)
	proc := newPlanProcessor(plan)

	hookErr := errors.New("pre-hook failure")
	update := Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: types.NamespacedName{Name: "p1", Namespace: "default"},
		Resource:       plan,
		PreHook: func(_ context.Context, _ *hibernatorv1alpha1.HibernatePlan) error {
			return hookErr
		},
		Mutator: MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.Phase = hibernatorv1alpha1.PhaseHibernating
		}),
	}

	err := proc.apply(ctx, update)
	require.ErrorIs(t, err, hookErr)

	// Status must not have changed.
	fresh := &hibernatorv1alpha1.HibernatePlan{}
	require.NoError(t, proc.apiReader.Get(ctx, update.NamespacedName, fresh))
	assert.Equal(t, hibernatorv1alpha1.PhaseActive, fresh.Status.Phase)
}

func TestApply_PostHook_CalledAfterWrite(t *testing.T) {
	ctx := context.Background()
	plan := basePlan("p1", "default", hibernatorv1alpha1.PhaseActive)
	proc := newPlanProcessor(plan)

	var postHookCalled bool
	update := Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: types.NamespacedName{Name: "p1", Namespace: "default"},
		Resource:       plan,
		Mutator: MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.Phase = hibernatorv1alpha1.PhaseHibernating
		}),
		PostHook: func(_ context.Context, _ *hibernatorv1alpha1.HibernatePlan) error {
			postHookCalled = true
			return nil
		},
	}

	err := proc.apply(ctx, update)
	require.NoError(t, err)
	assert.True(t, postHookCalled)
}

func TestApply_PostHook_NotCalledWhenNoWrite(t *testing.T) {
	ctx := context.Background()
	plan := basePlan("p1", "default", hibernatorv1alpha1.PhaseActive)
	proc := newPlanProcessor(plan)

	var postHookCalled bool
	update := Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: types.NamespacedName{Name: "p1", Namespace: "default"},
		Resource:       plan,
		// No-op: sets the same phase that is already stored.
		Mutator: MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.Phase = hibernatorv1alpha1.PhaseActive
		}),
		PostHook: func(_ context.Context, _ *hibernatorv1alpha1.HibernatePlan) error {
			postHookCalled = true
			return nil
		},
	}

	err := proc.apply(ctx, update)
	require.NoError(t, err)
	assert.False(t, postHookCalled, "post-hook must not be called when status write is skipped")
}

func TestApply_PostHook_ErrorIsNonFatal(t *testing.T) {
	ctx := context.Background()
	plan := basePlan("p1", "default", hibernatorv1alpha1.PhaseActive)
	proc := newPlanProcessor(plan)

	update := Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: types.NamespacedName{Name: "p1", Namespace: "default"},
		Resource:       plan,
		Mutator: MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.Phase = hibernatorv1alpha1.PhaseHibernating
		}),
		PostHook: func(_ context.Context, _ *hibernatorv1alpha1.HibernatePlan) error {
			return errors.New("post-hook failure")
		},
	}

	// PostHook errors must be logged but must not surface as an error.
	err := proc.apply(ctx, update)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// defaultUpdater.Send
// ---------------------------------------------------------------------------

func TestDefaultUpdater_Send_AppliesMutatorToResourceImmediately(t *testing.T) {
	plan := basePlan("p1", "default", hibernatorv1alpha1.PhaseActive)
	c := newTestFakeClient(plan)
	proc := NewUpdateProcessor[*hibernatorv1alpha1.HibernatePlan](logr.Discard(), c, c)
	updater := proc.Writer()

	update := Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: types.NamespacedName{Name: "p1", Namespace: "default"},
		Resource:       plan,
		Mutator: MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.Phase = hibernatorv1alpha1.PhaseHibernating
		}),
	}

	updater.Send(update)

	// Mutator must have been applied to the in-memory resource immediately.
	assert.Equal(t, hibernatorv1alpha1.PhaseHibernating, plan.Status.Phase)
}

func TestDefaultUpdater_Send_NilMutator_DeliveredToPool(t *testing.T) {
	plan := basePlan("p1", "default", hibernatorv1alpha1.PhaseActive)
	c := newTestFakeClient(plan)
	proc := NewUpdateProcessor[*hibernatorv1alpha1.HibernatePlan](logr.Discard(), c, c)
	updater := proc.Writer()

	update := Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: types.NamespacedName{Name: "p1", Namespace: "default"},
		Resource:       plan,
		Mutator:        nil,
	}

	// Must not panic.
	updater.Send(update)

	// The update was buffered in the pool.
	assert.Equal(t, 1, proc.pool.Len())
}
