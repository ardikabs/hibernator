/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/telepresenceio/watchable"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/internal/notification"
	statusprocessor "github.com/ardikabs/hibernator/internal/provider/processor/status"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// captureUpdater implements Updater[T] for testing. It applies the mutator
// to the Resource (mirroring defaultUpdater) and buffers the update.
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

func newNotificationScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = hibernatorv1alpha1.AddToScheme(s)
	return s
}

func newTestProcessor(t *testing.T, objs ...client.Object) (*LifecycleProcessor, *captureUpdater[*hibernatorv1alpha1.HibernateNotification]) {
	t.Helper()
	scheme := newNotificationScheme()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&hibernatorv1alpha1.HibernateNotification{}).
		WithObjects(objs...).
		Build()

	notifUpdater := newCaptureUpdater[*hibernatorv1alpha1.HibernateNotification](64)
	p := &LifecycleProcessor{
		Client:    c,
		Clock:     clocktesting.NewFakeClock(time.Now()),
		Log:       logr.Discard(),
		Resources: &message.ControllerResources{},
		Statuses: &statusprocessor.ControllerStatuses{
			NotificationStatuses: notifUpdater,
		},
	}
	return p, notifUpdater
}

func baseNotification(name string, sinks ...string) *hibernatorv1alpha1.HibernateNotification {
	notifSinks := make([]hibernatorv1alpha1.NotificationSink, len(sinks))
	for i, s := range sinks {
		notifSinks[i] = hibernatorv1alpha1.NotificationSink{
			Name:      s,
			Type:      hibernatorv1alpha1.SinkSlack,
			SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "secret"},
		}
	}
	return &hibernatorv1alpha1.HibernateNotification{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  "default",
			Generation: 1,
		},
		Spec: hibernatorv1alpha1.HibernateNotificationSpec{
			Selector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "test"}},
			OnEvents: []hibernatorv1alpha1.NotificationEvent{hibernatorv1alpha1.EventStart},
			Sinks:    notifSinks,
		},
	}
}

// ---------------------------------------------------------------------------
// upsertPlanRef
// ---------------------------------------------------------------------------

func TestUpsertPlanRef_AddsNewPlan(t *testing.T) {
	p, updater := newTestProcessor(t)

	notif := baseNotification("my-notif", "slack-prod")
	notifKey := types.NamespacedName{Name: "my-notif", Namespace: "default"}
	planKey := types.NamespacedName{Name: "plan-a", Namespace: "default"}
	bindingKey := lo.Must(message.NewNotificationBindingKey(notifKey, planKey))
	p.upsertPlanRef(logr.Discard(), bindingKey, notif)

	require.Equal(t, 1, updater.Len())
	upd := <-updater.C()
	assert.Equal(t, []hibernatorv1alpha1.PlanReference{{Name: "plan-a"}}, upd.Resource.Status.WatchedPlans)
	assert.Equal(t, int64(1), upd.Resource.Status.ObservedGeneration)
	assert.Equal(t, hibernatorv1alpha1.NotificationStateBound, upd.Resource.Status.State)
}

func TestUpsertPlanRef_MultiplePlans(t *testing.T) {
	p, updater := newTestProcessor(t)

	notif := baseNotification("my-notif", "slack-prod")
	notifKey := types.NamespacedName{Name: "my-notif", Namespace: "default"}
	planKey := types.NamespacedName{Name: "plan-a", Namespace: "default"}
	bindingKey := lo.Must(message.NewNotificationBindingKey(notifKey, planKey))

	p.upsertPlanRef(logr.Discard(), bindingKey, notif)
	require.Equal(t, 1, updater.Len())
	upd := <-updater.C()
	assert.Equal(t, []hibernatorv1alpha1.PlanReference{{Name: "plan-a"}}, upd.Resource.Status.WatchedPlans)

	planKey2 := types.NamespacedName{Name: "plan-b", Namespace: "default"}
	bindingKey2 := lo.Must(message.NewNotificationBindingKey(notifKey, planKey2))

	p.upsertPlanRef(logr.Discard(), bindingKey2, notif)
	require.Equal(t, 1, updater.Len())
	upd = <-updater.C()
	assert.Equal(t, []hibernatorv1alpha1.PlanReference{{Name: "plan-a"}, {Name: "plan-b"}}, upd.Resource.Status.WatchedPlans)
}

