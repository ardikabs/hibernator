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

func TestManager_SaveOrPreserve_NoExisting(t *testing.T) {
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
		IsLive:    true,
		State: map[string]interface{}{
			"instanceId": "db-1",
			"status":     "available",
		},
	}

	err := mgr.SaveOrPreserve(ctx, namespace, planName, targetName, data)
	if err != nil {
		t.Fatalf("SaveOrPreserve() error = %v", err)
	}

	// Verify data was saved
	loaded, err := mgr.Load(ctx, namespace, planName, targetName)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil {
		t.Fatal("Load() returned nil")
	}
	if !loaded.IsLive {
		t.Error("Expected IsLive=true for new data")
	}
	if loaded.State["instanceId"] != "db-1" {
		t.Errorf("Expected instanceId=db-1, got %v", loaded.State["instanceId"])
	}
}

func TestManager_SaveOrPreserve_HighQualityPreserved(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewManager(fakeClient)

	ctx := context.Background()
	namespace := "test-ns"
	planName := "test-plan"
	targetName := "test-target"

	// Save initial high-quality data
	initialData := &Data{
		Target:    targetName,
		Executor:  "rds",
		Version:   1,
		CreatedAt: metav1.Now(),
		IsLive:    true, // High quality
		State: map[string]interface{}{
			"instanceId": "db-1",
			"replicas":   float64(3),
		},
	}

	err := mgr.SaveOrPreserve(ctx, namespace, planName, targetName, initialData)
	if err != nil {
		t.Fatalf("SaveOrPreserve() initial save error = %v", err)
	}

	// Try to overwrite with low-quality data
	lowQualityData := &Data{
		Target:    targetName,
		Executor:  "rds",
		Version:   2,
		CreatedAt: metav1.Now(),
		IsLive:    false, // Low quality
		State: map[string]interface{}{
			"instanceId": "db-2", // Different value
			"replicas":   float64(0),
		},
	}

	err = mgr.SaveOrPreserve(ctx, namespace, planName, targetName, lowQualityData)
	if err != nil {
		t.Fatalf("SaveOrPreserve() second save error = %v", err)
	}

	// Verify high-quality data was preserved
	loaded, err := mgr.Load(ctx, namespace, planName, targetName)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil {
		t.Fatal("Load() returned nil")
	}
	if !loaded.IsLive {
		t.Error("Expected IsLive=true (high quality preserved)")
	}
	if loaded.State["instanceId"] != "db-1" {
		t.Errorf("Expected instanceId=db-1 (preserved), got %v", loaded.State["instanceId"])
	}
	if loaded.State["replicas"] != float64(3) {
		t.Errorf("Expected replicas=3 (preserved), got %v", loaded.State["replicas"])
	}
}

func TestManager_SaveOrPreserve_LowQualityUpgraded(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewManager(fakeClient)

	ctx := context.Background()
	namespace := "test-ns"
	planName := "test-plan"
	targetName := "test-target"

	// Save initial low-quality data
	initialData := &Data{
		Target:    targetName,
		Executor:  "ec2",
		Version:   1,
		CreatedAt: metav1.Now(),
		IsLive:    false, // Low quality
		State: map[string]interface{}{
			"instanceId": "i-stopped",
			"state":      "stopped",
		},
	}

	err := mgr.SaveOrPreserve(ctx, namespace, planName, targetName, initialData)
	if err != nil {
		t.Fatalf("SaveOrPreserve() initial save error = %v", err)
	}

	// Upgrade with high-quality data
	highQualityData := &Data{
		Target:    targetName,
		Executor:  "ec2",
		Version:   2,
		CreatedAt: metav1.Now(),
		IsLive:    true, // High quality
		State: map[string]interface{}{
			"instanceId": "i-running",
			"state":      "running",
		},
	}

	err = mgr.SaveOrPreserve(ctx, namespace, planName, targetName, highQualityData)
	if err != nil {
		t.Fatalf("SaveOrPreserve() upgrade error = %v", err)
	}

	// Verify data was upgraded
	loaded, err := mgr.Load(ctx, namespace, planName, targetName)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil {
		t.Fatal("Load() returned nil")
	}
	if !loaded.IsLive {
		t.Error("Expected IsLive=true (upgraded)")
	}
	if loaded.State["instanceId"] != "i-running" {
		t.Errorf("Expected instanceId=i-running (upgraded), got %v", loaded.State["instanceId"])
	}
	if loaded.State["state"] != "running" {
		t.Errorf("Expected state=running (upgraded), got %v", loaded.State["state"])
	}
}

func TestManager_SaveOrPreserve_PerKeyMerge(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewManager(fakeClient)

	ctx := context.Background()
	namespace := "test-ns"
	planName := "test-plan"
	targetName := "test-target"

	// Save initial high-quality data with keys A and B
	initialData := &Data{
		Target:    targetName,
		Executor:  "eks",
		Version:   1,
		CreatedAt: metav1.Now(),
		IsLive:    true,
		State: map[string]interface{}{
			"keyA": "valueA-live",
			"keyB": "valueB-live",
		},
	}

	err := mgr.SaveOrPreserve(ctx, namespace, planName, targetName, initialData)
	if err != nil {
		t.Fatalf("SaveOrPreserve() initial save error = %v", err)
	}

	// Add new key C with low-quality data (should preserve A and B, add C)
	newData := &Data{
		Target:    targetName,
		Executor:  "eks",
		Version:   2,
		CreatedAt: metav1.Now(),
		IsLive:    false,
		State: map[string]interface{}{
			"keyA": "valueA-new", // Should be ignored (existing is high-quality)
			"keyC": "valueC-new", // Should be added (new key)
		},
	}

	err = mgr.SaveOrPreserve(ctx, namespace, planName, targetName, newData)
	if err != nil {
		t.Fatalf("SaveOrPreserve() merge error = %v", err)
	}

	// Verify per-key merge logic
	loaded, err := mgr.Load(ctx, namespace, planName, targetName)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil {
		t.Fatal("Load() returned nil")
	}
	if !loaded.IsLive {
		t.Error("Expected IsLive=true (best quality wins)")
	}
	if loaded.State["keyA"] != "valueA-live" {
		t.Errorf("Expected keyA=valueA-live (preserved), got %v", loaded.State["keyA"])
	}
	if loaded.State["keyB"] != "valueB-live" {
		t.Errorf("Expected keyB=valueB-live (preserved), got %v", loaded.State["keyB"])
	}
	if loaded.State["keyC"] != "valueC-new" {
		t.Errorf("Expected keyC=valueC-new (added), got %v", loaded.State["keyC"])
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

	// Save initial data with IsLive=true
	data := &Data{
		Target:    targetName,
		Executor:  "rds",
		Version:   1,
		CreatedAt: metav1.Now(),
		IsLive:    true,
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

	// Verify IsLive was reset to false
	loaded, err := mgr.Load(ctx, namespace, planName, targetName)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil {
		t.Fatal("Load() returned nil")
	}
	if loaded.IsLive {
		t.Error("Expected IsLive=false after marking as restored")
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
			IsLive:    true,
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
			IsLive:    true,
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
		IsLive:    true,
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
