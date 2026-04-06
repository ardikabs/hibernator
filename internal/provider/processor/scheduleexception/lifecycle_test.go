/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package scheduleexception

import (
	"context"
	"fmt"
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
		WithIndex(&hibernatorv1alpha1.ScheduleException{}, wellknown.FieldIndexExceptionPlanRef, func(obj client.Object) []string {
			exc, ok := obj.(*hibernatorv1alpha1.ScheduleException)
			if !ok {
				return nil
			}
			return []string{exc.Spec.PlanRef.Name}
		}).
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
		Clock:     clocktesting.NewFakeClock(time.Now()),
		Log:       logr.Discard(),
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
	p.handleExceptionUpdate(context.Background(), logr.Discard(),
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
	p.handleExceptionUpdate(context.Background(), logr.Discard(),
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
	// Add a finalizer so the fake client accepts the object with DeletionTimestamp.
	ex.Finalizers = []string{wellknown.ExceptionFinalizerName}
	nowTime := metav1.Now()
	ex.DeletionTimestamp = &nowTime

	p, _ := newTestProcessor(t, ex)
	key := types.NamespacedName{Name: "ex-del", Namespace: "default"}

	errChan := make(chan error, 1)
	p.handleExceptionUpdate(context.Background(), logr.Discard(), key, ex, errChan)

	// handleExceptionUpdate with DeletionTimestamp delegates to handleExceptionDelete,
	// which re-fetches from APIReader and handles cleanup (finalizer removal).
	select {
	case err := <-errChan:
		t.Fatalf("unexpected error during deletion handling: %v", err)
	default:
	}

	// After finalizer removal, the fake client auto-deletes the object
	// because DeletionTimestamp is set and no finalizers remain.
	updated := &hibernatorv1alpha1.ScheduleException{}
	err := p.Get(context.Background(), key, updated)
	assert.True(t, client.IgnoreNotFound(err) == nil, "exception should be deleted after finalizer removal")
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

// ---------------------------------------------------------------------------
// hasOwnerReferenceToPlan
// ---------------------------------------------------------------------------

func TestHasOwnerReferenceToPlan_NoOwnerRefs(t *testing.T) {
	ex := baseScheduleException("ex1", "my-plan")
	key := types.NamespacedName{Name: "my-plan", Namespace: "default"}
	assert.False(t, hasOwnerReferenceToPlan(ex, key))
}

func TestHasOwnerReferenceToPlan_MatchingRef(t *testing.T) {
	ex := baseScheduleException("ex1", "my-plan")
	ex.OwnerReferences = []metav1.OwnerReference{
		{
			APIVersion: hibernatorv1alpha1.GroupVersion.String(),
			Kind:       "HibernatePlan",
			Name:       "my-plan",
		},
	}
	key := types.NamespacedName{Name: "my-plan", Namespace: "default"}
	assert.True(t, hasOwnerReferenceToPlan(ex, key))
}

func TestHasOwnerReferenceToPlan_DifferentPlanName(t *testing.T) {
	ex := baseScheduleException("ex1", "my-plan")
	ex.OwnerReferences = []metav1.OwnerReference{
		{
			APIVersion: hibernatorv1alpha1.GroupVersion.String(),
			Kind:       "HibernatePlan",
			Name:       "other-plan",
		},
	}
	key := types.NamespacedName{Name: "my-plan", Namespace: "default"}
	assert.False(t, hasOwnerReferenceToPlan(ex, key))
}

func TestHasOwnerReferenceToPlan_DifferentKind(t *testing.T) {
	ex := baseScheduleException("ex1", "my-plan")
	ex.OwnerReferences = []metav1.OwnerReference{
		{
			APIVersion: hibernatorv1alpha1.GroupVersion.String(),
			Kind:       "CloudProvider",
			Name:       "my-plan",
		},
	}
	key := types.NamespacedName{Name: "my-plan", Namespace: "default"}
	assert.False(t, hasOwnerReferenceToPlan(ex, key))
}

// ---------------------------------------------------------------------------
// transitionToDetached
// ---------------------------------------------------------------------------

func TestTransitionToDetached_QueuesUpdate(t *testing.T) {
	ex := baseScheduleException("ex1", "my-plan")
	ex.Status.State = hibernatorv1alpha1.ExceptionStateActive

	p, statuses := newTestProcessor(t, ex)
	key := types.NamespacedName{Name: "ex1", Namespace: "default"}

	p.transitionToDetached(context.Background(), logr.Discard(), key, ex, "my-plan")

	excUpdater := statuses.ExceptionStatuses.(*captureUpdater[*hibernatorv1alpha1.ScheduleException])
	require.Equal(t, 1, excUpdater.Len(), "should have queued one exception status update")

	upd := <-excUpdater.C()
	assert.Equal(t, hibernatorv1alpha1.ExceptionStateDetached, upd.Resource.Status.State)
	assert.Contains(t, upd.Resource.Status.Message, "my-plan")
	assert.Contains(t, upd.Resource.Status.Message, "no longer exists")
	assert.NotNil(t, upd.Resource.Status.DetachedAt, "DetachedAt should be set when transitioning to Detached")
	assert.False(t, upd.Resource.Status.DetachedAt.IsZero(), "DetachedAt should not be zero")
}

func TestTransitionToDetached_AlreadyDetached_IsNoop(t *testing.T) {
	ex := baseScheduleException("ex1", "my-plan")
	ex.Status.State = hibernatorv1alpha1.ExceptionStateDetached

	p, statuses := newTestProcessor(t, ex)
	key := types.NamespacedName{Name: "ex1", Namespace: "default"}

	p.transitionToDetached(context.Background(), logr.Discard(), key, ex, "my-plan")

	excUpdater := statuses.ExceptionStatuses.(*captureUpdater[*hibernatorv1alpha1.ScheduleException])
	assert.Equal(t, 0, excUpdater.Len(), "should not queue update for already-detached exception")
}

// ---------------------------------------------------------------------------
// handlePlanDelete — Detached vs OwnerReference
// ---------------------------------------------------------------------------

func TestHandlePlanDelete_NoOwnerRef_TransitionsToDetached(t *testing.T) {
	ex1 := baseScheduleException("ex1", "my-plan")
	ex1.Status.State = hibernatorv1alpha1.ExceptionStateActive
	ex2 := baseScheduleException("ex2", "my-plan")
	ex2.Status.State = hibernatorv1alpha1.ExceptionStatePending

	p, statuses := newTestProcessor(t, ex1, ex2)
	planKey := types.NamespacedName{Name: "my-plan", Namespace: "default"}
	errChan := make(chan error, 4)

	p.handlePlanDelete(context.Background(), logr.Discard(), planKey, errChan)

	select {
	case err := <-errChan:
		t.Fatalf("unexpected error: %v", err)
	default:
	}

	excUpdater := statuses.ExceptionStatuses.(*captureUpdater[*hibernatorv1alpha1.ScheduleException])
	assert.Equal(t, 2, excUpdater.Len(), "both exceptions should be transitioned to Detached")

	for range 2 {
		upd := <-excUpdater.C()
		assert.Equal(t, hibernatorv1alpha1.ExceptionStateDetached, upd.Resource.Status.State)
	}
}

func TestHandlePlanDelete_WithOwnerRef_SkipsException(t *testing.T) {
	ex := baseScheduleException("ex-owned", "my-plan")
	ex.Status.State = hibernatorv1alpha1.ExceptionStateActive
	ex.OwnerReferences = []metav1.OwnerReference{
		{
			APIVersion: hibernatorv1alpha1.GroupVersion.String(),
			Kind:       "HibernatePlan",
			Name:       "my-plan",
		},
	}

	p, statuses := newTestProcessor(t, ex)
	planKey := types.NamespacedName{Name: "my-plan", Namespace: "default"}
	errChan := make(chan error, 4)

	p.handlePlanDelete(context.Background(), logr.Discard(), planKey, errChan)

	select {
	case err := <-errChan:
		t.Fatalf("unexpected error: %v", err)
	default:
	}

	excUpdater := statuses.ExceptionStatuses.(*captureUpdater[*hibernatorv1alpha1.ScheduleException])
	assert.Equal(t, 0, excUpdater.Len(), "owned exception should be skipped (K8s GC handles it)")
}

func TestHandlePlanDelete_MixedOwnership(t *testing.T) {
	// ex-owned has OwnerReference — should be skipped.
	exOwned := baseScheduleException("ex-owned", "my-plan")
	exOwned.Status.State = hibernatorv1alpha1.ExceptionStateActive
	exOwned.OwnerReferences = []metav1.OwnerReference{
		{
			APIVersion: hibernatorv1alpha1.GroupVersion.String(),
			Kind:       "HibernatePlan",
			Name:       "my-plan",
		},
	}

	// ex-standalone has no OwnerReference — should become Detached.
	exStandalone := baseScheduleException("ex-standalone", "my-plan")
	exStandalone.Status.State = hibernatorv1alpha1.ExceptionStateActive

	p, statuses := newTestProcessor(t, exOwned, exStandalone)
	planKey := types.NamespacedName{Name: "my-plan", Namespace: "default"}
	errChan := make(chan error, 4)

	p.handlePlanDelete(context.Background(), logr.Discard(), planKey, errChan)

	select {
	case err := <-errChan:
		t.Fatalf("unexpected error: %v", err)
	default:
	}

	excUpdater := statuses.ExceptionStatuses.(*captureUpdater[*hibernatorv1alpha1.ScheduleException])
	assert.Equal(t, 1, excUpdater.Len(), "only standalone exception should be transitioned")

	upd := <-excUpdater.C()
	assert.Equal(t, "ex-standalone", upd.NamespacedName.Name)
	assert.Equal(t, hibernatorv1alpha1.ExceptionStateDetached, upd.Resource.Status.State)
}

// ---------------------------------------------------------------------------
// updateExceptionReferences
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Re-attachment: Plan exists → deleted (Detached) → re-created (re-attached)
// ---------------------------------------------------------------------------

func TestReattachment_DetachedExceptionTransitionsOnPlanRecreate(t *testing.T) {
	// This test verifies the full re-attachment lifecycle:
	// 1. Exception is Active while plan exists
	// 2. Plan is deleted → exception becomes Detached
	// 3. Plan is re-created → exception re-attaches and returns to the correct
	//    time-based state (Pending, Active, or Expired)

	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	fakeClock := clocktesting.NewFakeClock(now)

	// Three exceptions with different time windows to verify all re-attachment targets.
	// No Finalizers here — transitionToDetached's Patch call to remove the finalizer
	// would overwrite the in-memory Status via the fake client response, corrupting
	// the captureUpdater assertion. The finalizerless path still exercises the
	// Detached status transition which is what we care about.

	// (a) Active window: started 1h ago, ends in 2h
	excActive := baseScheduleException("exc-active", "my-plan")
	excActive.Spec.Type = "suspend"
	excActive.Spec.ValidFrom = metav1.Time{Time: now.Add(-1 * time.Hour)}
	excActive.Spec.ValidUntil = metav1.Time{Time: now.Add(2 * time.Hour)}
	excActive.Labels = map[string]string{wellknown.LabelPlan: "my-plan"}
	excActive.Status.State = hibernatorv1alpha1.ExceptionStateActive

	// (b) Pending window: starts in 3h, ends in 6h
	excPending := baseScheduleException("exc-pending", "my-plan")
	excPending.Spec.Type = "extend"
	excPending.Spec.ValidFrom = metav1.Time{Time: now.Add(3 * time.Hour)}
	excPending.Spec.ValidUntil = metav1.Time{Time: now.Add(6 * time.Hour)}
	excPending.Labels = map[string]string{wellknown.LabelPlan: "my-plan"}
	excPending.Status.State = hibernatorv1alpha1.ExceptionStatePending

	// (c) Expired window: ended 1h ago
	excExpired := baseScheduleException("exc-expired", "my-plan")
	excExpired.Spec.Type = "suspend"
	excExpired.Spec.ValidFrom = metav1.Time{Time: now.Add(-5 * time.Hour)}
	excExpired.Spec.ValidUntil = metav1.Time{Time: now.Add(-1 * time.Hour)}
	excExpired.Labels = map[string]string{wellknown.LabelPlan: "my-plan"}
	excExpired.Status.State = hibernatorv1alpha1.ExceptionStateExpired

	p, statuses := newTestProcessor(t, excActive, excPending, excExpired)
	p.Clock = fakeClock

	planKey := types.NamespacedName{Name: "my-plan", Namespace: "default"}
	errChan := make(chan error, 16)

	// ── Step 1: Delete plan → all exceptions become Detached ──
	p.handlePlanDelete(context.Background(), logr.Discard(), planKey, errChan)

	select {
	case err := <-errChan:
		t.Fatalf("unexpected error during plan delete: %v", err)
	default:
	}

	excUpdater := statuses.ExceptionStatuses.(*captureUpdater[*hibernatorv1alpha1.ScheduleException])
	require.Equal(t, 3, excUpdater.Len(), "all three exceptions should transition to Detached")

	detachedExceptions := make(map[string]*hibernatorv1alpha1.ScheduleException)
	for range 3 {
		upd := <-excUpdater.C()
		assert.Equal(t, hibernatorv1alpha1.ExceptionStateDetached, upd.Resource.Status.State,
			"exception %s should be Detached", upd.NamespacedName.Name)
		assert.NotNil(t, upd.Resource.Status.DetachedAt, "DetachedAt should be set")
		detachedExceptions[upd.NamespacedName.Name] = upd.Resource
	}

	// ── Step 2: Re-create plan → simulate handlePlanUpdate with exceptions ──
	// Build the PlanContext as if the reconciler rediscovered the exceptions.
	// The exceptions now have Detached state — handleExceptionUpdate should
	// compute the correct time-based state and transition them.
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "my-plan", Namespace: "default"},
	}

	// Use the detached copies to simulate what the reconciler would see.
	excActiveDetached := detachedExceptions["exc-active"]
	excPendingDetached := detachedExceptions["exc-pending"]
	excExpiredDetached := detachedExceptions["exc-expired"]

	planCtx := &message.PlanContext{
		Plan: plan,
		Exceptions: []hibernatorv1alpha1.ScheduleException{
			*excActiveDetached,
			*excPendingDetached,
			*excExpiredDetached,
		},
	}

	p.handlePlanUpdate(context.Background(), logr.Discard(), planKey, planCtx, errChan)

	select {
	case err := <-errChan:
		t.Fatalf("unexpected error during plan re-creation: %v", err)
	default:
	}

	// Collect exception status updates from the re-attachment.
	// 3 transitions (Detached→Active, Detached→Pending, Detached→Expired) + 1 plan status update for refs.
	reattached := make(map[string]hibernatorv1alpha1.ExceptionState)
	reattachedResources := make(map[string]*hibernatorv1alpha1.ScheduleException)
	for excUpdater.Len() > 0 {
		upd := <-excUpdater.C()
		reattached[upd.NamespacedName.Name] = upd.Resource.Status.State
		reattachedResources[upd.NamespacedName.Name] = upd.Resource
	}

	// Verify each exception returned to its correct time-based state.
	assert.Equal(t, hibernatorv1alpha1.ExceptionStateActive, reattached["exc-active"],
		"exc-active should re-attach as Active (within time window)")
	assert.Equal(t, hibernatorv1alpha1.ExceptionStatePending, reattached["exc-pending"],
		"exc-pending should re-attach as Pending (future time window)")
	assert.Equal(t, hibernatorv1alpha1.ExceptionStateExpired, reattached["exc-expired"],
		"exc-expired should re-attach as Expired (past time window)")

	// Verify DetachedAt is cleared for re-attached exceptions.
	assert.Nil(t, reattachedResources["exc-active"].Status.DetachedAt,
		"DetachedAt should be nil after re-attachment to Active")
	assert.Nil(t, reattachedResources["exc-pending"].Status.DetachedAt,
		"DetachedAt should be nil after re-attachment to Pending")
	assert.Nil(t, reattachedResources["exc-expired"].Status.DetachedAt,
		"DetachedAt should be nil after re-attachment to Expired")
}

func TestReattachment_AlreadyCorrectState_NoRedundantTransition(t *testing.T) {
	// Verify that if a previously-detached exception happens to be in the correct
	// time-based state already is not the case — Detached is never equal to a
	// time-based state, so computeDesiredState always triggers a transition.
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	fakeClock := clocktesting.NewFakeClock(now)

	exc := baseScheduleException("exc-reattach", "my-plan")
	exc.Spec.Type = "suspend"
	exc.Spec.ValidFrom = metav1.Time{Time: now.Add(-1 * time.Hour)}
	exc.Spec.ValidUntil = metav1.Time{Time: now.Add(2 * time.Hour)}
	exc.Labels = map[string]string{wellknown.LabelPlan: "my-plan"}
	exc.Finalizers = []string{wellknown.ExceptionFinalizerName}
	// Simulate a Detached exception (plan was previously deleted)
	exc.Status.State = hibernatorv1alpha1.ExceptionStateDetached
	exc.Status.DetachedAt = &metav1.Time{Time: now.Add(-30 * time.Minute)}

	p, statuses := newTestProcessor(t, exc)
	p.Clock = fakeClock

	key := types.NamespacedName{Name: "exc-reattach", Namespace: "default"}
	errChan := make(chan error, 4)

	// Simulate plan update that includes this detached exception.
	p.handleExceptionUpdate(context.Background(), logr.Discard(), key, exc, errChan)

	select {
	case err := <-errChan:
		t.Fatalf("unexpected error: %v", err)
	default:
	}

	excUpdater := statuses.ExceptionStatuses.(*captureUpdater[*hibernatorv1alpha1.ScheduleException])
	require.Equal(t, 1, excUpdater.Len(), "should queue exactly one transition from Detached to Active")

	upd := <-excUpdater.C()
	assert.Equal(t, hibernatorv1alpha1.ExceptionStateActive, upd.Resource.Status.State)
	assert.NotNil(t, upd.Resource.Status.AppliedAt, "AppliedAt should be set for Active state")
	assert.Nil(t, upd.Resource.Status.DetachedAt, "DetachedAt should be cleared")
}

func TestUpdateExceptionReferences_BuildsSortedRefs(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "plan-a", Namespace: "default"},
	}

	// Active exception (validFrom in the past, validUntil in the future)
	activeExc := hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "exc-active", Namespace: "default"},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       "suspend",
			ValidFrom:  metav1.Time{Time: now.Add(-1 * time.Hour)},
			ValidUntil: metav1.Time{Time: now.Add(2 * time.Hour)},
		},
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{
			State:     hibernatorv1alpha1.ExceptionStateActive,
			AppliedAt: &metav1.Time{Time: now.Add(-1 * time.Hour)},
		},
	}

	// Pending exception (validFrom in the future)
	pendingExc := hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "exc-pending", Namespace: "default"},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       "extend",
			ValidFrom:  metav1.Time{Time: now.Add(5 * time.Hour)},
			ValidUntil: metav1.Time{Time: now.Add(10 * time.Hour)},
		},
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{
			State: hibernatorv1alpha1.ExceptionStatePending,
		},
	}

	// Expired exception (validUntil in the past)
	expiredExc := hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "exc-expired", Namespace: "default"},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       "suspend",
			ValidFrom:  metav1.Time{Time: now.Add(-5 * time.Hour)},
			ValidUntil: metav1.Time{Time: now.Add(-1 * time.Hour)},
		},
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{
			State: hibernatorv1alpha1.ExceptionStateExpired,
		},
	}

	planCtx := &message.PlanContext{
		Plan:       plan,
		Exceptions: []hibernatorv1alpha1.ScheduleException{expiredExc, pendingExc, activeExc},
	}
	key := types.NamespacedName{Name: plan.Name, Namespace: plan.Namespace}
	p, statuses := newTestProcessor(t)
	p.updateExceptionReferences(logr.Discard(), key, plan, planCtx.Exceptions)

	planUpdater := statuses.PlanStatuses.(*captureUpdater[*hibernatorv1alpha1.HibernatePlan])
	require.Equal(t, 1, planUpdater.Len(), "should queue exactly one plan status update")

	upd := <-planUpdater.C()
	refs := upd.Resource.Status.ExceptionReferences
	require.Len(t, refs, 3)

	// Order: Active > Pending > Expired
	assert.Equal(t, "exc-active", refs[0].Name)
	assert.Equal(t, "exc-pending", refs[1].Name)
	assert.Equal(t, "exc-expired", refs[2].Name)
}

