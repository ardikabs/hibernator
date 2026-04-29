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
	"github.com/samber/lo"
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
		RestoreManager:    restore.NewManager(fakeClient, logr.Discard()),
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
// PlanReconciler.filterActiveExceptions (existing coverage, adapted)
// ---------------------------------------------------------------------------

func TestFilterActiveExceptions_PendingException_IsExcluded(t *testing.T) {
	now := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	clk := clocktesting.NewFakeClock(now)
	r, _ := newPlanReconciler(clk)

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
	assert.Empty(t, active, "future-ValidFrom exception should be filtered out")
}

func TestFilterActiveExceptions_ExpiredTimeWindow_IsExcluded(t *testing.T) {
	now := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	clk := clocktesting.NewFakeClock(now)
	r, _ := newPlanReconciler(clk)

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
	assert.Empty(t, active, "exception past ValidUntil should be filtered out")
}

func TestFilterActiveExceptions_SortsNewestFirst(t *testing.T) {
	now := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	clk := clocktesting.NewFakeClock(now)
	r, _ := newPlanReconciler(clk)

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
	}

	active := r.filterActiveExceptions([]hibernatorv1alpha1.ScheduleException{older, newer})
	require.Len(t, active, 2)
	assert.Equal(t, "exc-newer", active[0].Name, "newest exception should be first")
	assert.Equal(t, "exc-older", active[1].Name, "oldest exception should be last")
}

// ---------------------------------------------------------------------------
// convertException (package-level function)
// ---------------------------------------------------------------------------

func TestConvertException_BasicConversion(t *testing.T) {
	now := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)

	exc := hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "exc-basic"},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       hibernatorv1alpha1.ExceptionSuspend,
			ValidFrom:  metav1.NewTime(now.Add(-1 * time.Hour)),
			ValidUntil: metav1.NewTime(now.Add(2 * time.Hour)),
			LeadTime:   "15m",
			Windows: []hibernatorv1alpha1.OffHourWindow{
				{Start: "08:00", End: "12:00", DaysOfWeek: []string{"MON", "TUE"}},
				{Start: "14:00", End: "18:00", DaysOfWeek: []string{"WED"}},
			},
		},
	}

	result := convertException(exc)
	assert.Equal(t, scheduler.ExceptionType(hibernatorv1alpha1.ExceptionSuspend), result.Type)
	assert.Equal(t, now.Add(-1*time.Hour), result.ValidFrom)
	assert.Equal(t, now.Add(2*time.Hour), result.ValidUntil)
	assert.Equal(t, 15*time.Minute, result.LeadTime)
	require.Len(t, result.Windows, 2)
	assert.Equal(t, "08:00", result.Windows[0].Start)
	assert.Equal(t, "12:00", result.Windows[0].End)
	assert.Equal(t, []string{"MON", "TUE"}, result.Windows[0].DaysOfWeek)
	assert.Equal(t, "14:00", result.Windows[1].Start)
}

func TestConvertException_NoLeadTime(t *testing.T) {
	now := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)

	exc := hibernatorv1alpha1.ScheduleException{
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       hibernatorv1alpha1.ExceptionExtend,
			ValidFrom:  metav1.NewTime(now),
			ValidUntil: metav1.NewTime(now.Add(1 * time.Hour)),
			Windows: []hibernatorv1alpha1.OffHourWindow{
				{Start: "22:00", End: "02:00", DaysOfWeek: []string{"FRI"}},
			},
		},
	}

	result := convertException(exc)
	assert.Equal(t, time.Duration(0), result.LeadTime)
}

func TestConvertException_InvalidLeadTime_ZeroDuration(t *testing.T) {
	now := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)

	exc := hibernatorv1alpha1.ScheduleException{
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       hibernatorv1alpha1.ExceptionSuspend,
			ValidFrom:  metav1.NewTime(now),
			ValidUntil: metav1.NewTime(now.Add(1 * time.Hour)),
			LeadTime:   "invalid-duration",
			Windows: []hibernatorv1alpha1.OffHourWindow{
				{Start: "08:00", End: "12:00", DaysOfWeek: []string{"MON"}},
			},
		},
	}

	result := convertException(exc)
	assert.Equal(t, time.Duration(0), result.LeadTime, "invalid lead time should default to zero")
}

func TestConvertException_EmptyWindows(t *testing.T) {
	now := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)

	exc := hibernatorv1alpha1.ScheduleException{
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			Type:       hibernatorv1alpha1.ExceptionReplace,
			ValidFrom:  metav1.NewTime(now),
			ValidUntil: metav1.NewTime(now.Add(1 * time.Hour)),
		},
	}

	result := convertException(exc)
	assert.Empty(t, result.Windows)
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