func TestUpsertPlanRef_SkipsWhenAlreadyTracked(t *testing.T) {
	p, updater := newTestProcessor(t)

	notif := baseNotification("my-notif", "slack-prod")
	notif.Status.WatchedPlans = []hibernatorv1alpha1.PlanReference{{Name: "plan-a"}}
	notif.Status.ObservedGeneration = 1
	notif.Status.State = hibernatorv1alpha1.NotificationStateBound
	notifKey := types.NamespacedName{Name: "my-notif", Namespace: "default"}
	planKey := types.NamespacedName{Name: "plan-a", Namespace: "default"}
	bindingKey := lo.Must(message.NewNotificationBindingKey(notifKey, planKey))

	p.upsertPlanRef(logr.Discard(), bindingKey, notif)

	assert.Equal(t, 0, updater.Len(), "should skip when plan already tracked and generation unchanged")
}

func TestUpsertPlanRef_UpdatesWhenGenerationChanged(t *testing.T) {
	p, updater := newTestProcessor(t)

	notif := baseNotification("my-notif", "slack-prod")
	notif.Status.WatchedPlans = []hibernatorv1alpha1.PlanReference{{Name: "plan-a"}}
	notif.Status.ObservedGeneration = 0
	notifKey := types.NamespacedName{Name: "my-notif", Namespace: "default"}
	planKey := types.NamespacedName{Name: "plan-a", Namespace: "default"}
	bindingKey := lo.Must(message.NewNotificationBindingKey(notifKey, planKey))

	p.upsertPlanRef(logr.Discard(), bindingKey, notif)

	require.Equal(t, 1, updater.Len(), "should update when generation changed")
	upd := <-updater.C()
	assert.Equal(t, int64(1), upd.Resource.Status.ObservedGeneration)
}

// ---------------------------------------------------------------------------
// HandleDeliveryResult
// ---------------------------------------------------------------------------

func TestHandleDeliveryResult_Success(t *testing.T) {
	p, updater := newTestProcessor(t)

	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	p.HandleDeliveryResult(notification.DeliveryResult{
		NotificationRef: types.NamespacedName{Name: "my-notif", Namespace: "default"},
		SinkName:        "slack-prod",
		PlanNamespace:   "default",
		PlanName:        "my-plan",
		CycleID:         "cycle-001",
		Operation:       "shutdown",
		Timestamp:       now,
		Success:         true,
	})

	require.Equal(t, 1, updater.Len())
	upd := <-updater.C()

	// Root-level timestamp updated.
	require.NotNil(t, upd.Resource.Status.LastDeliveryTime)
	assert.True(t, upd.Resource.Status.LastDeliveryTime.Equal(&metav1.Time{Time: now}))
	assert.Nil(t, upd.Resource.Status.LastFailureTime)

	require.Len(t, upd.Resource.Status.SinkStatuses, 1)
	var ss hibernatorv1alpha1.NotificationSinkStatus
	for _, v := range upd.Resource.Status.SinkStatuses {
		ss = v
		break
	}
	assert.Equal(t, "slack-prod", ss.SinkName)
	assert.Equal(t, "default", ss.PlanRef.Namespace)
	assert.Equal(t, "my-plan", ss.PlanRef.Name)
	assert.Equal(t, "cycle-001", ss.CycleID)
	assert.Equal(t, "shutdown", ss.Operation)
	assert.True(t, ss.Success)
	assert.Equal(t, "Successfully sent notification for slack-prod", ss.Message)
	assert.True(t, ss.TransitionTimestamp.Equal(&metav1.Time{Time: now}))
	assert.Nil(t, ss.States)
	assert.EqualValues(t, 1, ss.SuccessCount)
	assert.EqualValues(t, 0, ss.FailureCount)
}

func TestHandleDeliveryResult_PersistsMetadata(t *testing.T) {
	p, updater := newTestProcessor(t)

	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	p.HandleDeliveryResult(notification.DeliveryResult{
		NotificationRef: types.NamespacedName{Name: "my-notif", Namespace: "default"},
		SinkName:        "slack-prod",
		PlanNamespace:   "default",
		PlanName:        "my-plan",
		CycleID:         "cycle-001",
		Operation:       "shutdown",
		Timestamp:       now,
		Success:         true,
		States: map[string]string{
			"slack.thread.root_ts": "12345.67890",
		},
	})

	require.Equal(t, 1, updater.Len())
	upd := <-updater.C()
	require.Len(t, upd.Resource.Status.SinkStatuses, 1)
	for _, ss := range upd.Resource.Status.SinkStatuses {
		require.NotNil(t, ss.States)
		assert.Equal(t, "12345.67890", ss.States["slack.thread.root_ts"])
	}
}