func TestUpdateExceptionReferences_SkipsDeletingExceptions(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	nowMeta := metav1.Now()
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "plan-a", Namespace: "default"},
	}

	deletingExc := hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "exc-deleting",
			Namespace:         "default",
			DeletionTimestamp: &nowMeta,
			Finalizers:        []string{wellknown.ExceptionFinalizerName},
		},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       "suspend",
			ValidFrom:  metav1.Time{Time: now.Add(-1 * time.Hour)},
			ValidUntil: metav1.Time{Time: now.Add(2 * time.Hour)},
		},
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{
			State: hibernatorv1alpha1.ExceptionStateActive,
		},
	}

	activeExc := hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "exc-active", Namespace: "default"},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       "extend",
			ValidFrom:  metav1.Time{Time: now.Add(-1 * time.Hour)},
			ValidUntil: metav1.Time{Time: now.Add(2 * time.Hour)},
		},
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{
			State: hibernatorv1alpha1.ExceptionStateActive,
		},
	}

	planCtx := &message.PlanContext{
		Plan:       plan,
		Exceptions: []hibernatorv1alpha1.ScheduleException{deletingExc, activeExc},
	}
	key := types.NamespacedName{Name: plan.Name, Namespace: plan.Namespace}
	p, statuses := newTestProcessor(t)
	p.updateExceptionReferences(logr.Discard(), key, planCtx.Plan, planCtx.Exceptions)

	planUpdater := statuses.PlanStatuses.(*captureUpdater[*hibernatorv1alpha1.HibernatePlan])
	require.Equal(t, 1, planUpdater.Len())

	upd := <-planUpdater.C()
	refs := upd.Resource.Status.ExceptionReferences
	require.Len(t, refs, 1, "deleting exception should be excluded")
	assert.Equal(t, "exc-active", refs[0].Name)
}

