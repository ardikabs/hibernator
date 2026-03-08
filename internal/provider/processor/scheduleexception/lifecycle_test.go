/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package scheduleexception

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
	clocktesting "k8s.io/utils/clock/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
	statusprocessor "github.com/ardikabs/hibernator/internal/provider/processor/status"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newLifecycleScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = hibernatorv1alpha1.AddToScheme(s)
	return s
}

func newTestProcessor(t *testing.T, objs ...client.Object) (*LifecycleProcessor, *statusprocessor.ControllerStatuses) {
	t.Helper()
	scheme := newLifecycleScheme()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(
			&hibernatorv1alpha1.ScheduleException{},
			&hibernatorv1alpha1.HibernatePlan{},
		).
		WithObjects(objs...).
		Build()

	planUpdater := newCaptureUpdater[*hibernatorv1alpha1.HibernatePlan](64)
	exceptionUpdater := newCaptureUpdater[*hibernatorv1alpha1.ScheduleException](64)
	statuses := &statusprocessor.ControllerStatuses{
		PlanStatuses:      planUpdater,
		ExceptionStatuses: exceptionUpdater,
	}
	p := &LifecycleProcessor{
		Client:    c,
		APIReader: c,
		Clock:     clocktesting.NewFakeClock(time.Now()),
		Log:       logr.Discard(),
		Scheme:    scheme,
		Resources: &message.ControllerResources{},
		Statuses:  statuses,
	}
	return p, statuses
}

// captureUpdater is a test helper that buffers statusprocessor.Update[T] values
// sent via Send so that tests can inspect Len() or drain the channel.
type captureUpdater[T client.Object] struct {
	ch chan statusprocessor.Update[T]
}

func newCaptureUpdater[T client.Object](cap int) *captureUpdater[T] {
	return &captureUpdater[T]{ch: make(chan statusprocessor.Update[T], cap)}
}

func (u *captureUpdater[T]) Send(upd statusprocessor.Update[T]) {
	if upd.Mutator != nil {
		upd.Mutator.Mutate(upd.Resource)
	}
	u.ch <- upd
}
func (u *captureUpdater[T]) Len() int                            { return len(u.ch) }
func (u *captureUpdater[T]) C() <-chan statusprocessor.Update[T] { return u.ch }

// zeroLP returns a LifecycleProcessor with no fields set, usable only for pure methods.
func zeroLP() *LifecycleProcessor { return &LifecycleProcessor{} }

func exceptionWithWindow(from, until time.Time) *hibernatorv1alpha1.ScheduleException {
	ex := &hibernatorv1alpha1.ScheduleException{}
	if !from.IsZero() {
		ex.Spec.ValidFrom = metav1.Time{Time: from}
	}
	if !until.IsZero() {
		ex.Spec.ValidUntil = metav1.Time{Time: until}
	}
	return ex
}

func baseScheduleException(name, planRef string) *hibernatorv1alpha1.ScheduleException {
	return &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			PlanRef: hibernatorv1alpha1.PlanReference{Name: planRef},
		},
	}
}

// ---------------------------------------------------------------------------
// computeDesiredState
// ---------------------------------------------------------------------------

func TestComputeDesiredState_BeforeValidFrom_Pending(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	ex := exceptionWithWindow(now.Add(2*time.Hour), now.Add(4*time.Hour))
	assert.Equal(t, hibernatorv1alpha1.ExceptionStatePending,
		zeroLP().computeDesiredState(now, ex))
}

func TestComputeDesiredState_WithinWindow_Active(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	ex := exceptionWithWindow(now.Add(-1*time.Hour), now.Add(2*time.Hour))
	assert.Equal(t, hibernatorv1alpha1.ExceptionStateActive,
		zeroLP().computeDesiredState(now, ex))
}

func TestComputeDesiredState_AfterValidUntil_Expired(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	ex := exceptionWithWindow(now.Add(-3*time.Hour), now.Add(-1*time.Hour))
	assert.Equal(t, hibernatorv1alpha1.ExceptionStateExpired,
		zeroLP().computeDesiredState(now, ex))
}

func TestComputeDesiredState_NoValidUntil_ActiveIndefinitely(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	ex := exceptionWithWindow(now.Add(-1*time.Hour), time.Time{})
	assert.Equal(t, hibernatorv1alpha1.ExceptionStateActive,
		zeroLP().computeDesiredState(now, ex))
}

func TestComputeDesiredState_ExactlyAtValidFrom_Active(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	ex := exceptionWithWindow(now, now.Add(2*time.Hour))
	assert.Equal(t, hibernatorv1alpha1.ExceptionStateActive,
		zeroLP().computeDesiredState(now, ex))
}

// ---------------------------------------------------------------------------
// formatPendingMessage
// ---------------------------------------------------------------------------

func TestFormatPendingMessage_ZeroValidFrom_Generic(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	assert.Equal(t, "Exception pending",
		formatPendingMessage(now, &hibernatorv1alpha1.ScheduleException{}))
}

func TestFormatPendingMessage_MoreThanOneDayAway(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	ex := exceptionWithWindow(now.Add(48*time.Hour+30*time.Minute), time.Time{})
	assert.Contains(t, formatPendingMessage(now, ex), "days")
}

func TestFormatPendingMessage_FewHoursAway(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	ex := exceptionWithWindow(now.Add(3*time.Hour), time.Time{})
	msg := formatPendingMessage(now, ex)
	assert.Contains(t, msg, "hours")
	assert.NotContains(t, msg, "days")
}

