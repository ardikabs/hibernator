/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package provider

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	clocktesting "k8s.io/utils/clock/testing"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

func newProviderTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = hibernatorv1alpha1.AddToScheme(scheme)
	_ = batchv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	return scheme
}

// newPlanReconciler wires a PlanReconciler with a fake client seeded with objs.
func newPlanReconciler(clk *clocktesting.FakeClock, objs ...client.Object) (*PlanReconciler, *message.ControllerResources) {
	scheme := newProviderTestScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&hibernatorv1alpha1.HibernatePlan{}).
		WithIndex(&hibernatorv1alpha1.ScheduleException{}, wellknown.FieldIndexExceptionPlanRef, func(obj client.Object) []string {
			exc, ok := obj.(*hibernatorv1alpha1.ScheduleException)
			if !ok {
				return nil
			}
			return []string{exc.Spec.PlanRef.Name}
		}).
		Build()

	resources := new(message.ControllerResources)

	r := &PlanReconciler{
		Client:            fakeClient,
		APIReader:         fakeClient,
		Clock:             clk,
		Log:               logr.Discard(),
		Scheme:            scheme,
		Resources:         resources,
		ScheduleEvaluator: scheduler.NewScheduleEvaluator(clk),
		RestoreManager:    restore.NewManager(fakeClient),
		Planner:           scheduler.NewPlanner(),
	}
	return r, resources
}

// simplePlan builds a minimal HibernatePlan for testing.
func simplePlan(name, namespace string) *hibernatorv1alpha1.HibernatePlan {
	return &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: hibernatorv1alpha1.HibernatePlanSpec{
			Schedule: hibernatorv1alpha1.Schedule{
				Timezone: "UTC",
				OffHours: []hibernatorv1alpha1.OffHourWindow{
					{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
				},
			},
		},
	}
}

// ---------------------------------------------------------------------------
// PlanReconciler.Reconcile
// ---------------------------------------------------------------------------

func TestPlanReconciler_Reconcile_PlanNotFound_RemovesFromWatchable(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	r, resources := newPlanReconciler(clk) // no objects

	key := types.NamespacedName{Name: "missing", Namespace: "default"}
	// Pre-seed the map so we can confirm it is removed.
	resources.PlanResources.Store(key, &message.PlanContext{})

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)

	_, ok := resources.PlanResources.Load(key)
	assert.False(t, ok, "deleted plan should be removed from watchable map")
}

func TestPlanReconciler_Reconcile_PlanFound_StoresPlanContext(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	plan := simplePlan("my-plan", "default")
	r, resources := newPlanReconciler(clk, plan)

	key := types.NamespacedName{Name: "my-plan", Namespace: "default"}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)

	stored, ok := resources.PlanResources.Load(key)
	require.True(t, ok, "plan should be stored in watchable map")
	assert.NotNil(t, stored.Plan)
	assert.Equal(t, "my-plan", stored.Plan.Name)
}

func TestPlanReconciler_Reconcile_WithException_PopulatesExceptions(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	plan := simplePlan("my-plan", "default")
	exception := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "exc-1",
			Namespace: "default",
			Labels:    map[string]string{wellknown.LabelPlan: "my-plan"},
		},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			PlanRef:    hibernatorv1alpha1.PlanReference{Name: "my-plan"},
			ValidFrom:  metav1.NewTime(clk.Now().Add(-1 * time.Hour)),
			ValidUntil: metav1.NewTime(clk.Now().Add(1 * time.Hour)),
			Type:       hibernatorv1alpha1.ExceptionSuspend,
		},
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{
			State: hibernatorv1alpha1.ExceptionStateActive,
		},
	}
	r, resources := newPlanReconciler(clk, plan, exception)

	key := types.NamespacedName{Name: "my-plan", Namespace: "default"}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)

	stored, ok := resources.PlanResources.Load(key)
	require.True(t, ok)
	assert.NotNil(t, stored.Schedule, "Schedule should be populated in PlanContext")
	assert.Len(t, stored.Schedule.Exceptions, 1, "one exception should be attached to PlanContext's schedule")
}

// ---------------------------------------------------------------------------
// PlanReconciler.selectActiveException
// ---------------------------------------------------------------------------

func TestSelectActiveException_NoExceptions_ReturnsNil(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	r, _ := newPlanReconciler(clk)
	plan := simplePlan("p", "default")

	exc, err := r.selectActiveException(logr.Discard(), plan, nil)
	require.NoError(t, err)
	assert.Nil(t, exc)
}

func TestSelectActiveException_ActiveMatchingException_ReturnsException(t *testing.T) {
	now := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	clk := clocktesting.NewFakeClock(now)
	r, _ := newPlanReconciler(clk)
	plan := simplePlan("p", "default")

	exceptions := []hibernatorv1alpha1.ScheduleException{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "exc-1", Namespace: "default"},
			Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
				PlanRef:    hibernatorv1alpha1.PlanReference{Name: "p"},
				ValidFrom:  metav1.NewTime(now.Add(-1 * time.Hour)),
				ValidUntil: metav1.NewTime(now.Add(1 * time.Hour)),
				Type:       hibernatorv1alpha1.ExceptionSuspend,
				Windows:    []hibernatorv1alpha1.OffHourWindow{{Start: "08:00", End: "22:00", DaysOfWeek: []string{"MON"}}},
			},
			Status: hibernatorv1alpha1.ScheduleExceptionStatus{
				State: hibernatorv1alpha1.ExceptionStateActive,
			},
		},
	}

	exc, err := r.selectActiveException(logr.Discard(), plan, exceptions)
	require.NoError(t, err)
	require.NotNil(t, exc)
}

