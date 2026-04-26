/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"context"
	"testing"

	"github.com/ardikabs/hibernator/internal/wellknown"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestManager_Save(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewManager(fakeClient)

	ctx := context.Background()
	namespace := "test-ns"
	planName := "test-plan"
	targetName := "test-target"

	// Test Save
	data := &Data{
		Target:    targetName,
		Executor:  "eks",
		Version:   1,
		CreatedAt: metav1.Now(),
		State: map[string]interface{}{
			"nodeGroups": []interface{}{
				map[string]interface{}{
					"name":    "ng-1",
					"minSize": float64(2),
					"maxSize": float64(5),
				},
			},
		},
	}

	err := mgr.Save(ctx, namespace, planName, targetName, data)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Test Load
	loaded, err := mgr.Load(ctx, namespace, planName, targetName)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil {
		t.Fatal("Load() returned nil")
	}
	if loaded.Target != targetName {
		t.Errorf("Load() Target = %v, want %v", loaded.Target, targetName)
	}
	if loaded.Executor != "eks" {
		t.Errorf("Load() Executor = %v, want eks", loaded.Executor)
	}

	// Test Load non-existent target
	loaded, err = mgr.Load(ctx, namespace, planName, "non-existent")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded != nil {
		t.Errorf("Load() should return nil for non-existent target")
	}
}

func TestManager_SaveState_NoExisting(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewManager(fakeClient)

	ctx := context.Background()
	namespace := "test-ns"
	planName := "test-plan"
	targetName := "test-target"

	// Save new data when no existing data
	data := &Data{
		Target:    targetName,
		Executor:  "rds",
		Version:   1,
		CreatedAt: metav1.Now(),
		State: map[string]interface{}{
			"instanceId": "db-1",
			"status":     "available",
		},
	}

	err := mgr.SaveState(ctx, namespace, planName, targetName, data, 3)
	if err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}

	// Verify data was saved
	loaded, err := mgr.Load(ctx, namespace, planName, targetName)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil {
		t.Fatal("Load() returned nil")
	}
	if loaded.State["instanceId"] != "db-1" {
		t.Errorf("Expected instanceId=db-1, got %v", loaded.State["instanceId"])
	}
}

func TestManager_SaveState_StalenessHousekeeping(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewManager(fakeClient)

	ctx := context.Background()
	namespace := "test-ns"
	planName := "test-plan"
	targetName := "test-target"
	maxStaleCount := 3

	// Initial save: keys A and B
	initialData := &Data{
		Target:    targetName,
		Executor:  "eks",
		Version:   1,
		CreatedAt: metav1.Now(),
		State: map[string]interface{}{
			"keyA": "valA",
			"keyB": "valB",
		},
	}

	err := mgr.SaveState(ctx, namespace, planName, targetName, initialData, maxStaleCount)
	if err != nil {
		t.Fatalf("SaveState() initial error = %v", err)
	}

	// Second cycle: only report B and add C. A becomes stale (count=1)
	cycle2Data := &Data{
		Target:    targetName,
		Executor:  "eks",
		Version:   1,
		CreatedAt: metav1.Now(),
		State: map[string]interface{}{
			"keyB": "valB2",
			"keyC": "valC",
		},
	}

	err = mgr.SaveState(ctx, namespace, planName, targetName, cycle2Data, maxStaleCount)
	if err != nil {
		t.Fatalf("SaveState() cycle 2 error = %v", err)
	}

	loaded, _ := mgr.Load(ctx, namespace, planName, targetName)
	if loaded.StaleCounts["keyA"] != 1 {
		t.Errorf("Expected keyA StaleCount=1, got %v", loaded.StaleCounts["keyA"])
	}
	if loaded.State["keyA"] != "valA" {
		t.Errorf("Expected keyA value to be preserved")
	}
	if loaded.State["keyB"] != "valB2" {
		t.Errorf("Expected keyB to be updated")
	}

	// Third cycle: report nothing. A (count=2), B (count=1), C (count=1)
	cycle3Data := &Data{Target: targetName, Executor: "eks", State: map[string]interface{}{}}
	_ = mgr.SaveState(ctx, namespace, planName, targetName, cycle3Data, maxStaleCount)

	loaded, _ = mgr.Load(ctx, namespace, planName, targetName)
	if loaded.StaleCounts["keyA"] != 2 {
		t.Errorf("Expected keyA StaleCount=2")
	}
	if loaded.StaleCounts["keyB"] != 1 {
		t.Errorf("Expected keyB StaleCount=1")
	}

	// Fourth cycle: report C. C is no longer stale, A becomes evicted, B (count=2)
	cycle4Data := &Data{Target: targetName, Executor: "eks", State: map[string]interface{}{"keyC": "valC4"}}
	_ = mgr.SaveState(ctx, namespace, planName, targetName, cycle4Data, maxStaleCount)

	loaded, _ = mgr.Load(ctx, namespace, planName, targetName)
	if _, exists := loaded.State["keyA"]; exists {
		t.Errorf("Expected keyA to be evicted, but it still exists")
	}
	if _, exists := loaded.StaleCounts["keyC"]; exists {
		t.Errorf("Expected keyC to have StaleCount cleared")
	}
	if loaded.State["keyC"] != "valC4" {
		t.Errorf("Expected keyC to be updated to valC4")
	}
	if loaded.StaleCounts["keyB"] != 2 {
		t.Errorf("Expected keyB StaleCount=2")
	}
}

func TestManager_MarkTargetRestored(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewManager(fakeClient)

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
	mgr := NewManager(fakeClient)

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
	mgr := NewManager(fakeClient)

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
	mgr := NewManager(fakeClient)

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
	mgr := NewManager(fakeClient)

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
