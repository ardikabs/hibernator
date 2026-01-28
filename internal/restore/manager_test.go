/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestManager_SaveAndLoad(t *testing.T) {
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

func TestManager_LoadAll(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewManager(fakeClient)

	ctx := context.Background()
	namespace := "test-ns"
	planName := "test-plan"

	// Save multiple targets
	targets := []string{"target-1", "target-2", "target-3"}
	for i, target := range targets {
		data := &Data{
			Target:    target,
			Executor:  "ec2",
			Version:   int64(i + 1),
			CreatedAt: metav1.NewTime(time.Now()),
		}
		if err := mgr.Save(ctx, namespace, planName, target, data); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}

	// Load all
	all, err := mgr.LoadAll(ctx, namespace, planName)
	if err != nil {
		t.Fatalf("LoadAll() error = %v", err)
	}
	if len(all) != 3 {
		t.Errorf("LoadAll() returned %d items, want 3", len(all))
	}

	for _, target := range targets {
		if _, ok := all[target]; !ok {
			t.Errorf("LoadAll() missing target %s", target)
		}
	}
}

func TestManager_Delete(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewManager(fakeClient)

	ctx := context.Background()
	namespace := "test-ns"
	planName := "test-plan"

	// Save two targets
	for _, target := range []string{"target-1", "target-2"} {
		data := &Data{Target: target, Executor: "rds", Version: 1}
		if err := mgr.Save(ctx, namespace, planName, target, data); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}

	// Delete one target
	if err := mgr.Delete(ctx, namespace, planName, "target-1"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// Verify target-1 is gone
	loaded, _ := mgr.Load(ctx, namespace, planName, "target-1")
	if loaded != nil {
		t.Error("Delete() did not remove target-1")
	}

	// Verify target-2 still exists
	loaded, _ = mgr.Load(ctx, namespace, planName, "target-2")
	if loaded == nil {
		t.Error("Delete() incorrectly removed target-2")
	}
}

func TestManager_DeleteAll(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewManager(fakeClient)

	ctx := context.Background()
	namespace := "test-ns"
	planName := "test-plan"

	// Save data
	data := &Data{Target: "target", Executor: "eks", Version: 1}
	if err := mgr.Save(ctx, namespace, planName, "target", data); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Delete all
	if err := mgr.DeleteAll(ctx, namespace, planName); err != nil {
		t.Fatalf("DeleteAll() error = %v", err)
	}

	// Verify all data is gone
	all, err := mgr.LoadAll(ctx, namespace, planName)
	if err != nil {
		t.Fatalf("LoadAll() error = %v", err)
	}
	if all != nil {
		t.Error("DeleteAll() did not remove all data")
	}
}

func TestManager_GetConfigMapRef(t *testing.T) {
	mgr := &Manager{}
	ref := mgr.GetConfigMapRef("my-plan")
	expected := "hibernator-restore-my-plan"
	if ref != expected {
		t.Errorf("GetConfigMapRef() = %v, want %v", ref, expected)
	}
}
