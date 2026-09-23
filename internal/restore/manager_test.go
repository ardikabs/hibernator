/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestManager_MarkTargetRestored(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewManager(fakeClient, logr.Discard())

	ctx := context.Background()
	namespace := "test-ns"
	planName := "test-plan"
	targetName := "test-target"

	// Save initial data
	data := &Data{
		Target:    targetName,
		Executor:  "rds",
		Version:   1,
		CreatedAt: metav1.Now(),
		State: map[string]interface{}{
			"instanceId": "db-1",
		},
	}

	err := mgr.Save(ctx, namespace, planName, targetName, data)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Mark as restored
	err = mgr.MarkTargetRestored(ctx, namespace, planName, targetName)
	if err != nil {
		t.Fatalf("MarkTargetRestored() error = %v", err)
	}

	// Verify annotation was set
	cmName := configMapName(planName)
	var cm corev1.ConfigMap
	err = fakeClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: cmName}, &cm)
	if err != nil {
		t.Fatalf("Get ConfigMap error = %v", err)
	}

	annotationKey := wellknown.AnnotationRestoredPrefix + targetName
	if cm.Annotations[annotationKey] != "true" {
		t.Errorf("Expected annotation %s=true, got %v", annotationKey, cm.Annotations[annotationKey])
	}

	// Verify state remains preserved
	loaded, err := mgr.Load(ctx, namespace, planName, targetName)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil {
		t.Fatal("Load() returned nil")
	}
	if loaded.State["instanceId"] != "db-1" {
		t.Errorf("Expected state preserved, got %v", loaded.State["instanceId"])
	}
}

func TestManager_MarkTargetRestored_NoConfigMap(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewManager(fakeClient, logr.Discard())

	ctx := context.Background()
	namespace := "test-ns"
	planName := "non-existent-plan"
	targetName := "test-target"

	// Should not error if ConfigMap doesn't exist
	err := mgr.MarkTargetRestored(ctx, namespace, planName, targetName)
	if err != nil {
		t.Errorf("MarkTargetRestored() should not error on non-existent ConfigMap, got = %v", err)
	}
}

func TestManager_MarkAllTargetsRestored(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewManager(fakeClient, logr.Discard())

	ctx := context.Background()
	namespace := "test-ns"
	planName := "test-plan"
	targetNames := []string{"target-1", "target-2", "target-3"}

	// Save data for all targets
	for _, target := range targetNames {
		data := &Data{
			Target:    target,
			Executor:  "eks",
			Version:   1,
			CreatedAt: metav1.Now(),
			State:     map[string]interface{}{"key": "value"},
		}
		err := mgr.Save(ctx, namespace, planName, target, data)
		if err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}

	// Initially, no targets are marked as restored
	allRestored, err := mgr.MarkAllTargetsRestored(ctx, namespace, planName, targetNames)
	if err != nil {
		t.Fatalf("MarkAllTargetsRestored() error = %v", err)
	}
	if allRestored {
		t.Error("Expected allRestored=false initially")
	}

	// Mark first two targets as restored
	for i := 0; i < 2; i++ {
		err := mgr.MarkTargetRestored(ctx, namespace, planName, targetNames[i])
		if err != nil {
			t.Fatalf("MarkTargetRestored() error = %v", err)
		}
	}

	// Should still be false (not all restored)
	allRestored, err = mgr.MarkAllTargetsRestored(ctx, namespace, planName, targetNames)
	if err != nil {
		t.Fatalf("MarkAllTargetsRestored() error = %v", err)
	}
	if allRestored {
		t.Error("Expected allRestored=false when not all targets restored")
	}

	// Mark last target as restored
	err = mgr.MarkTargetRestored(ctx, namespace, planName, targetNames[2])
	if err != nil {
		t.Fatalf("MarkTargetRestored() error = %v", err)
	}

	// Now all should be restored
	allRestored, err = mgr.MarkAllTargetsRestored(ctx, namespace, planName, targetNames)
	if err != nil {
		t.Fatalf("MarkAllTargetsRestored() error = %v", err)
	}
	if !allRestored {
		t.Error("Expected allRestored=true when all targets restored")
	}
}

