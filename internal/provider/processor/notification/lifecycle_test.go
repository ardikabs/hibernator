/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/internal/notification"
	statusprocessor "github.com/ardikabs/hibernator/internal/provider/processor/status"
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

func newTestProcessor(t *testing.T) (*LifecycleProcessor, *captureUpdater[*hibernatorv1alpha1.HibernateNotification]) {
	t.Helper()
	notifUpdater := newCaptureUpdater[*hibernatorv1alpha1.HibernateNotification](64)
	p := &LifecycleProcessor{
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

	p.upsertPlanRef(logr.Discard(), notifKey, notif, planKey)

	require.Equal(t, 1, updater.Len())
	upd := <-updater.C()
	assert.Equal(t, []hibernatorv1alpha1.PlanReference{{Name: "plan-a"}}, upd.Resource.Status.WatchedPlans)
	assert.Equal(t, int64(1), upd.Resource.Status.ObservedGeneration)
}

func TestUpsertPlanRef_MultiplePlans(t *testing.T) {
	p, updater := newTestProcessor(t)

	notif := baseNotification("my-notif", "slack-prod")
	notifKey := types.NamespacedName{Name: "my-notif", Namespace: "default"}

	p.upsertPlanRef(logr.Discard(), notifKey, notif, types.NamespacedName{Name: "plan-b", Namespace: "default"})
	require.Equal(t, 1, updater.Len())
	upd := <-updater.C()
	assert.Equal(t, []hibernatorv1alpha1.PlanReference{{Name: "plan-b"}}, upd.Resource.Status.WatchedPlans)

	p.upsertPlanRef(logr.Discard(), notifKey, notif, types.NamespacedName{Name: "plan-a", Namespace: "default"})
	require.Equal(t, 1, updater.Len())
	upd = <-updater.C()
	assert.Equal(t, []hibernatorv1alpha1.PlanReference{{Name: "plan-a"}, {Name: "plan-b"}}, upd.Resource.Status.WatchedPlans)
}

func TestUpsertPlanRef_SkipsWhenAlreadyTracked(t *testing.T) {
	p, updater := newTestProcessor(t)

	notif := baseNotification("my-notif", "slack-prod")
	notif.Status.WatchedPlans = []hibernatorv1alpha1.PlanReference{{Name: "plan-a"}}
	notif.Status.ObservedGeneration = 1
	notifKey := types.NamespacedName{Name: "my-notif", Namespace: "default"}

	p.upsertPlanRef(logr.Discard(), notifKey, notif, types.NamespacedName{Name: "plan-a", Namespace: "default"})

	assert.Equal(t, 0, updater.Len(), "should skip when plan already tracked and generation unchanged")
}

func TestUpsertPlanRef_UpdatesWhenGenerationChanged(t *testing.T) {
	p, updater := newTestProcessor(t)

	notif := baseNotification("my-notif", "slack-prod")
	notif.Status.WatchedPlans = []hibernatorv1alpha1.PlanReference{{Name: "plan-a"}}
	notif.Status.ObservedGeneration = 0
	notifKey := types.NamespacedName{Name: "my-notif", Namespace: "default"}

	p.upsertPlanRef(logr.Discard(), notifKey, notif, types.NamespacedName{Name: "plan-a", Namespace: "default"})

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
		Timestamp:       now,
		Success:         true,
	})

	require.Equal(t, 1, updater.Len())
	upd := <-updater.C()

	// Root-level timestamp updated.
	require.NotNil(t, upd.Resource.Status.LastDeliveryTime)
	assert.True(t, upd.Resource.Status.LastDeliveryTime.Equal(&metav1.Time{Time: now}))
	assert.Nil(t, upd.Resource.Status.LastFailureTime)

	// History entry prepended.
	require.Len(t, upd.Resource.Status.SinkStatuses, 1)
	ss := upd.Resource.Status.SinkStatuses[0]
	assert.Equal(t, "slack-prod", ss.Name)
	assert.True(t, ss.Success)
	assert.Equal(t, "Successfully sent notification for slack-prod", ss.Message)
	assert.True(t, ss.TransitionTimestamp.Equal(&metav1.Time{Time: now}))
}

