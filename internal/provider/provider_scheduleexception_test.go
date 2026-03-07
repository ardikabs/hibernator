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
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

func newExceptionReconciler(clk *clocktesting.FakeClock, objs ...client.Object) (*ExceptionReconciler, *message.ControllerResources) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = hibernatorv1alpha1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&hibernatorv1alpha1.ScheduleException{}).
		Build()

	resources := new(message.ControllerResources)

	r := &ExceptionReconciler{
		Client:    fakeClient,
		APIReader: fakeClient,
		Clock:     clk,
		Log:       logr.Discard(),
		Scheme:    scheme,
		Resources: resources,
	}
	return r, resources
}

// simpleException returns a ScheduleException in the given state.
func simpleException(name, namespace, planName string, state hibernatorv1alpha1.ExceptionState, validFrom, validUntil time.Time) *hibernatorv1alpha1.ScheduleException {
	return &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{wellknown.LabelPlan: planName},
		},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			PlanRef:    hibernatorv1alpha1.PlanReference{Name: planName},
			ValidFrom:  metav1.NewTime(validFrom),
			ValidUntil: metav1.NewTime(validUntil),
			Type:       hibernatorv1alpha1.ExceptionSuspend,
		},
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{
			State: state,
		},
	}
}

// ---------------------------------------------------------------------------
// ExceptionReconciler.Reconcile
// ---------------------------------------------------------------------------

func TestExceptionReconciler_Reconcile_NotFound_RemovesFromWatchable(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	r, resources := newExceptionReconciler(clk) // no objects

	key := types.NamespacedName{Name: "missing", Namespace: "default"}
	// Pre-seed so we can confirm removal.
	resources.ExceptionResources.Store(key, &hibernatorv1alpha1.ScheduleException{})

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)

	_, ok := resources.ExceptionResources.Load(key)
	assert.False(t, ok, "deleted exception should be removed from watchable map")
}

func TestExceptionReconciler_Reconcile_ExpiredState_RemovesFromWatchable(t *testing.T) {
	now := time.Now()
	clk := clocktesting.NewFakeClock(now)

	exc := simpleException("exc-1", "default", "plan-a",
		hibernatorv1alpha1.ExceptionStateExpired,
		now.Add(-3*time.Hour), now.Add(-1*time.Hour))

	r, resources := newExceptionReconciler(clk, exc)

	key := types.NamespacedName{Name: "exc-1", Namespace: "default"}
	resources.ExceptionResources.Store(key, exc)

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)

	_, ok := resources.ExceptionResources.Load(key)
	assert.False(t, ok, "expired exception should be removed from watchable map")
}

func TestExceptionReconciler_Reconcile_ActiveState_StoresInWatchable(t *testing.T) {
	now := time.Now()
	clk := clocktesting.NewFakeClock(now)

	exc := simpleException("exc-1", "default", "plan-a",
		hibernatorv1alpha1.ExceptionStateActive,
		now.Add(-1*time.Hour), now.Add(1*time.Hour))

	r, resources := newExceptionReconciler(clk, exc)

	key := types.NamespacedName{Name: "exc-1", Namespace: "default"}
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)

	stored, ok := resources.ExceptionResources.Load(key)
	require.True(t, ok, "active exception should be stored in watchable map")
	assert.Equal(t, "exc-1", stored.Name)

	// Active exception should requeue at ValidUntil.
	wantRequeue := exc.Spec.ValidUntil.Time.Sub(now)
	assert.InDelta(t, wantRequeue.Seconds(), result.RequeueAfter.Seconds(), 1,
		"should requeue at ValidUntil boundary")
}

func TestExceptionReconciler_Reconcile_PendingState_StoresInWatchable(t *testing.T) {
	now := time.Now()
	clk := clocktesting.NewFakeClock(now)

	exc := simpleException("exc-1", "default", "plan-a",
		hibernatorv1alpha1.ExceptionStatePending,
		now.Add(30*time.Minute), now.Add(2*time.Hour))

	r, resources := newExceptionReconciler(clk, exc)

	key := types.NamespacedName{Name: "exc-1", Namespace: "default"}
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)

	stored, ok := resources.ExceptionResources.Load(key)
	require.True(t, ok, "pending exception should be stored in watchable map")
	assert.Equal(t, "exc-1", stored.Name)

	// Pending exception should requeue at ValidFrom.
	wantRequeue := exc.Spec.ValidFrom.Time.Sub(now)
	assert.InDelta(t, wantRequeue.Seconds(), result.RequeueAfter.Seconds(), 1,
		"should requeue at ValidFrom (activation time)")
}

// ---------------------------------------------------------------------------
// ExceptionReconciler.handleRequeue
// ---------------------------------------------------------------------------

func TestHandleRequeue_Pending_RequeuesAtValidFrom(t *testing.T) {
	now := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	clk := clocktesting.NewFakeClock(now)
	r, _ := newExceptionReconciler(clk)

	validFrom := now.Add(2 * time.Hour)
	exc := &hibernatorv1alpha1.ScheduleException{
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			ValidFrom:  metav1.NewTime(validFrom),
			ValidUntil: metav1.NewTime(now.Add(4 * time.Hour)),
		},
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{
			State: hibernatorv1alpha1.ExceptionStatePending,
		},
	}

	result, err := r.handleRequeue(exc)
	require.NoError(t, err)
	assert.InDelta(t, (2 * time.Hour).Seconds(), result.RequeueAfter.Seconds(), 1)
}

func TestHandleRequeue_Active_WithValidUntil_RequeuesAtExpiry(t *testing.T) {
	now := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	clk := clocktesting.NewFakeClock(now)
	r, _ := newExceptionReconciler(clk)

	validUntil := now.Add(3 * time.Hour)
	exc := &hibernatorv1alpha1.ScheduleException{
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			ValidFrom:  metav1.NewTime(now.Add(-1 * time.Hour)),
			ValidUntil: metav1.NewTime(validUntil),
		},
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{
			State: hibernatorv1alpha1.ExceptionStateActive,
		},
	}

	result, err := r.handleRequeue(exc)
	require.NoError(t, err)
	assert.InDelta(t, (3 * time.Hour).Seconds(), result.RequeueAfter.Seconds(), 1)
}

func TestHandleRequeue_Active_WithoutValidUntil_NoRequeue(t *testing.T) {
	now := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	clk := clocktesting.NewFakeClock(now)
	r, _ := newExceptionReconciler(clk)

	exc := &hibernatorv1alpha1.ScheduleException{
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			ValidFrom: metav1.NewTime(now.Add(-1 * time.Hour)),
			// ValidUntil zero
		},
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{
			State: hibernatorv1alpha1.ExceptionStateActive,
		},
	}

	result, err := r.handleRequeue(exc)
	require.NoError(t, err)
	assert.Zero(t, result.RequeueAfter, "no ValidUntil → no requeue")
}

func TestHandleRequeue_ValidFromInPast_RequeuesImmediately(t *testing.T) {
	now := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	clk := clocktesting.NewFakeClock(now)
	r, _ := newExceptionReconciler(clk)

	// validFrom was 1 hour ago; max(0, ...) should clamp to 0.
	exc := &hibernatorv1alpha1.ScheduleException{
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			ValidFrom:  metav1.NewTime(now.Add(-1 * time.Hour)),
			ValidUntil: metav1.NewTime(now.Add(1 * time.Hour)),
		},
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{
			State: hibernatorv1alpha1.ExceptionStatePending,
		},
	}

	result, err := r.handleRequeue(exc)
	require.NoError(t, err)
	assert.Zero(t, result.RequeueAfter, "past ValidFrom should result in immediate requeue (0)")
}