func TestManager_UnlockRestoreData(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewManager(fakeClient, logr.Discard())

	ctx := context.Background()
	namespace := "test-ns"
	planName := "test-plan"
	targetNames := []string{"target-1", "target-2"}

	// Save and mark targets as restored
	for _, target := range targetNames {
		data := &Data{
			Target:    target,
			Executor:  "eks",
			Version:   1,
			CreatedAt: metav1.Now(),
			State:     map[string]interface{}{"key": "value"},
		}
		err := mgr.Save(ctx, namespace, planName, target, data)
		if err != nil {
			t.Fatalf("Save() error = %v", err)
		}
		err = mgr.MarkTargetRestored(ctx, namespace, planName, target)
		if err != nil {
			t.Fatalf("MarkTargetRestored() error = %v", err)
		}
	}

	// Unlock restore data
	err := mgr.UnlockRestoreData(ctx, namespace, planName)
	if err != nil {
		t.Fatalf("UnlockRestoreData() error = %v", err)
	}

	// Verify all restored annotations were cleared
	cmName := configMapName(planName)
	var cm corev1.ConfigMap
	err = fakeClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: cmName}, &cm)
	if err != nil {
		t.Fatalf("Get ConfigMap error = %v", err)
	}

	for _, target := range targetNames {
		annotationKey := wellknown.AnnotationRestoredPrefix + target
		if _, exists := cm.Annotations[annotationKey]; exists {
			t.Errorf("Expected annotation %s to be removed, but it still exists", annotationKey)
		}
	}

	// Verify data is still present (not deleted)
	for _, target := range targetNames {
		loaded, err := mgr.Load(ctx, namespace, planName, target)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if loaded == nil {
			t.Errorf("Expected data for target %s to still exist", target)
		}
	}
}

func TestManager_HasRestoreData(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewManager(fakeClient, logr.Discard())

	ctx := context.Background()
	namespace := "test-ns"
	planName := "test-plan"

	// Initially no restore data
	hasData, err := mgr.HasRestoreData(ctx, namespace, planName)
	if err != nil {
		t.Fatalf("HasRestoreData() error = %v", err)
	}
	if hasData {
		t.Error("Expected hasData=false for non-existent plan")
	}

	// Save some data
	data := &Data{
		Target:    "test-target",
		Executor:  "eks",
		Version:   1,
		CreatedAt: metav1.Now(),
		State:     map[string]interface{}{"key": "value"},
	}
	err = mgr.Save(ctx, namespace, planName, "test-target", data)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Now should have data
	hasData, err = mgr.HasRestoreData(ctx, namespace, planName)
	if err != nil {
		t.Fatalf("HasRestoreData() error = %v", err)
	}
	if !hasData {
		t.Error("Expected hasData=true after saving data")
	}
}

// ---------------------------------------------------------------------------
// Concurrency: parallel writers on the shared restore ConfigMap
// ---------------------------------------------------------------------------
//
// Contract: concurrent wakeup runners (Parallel/DAG strategies) mark distinct
// targets on the same restore ConfigMap, while the controller may unlock it
// mid-flight. Marks must compose (disjoint merge patches) and any residual
// write collision must be retried — never silently dropped. A dropped mark
// blocks MarkAllTargetsRestored forever and leaves restore data locked, which
// surfaced as the intermittent kind failure
// "wakeup must consume restore data for target ns-shop"
// (TestScenarios/partial-failure-besteffort).

func concurrencyTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}
	return scheme
}

func seedLiveTarget(t *testing.T, ctx context.Context, mgr *Manager, namespace, planName, target string) {
	t.Helper()
	data := &Data{
		Target:    target,
		Executor:  "noop",
		Version:   1,
		CreatedAt: metav1.Now(),
		IsLive:    true,
		CycleID:   "cycle-001",
		State:     map[string]any{"marker": target},
	}
	if err := mgr.Save(ctx, namespace, planName, target, data); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
}

func conflictError() error {
	return apierrors.NewConflict(
		schema.GroupResource{Group: "", Resource: "configmaps"},
		"hibernator-restore-test",
		fmt.Errorf("simulated concurrent write"),
	)
}