func TestHandleDeliveryResult_Failure(t *testing.T) {
	p, updater := newTestProcessor(t)

	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	p.HandleDeliveryResult(notification.DeliveryResult{
		NotificationRef: types.NamespacedName{Name: "my-notif", Namespace: "default"},
		SinkName:        "slack-prod",
		PlanNamespace:   "default",
		PlanName:        "my-plan",
		CycleID:         "cycle-001",
		Operation:       "shutdown",
		Timestamp:       now,
		Success:         false,
		Error:           errors.New("connection refused"),
	})

	require.Equal(t, 1, updater.Len())
	upd := <-updater.C()

	// Root-level timestamp updated.
	assert.Nil(t, upd.Resource.Status.LastDeliveryTime)
	require.NotNil(t, upd.Resource.Status.LastFailureTime)
	assert.True(t, upd.Resource.Status.LastFailureTime.Equal(&metav1.Time{Time: now}))

	require.Len(t, upd.Resource.Status.SinkStatuses, 1)
	var ss hibernatorv1alpha1.NotificationSinkStatus
	for _, v := range upd.Resource.Status.SinkStatuses {
		ss = v
		break
	}
	assert.Equal(t, "slack-prod", ss.SinkName)
	assert.Equal(t, "default", ss.PlanRef.Namespace)
	assert.Equal(t, "my-plan", ss.PlanRef.Name)
	assert.Equal(t, "cycle-001", ss.CycleID)
	assert.Equal(t, "shutdown", ss.Operation)
	assert.False(t, ss.Success)
	assert.Equal(t, "connection refused", ss.Message)
}

func TestHandleDeliveryResult_FailureWithNilError(t *testing.T) {
	p, updater := newTestProcessor(t)

	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	p.HandleDeliveryResult(notification.DeliveryResult{
		NotificationRef: types.NamespacedName{Name: "my-notif", Namespace: "default"},
		SinkName:        "slack-prod",
		PlanNamespace:   "default",
		PlanName:        "my-plan",
		CycleID:         "cycle-001",
		Operation:       "shutdown",
		Timestamp:       now,
		Success:         false,
	})

	require.Equal(t, 1, updater.Len())
	upd := <-updater.C()

	for _, ss := range upd.Resource.Status.SinkStatuses {
		assert.False(t, ss.Success)
		assert.Equal(t, "Failed to send notification for slack-prod", ss.Message, "message should use fallback when error is nil")
	}
}

func TestHandleDeliveryResult_SkipsEmptyRef(t *testing.T) {
	p, updater := newTestProcessor(t)

	p.HandleDeliveryResult(notification.DeliveryResult{
		SinkName:  "slack-prod",
		Timestamp: time.Now(),
		Success:   true,
	})

	assert.Equal(t, 0, updater.Len(), "should skip when NotificationRef is empty")
}

func TestHandleDeliveryResult_NewestFirst(t *testing.T) {
	p, updater := newTestProcessor(t)

	ref := types.NamespacedName{Name: "notif", Namespace: "default"}
	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 1, 11, 0, 0, 0, time.UTC)

	p.HandleDeliveryResult(notification.DeliveryResult{
		NotificationRef: ref, SinkName: "slack", PlanNamespace: "default", PlanName: "plan-a", CycleID: "cycle-001", Operation: "shutdown", Timestamp: t1, Success: true,
	})
	p.HandleDeliveryResult(notification.DeliveryResult{
		NotificationRef: ref, SinkName: "telegram", PlanNamespace: "default", PlanName: "plan-a", CycleID: "cycle-001", Operation: "shutdown", Timestamp: t2, Success: false,
		Error: errors.New("timeout"),
	})

	require.Equal(t, 2, updater.Len())

	// Simulate pool FIFO: apply sequentially to a shared notification.
	notif := &hibernatorv1alpha1.HibernateNotification{}
	for i := 0; i < 2; i++ {
		upd := <-updater.C()
		upd.Mutator.Mutate(notif)
	}

	require.Len(t, notif.Status.SinkStatuses, 2)
	var sinkNames []string
	for _, ss := range notif.Status.SinkStatuses {
		sinkNames = append(sinkNames, ss.SinkName)
	}
	assert.ElementsMatch(t, []string{"telegram", "slack"}, sinkNames)
}