// ---------------------------------------------------------------------------
// PlanReconciler.fetchAndPublishNotifications
// ---------------------------------------------------------------------------

func TestFetchAndPublishNotifications_MatchingSelector_PublishesBinding(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	plan := simplePlan("my-plan", "default")
	plan.Labels = map[string]string{"env": "prod", "team": "infra"}

	notif := &hibernatorv1alpha1.HibernateNotification{
		ObjectMeta: metav1.ObjectMeta{Name: "notif-1", Namespace: "default"},
		Spec: hibernatorv1alpha1.HibernateNotificationSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{"env": "prod"},
			},
			OnEvents: []hibernatorv1alpha1.NotificationEvent{hibernatorv1alpha1.EventStart},
			Sinks: []hibernatorv1alpha1.NotificationSink{
				{Name: "slack", Type: hibernatorv1alpha1.SinkSlack, SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "sink-config"}},
			},
		},
	}
	r, resources := newPlanReconciler(clk, plan, notif)

	planKey := types.NamespacedName{Name: "my-plan", Namespace: "default"}
	matched, err := r.fetchAndPublishNotifications(context.Background(), plan, planKey)
	require.NoError(t, err)
	require.Len(t, matched, 1)
	assert.Equal(t, "notif-1", matched[0].Name)

	// Verify binding was published to NotificationResources.
	bindingKey := lo.Must(message.NewNotificationBindingKey(client.ObjectKeyFromObject(notif), planKey))
	nc, ok := resources.NotificationResources.Load(bindingKey)
	require.True(t, ok)
	assert.True(t, nc.Matches)
	assert.Equal(t, planKey, nc.PlanKey)
	assert.Equal(t, "notif-1", nc.Notification.Name)
}

func TestFetchAndPublishNotifications_NonMatchingSelector_PublishesUnmatchedBinding(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	plan := simplePlan("my-plan", "default")
	plan.Labels = map[string]string{"env": "staging"}

	notif := &hibernatorv1alpha1.HibernateNotification{
		ObjectMeta: metav1.ObjectMeta{Name: "notif-1", Namespace: "default"},
		Spec: hibernatorv1alpha1.HibernateNotificationSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{"env": "prod"},
			},
			OnEvents: []hibernatorv1alpha1.NotificationEvent{hibernatorv1alpha1.EventStart},
			Sinks: []hibernatorv1alpha1.NotificationSink{
				{Name: "slack", Type: hibernatorv1alpha1.SinkSlack, SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "sink-config"}},
			},
		},
	}
	r, resources := newPlanReconciler(clk, plan, notif)

	planKey := types.NamespacedName{Name: "my-plan", Namespace: "default"}
	matched, err := r.fetchAndPublishNotifications(context.Background(), plan, planKey)
	require.NoError(t, err)
	assert.Empty(t, matched)

	// Binding should still be published with Matches=false.
	bindingKey := lo.Must(message.NewNotificationBindingKey(client.ObjectKeyFromObject(notif), planKey))

	nc, ok := resources.NotificationResources.Load(bindingKey)
	require.True(t, ok)
	assert.False(t, nc.Matches)
}

func TestFetchAndPublishNotifications_DifferentNamespace_NothingPublished(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	plan := simplePlan("my-plan", "default")
	plan.Labels = map[string]string{"env": "prod"}

	notif := &hibernatorv1alpha1.HibernateNotification{
		ObjectMeta: metav1.ObjectMeta{Name: "notif-1", Namespace: "other-ns"},
		Spec: hibernatorv1alpha1.HibernateNotificationSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{"env": "prod"},
			},
			OnEvents: []hibernatorv1alpha1.NotificationEvent{hibernatorv1alpha1.EventStart},
			Sinks: []hibernatorv1alpha1.NotificationSink{
				{Name: "slack", Type: hibernatorv1alpha1.SinkSlack, SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "sink-config"}},
			},
		},
	}
	r, resources := newPlanReconciler(clk, plan, notif)

	planKey := types.NamespacedName{Name: "my-plan", Namespace: "default"}
	matched, err := r.fetchAndPublishNotifications(context.Background(), plan, planKey)
	require.NoError(t, err)
	assert.Empty(t, matched)

	// No binding should exist since notification is in a different namespace.
	assert.Empty(t, resources.NotificationResources.Len())
}

