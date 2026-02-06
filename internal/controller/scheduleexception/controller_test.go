/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package scheduleexception

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func newTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = hibernatorv1alpha1.AddToScheme(scheme)
	return scheme
}

var interceptorFunc = interceptor.Funcs{Patch: func(
	ctx context.Context,
	clnt client.WithWatch,
	obj client.Object,
	patch client.Patch,
	opts ...client.PatchOption,
) error {
	// Apply patches are supposed to upsert, but fake client fails if the object doesn't exist,
	// if an apply patch occurs for an object that doesn't yet exist, create it.
	if patch.Type() != types.ApplyPatchType {
		return clnt.Patch(ctx, obj, patch, opts...)
	}
	check, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		return fmt.Errorf("could not check for object in fake client")
	}
	if err := clnt.Get(ctx, client.ObjectKeyFromObject(obj), check); errors.IsNotFound(err) {
		if err := clnt.Create(ctx, check); err != nil {
			return fmt.Errorf("could not inject object creation for fake: %w", err)
		}
	} else if err != nil {
		return err
	}
	obj.SetResourceVersion(check.GetResourceVersion())
	return clnt.Update(ctx, obj)
}}

func newScheduleExceptionReconciler(objs ...client.Object) (*Reconciler, client.Client) {
	scheme := newTestScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&hibernatorv1alpha1.ScheduleException{}, &hibernatorv1alpha1.HibernatePlan{}).
		WithInterceptorFuncs(interceptorFunc).
		Build()

	reconciler := &Reconciler{
		Client: fakeClient,
		Log:    logr.Discard(),
		Scheme: scheme,
	}
	return reconciler, fakeClient
}

func TestScheduleExceptionReconciler_InitializesStatus(t *testing.T) {
	ctx := context.Background()

	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-plan",
			Namespace: "default",
		},
		Spec: hibernatorv1alpha1.HibernatePlanSpec{
			Schedule: hibernatorv1alpha1.Schedule{
				Timezone: "UTC",
				OffHours: []hibernatorv1alpha1.OffHourWindow{
					{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON"}},
				},
			},
			Execution: hibernatorv1alpha1.Execution{
				Strategy: hibernatorv1alpha1.ExecutionStrategy{
					Type: hibernatorv1alpha1.StrategySequential,
				},
			},
			Targets: []hibernatorv1alpha1.Target{
				{Name: "t1", Type: "eks", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "K8SCluster", Name: "k"}},
			},
		},
	}

	exception := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-exception",
			Namespace: "default",
		},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			PlanRef: hibernatorv1alpha1.PlanReference{
				Name:      "test-plan",
				Namespace: "default",
			},
			ValidFrom:  metav1.Time{Time: time.Now().Add(-1 * time.Hour)},
			ValidUntil: metav1.Time{Time: time.Now().Add(7 * 24 * time.Hour)},
			Type:       "extend",
			Windows: []hibernatorv1alpha1.OffHourWindow{
				{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SAT"}},
			},
		},
	}

	reconciler, fakeClient := newScheduleExceptionReconciler(plan, exception)

	// Reconcile multiple times to process through all stages
	// (finalizer add, status init, label add, etc.)
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "test-exception", Namespace: "default"}}
	for i := 0; i < 5; i++ {
		_, err := reconciler.Reconcile(ctx, req)
		if err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
	}

	// Verify status was initialized
	var updated hibernatorv1alpha1.ScheduleException
	if err := fakeClient.Get(ctx, req.NamespacedName, &updated); err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if updated.Status.State != hibernatorv1alpha1.ExceptionStateActive {
		t.Errorf("Status.State = %v, want %v", updated.Status.State, hibernatorv1alpha1.ExceptionStateActive)
	}
	if updated.Status.AppliedAt.IsZero() {
		t.Error("Status.AppliedAt should be set")
	}
}

func TestScheduleExceptionReconciler_AddsPlanLabel(t *testing.T) {
	ctx := context.Background()

	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-plan",
			Namespace: "default",
		},
		Spec: hibernatorv1alpha1.HibernatePlanSpec{
			Schedule: hibernatorv1alpha1.Schedule{
				Timezone: "UTC",
				OffHours: []hibernatorv1alpha1.OffHourWindow{
					{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON"}},
				},
			},
			Execution: hibernatorv1alpha1.Execution{
				Strategy: hibernatorv1alpha1.ExecutionStrategy{
					Type: hibernatorv1alpha1.StrategySequential,
				},
			},
			Targets: []hibernatorv1alpha1.Target{
				{Name: "t1", Type: "eks", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "K8SCluster", Name: "k"}},
			},
		},
	}

	exception := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-exception",
			Namespace: "default",
			// No labels set
		},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			PlanRef: hibernatorv1alpha1.PlanReference{
				Name:      "test-plan",
				Namespace: "default",
			},
			ValidFrom:  metav1.Time{Time: time.Now().Add(-1 * time.Hour)},
			ValidUntil: metav1.Time{Time: time.Now().Add(7 * 24 * time.Hour)},
			Type:       "extend",
			Windows: []hibernatorv1alpha1.OffHourWindow{
				{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SAT"}},
			},
		},
	}

	reconciler, fakeClient := newScheduleExceptionReconciler(plan, exception)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "test-exception", Namespace: "default"}}
	// Reconcile multiple times to process through stages
	for i := 0; i < 5; i++ {
		_, err := reconciler.Reconcile(ctx, req)
		if err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
	}

	// Verify label was added
	var updated hibernatorv1alpha1.ScheduleException
	if err := fakeClient.Get(ctx, req.NamespacedName, &updated); err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	labelValue, ok := updated.Labels[wellknown.LabelPlan]
	if !ok {
		t.Errorf("Label %s not found", wellknown.LabelPlan)
	}
	if labelValue != "test-plan" {
		t.Errorf("Label value = %v, want %v", labelValue, "test-plan")
	}
}