func TestHandleDeliveryResult_KeepsOnlyLast2CyclesPerSinkPlan(t *testing.T) {
	p, updater := newTestProcessor(t)

	ref := types.NamespacedName{Name: "notif", Namespace: "default"}
	base := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	const total = 5
	for i := 0; i < total; i++ {
		p.HandleDeliveryResult(notification.DeliveryResult{
			NotificationRef: ref,
			SinkName:        "sink-a",
			PlanNamespace:   "default",
			PlanName:        "plan-a",
			CycleID:         fmt.Sprintf("cycle-%02d", i),
			Operation:       "shutdown",
			Timestamp:       base.Add(time.Duration(i) * time.Minute),
			Success:         true,
		})
	}

	require.Equal(t, total, updater.Len())

	notif := &hibernatorv1alpha1.HibernateNotification{}
	for i := 0; i < total; i++ {
		upd := <-updater.C()
		upd.Mutator.Mutate(notif)
	}

	assert.Len(t, notif.Status.SinkStatuses, hibernatorv1alpha1.MaxSinkStatusCyclesPerPlan)
	var cycleIDs []string
	for _, ss := range notif.Status.SinkStatuses {
		cycleIDs = append(cycleIDs, ss.CycleID)
	}
	assert.ElementsMatch(t, []string{"cycle-04", "cycle-03"}, cycleIDs)
}

// ---------------------------------------------------------------------------
// handleBinding
// ---------------------------------------------------------------------------

func TestHandleBinding_MatchedBinding_UpsertsWatchedPlan(t *testing.T) {
	notif := baseNotification("my-notif", "slack-prod")
	p, updater := newTestProcessor(t, notif.DeepCopy())

	planKey := types.NamespacedName{Name: "plan-a", Namespace: "default"}

	p.handleBinding(context.Background(), logr.Discard(), watchable.Update[message.NotificationBindingKey, *message.NotificationContext]{
		Key: lo.Must(message.NewNotificationBindingKey(client.ObjectKeyFromObject(notif), planKey)),
		Value: &message.NotificationContext{
			Notification: notif,
			PlanKey:      planKey,
			Matches:      true,
		},
	})

	require.Equal(t, 1, updater.Len())
	upd := <-updater.C()
	assert.Equal(t, []hibernatorv1alpha1.PlanReference{{Name: "plan-a"}}, upd.Resource.Status.WatchedPlans)
}

func TestHandleBinding_UnmatchedBinding_RemovesWatchedPlan(t *testing.T) {
	p, updater := newTestProcessor(t)

	notif := baseNotification("my-notif", "slack-prod")
	notif.Status.WatchedPlans = []hibernatorv1alpha1.PlanReference{{Name: "plan-a"}}
	notif.Status.State = hibernatorv1alpha1.NotificationStateBound
	planKey := types.NamespacedName{Name: "plan-a", Namespace: "default"}

	p.handleBinding(context.Background(), logr.Discard(), watchable.Update[message.NotificationBindingKey, *message.NotificationContext]{
		Key: lo.Must(message.NewNotificationBindingKey(client.ObjectKeyFromObject(notif), planKey)),
		Value: &message.NotificationContext{
			Notification: notif,
			PlanKey:      planKey,
			Matches:      false,
		},
	})

	require.Equal(t, 1, updater.Len())
	upd := <-updater.C()
	assert.Empty(t, upd.Resource.Status.WatchedPlans)
	assert.Equal(t, hibernatorv1alpha1.NotificationStateDetached, upd.Resource.Status.State, "state should be Detached when watchedPlans becomes empty")
}

func TestHandleBinding_UnmatchedBinding_SkipsWhenNotTracked(t *testing.T) {
	p, updater := newTestProcessor(t)

	notif := baseNotification("my-notif", "slack-prod")
	// No watchedPlans — plan-a was never tracked.
	planKey := types.NamespacedName{Name: "plan-a", Namespace: "default"}

	p.handleBinding(context.Background(), logr.Discard(), watchable.Update[message.NotificationBindingKey, *message.NotificationContext]{
		Key: lo.Must(message.NewNotificationBindingKey(client.ObjectKeyFromObject(notif), planKey)),
		Value: &message.NotificationContext{
			Notification: notif,
			PlanKey:      planKey,
			Matches:      false,
		},
	})

	assert.Equal(t, 0, updater.Len(), "should skip removal when plan is not tracked")
}