func TestFetchAndPublishNotifications_MultipleNotifications_PartitionsCorrectly(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	plan := simplePlan("my-plan", "default")
	plan.Labels = map[string]string{"env": "prod", "team": "infra"}

	matching := &hibernatorv1alpha1.HibernateNotification{
		ObjectMeta: metav1.ObjectMeta{Name: "notif-match", Namespace: "default"},
		Spec: hibernatorv1alpha1.HibernateNotificationSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{"team": "infra"},
			},
			OnEvents: []hibernatorv1alpha1.NotificationEvent{hibernatorv1alpha1.EventStart},
			Sinks: []hibernatorv1alpha1.NotificationSink{
				{Name: "slack", Type: hibernatorv1alpha1.SinkSlack, SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "sink-config"}},
			},
		},
	}
	nonmatching := &hibernatorv1alpha1.HibernateNotification{
		ObjectMeta: metav1.ObjectMeta{Name: "notif-no-match", Namespace: "default"},
		Spec: hibernatorv1alpha1.HibernateNotificationSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{"team": "security"},
			},
			OnEvents: []hibernatorv1alpha1.NotificationEvent{hibernatorv1alpha1.EventFailure},
			Sinks: []hibernatorv1alpha1.NotificationSink{
				{Name: "telegram", Type: hibernatorv1alpha1.SinkTelegram, SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "sink-config"}},
			},
		},
	}
	r, resources := newPlanReconciler(clk, plan, matching, nonmatching)

	planKey := types.NamespacedName{Name: "my-plan", Namespace: "default"}
	matched, err := r.fetchAndPublishNotifications(context.Background(), plan, planKey)
	require.NoError(t, err)
	require.Len(t, matched, 1)
	assert.Equal(t, "notif-match", matched[0].Name)

	// Both bindings should exist in NotificationResources.
	bindingKey1 := lo.Must(message.NewNotificationBindingKey(client.ObjectKeyFromObject(matching), planKey))
	ncMatch, ok := resources.NotificationResources.Load(bindingKey1)
	require.True(t, ok)
	assert.True(t, ncMatch.Matches)

	bindingKey2 := lo.Must(message.NewNotificationBindingKey(client.ObjectKeyFromObject(nonmatching), planKey))
	ncNoMatch, ok := resources.NotificationResources.Load(bindingKey2)
	require.True(t, ok)
	assert.False(t, ncNoMatch.Matches)
}

func TestFetchAndPublishNotifications_EmptySelector_MatchesAllPlans(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	plan := simplePlan("my-plan", "default")
	plan.Labels = map[string]string{"env": "prod"}

	notif := &hibernatorv1alpha1.HibernateNotification{
		ObjectMeta: metav1.ObjectMeta{Name: "notif-all", Namespace: "default"},
		Spec: hibernatorv1alpha1.HibernateNotificationSpec{
			Selector: metav1.LabelSelector{},
			OnEvents: []hibernatorv1alpha1.NotificationEvent{hibernatorv1alpha1.EventPhaseChange},
			Sinks: []hibernatorv1alpha1.NotificationSink{
				{Name: "slack", Type: hibernatorv1alpha1.SinkSlack, SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "sink-config"}},
			},
		},
	}
	r, resources := newPlanReconciler(clk, plan, notif)

	planKey := types.NamespacedName{Name: "my-plan", Namespace: "default"}
	matched, err := r.fetchAndPublishNotifications(context.Background(), plan, planKey)
	require.NoError(t, err)
	assert.Len(t, matched, 1)

	bindingKey := lo.Must(message.NewNotificationBindingKey(client.ObjectKeyFromObject(notif), planKey))
	nc, ok := resources.NotificationResources.Load(bindingKey)
	require.True(t, ok)
	assert.True(t, nc.Matches)
}

func TestFetchAndPublishNotifications_PlanWithNoLabels_MatchesEmptySelector(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	plan := simplePlan("my-plan", "default")

	notif := &hibernatorv1alpha1.HibernateNotification{
		ObjectMeta: metav1.ObjectMeta{Name: "notif-all", Namespace: "default"},
		Spec: hibernatorv1alpha1.HibernateNotificationSpec{
			Selector: metav1.LabelSelector{},
			OnEvents: []hibernatorv1alpha1.NotificationEvent{hibernatorv1alpha1.EventStart},
			Sinks: []hibernatorv1alpha1.NotificationSink{
				{Name: "slack", Type: hibernatorv1alpha1.SinkSlack, SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "sink-config"}},
			},
		},
	}
	r, resources := newPlanReconciler(clk, plan, notif)

	planKey := types.NamespacedName{Name: "my-plan", Namespace: "default"}
	matched, err := r.fetchAndPublishNotifications(context.Background(), plan, planKey)
	require.NoError(t, err)
	assert.Len(t, matched, 1)

	bindingKey := lo.Must(message.NewNotificationBindingKey(client.ObjectKeyFromObject(notif), planKey))
	nc, ok := resources.NotificationResources.Load(bindingKey)
	require.True(t, ok)
	assert.True(t, nc.Matches)
}