func TestUpdateExceptionReferences_NoChange_SkipsUpdate(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	appliedAt := &metav1.Time{Time: now.Add(-1 * time.Hour)}

	existingRefs := []hibernatorv1alpha1.ExceptionReference{
		{
			Name:       "exc-active",
			Type:       "suspend",
			ValidFrom:  metav1.Time{Time: now.Add(-1 * time.Hour)},
			ValidUntil: metav1.Time{Time: now.Add(2 * time.Hour)},
			State:      hibernatorv1alpha1.ExceptionStateActive,
			AppliedAt:  appliedAt,
		},
	}

	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "plan-a", Namespace: "default"},
		Status:     hibernatorv1alpha1.HibernatePlanStatus{ExceptionReferences: existingRefs},
	}

	exc := hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "exc-active", Namespace: "default"},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       "suspend",
			ValidFrom:  metav1.Time{Time: now.Add(-1 * time.Hour)},
			ValidUntil: metav1.Time{Time: now.Add(2 * time.Hour)},
		},
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{
			State:     hibernatorv1alpha1.ExceptionStateActive,
			AppliedAt: appliedAt,
		},
	}

	planCtx := &message.PlanContext{
		Plan:       plan,
		Exceptions: []hibernatorv1alpha1.ScheduleException{exc},
	}
	key := types.NamespacedName{Name: plan.Name, Namespace: plan.Namespace}

	p, statuses := newTestProcessor(t)
	p.updateExceptionReferences(logr.Discard(), key, planCtx.Plan, planCtx.Exceptions)

	planUpdater := statuses.PlanStatuses.(*captureUpdater[*hibernatorv1alpha1.HibernatePlan])
	assert.Equal(t, 0, planUpdater.Len(), "should not queue update when references unchanged")
}