func TestHandleBinding_DeleteEvent_RemovesWatchedPlan(t *testing.T) {
	p, updater := newTestProcessor(t)

	notif := baseNotification("my-notif", "slack-prod")
	notif.Status.WatchedPlans = []hibernatorv1alpha1.PlanReference{{Name: "plan-a"}, {Name: "other-plan"}}
	planKey := types.NamespacedName{Name: "plan-a", Namespace: "default"}
	notifKey := types.NamespacedName{Name: "my-notif", Namespace: "default"}
	bindingKey := lo.Must(message.NewNotificationBindingKey(notifKey, planKey))

	// Delete events are handled by Start() which calls removePlanRef directly.
	p.removePlanRef(context.Background(), logr.Discard(), bindingKey, notif)

	require.Equal(t, 1, updater.Len())
	upd := <-updater.C()
	assert.Equal(t, []hibernatorv1alpha1.PlanReference{{Name: "other-plan"}}, upd.Resource.Status.WatchedPlans)
}

func TestHandleBinding_DeleteEvent_NilValue_Skips(t *testing.T) {
	p, updater := newTestProcessor(t)

	p.handleBinding(context.Background(), logr.Discard(), watchable.Update[message.NotificationBindingKey, *message.NotificationContext]{
		Key:    lo.Must(message.NewNotificationBindingKey(types.NamespacedName{Namespace: "default", Name: "my-notif"}, types.NamespacedName{Namespace: "default", Name: "plan-a"})),
		Delete: true,
		Value:  nil,
	})

	assert.Equal(t, 0, updater.Len())
}

func TestHandleBinding_NilContext_Skips(t *testing.T) {
	p, updater := newTestProcessor(t)

	p.handleBinding(context.Background(), logr.Discard(), watchable.Update[message.NotificationBindingKey, *message.NotificationContext]{
		Key:   lo.Must(message.NewNotificationBindingKey(types.NamespacedName{Namespace: "default", Name: "my-notif"}, types.NamespacedName{Namespace: "default", Name: "plan-a"})),
		Value: nil,
	})

	assert.Equal(t, 0, updater.Len())
}

// ---------------------------------------------------------------------------
// ensureFinalizer
// ---------------------------------------------------------------------------

func TestEnsureFinalizer_AddsFinalizerWhenMissing(t *testing.T) {
	notif := baseNotification("my-notif", "slack-prod")
	p, _ := newTestProcessor(t, notif.DeepCopy())

	ctx := context.Background()
	notifKey := types.NamespacedName{Name: "my-notif", Namespace: "default"}

	p.ensureFinalizer(ctx, logr.Discard(), notifKey)

	// Verify finalizer was added by re-fetching from the fake client.
	got := new(hibernatorv1alpha1.HibernateNotification)
	require.NoError(t, p.Get(ctx, notifKey, got))
	assert.Contains(t, got.Finalizers, wellknown.NotificationFinalizerName)
}

func TestEnsureFinalizer_SkipsWhenAlreadyPresent(t *testing.T) {
	notif := baseNotification("my-notif", "slack-prod")
	notif.Finalizers = []string{wellknown.NotificationFinalizerName}
	p, _ := newTestProcessor(t, notif.DeepCopy())

	ctx := context.Background()
	notifKey := types.NamespacedName{Name: "my-notif", Namespace: "default"}

	p.ensureFinalizer(ctx, logr.Discard(), notifKey)

	got := new(hibernatorv1alpha1.HibernateNotification)
	require.NoError(t, p.Get(ctx, notifKey, got))
	assert.Equal(t, []string{wellknown.NotificationFinalizerName}, got.Finalizers)
}

// ---------------------------------------------------------------------------
// handleNotificationDeletion
// ---------------------------------------------------------------------------