func TestFetchAndPublishNotifications_CleansUpStaleBindings(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	plan := simplePlan("my-plan", "default")
	plan.Labels = map[string]string{"env": "prod"}

	notif := &hibernatorv1alpha1.HibernateNotification{
		ObjectMeta: metav1.ObjectMeta{Name: "notif-1", Namespace: "default"},
		Spec: hibernatorv1alpha1.HibernateNotificationSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{"env": "prod"},
			},
			OnEvents: []hibernatorv1alpha1.NotificationEvent{hibernatorv1alpha1.EventStart},
			Sinks: []hibernatorv1alpha1.NotificationSink{
				{Name: "slack", Type: hibernatorv1alpha1.SinkSlack, SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "sink-config"}},
			},
		},
	}
	r, resources := newPlanReconciler(clk, plan, notif)

	planKey := types.NamespacedName{Name: "my-plan", Namespace: "default"}

	// First reconcile — binding exists.
	_, err := r.fetchAndPublishNotifications(context.Background(), plan, planKey)
	require.NoError(t, err)
	bindingKey := lo.Must(message.NewNotificationBindingKey(client.ObjectKeyFromObject(notif), planKey))
	_, ok := resources.NotificationResources.Load(bindingKey)
	require.True(t, ok)

	// Simulate notification deletion: delete from the fake client.
	require.NoError(t, r.Delete(context.Background(), notif))

	// Second reconcile — stale binding should be deleted.
	_, err = r.fetchAndPublishNotifications(context.Background(), plan, planKey)
	require.NoError(t, err)
	_, ok = resources.NotificationResources.Load(bindingKey)
	assert.False(t, ok, "stale binding should have been deleted")
}

// ---------------------------------------------------------------------------
// PlanReconciler.findPlansForNotification
// ---------------------------------------------------------------------------

func TestFindPlansForNotification_MatchingPlans_ReturnsRequests(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	plan1 := simplePlan("plan-1", "default")
	plan1.Labels = map[string]string{"env": "prod"}
	plan2 := simplePlan("plan-2", "default")
	plan2.Labels = map[string]string{"env": "staging"}

	r, _ := newPlanReconciler(clk, plan1, plan2)

	notif := &hibernatorv1alpha1.HibernateNotification{
		ObjectMeta: metav1.ObjectMeta{Name: "notif-1", Namespace: "default"},
		Spec: hibernatorv1alpha1.HibernateNotificationSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{"env": "prod"},
			},
			OnEvents: []hibernatorv1alpha1.NotificationEvent{hibernatorv1alpha1.EventStart},
			Sinks: []hibernatorv1alpha1.NotificationSink{
				{Name: "slack", Type: hibernatorv1alpha1.SinkSlack, SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "sink-config"}},
			},
		},
	}

	requests := r.findPlansForNotification(context.Background(), notif)
	require.Len(t, requests, 1)
	assert.Equal(t, "plan-1", requests[0].Name)
	assert.Equal(t, "default", requests[0].Namespace)
}

func TestFindPlansForNotification_NonNotificationObject_ReturnsNil(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	r, _ := newPlanReconciler(clk)

	requests := r.findPlansForNotification(context.Background(), &hibernatorv1alpha1.HibernatePlan{})
	assert.Nil(t, requests)
}

// ---------------------------------------------------------------------------
// PlanReconciler.Reconcile — notification integration
// ---------------------------------------------------------------------------

func TestPlanReconciler_Reconcile_WithNotification_PopulatesNotifications(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	plan := simplePlan("my-plan", "default")
	plan.Labels = map[string]string{"env": "prod"}

	notif := &hibernatorv1alpha1.HibernateNotification{
		ObjectMeta: metav1.ObjectMeta{Name: "notif-1", Namespace: "default"},
		Spec: hibernatorv1alpha1.HibernateNotificationSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{"env": "prod"},
			},
			OnEvents: []hibernatorv1alpha1.NotificationEvent{hibernatorv1alpha1.EventStart},
			Sinks: []hibernatorv1alpha1.NotificationSink{
				{Name: "slack", Type: hibernatorv1alpha1.SinkSlack, SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "sink-config"}},
			},
		},
	}
	r, resources := newPlanReconciler(clk, plan, notif)

	key := types.NamespacedName{Name: "my-plan", Namespace: "default"}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)

	stored, ok := resources.PlanResources.Load(key)
	require.True(t, ok)
	assert.Len(t, stored.Notifications, 1)
	assert.Equal(t, "notif-1", stored.Notifications[0].Name)
}

func TestPlanReconciler_Reconcile_NoNotification_EmptyNotifications(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	plan := simplePlan("my-plan", "default")
	plan.Labels = map[string]string{"env": "prod"}

	r, resources := newPlanReconciler(clk, plan)

	key := types.NamespacedName{Name: "my-plan", Namespace: "default"}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)

	stored, ok := resources.PlanResources.Load(key)
	require.True(t, ok)
	assert.Empty(t, stored.Notifications)
}