func TestUpdateExceptionReferences_CapsAt10(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "plan-a", Namespace: "default"},
	}

	exceptions := make([]hibernatorv1alpha1.ScheduleException, 15)
	for i := range exceptions {
		exceptions[i] = hibernatorv1alpha1.ScheduleException{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("exc-%02d", i),
				Namespace: "default",
			},
			Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
				Type:       "suspend",
				ValidFrom:  metav1.Time{Time: now.Add(time.Duration(-i) * time.Hour)},
				ValidUntil: metav1.Time{Time: now.Add(time.Duration(15-i) * time.Hour)},
			},
			Status: hibernatorv1alpha1.ScheduleExceptionStatus{
				State: hibernatorv1alpha1.ExceptionStateActive,
			},
		}
	}

	planCtx := &message.PlanContext{
		Plan:       plan,
		Exceptions: exceptions,
	}
	key := types.NamespacedName{Name: plan.Name, Namespace: plan.Namespace}

	p, statuses := newTestProcessor(t)
	p.updateExceptionReferences(logr.Discard(), key, planCtx.Plan, planCtx.Exceptions)

	planUpdater := statuses.PlanStatuses.(*captureUpdater[*hibernatorv1alpha1.HibernatePlan])
	require.Equal(t, 1, planUpdater.Len())

	upd := <-planUpdater.C()
	assert.Len(t, upd.Resource.Status.ExceptionReferences, 10, "should cap at 10 references")
}

func TestUpdateExceptionReferences_NoExceptions_ClearsRefs(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "plan-a", Namespace: "default"},
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			ExceptionReferences: []hibernatorv1alpha1.ExceptionReference{
				{
					Name:       "old-exc",
					Type:       "suspend",
					ValidFrom:  metav1.Time{Time: now.Add(-5 * time.Hour)},
					ValidUntil: metav1.Time{Time: now.Add(-1 * time.Hour)},
					State:      hibernatorv1alpha1.ExceptionStateExpired,
				},
			},
		},
	}

	planCtx := &message.PlanContext{
		Plan:       plan,
		Exceptions: nil,
	}
	key := types.NamespacedName{Name: plan.Name, Namespace: plan.Namespace}

	p, statuses := newTestProcessor(t)
	p.updateExceptionReferences(logr.Discard(), key, planCtx.Plan, planCtx.Exceptions)

	planUpdater := statuses.PlanStatuses.(*captureUpdater[*hibernatorv1alpha1.HibernatePlan])
	require.Equal(t, 1, planUpdater.Len(), "should queue update to clear stale references")

	upd := <-planUpdater.C()
	assert.Empty(t, upd.Resource.Status.ExceptionReferences)
}