func TestHandleNotificationDeletion_ClearsWatchedPlansAndRemovesFinalizer(t *testing.T) {
	now := metav1.Now()
	notif := baseNotification("my-notif", "slack-prod")
	notif.Finalizers = []string{wellknown.NotificationFinalizerName}
	notif.DeletionTimestamp = &now
	notif.Status.WatchedPlans = []hibernatorv1alpha1.PlanReference{{Name: "plan-a"}, {Name: "plan-b"}}

	p, updater := newTestProcessor(t, notif.DeepCopy())

	ctx := context.Background()
	notifKey := types.NamespacedName{Name: "my-notif", Namespace: "default"}

	p.handleNotificationDeletion(ctx, logr.Discard(), notifKey)

	// Verify watchedPlans clear was queued.
	require.Equal(t, 1, updater.Len())
	upd := <-updater.C()
	assert.Nil(t, upd.Resource.Status.WatchedPlans)

	// After removing the finalizer, the fake client deletes the object
	// (DeletionTimestamp + no finalizers → gone). Verify it's actually gone,
	// confirming the finalizer was removed and GC proceeded.
	got := new(hibernatorv1alpha1.HibernateNotification)
	err := p.Get(ctx, notifKey, got)
	assert.True(t, client.IgnoreNotFound(err) == nil, "notification should be gone after finalizer removal")
}

func TestHandleNotificationDeletion_AlreadyGone(t *testing.T) {
	// Notification doesn't exist — should not panic or produce errors.
	p, updater := newTestProcessor(t)

	ctx := context.Background()
	notifKey := types.NamespacedName{Name: "gone-notif", Namespace: "default"}

	p.handleNotificationDeletion(ctx, logr.Discard(), notifKey)

	assert.Equal(t, 0, updater.Len())
}

func TestHandleNotificationDeletion_NoFinalizer(t *testing.T) {
	now := metav1.Now()
	notif := baseNotification("my-notif", "slack-prod")
	notif.DeletionTimestamp = &now
	// A different finalizer so the fake client accepts DeletionTimestamp.
	notif.Finalizers = []string{"some-other-controller/finalizer"}
	notif.Status.WatchedPlans = []hibernatorv1alpha1.PlanReference{{Name: "plan-a"}}

	p, updater := newTestProcessor(t, notif.DeepCopy())

	ctx := context.Background()
	notifKey := types.NamespacedName{Name: "my-notif", Namespace: "default"}

	p.handleNotificationDeletion(ctx, logr.Discard(), notifKey)

	// WatchedPlans clear should still be queued.
	require.Equal(t, 1, updater.Len())
	upd := <-updater.C()
	assert.Nil(t, upd.Resource.Status.WatchedPlans)

	// Our finalizer wasn't present so the other controller's finalizer remains.
	got := new(hibernatorv1alpha1.HibernateNotification)
	require.NoError(t, p.Get(ctx, notifKey, got))
	assert.Equal(t, []string{"some-other-controller/finalizer"}, got.Finalizers)
}

func TestHandleBinding_DeletionTimestamp_TriggersNotificationDeletion(t *testing.T) {
	now := metav1.Now()
	notif := baseNotification("my-notif", "slack-prod")
	notif.Finalizers = []string{wellknown.NotificationFinalizerName}
	notif.DeletionTimestamp = &now
	notif.Status.WatchedPlans = []hibernatorv1alpha1.PlanReference{{Name: "plan-a"}}

	p, updater := newTestProcessor(t, notif.DeepCopy())

	planKey := types.NamespacedName{Name: "plan-a", Namespace: "default"}

	p.handleBinding(context.Background(), logr.Discard(), watchable.Update[message.NotificationBindingKey, *message.NotificationContext]{
		Key: lo.Must(message.NewNotificationBindingKey(client.ObjectKeyFromObject(notif), planKey)),
		Value: &message.NotificationContext{
			Notification: notif,
			PlanKey:      planKey,
			Matches:      true,
		},
	})

	// Should clear watchedPlans and remove finalizer instead of upserting.
	require.Equal(t, 1, updater.Len())
	upd := <-updater.C()
	assert.Nil(t, upd.Resource.Status.WatchedPlans)
	assert.Equal(t, hibernatorv1alpha1.NotificationStateDetached, upd.Resource.Status.State)

	// After removing the finalizer, the fake client deletes the object
	// (DeletionTimestamp + no finalizers → gone).
	got := new(hibernatorv1alpha1.HibernateNotification)
	err := p.Get(context.Background(), types.NamespacedName{Name: "my-notif", Namespace: "default"}, got)
	assert.True(t, client.IgnoreNotFound(err) == nil, "notification should be gone after finalizer removal")
}

// ---------------------------------------------------------------------------
// State transitions: Bound → Detached → Bound
// ---------------------------------------------------------------------------

