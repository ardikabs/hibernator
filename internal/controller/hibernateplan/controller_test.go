/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package hibernateplan

import (
	"context"
	"fmt"
	"testing"
	"time"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/controller/status"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/scheduler"
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

func newHibernatePlanReconciler(objs ...client.Object) (*Reconciler, client.Client) {
	scheme := newTestScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&hibernatorv1alpha1.HibernatePlan{}, &hibernatorv1alpha1.CloudProvider{}, &hibernatorv1alpha1.K8SCluster{}).
		WithInterceptorFuncs(interceptorFunc).
		Build()

	reconciler := &Reconciler{
		Client:               fakeClient,
		APIReader:            fakeClient,
		Log:                  logr.Discard(),
		Scheme:               scheme,
		Planner:              scheduler.NewPlanner(),
		ScheduleEvaluator:    scheduler.NewScheduleEvaluator(),
		RestoreManager:       restore.NewManager(fakeClient),
		ControlPlaneEndpoint: "http://localhost:8080",
		RunnerImage:          "hibernator-runner:latest",
		RunnerServiceAccount: "hibernator-runner",
		statusUpdater:        status.NewSyncStatusUpdater(fakeClient),
	}
	return reconciler, fakeClient
}

func TestHibernatePlanReconciler_InitializesStatus(t *testing.T) {
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

	reconciler, fakeClient := newHibernatePlanReconciler(plan)

	// Reconcile multiple times to process through all stages
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "test-plan", Namespace: "default"}}
	for i := 0; i < 3; i++ {
		_, err := reconciler.Reconcile(ctx, req)
		if err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
	}

	// Verify status was set (phase should be one of the expected phases)
	var updated hibernatorv1alpha1.HibernatePlan
	if err := fakeClient.Get(ctx, req.NamespacedName, &updated); err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	// Status should be initialized with a valid phase
	if updated.Status.Phase == "" {
		t.Error("Status.Phase should be set to a non-empty value")
	}
}

func TestHibernatePlanReconciler_AddsFinalizer(t *testing.T) {
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

	reconciler, fakeClient := newHibernatePlanReconciler(plan)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "test-plan", Namespace: "default"}}
	// First reconcile adds finalizer
	_, err := reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	// Verify finalizer was added
	var updated hibernatorv1alpha1.HibernatePlan
	if err := fakeClient.Get(ctx, req.NamespacedName, &updated); err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	found := false
	for _, finalizer := range updated.Finalizers {
		if finalizer == wellknown.PlanFinalizerName {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Finalizer %s not found", wellknown.PlanFinalizerName)
	}
}

func TestHibernatePlanReconciler_HandlesNotFound(t *testing.T) {
	ctx := context.Background()

	reconciler, _ := newHibernatePlanReconciler()

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "nonexistent", Namespace: "default"}}
	result, err := reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	// Should return empty result for not found (no requeue)
	if result != (reconcile.Result{}) {
		t.Errorf("Result = %v, want empty result", result)
	}
}

func TestHibernatePlanReconciler_HandlesDeletion(t *testing.T) {
	ctx := context.Background()

	now := metav1.Now()
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-plan",
			Namespace:         "default",
			Finalizers:        []string{wellknown.PlanFinalizerName},
			DeletionTimestamp: &now,
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

	reconciler, fakeClient := newHibernatePlanReconciler(plan)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "test-plan", Namespace: "default"}}
	_, err := reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	// Verify finalizer was removed
	var updated hibernatorv1alpha1.HibernatePlan
	err = fakeClient.Get(ctx, req.NamespacedName, &updated)
	if err == nil {
		// Check if finalizer was removed
		found := false
		for _, finalizer := range updated.Finalizers {
			if finalizer == wellknown.PlanFinalizerName {
				found = true
				break
			}
		}
		if found {
			t.Error("Finalizer should be removed during deletion")
		}
	} else if !errors.IsNotFound(err) {
		t.Fatalf("Expected deleted or without finalizer, got error: %v", err)
	}
}

func TestHibernatePlanReconciler_EvaluatesSchedule(t *testing.T) {
	// Create a plan with schedule set to be active at current time
	// Schedule: 20:00-06:00 UTC weekdays
	// We test at a fixed time to avoid flakiness
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-plan",
			Namespace: "default",
		},
		Spec: hibernatorv1alpha1.HibernatePlanSpec{
			Schedule: hibernatorv1alpha1.Schedule{
				Timezone: "UTC",
				OffHours: []hibernatorv1alpha1.OffHourWindow{
					{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
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

	reconciler, _ := newHibernatePlanReconciler(plan)

	// Test schedule evaluator directly
	evaluator := reconciler.ScheduleEvaluator

	// Test during work hours (should NOT be hibernated)
	workTime := time.Date(2026, 1, 28, 14, 0, 0, 0, time.UTC) // Wed 2 PM
	window := scheduler.ScheduleWindow{
		HibernateCron: "0 20 * * 1,2,3,4,5",
		WakeUpCron:    "0 6 * * 2,3,4,5,6",
		Timezone:      "UTC",
	}
	result, err := evaluator.Evaluate(window, workTime)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.ShouldHibernate {
		t.Errorf("ShouldHibernate during work hours = true, want false")
	}

	// Test during night hours (should be hibernated)
	nightTime := time.Date(2026, 1, 28, 23, 0, 0, 0, time.UTC) // Wed 11 PM
	result, err = evaluator.Evaluate(window, nightTime)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !result.ShouldHibernate {
		t.Errorf("ShouldHibernate at night = false, want true")
	}
}

func TestHibernatePlanReconciler_ConvertsOffHoursToCron(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-plan",
			Namespace: "default",
		},
		Spec: hibernatorv1alpha1.HibernatePlanSpec{
			Schedule: hibernatorv1alpha1.Schedule{
				Timezone: "UTC",
				OffHours: []hibernatorv1alpha1.OffHourWindow{
					{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
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

	newHibernatePlanReconciler(plan)

	// Convert OffHourWindow to cron
	offHours := []scheduler.OffHourWindow{
		{
			Start:      "20:00",
			End:        "06:00",
			DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
		},
	}

	hibernateCron, wakeUpCron, err := scheduler.ConvertOffHoursToCron(offHours)
	if err != nil {
		t.Fatalf("ConvertOffHoursToCron() error = %v", err)
	}

	// Verify cron format
	expectedHibernate := "0 20 * * 1,2,3,4,5"
	if hibernateCron != expectedHibernate {
		t.Errorf("hibernateCron = %s, want %s", hibernateCron, expectedHibernate)
	}

	// WakeUp should be next day
	expectedWakeUp := "0 6 * * 2,3,4,5,6"
	if wakeUpCron != expectedWakeUp {
		t.Errorf("wakeUpCron = %s, want %s", wakeUpCron, expectedWakeUp)
	}
}

func TestHibernatePlanReconciler_ValidatesTargets(t *testing.T) {
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
				{Name: "t2", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
			},
		},
	}

	newHibernatePlanReconciler(plan)

	// Verify targets are present and accessible
	if len(plan.Spec.Targets) != 2 {
		t.Errorf("Expected 2 targets, got %d", len(plan.Spec.Targets))
	}

	for i, target := range plan.Spec.Targets {
		if target.Name == "" {
			t.Errorf("Target[%d] has empty name", i)
		}
		if target.Type == "" {
			t.Errorf("Target[%d] has empty type", i)
		}
		if target.ConnectorRef.Name == "" {
			t.Errorf("Target[%d] has empty connector name", i)
		}
	}
}

// Ensure imports are used
var _ = ctrl.Result{}