func TestSelectActiveException_PendingException_IsIgnored(t *testing.T) {
	now := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	clk := clocktesting.NewFakeClock(now)
	r, _ := newPlanReconciler(clk)
	plan := simplePlan("p", "default")

	// Exception whose ValidFrom is still in the future — genuinely pending by time.
	// filterActiveExceptions uses time-based logic only (no Status.State check),
	// so this exception is correctly excluded before reaching selectActiveException.
	exceptions := []hibernatorv1alpha1.ScheduleException{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "exc-1", Namespace: "default"},
			Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
				ValidFrom:  metav1.NewTime(now.Add(1 * time.Hour)), // future
				ValidUntil: metav1.NewTime(now.Add(2 * time.Hour)),
				Type:       hibernatorv1alpha1.ExceptionSuspend,
			},
		},
	}

	active := r.filterActiveExceptions(exceptions)
	exc, err := r.selectActiveException(logr.Discard(), plan, active)
	require.NoError(t, err)
	assert.Nil(t, exc, "future-ValidFrom exception should be excluded from schedule evaluation")
}

func TestSelectActiveException_ExpiredTimeWindow_IsIgnored(t *testing.T) {
	now := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	clk := clocktesting.NewFakeClock(now)
	r, _ := newPlanReconciler(clk)
	plan := simplePlan("p", "default")

	exceptions := []hibernatorv1alpha1.ScheduleException{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "exc-1", Namespace: "default"},
			Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
				ValidFrom:  metav1.NewTime(now.Add(-3 * time.Hour)),
				ValidUntil: metav1.NewTime(now.Add(-1 * time.Hour)), // already expired
				Type:       hibernatorv1alpha1.ExceptionSuspend,
			},
		},
	}

	active := r.filterActiveExceptions(exceptions)
	exc, err := r.selectActiveException(logr.Discard(), plan, active)
	require.NoError(t, err)
	assert.Nil(t, exc, "exception past ValidUntil should be excluded from schedule evaluation")
}

func TestSelectActiveException_MultipleActive_PicksNewest(t *testing.T) {
	now := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	clk := clocktesting.NewFakeClock(now)
	r, _ := newPlanReconciler(clk)
	plan := simplePlan("p", "default")

	older := hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "exc-older",
			Namespace:         "default",
			CreationTimestamp: metav1.NewTime(now.Add(-2 * time.Hour)),
		},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			ValidFrom:  metav1.NewTime(now.Add(-1 * time.Hour)),
			ValidUntil: metav1.NewTime(now.Add(1 * time.Hour)),
			Type:       hibernatorv1alpha1.ExceptionSuspend,
			Windows:    []hibernatorv1alpha1.OffHourWindow{{Start: "08:00", End: "22:00", DaysOfWeek: []string{"MON"}}},
		},
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive},
	}
	newer := hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "exc-newer",
			Namespace:         "default",
			CreationTimestamp: metav1.NewTime(now.Add(-30 * time.Minute)),
		},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			ValidFrom:  metav1.NewTime(now.Add(-1 * time.Hour)),
			ValidUntil: metav1.NewTime(now.Add(1 * time.Hour)),
			Type:       hibernatorv1alpha1.ExceptionExtend,
			Windows:    []hibernatorv1alpha1.OffHourWindow{{Start: "08:00", End: "22:00", DaysOfWeek: []string{"TUE"}}},
		},
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive},
	}

	active := r.filterActiveExceptions([]hibernatorv1alpha1.ScheduleException{older, newer})
	exc, err := r.selectActiveException(logr.Discard(), plan, active)
	require.NoError(t, err)
	require.NotNil(t, exc)
	// The newer exception has ExceptionExtend type; verify correct exception was picked.
	assert.Equal(t, scheduler.ExceptionType(hibernatorv1alpha1.ExceptionExtend), exc.Type)
}

// ---------------------------------------------------------------------------
// PlanReconciler.findPlansForException
// ---------------------------------------------------------------------------

func TestFindPlansForException_ReturnsReconcileRequest(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	r, _ := newPlanReconciler(clk)

	exception := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "exc-1", Namespace: "default"},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			PlanRef: hibernatorv1alpha1.PlanReference{Name: "my-plan"},
		},
	}

	requests := r.findPlansForException(context.Background(), exception)
	require.Len(t, requests, 1)
	assert.Equal(t, "my-plan", requests[0].Name)
	assert.Equal(t, "default", requests[0].Namespace)
}

func TestFindPlansForException_NonExceptionObject_ReturnsNil(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	r, _ := newPlanReconciler(clk)

	requests := r.findPlansForException(context.Background(), &hibernatorv1alpha1.HibernatePlan{})
	assert.Nil(t, requests)
}