func TestRemovePlanRef_LastPlan_TransitionsToDetachedAndRemovesFinalizer(t *testing.T) {
	notif := baseNotification("my-notif", "slack-prod")
	notif.Finalizers = []string{wellknown.NotificationFinalizerName}
	notif.Status.WatchedPlans = []hibernatorv1alpha1.PlanReference{{Name: "plan-a"}}
	notif.Status.State = hibernatorv1alpha1.NotificationStateBound

	p, updater := newTestProcessor(t, notif.DeepCopy())

	ctx := context.Background()
	notifKey := types.NamespacedName{Name: "my-notif", Namespace: "default"}
	planKey := types.NamespacedName{Name: "plan-a", Namespace: "default"}
	bindingKey := lo.Must(message.NewNotificationBindingKey(notifKey, planKey))
	p.removePlanRef(ctx, logr.Discard(), bindingKey, notif)

	// Status update: Detached + empty watchedPlans.
	require.Equal(t, 1, updater.Len())
	upd := <-updater.C()
	assert.Empty(t, upd.Resource.Status.WatchedPlans)
	assert.Equal(t, hibernatorv1alpha1.NotificationStateDetached, upd.Resource.Status.State)

	// Finalizer should have been removed.
	got := new(hibernatorv1alpha1.HibernateNotification)
	require.NoError(t, p.Get(ctx, notifKey, got))
	assert.NotContains(t, got.Finalizers, wellknown.NotificationFinalizerName)
}

func TestRemovePlanRef_NotLastPlan_StaysBound(t *testing.T) {
	notif := baseNotification("my-notif", "slack-prod")
	notif.Finalizers = []string{wellknown.NotificationFinalizerName}
	notif.Status.WatchedPlans = []hibernatorv1alpha1.PlanReference{{Name: "plan-a"}, {Name: "plan-b"}}
	notif.Status.State = hibernatorv1alpha1.NotificationStateBound

	p, updater := newTestProcessor(t, notif.DeepCopy())

	ctx := context.Background()
	notifKey := types.NamespacedName{Name: "my-notif", Namespace: "default"}
	planKey := types.NamespacedName{Name: "plan-a", Namespace: "default"}
	bindingKey := lo.Must(message.NewNotificationBindingKey(notifKey, planKey))

	p.removePlanRef(ctx, logr.Discard(), bindingKey, notif)

	// Status update: plan-b remains.
	require.Equal(t, 1, updater.Len())
	upd := <-updater.C()
	assert.Equal(t, []hibernatorv1alpha1.PlanReference{{Name: "plan-b"}}, upd.Resource.Status.WatchedPlans)
	// State not changed by the mutator (plan-b still exists).

	// Finalizer should still be present.
	got := new(hibernatorv1alpha1.HibernateNotification)
	require.NoError(t, p.Get(ctx, notifKey, got))
	assert.Contains(t, got.Finalizers, wellknown.NotificationFinalizerName)
}

func TestRemoveFinalizer_RemovesWhenPresent(t *testing.T) {
	notif := baseNotification("my-notif", "slack-prod")
	notif.Finalizers = []string{wellknown.NotificationFinalizerName}

	p, _ := newTestProcessor(t, notif.DeepCopy())

	ctx := context.Background()
	notifKey := types.NamespacedName{Name: "my-notif", Namespace: "default"}

	p.removeFinalizer(ctx, logr.Discard(), notifKey)

	got := new(hibernatorv1alpha1.HibernateNotification)
	require.NoError(t, p.Get(ctx, notifKey, got))
	assert.NotContains(t, got.Finalizers, wellknown.NotificationFinalizerName)
}

func TestRemoveFinalizer_SkipsWhenAbsent(t *testing.T) {
	notif := baseNotification("my-notif", "slack-prod")
	// No finalizer set.

	p, _ := newTestProcessor(t, notif.DeepCopy())

	ctx := context.Background()
	notifKey := types.NamespacedName{Name: "my-notif", Namespace: "default"}

	p.removeFinalizer(ctx, logr.Discard(), notifKey)

	got := new(hibernatorv1alpha1.HibernateNotification)
	require.NoError(t, p.Get(ctx, notifKey, got))
	assert.Empty(t, got.Finalizers)
}

func TestRemoveFinalizer_NotFoundIsNoop(t *testing.T) {
	p, _ := newTestProcessor(t)

	// Should not panic when the notification doesn't exist.
	p.removeFinalizer(context.Background(), logr.Discard(), types.NamespacedName{Name: "gone", Namespace: "default"})
}