func TestHandleDeliveryResult_Failure(t *testing.T) {
	p, updater := newTestProcessor(t)

	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	p.HandleDeliveryResult(notification.DeliveryResult{
		NotificationRef: types.NamespacedName{Name: "my-notif", Namespace: "default"},
		SinkName:        "slack-prod",
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

	// History entry.
	require.Len(t, upd.Resource.Status.SinkStatuses, 1)
	ss := upd.Resource.Status.SinkStatuses[0]
	assert.Equal(t, "slack-prod", ss.Name)
	assert.False(t, ss.Success)
	assert.Equal(t, "connection refused", ss.Message)
}

func TestHandleDeliveryResult_FailureWithNilError(t *testing.T) {
	p, updater := newTestProcessor(t)

	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	p.HandleDeliveryResult(notification.DeliveryResult{
		NotificationRef: types.NamespacedName{Name: "my-notif", Namespace: "default"},
		SinkName:        "slack-prod",
		Timestamp:       now,
		Success:         false,
	})

	require.Equal(t, 1, updater.Len())
	upd := <-updater.C()

	ss := upd.Resource.Status.SinkStatuses[0]
	assert.False(t, ss.Success)
	assert.Equal(t, "Failed to send notification for slack-prod", ss.Message, "message should use fallback when error is nil")
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
		NotificationRef: ref, SinkName: "slack", Timestamp: t1, Success: true,
	})
	p.HandleDeliveryResult(notification.DeliveryResult{
		NotificationRef: ref, SinkName: "telegram", Timestamp: t2, Success: false,
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
	// Newest (telegram @ t2) should be at index 0.
	assert.Equal(t, "telegram", notif.Status.SinkStatuses[0].Name)
	assert.False(t, notif.Status.SinkStatuses[0].Success)
	assert.Equal(t, "slack", notif.Status.SinkStatuses[1].Name)
	assert.True(t, notif.Status.SinkStatuses[1].Success)
}

func TestHandleDeliveryResult_CapsAtMaxHistory(t *testing.T) {
	p, updater := newTestProcessor(t)

	ref := types.NamespacedName{Name: "notif", Namespace: "default"}
	base := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	const total = 25
	for i := 0; i < total; i++ {
		p.HandleDeliveryResult(notification.DeliveryResult{
			NotificationRef: ref,
			SinkName:        fmt.Sprintf("sink-%02d", i),
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

	assert.Len(t, notif.Status.SinkStatuses, hibernatorv1alpha1.MaxSinkStatusHistory)
	// The newest entry (sink-24) should be at index 0.
	assert.Equal(t, "sink-24", notif.Status.SinkStatuses[0].Name)
	// The oldest retained entry should be sink-05 (25-20=5).
	assert.Equal(t, "sink-05", notif.Status.SinkStatuses[hibernatorv1alpha1.MaxSinkStatusHistory-1].Name)
}

// ---------------------------------------------------------------------------
// syncWatchedPlans
// ---------------------------------------------------------------------------

func TestSyncWatchedPlans_NilContext(t *testing.T) {
	p, updater := newTestProcessor(t)

	p.syncWatchedPlans(nil, logr.Discard(), types.NamespacedName{Name: "plan", Namespace: "default"}, nil, nil)

	assert.Equal(t, 0, updater.Len())
}

func TestSyncWatchedPlans_SyncsAllNotifications(t *testing.T) {
	p, updater := newTestProcessor(t)

	planCtx := &message.PlanContext{
		Plan: &hibernatorv1alpha1.HibernatePlan{
			ObjectMeta: metav1.ObjectMeta{Name: "my-plan", Namespace: "default"},
		},
		Notifications: []hibernatorv1alpha1.HibernateNotification{
			*baseNotification("notif-1", "slack"),
			*baseNotification("notif-2", "telegram"),
		},
	}

	planKey := types.NamespacedName{Name: "my-plan", Namespace: "default"}
	p.syncWatchedPlans(nil, logr.Discard(), planKey, planCtx, nil)

	assert.Equal(t, 2, updater.Len(), "should queue updates for both notifications")
}