// TestMarkTargetRestored_RetriesOnConflict proves a transient write conflict
// (parallel runners hammering the same restore ConfigMap) is retried instead
// of surfacing to the caller — the runner treats mark errors as non-fatal,
// so an un-retried conflict silently loses the restored-* annotation.
func TestMarkTargetRestored_RetriesOnConflict(t *testing.T) {
	scheme := concurrencyTestScheme(t)
	ctx := context.Background()
	namespace, planName, target := "test-ns", "test-plan", "target-a"

	var attempts atomic.Int32
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if attempts.Add(1) == 1 {
					return conflictError()
				}
				return c.Patch(ctx, obj, patch, opts...)
			},
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				if attempts.Add(1) == 1 {
					return conflictError()
				}
				return c.Update(ctx, obj, opts...)
			},
		}).
		Build()
	mgr := NewManager(fakeClient, logr.Discard())

	seedLiveTarget(t, ctx, mgr, namespace, planName, target)

	if err := mgr.MarkTargetRestored(ctx, namespace, planName, target); err != nil {
		t.Fatalf("MarkTargetRestored() should retry conflict, got = %v", err)
	}
	if got := attempts.Load(); got < 2 {
		t.Fatalf("expected a retry after conflict, attempts = %d", got)
	}

	loaded, err := mgr.Load(ctx, namespace, planName, target)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil || loaded.IsLive {
		t.Fatalf("expected consumed restore data (IsLive=false), got %+v", loaded)
	}

	var cm corev1.ConfigMap
	if err := fakeClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: configMapName(planName)}, &cm); err != nil {
		t.Fatalf("Get ConfigMap error = %v", err)
	}
	if cm.Annotations[wellknown.AnnotationRestoredPrefix+target] != "true" {
		t.Fatalf("expected restored annotation after retry, got %v", cm.Annotations)
	}
}

// TestUnlockRestoreData_RetriesOnConflict proves the controller-side unlock
// survives a concurrent runner mark landing mid-unlock.
func TestUnlockRestoreData_RetriesOnConflict(t *testing.T) {
	scheme := concurrencyTestScheme(t)
	ctx := context.Background()
	namespace, planName := "test-ns", "test-plan"

	var attempts atomic.Int32
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if attempts.Add(1) == 1 {
					return conflictError()
				}
				return c.Patch(ctx, obj, patch, opts...)
			},
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				if attempts.Add(1) == 1 {
					return conflictError()
				}
				return c.Update(ctx, obj, opts...)
			},
		}).
		Build()
	mgr := NewManager(fakeClient, logr.Discard())

	for _, target := range []string{"target-a", "target-b"} {
		seedLiveTarget(t, ctx, mgr, namespace, planName, target)
		if err := mgr.MarkTargetRestored(ctx, namespace, planName, target); err != nil {
			t.Fatalf("MarkTargetRestored(%s) error = %v", target, err)
		}
	}
	attempts.Store(0)

	if err := mgr.UnlockRestoreData(ctx, namespace, planName); err != nil {
		t.Fatalf("UnlockRestoreData() should retry conflict, got = %v", err)
	}
	if got := attempts.Load(); got < 2 {
		t.Fatalf("expected a retry after conflict, attempts = %d", got)
	}

	for _, target := range []string{"target-a", "target-b"} {
		loaded, err := mgr.Load(ctx, namespace, planName, target)
		if err != nil {
			t.Fatalf("Load(%s) error = %v", target, err)
		}
		if loaded == nil || loaded.CycleID != "" {
			t.Fatalf("expected cleared CycleID for %s, got %+v", target, loaded)
		}
	}
}

// TestConcurrentMarkTargetRestored_NoLostMarks reproduces the kind-flake shape:
// parallel runners marking distinct targets must not clobber each other's
// restored-* annotations. Full-object Update loses marks under overlapping
// Get→Update windows; disjoint merge patches compose.
func TestConcurrentMarkTargetRestored_NoLostMarks(t *testing.T) {
	scheme := concurrencyTestScheme(t)
	ctx := context.Background()
	namespace, planName := "test-ns", "test-plan"
	targets := []string{"ns-shop", "ns-pricing", "ns-reports"}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewManager(fakeClient, logr.Discard())

	for _, target := range targets {
		seedLiveTarget(t, ctx, mgr, namespace, planName, target)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(targets))
	start := make(chan struct{})
	for _, target := range targets {
		wg.Add(1)
		go func(target string) {
			defer wg.Done()
			<-start // release all writers at once to force overlap
			if err := mgr.MarkTargetRestored(ctx, namespace, planName, target); err != nil {
				errCh <- fmt.Errorf("MarkTargetRestored(%s): %w", target, err)
			}
		}(target)
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	var cm corev1.ConfigMap
	if err := fakeClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: configMapName(planName)}, &cm); err != nil {
		t.Fatalf("Get ConfigMap error = %v", err)
	}
	for _, target := range targets {
		if cm.Annotations[wellknown.AnnotationRestoredPrefix+target] != "true" {
			t.Fatalf("lost restored annotation for %s, annotations = %v", target, cm.Annotations)
		}
		loaded, err := mgr.Load(ctx, namespace, planName, target)
		if err != nil {
			t.Fatalf("Load(%s) error = %v", target, err)
		}
		if loaded == nil || loaded.IsLive {
			t.Fatalf("expected consumed restore data for %s, got %+v", target, loaded)
		}
	}
}