func TestFormatPendingMessage_ActivatesSoon(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	ex := exceptionWithWindow(now.Add(45*time.Minute), time.Time{})
	assert.Equal(t, "Exception pending, activates soon",
		formatPendingMessage(now, ex))
}

// ---------------------------------------------------------------------------
// formatActiveMessage
// ---------------------------------------------------------------------------

func TestFormatActiveMessage_ZeroValidUntil_Generic(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	assert.Equal(t, "Exception active",
		formatActiveMessage(now, &hibernatorv1alpha1.ScheduleException{}))
}

func TestFormatActiveMessage_MoreThanOneDayLeft(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	ex := exceptionWithWindow(now.Add(-1*time.Hour), now.Add(72*time.Hour+10*time.Minute))
	assert.Contains(t, formatActiveMessage(now, ex), "days")
}

func TestFormatActiveMessage_FewHoursLeft(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	ex := exceptionWithWindow(now.Add(-1*time.Hour), now.Add(5*time.Hour))
	msg := formatActiveMessage(now, ex)
	assert.Contains(t, msg, "hours")
	assert.NotContains(t, msg, "days")
}

func TestFormatActiveMessage_ExpiresSoon(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	ex := exceptionWithWindow(now.Add(-1*time.Hour), now.Add(20*time.Minute))
	assert.Equal(t, "Exception active, expires soon",
		formatActiveMessage(now, ex))
}

// ---------------------------------------------------------------------------
// ensurePlanLabel
// ---------------------------------------------------------------------------

func TestEnsurePlanLabel_AddsLabel(t *testing.T) {
	ex := baseScheduleException("ex1", "my-plan")
	p, _ := newTestProcessor(t, ex)

	err := p.ensurePlanLabel(context.Background(), logr.Discard(), ex)
	require.NoError(t, err)

	updated := &hibernatorv1alpha1.ScheduleException{}
	require.NoError(t, p.Get(context.Background(),
		types.NamespacedName{Name: "ex1", Namespace: "default"}, updated))
	assert.Equal(t, "my-plan", updated.Labels[wellknown.LabelPlan])
}

func TestEnsurePlanLabel_AlreadySet_IsNoop(t *testing.T) {
	ex := baseScheduleException("ex2", "my-plan")
	ex.Labels = map[string]string{wellknown.LabelPlan: "my-plan"}
	p, _ := newTestProcessor(t, ex)

	err := p.ensurePlanLabel(context.Background(), logr.Discard(), ex)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// handleUpdate — finalizer path
// ---------------------------------------------------------------------------

func TestHandleUpdate_NilException_IsNoop(t *testing.T) {
	p, _ := newTestProcessor(t)
	errChan := make(chan error, 1)
	// Must not panic.
	p.handleUpdate(context.Background(), logr.Discard(),
		types.NamespacedName{Name: "x", Namespace: "default"}, nil, errChan)
}

func TestHandleUpdate_AddsFinalizer(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	from := now.Add(1 * time.Hour) // pending — so state stays Pending; no status write needed

	ex := baseScheduleException("ex-fin", "plan-a")
	ex.Spec.ValidFrom = metav1.Time{Time: from}
	p, _ := newTestProcessor(t, ex)
	p.Clock = clocktesting.NewFakeClock(now)

	errChan := make(chan error, 1)
	p.handleUpdate(context.Background(), logr.Discard(),
		types.NamespacedName{Name: "ex-fin", Namespace: "default"}, ex, errChan)

	select {
	case err := <-errChan:
		t.Fatalf("unexpected error: %v", err)
	default:
	}

	updated := &hibernatorv1alpha1.ScheduleException{}
	require.NoError(t, p.Get(context.Background(),
		types.NamespacedName{Name: "ex-fin", Namespace: "default"}, updated))

	hasFinalizer := false
	for _, f := range updated.Finalizers {
		if f == wellknown.ExceptionFinalizerName {
			hasFinalizer = true
			break
		}
	}
	assert.True(t, hasFinalizer, "finalizer should have been added")
}

func TestHandleUpdate_DeletionTimestamp_RemovesFromResources(t *testing.T) {
	ex := baseScheduleException("ex-del", "plan-a")
	nowTime := metav1.Now()
	ex.DeletionTimestamp = &nowTime

	p, _ := newTestProcessor(t)
	key := types.NamespacedName{Name: "ex-del", Namespace: "default"}

	// Pre-populate the watchable map so we can verify it gets deleted.
	p.Resources.ExceptionResources.Store(key, ex)

	errChan := make(chan error, 1)
	p.handleUpdate(context.Background(), logr.Discard(), key, ex, errChan)

	_, exists := p.Resources.ExceptionResources.Load(key)
	assert.False(t, exists, "exception should have been removed from watchable map on deletion")
}

// ---------------------------------------------------------------------------
// removeFromPlanStatus
// ---------------------------------------------------------------------------

func TestRemoveFromPlanStatus_QueuesStatusUpdate(t *testing.T) {
	ex := baseScheduleException("ex-rm", "plan-a")
	p, statuses := newTestProcessor(t)

	err := p.removeFromPlanStatus(context.Background(), logr.Discard(), ex)
	require.NoError(t, err)

	planUpdater := statuses.PlanStatuses.(*captureUpdater[*hibernatorv1alpha1.HibernatePlan])
	assert.Equal(t, 1, planUpdater.Len(),
		"should have queued one plan status update")
}