func TestScheduleExceptionReconciler_ExpiresException(t *testing.T) {
	ctx := context.Background()

	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-plan",
			Namespace: "default",
		},
		Spec: hibernatorv1alpha1.HibernatePlanSpec{
			Schedule: hibernatorv1alpha1.Schedule{
				Timezone: "UTC",
				OffHours: []hibernatorv1alpha1.OffHourWindow{
					{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON"}},
				},
			},
			Execution: hibernatorv1alpha1.Execution{
				Strategy: hibernatorv1alpha1.ExecutionStrategy{
					Type: hibernatorv1alpha1.StrategySequential,
				},
			},
			Targets: []hibernatorv1alpha1.Target{
				{Name: "t1", Type: "eks", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "K8SCluster", Name: "k"}},
			},
		},
	}

	// Exception that has already expired
	exception := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "expired-exception",
			Namespace: "default",
			Labels:    map[string]string{wellknown.LabelPlan: "test-plan"},
		},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			PlanRef: hibernatorv1alpha1.PlanReference{
				Name:      "test-plan",
				Namespace: "default",
			},
			ValidFrom:  metav1.Time{Time: time.Now().Add(-48 * time.Hour)},
			ValidUntil: metav1.Time{Time: time.Now().Add(-1 * time.Hour)}, // Expired 1 hour ago
			Type:       "extend",
			Windows: []hibernatorv1alpha1.OffHourWindow{
				{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SAT"}},
			},
		},
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{
			State:     hibernatorv1alpha1.ExceptionStateActive,
			AppliedAt: &metav1.Time{Time: time.Now().Add(-48 * time.Hour)},
		},
	}

	reconciler, fakeClient := newScheduleExceptionReconciler(plan, exception)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "expired-exception", Namespace: "default"}}
	// Reconcile multiple times to process through stages
	for i := 0; i < 5; i++ {
		_, err := reconciler.Reconcile(ctx, req)
		if err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
	}

	// Verify exception was expired
	var updated hibernatorv1alpha1.ScheduleException
	if err := fakeClient.Get(ctx, req.NamespacedName, &updated); err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if updated.Status.State != hibernatorv1alpha1.ExceptionStateExpired {
		t.Errorf("Status.State = %v, want %v", updated.Status.State, hibernatorv1alpha1.ExceptionStateExpired)
	}
	if updated.Status.ExpiredAt.IsZero() {
		t.Error("Status.ExpiredAt should be set")
	}
}

func TestScheduleExceptionReconciler_TriggersPlanReconciliation(t *testing.T) {
	ctx := context.Background()

	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-plan",
			Namespace: "default",
		},
		Spec: hibernatorv1alpha1.HibernatePlanSpec{
			Schedule: hibernatorv1alpha1.Schedule{
				Timezone: "UTC",
				OffHours: []hibernatorv1alpha1.OffHourWindow{
					{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON"}},
				},
			},
			Execution: hibernatorv1alpha1.Execution{
				Strategy: hibernatorv1alpha1.ExecutionStrategy{
					Type: hibernatorv1alpha1.StrategySequential,
				},
			},
			Targets: []hibernatorv1alpha1.Target{
				{Name: "t1", Type: "eks", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "K8SCluster", Name: "k"}},
			},
		},
	}

	exception := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-exception",
			Namespace: "default",
		},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			PlanRef: hibernatorv1alpha1.PlanReference{
				Name:      "test-plan",
				Namespace: "default",
			},
			ValidFrom:  metav1.Time{Time: time.Now().Add(-1 * time.Hour)},
			ValidUntil: metav1.Time{Time: time.Now().Add(7 * 24 * time.Hour)},
			Type:       "extend",
			Windows: []hibernatorv1alpha1.OffHourWindow{
				{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SAT"}},
			},
		},
	}

	reconciler, fakeClient := newScheduleExceptionReconciler(plan, exception)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "test-exception", Namespace: "default"}}
	// Reconcile multiple times to process through stages
	for i := 0; i < 5; i++ {
		_, err := reconciler.Reconcile(ctx, req)
		if err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
	}

	// Verify exception was fully reconciled (has label and status)
	var updated hibernatorv1alpha1.ScheduleException
	if err := fakeClient.Get(ctx, req.NamespacedName, &updated); err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	// Verify label was added
	if updated.Labels[wellknown.LabelPlan] != "test-plan" {
		t.Errorf("Label %s = %v, want %v", wellknown.LabelPlan, updated.Labels[wellknown.LabelPlan], "test-plan")
	}

	// Verify status was initialized
	if updated.Status.State != hibernatorv1alpha1.ExceptionStateActive {
		t.Errorf("Status.State = %v, want %v", updated.Status.State, hibernatorv1alpha1.ExceptionStateActive)
	}

	// Verify message contains expiry info
	if updated.Status.Message == "" {
		t.Error("Status.Message should be set")
	}

	// Verify plan has the trigger annotation (triggers reconciliation)
	var updatedPlan hibernatorv1alpha1.HibernatePlan
	if err := fakeClient.Get(ctx, types.NamespacedName{Name: "test-plan", Namespace: "default"}, &updatedPlan); err != nil {
		t.Fatalf("Get() plan error = %v", err)
	}

	triggerAnnotation, ok := updatedPlan.Annotations[wellknown.AnnotationExceptionTrigger]
	if !ok {
		t.Errorf("Plan should have annotation %s to trigger reconciliation", wellknown.AnnotationExceptionTrigger)
	}
	if triggerAnnotation == "" {
		t.Error("Trigger annotation should not be empty")
	}
	// Verify annotation format contains exception name and state
	if !strings.Contains(triggerAnnotation, "test-exception") {
		t.Errorf("Trigger annotation should contain exception name, got: %s", triggerAnnotation)
	}
}

func TestScheduleExceptionReconciler_HandlesDeletion(t *testing.T) {
	ctx := context.Background()

	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-plan",
			Namespace: "default",
		},
		Spec: hibernatorv1alpha1.HibernatePlanSpec{
			Schedule: hibernatorv1alpha1.Schedule{
				Timezone: "UTC",
				OffHours: []hibernatorv1alpha1.OffHourWindow{
					{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON"}},
				},
			},
			Execution: hibernatorv1alpha1.Execution{
				Strategy: hibernatorv1alpha1.ExecutionStrategy{
					Type: hibernatorv1alpha1.StrategySequential,
				},
			},
			Targets: []hibernatorv1alpha1.Target{
				{Name: "t1", Type: "eks", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "K8SCluster", Name: "k"}},
			},
		},
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			ActiveExceptions: []hibernatorv1alpha1.ExceptionReference{
				{
					Name:       "test-exception",
					Type:       "extend",
					ValidFrom:  metav1.Time{Time: time.Now()},
					ValidUntil: metav1.Time{Time: time.Now().Add(7 * 24 * time.Hour)},
					State:      hibernatorv1alpha1.ExceptionStateActive,
				},
			},
		},
	}

	now := metav1.Now()
	exception := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-exception",
			Namespace:         "default",
			Labels:            map[string]string{wellknown.LabelPlan: "test-plan"},
			Finalizers:        []string{wellknown.ExceptionFinalizerName},
			DeletionTimestamp: &now,
		},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			PlanRef: hibernatorv1alpha1.PlanReference{
				Name:      "test-plan",
				Namespace: "default",
			},
			ValidFrom:  metav1.Time{Time: time.Now()},
			ValidUntil: metav1.Time{Time: time.Now().Add(7 * 24 * time.Hour)},
			Type:       "extend",
			Windows: []hibernatorv1alpha1.OffHourWindow{
				{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SAT"}},
			},
		},
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{
			State: hibernatorv1alpha1.ExceptionStateActive,
		},
	}

	reconciler, fakeClient := newScheduleExceptionReconciler(plan, exception)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "test-exception", Namespace: "default"}}
	_, err := reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	// Verify plan status no longer has exception
	var updatedPlan hibernatorv1alpha1.HibernatePlan
	if err := fakeClient.Get(ctx, types.NamespacedName{Name: "test-plan", Namespace: "default"}, &updatedPlan); err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if len(updatedPlan.Status.ActiveExceptions) != 0 {
		t.Errorf("Expected ActiveExceptions to be empty after deletion, got %d", len(updatedPlan.Status.ActiveExceptions))
	}

	// The exception should be deleted (finalizer removed, DeletionTimestamp was set)
	// so Get() should return NotFound
	var deletedExc hibernatorv1alpha1.ScheduleException
	err = fakeClient.Get(ctx, req.NamespacedName, &deletedExc)
	if err == nil {
		t.Error("Expected exception to be deleted (not found), but Get() succeeded")
	} else if !errors.IsNotFound(err) {
		t.Errorf("Expected NotFound error, got: %v", err)
	}
}

// Ensure imports are used
var _ = ctrl.Result{}
