/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"context"
	"fmt"
	"testing"

	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/stretchr/testify/require"
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

	now := metav1.Now()
	// Save new data when no existing data
	data := &Data{
		Target:     targetName,
		Executor:   "rds",
		Version:    1,
		CreatedAt:  now,
		CapturedAt: &now,
		State: map[string]interface{}{
			"i-12345678": map[string]interface{}{
				"instanceId": "i-12345678",
				"wasRunning": false,
			},
			"i-87654321": map[string]interface{}{
				"instanceId": "i-87654321",
				"wasRunning": true,
			},
		},
	}

	err := mgr.SaveState(ctx, namespace, planName, targetName, data, 3, "cycle-1")
	if err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}

	// Verify data was saved
	loaded, err := mgr.Load(ctx, namespace, planName, targetName)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	require.NoError(t, err)
	require.NotEmpty(t, loaded)

	// Verify instance data is preserved
	require.Equal(t, "i-12345678", loaded.State["i-12345678"].(map[string]any)["instanceId"])
	require.Equal(t, "i-87654321", loaded.State["i-87654321"].(map[string]any)["instanceId"])

	// Verify ManagedByCycleIDs is only set for resources in demanded state (wasRunning=true)
	// i-12345678 has wasRunning=false, so it should NOT be in ManagedByCycleIDs
	require.Empty(t, loaded.ManagedByCycleIDs["i-12345678"])
	// i-87654321 has wasRunning=true, so it SHOULD be in ManagedByCycleIDs
	require.Equal(t, "cycle-1", loaded.ManagedByCycleIDs["i-87654321"])
}

// TestManager_SaveState_StalenessHousekeeping comprehensively tests staleness tracking and eviction:
// 1. Resources accumulate stale counts when not reported
// 2. Stale counts are cleared when resources are reported again
// 3. Resources are evicted after maxStaleCount consecutive misses
// 4. ManagedByCycleIDs is properly cleaned up on eviction
// 5. State is preserved until eviction threshold is reached
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

	t.Run("staleness_accumulation_and_eviction", func(t *testing.T) {
		// === CYCLE 1: Initial state with 3 resources ===
		cycle1Data := &Data{
			Target:    targetName,
			Executor:  "eks",
			Version:   1,
			CreatedAt: metav1.Now(),
			State: map[string]any{
				"ng-active": map[string]any{"desired": 2, "wasScaled": true},
				"ng-stale":  map[string]any{"desired": 3, "wasScaled": true},
				"ng-evict":  map[string]any{"desired": 1, "wasScaled": true},
			},
		}

		err := mgr.SaveState(ctx, namespace, planName, targetName, cycle1Data, maxStaleCount, "cycle-001")
		require.NoError(t, err)

		loaded, _ := mgr.Load(ctx, namespace, planName, targetName)
		require.Len(t, loaded.State, 3)
		require.Len(t, loaded.ManagedByCycleIDs, 3)
		require.Empty(t, loaded.StaleCounts) // No stale resources yet

		// === CYCLE 2: ng-stale and ng-evict not reported ===
		cycle2Data := &Data{
			Target:    targetName,
			Executor:  "eks",
			Version:   1,
			CreatedAt: metav1.Now(),
			State: map[string]any{
				"ng-active": map[string]any{"desired": 2, "wasScaled": true},
				// ng-stale and ng-evict missing
			},
		}

		err = mgr.SaveState(ctx, namespace, planName, targetName, cycle2Data, maxStaleCount, "cycle-002")
		require.NoError(t, err)

		loaded, _ = mgr.Load(ctx, namespace, planName, targetName)
		// Stale counts incremented for missing resources
		require.Equal(t, 1, loaded.StaleCounts["ng-stale"])
		require.Equal(t, 1, loaded.StaleCounts["ng-evict"])
		// State preserved for stale resources
		require.NotNil(t, loaded.State["ng-stale"])
		require.NotNil(t, loaded.State["ng-evict"])
		// Active resource updated with new cycle
		require.Equal(t, "cycle-002", loaded.ManagedByCycleIDs["ng-active"])
		// Stale resources NOT reported this cycle - dropped from marker immediately
		require.Empty(t, loaded.ManagedByCycleIDs["ng-stale"],
			"Non-reported resources should be dropped from marker immediately")
		require.Empty(t, loaded.ManagedByCycleIDs["ng-evict"],
			"Non-reported resources should be dropped from marker immediately")

		// === CYCLE 3: ng-stale still missing, ng-evict still missing ===
		cycle3Data := &Data{
			Target:    targetName,
			Executor:  "eks",
			Version:   1,
			CreatedAt: metav1.Now(),
			State: map[string]any{
				"ng-active": map[string]any{"desired": 2, "wasScaled": true},
			},
		}

		err = mgr.SaveState(ctx, namespace, planName, targetName, cycle3Data, maxStaleCount, "cycle-003")
		require.NoError(t, err)

		loaded, _ = mgr.Load(ctx, namespace, planName, targetName)
		// Stale counts now at 2
		require.Equal(t, 2, loaded.StaleCounts["ng-stale"])
		require.Equal(t, 2, loaded.StaleCounts["ng-evict"])
		// State still preserved
		require.NotNil(t, loaded.State["ng-stale"])
		require.NotNil(t, loaded.State["ng-evict"])

		// === CYCLE 4: ng-stale reported again (recovers), ng-evict still missing ===
		cycle4Data := &Data{
			Target:    targetName,
			Executor:  "eks",
			Version:   1,
			CreatedAt: metav1.Now(),
			State: map[string]any{
				"ng-active": map[string]any{"desired": 2, "wasScaled": true},
				"ng-stale":  map[string]any{"desired": 3, "wasScaled": true}, // Back!
			},
		}

		err = mgr.SaveState(ctx, namespace, planName, targetName, cycle4Data, maxStaleCount, "cycle-004")
		require.NoError(t, err)

		loaded, _ = mgr.Load(ctx, namespace, planName, targetName)
		// ng-stale stale count cleared
		require.Empty(t, loaded.StaleCounts["ng-stale"])
		require.Equal(t, "cycle-004", loaded.ManagedByCycleIDs["ng-stale"])
		// ng-evict reached threshold (3) and was EVICTED in this cycle
		require.Nil(t, loaded.State["ng-evict"])
		require.Empty(t, loaded.StaleCounts["ng-evict"])
		require.Empty(t, loaded.ManagedByCycleIDs["ng-evict"])
		// Only 2 resources remaining (ng-active, ng-stale)
		require.Len(t, loaded.State, 2)
		require.Len(t, loaded.ManagedByCycleIDs, 2)
	})

	// Reset for next test
	_ = mgr.UnlockRestoreData(ctx, namespace, planName)

	t.Run("stale_resources_preserve_state_until_eviction", func(t *testing.T) {
		// Create resources with detailed state
		cycle1Data := &Data{
			Target:    targetName,
			Executor:  "ec2",
			Version:   1,
			CreatedAt: metav1.Now(),
			State: map[string]any{
				"i-123": map[string]any{
					"instanceId":   "i-123",
					"wasRunning":   true,
					"instanceType": "t3.micro",
					"tags": map[string]string{
						"Name":        "test-instance",
						"Environment": "prod",
					},
				},
			},
		}

		err := mgr.SaveState(ctx, namespace, planName, targetName, cycle1Data, maxStaleCount, "cycle-001")
		require.NoError(t, err)

		// Cycles 2-3: Resource not reported but state preserved
		for i := 2; i <= 3; i++ {
			cycleData := &Data{
				Target:    targetName,
				Executor:  "ec2",
				Version:   1,
				CreatedAt: metav1.Now(),
				State:     map[string]any{}, // Empty
			}
			err = mgr.SaveState(ctx, namespace, planName, targetName, cycleData, maxStaleCount, fmt.Sprintf("cycle-00%d", i))
			require.NoError(t, err)
		}

		loaded, _ := mgr.Load(ctx, namespace, planName, targetName)
		// State should still have original values before eviction
		require.Equal(t, 2, loaded.StaleCounts["i-123"])
		state := loaded.State["i-123"].(map[string]any)
		require.Equal(t, "i-123", state["instanceId"])
		require.Equal(t, "t3.micro", state["instanceType"])
		// JSON unmarshals nested maps as map[string]interface{}
		tags := state["tags"].(map[string]any)
		require.Equal(t, "test-instance", tags["Name"])

		// Cycle 4: Resource evicted
		cycle4Data := &Data{
			Target:    targetName,
			Executor:  "ec2",
			Version:   1,
			CreatedAt: metav1.Now(),
			State:     map[string]any{}, // Empty
		}
		err = mgr.SaveState(ctx, namespace, planName, targetName, cycle4Data, maxStaleCount, "cycle-004")
		require.NoError(t, err)

		loaded, _ = mgr.Load(ctx, namespace, planName, targetName)
		// Now fully removed
		require.Nil(t, loaded.State["i-123"])
		require.Empty(t, loaded.StaleCounts["i-123"])
	})

	_ = mgr.UnlockRestoreData(ctx, namespace, planName)

	t.Run("multiple_resources_partial_reporting", func(t *testing.T) {
		// Initial: 5 resources
		cycle1Data := &Data{
			Target:    targetName,
			Executor:  "eks",
			Version:   1,
			CreatedAt: metav1.Now(),
			State: map[string]any{
				"ng-1": map[string]any{"desired": 1, "wasScaled": true},
				"ng-2": map[string]any{"desired": 2, "wasScaled": true},
				"ng-3": map[string]any{"desired": 3, "wasScaled": true},
				"ng-4": map[string]any{"desired": 4, "wasScaled": true},
				"ng-5": map[string]any{"desired": 5, "wasScaled": true},
			},
		}

		err := mgr.SaveState(ctx, namespace, planName, targetName, cycle1Data, maxStaleCount, "cycle-001")
		require.NoError(t, err)

		// Cycle 2: Report only ng-1, ng-3, ng-5
		cycle2Data := &Data{
			Target:    targetName,
			Executor:  "eks",
			Version:   1,
			CreatedAt: metav1.Now(),
			State: map[string]any{
				"ng-1": map[string]any{"desired": 1, "wasScaled": true},
				"ng-3": map[string]any{"desired": 3, "wasScaled": true},
				"ng-5": map[string]any{"desired": 5, "wasScaled": true},
			},
		}

		err = mgr.SaveState(ctx, namespace, planName, targetName, cycle2Data, maxStaleCount, "cycle-002")
		require.NoError(t, err)

		loaded, _ := mgr.Load(ctx, namespace, planName, targetName)
		// ng-2 and ng-4 should have staleCount=1
		require.Equal(t, 1, loaded.StaleCounts["ng-2"])
		require.Equal(t, 1, loaded.StaleCounts["ng-4"])
		// ng-1, ng-3, ng-5 should have no stale count and updated cycle
		require.Empty(t, loaded.StaleCounts["ng-1"])
		require.Empty(t, loaded.StaleCounts["ng-3"])
		require.Empty(t, loaded.StaleCounts["ng-5"])
		require.Equal(t, "cycle-002", loaded.ManagedByCycleIDs["ng-1"])
		require.Equal(t, "cycle-002", loaded.ManagedByCycleIDs["ng-3"])
		require.Equal(t, "cycle-002", loaded.ManagedByCycleIDs["ng-5"])
		// All 5 still in state
		require.Len(t, loaded.State, 5)
	})

	_ = mgr.UnlockRestoreData(ctx, namespace, planName)
	uniqueTarget3 := "demanded-state-target"

	t.Run("reported_but_not_in_demanded_state_drops_tracker", func(t *testing.T) {
		// This is DIFFERENT from staleness - resource IS reported but NOT in demanded state
		// Cycle 1: Resource in demanded state
		cycle1Data := &Data{
			Target:    uniqueTarget3,
			Executor:  "ec2",
			Version:   1,
			CreatedAt: metav1.Now(),
			State: map[string]any{
				"i-123456": map[string]any{"instanceId": "i-123456", "wasRunning": true, "type": "t3.micro"},
			},
		}

		err := mgr.SaveState(ctx, namespace, planName, uniqueTarget3, cycle1Data, maxStaleCount, "cycle-001")
		require.NoError(t, err)

		loaded, _ := mgr.Load(ctx, namespace, planName, uniqueTarget3)
		require.Equal(t, "cycle-001", loaded.ManagedByCycleIDs["i-123456"])
		require.Equal(t, "t3.micro", loaded.State["i-123456"].(map[string]any)["type"])

		// Cycle 2: SAME resource reported but NOT in demanded state (wasRunning=false)
		// This is KEY: resource IS in the reported state, just not in demanded state
		cycle2Data := &Data{
			Target:    uniqueTarget3,
			Executor:  "ec2",
			Version:   1,
			CreatedAt: metav1.Now(),
			State: map[string]any{
				// Resource IS reported but wasRunning=false (not in demanded state)
				"i-123456": map[string]any{"instanceId": "i-123456", "wasRunning": false, "type": "t3.small"},
			},
		}

		// Same cycle ID restart (user restarted operation with same cycle ID)
		err = mgr.SaveState(ctx, namespace, planName, uniqueTarget3, cycle2Data, maxStaleCount, "cycle-001")
		require.NoError(t, err)

		loaded, _ = mgr.Load(ctx, namespace, planName, uniqueTarget3)

		// CRITICAL: Same cycle ID restart - resource PRESERVES marker even if non-demanded
		// This is intentional: user responsibility for post-hibernation state
		require.Equal(t, "cycle-001", loaded.ManagedByCycleIDs["i-123456"],
			"Same cycle ID restart: previously marked resource should preserve marker (user responsibility)")

		// State should be PRESERVED for same-cycle-id restart (user responsibility)
		require.Equal(t, true, loaded.State["i-123456"].(map[string]any)["wasRunning"],
			"wasRunning should be preserved for same-cycle-id restart")
		require.Equal(t, "t3.micro", loaded.State["i-123456"].(map[string]any)["type"],
			"type should be preserved for same-cycle-id restart")

		// Stale count should NOT be incremented (resource WAS reported)
		require.Empty(t, loaded.StaleCounts["i-123456"],
			"Stale count should not increment when resource is reported")

		// Cycle 3: Resource comes back to demanded state
		cycle3Data := &Data{
			Target:    uniqueTarget3,
			Executor:  "ec2",
			Version:   1,
			CreatedAt: metav1.Now(),
			State: map[string]any{
				"i-123456": map[string]any{"instanceId": "i-123456", "wasRunning": true, "type": "t3.large"},
			},
		}

		err = mgr.SaveState(ctx, namespace, planName, uniqueTarget3, cycle3Data, maxStaleCount, "cycle-003")
		require.NoError(t, err)

		loaded, _ = mgr.Load(ctx, namespace, planName, uniqueTarget3)

		// Resource should be UPDATE tracker with NEW cycle ID (back to demanded state)
		require.Equal(t, "cycle-003", loaded.ManagedByCycleIDs["i-123456"],
			"Resource back in demanded state should update tracker with current cycle ID")
		require.Equal(t, "t3.large", loaded.State["i-123456"].(map[string]any)["type"],
			"type should reflect latest capture")
	})
}

// TestManager_SaveState_IdempotencyAndStaleness comprehensively tests:
// 1. Idempotency during restart (retry hibernation with same resources)
// 2. Staleness tracking (resources not reported in a cycle)
// 3. Staleness housekeeping (eviction after maxStaleCount)
func TestManager_SaveState_IdempotencyAndStaleness(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewManager(fakeClient)

	ctx := context.Background()
	namespace := "test-ns"
	planName := "test-plan"
	targetName := "ec2-target"
	maxStaleCount := 3

	// === Attempt 1: Initial hibernation ===
	// 3 instances reported: but i-33333333 is already stopped (wasRunning=false) - should NOT be in demanded state
	cycle1Data := &Data{
		Target:    targetName,
		Executor:  "ec2",
		Version:   1,
		CreatedAt: metav1.Now(),
		State: map[string]any{
			"i-11111111": map[string]any{"instanceId": "i-11111111", "wasRunning": true, "instanceType": "t3.micro"},
			"i-22222222": map[string]any{"instanceId": "i-22222222", "wasRunning": true, "instanceType": "t3.small"},
			"i-33333333": map[string]any{"instanceId": "i-33333333", "wasRunning": false, "instanceType": "t3.medium"},
		},
	}

	err := mgr.SaveState(ctx, namespace, planName, targetName, cycle1Data, maxStaleCount, "cycle-001")
	require.NoError(t, err)

	loaded, _ := mgr.Load(ctx, namespace, planName, targetName)
	require.Len(t, loaded.State, 3)
	// All resources in demanded state should have cycleID in ManagedByCycleIDs
	require.Equal(t, "cycle-001", loaded.ManagedByCycleIDs["i-11111111"])
	require.Equal(t, "cycle-001", loaded.ManagedByCycleIDs["i-22222222"])
	require.Empty(t, loaded.ManagedByCycleIDs["i-33333333"])
	require.Empty(t, loaded.StaleCounts)

	// Verify instanceType is preserved
	require.Equal(t, "t3.micro", loaded.State["i-11111111"].(map[string]any)["instanceType"])

	// === Attempt 2: Retry hibernation (simulates restart due to failure) ===
	// 3 instances again reproted: but i-33333333 is already started so that it can be recorded in hibernation state
	// but the others already in stopped state from last hibernation operation - should still be in demanded state
	cycle2Data := &Data{
		Target:    targetName,
		Executor:  "ec2",
		Version:   1,
		CreatedAt: metav1.Now(),
		State: map[string]any{
			// Because these resources reported from API, it shown with was running false, cause it already stopped from last hibernation operation.
			"i-11111111": map[string]any{"instanceId": "i-11111111", "wasRunning": false, "instanceType": "t3.micro"},
			"i-22222222": map[string]any{"instanceId": "i-22222222", "wasRunning": false, "instanceType": "t3.small"},

			// However, i-33333333 is reported with different result, it was running
			"i-33333333": map[string]any{"instanceId": "i-33333333", "wasRunning": true, "instanceType": "t3.medium"},
		},
	}

	// Same cycle ID restart (user restarted operation with same cycle ID)
	err = mgr.SaveState(ctx, namespace, planName, targetName, cycle2Data, maxStaleCount, "cycle-001")
	require.NoError(t, err)

	loaded, _ = mgr.Load(ctx, namespace, planName, targetName)
	// i-11111111 and i-22222222 should have same cycleID
	require.Equal(t, "cycle-001", loaded.ManagedByCycleIDs["i-11111111"])
	require.True(t, loaded.State["i-11111111"].(map[string]any)["wasRunning"].(bool) == true)
	require.Equal(t, "cycle-001", loaded.ManagedByCycleIDs["i-22222222"])
	require.True(t, loaded.State["i-22222222"].(map[string]any)["wasRunning"].(bool) == true)
	// i-33333333 should PRESERVE marker (same cycle ID restart - user responsibility)
	require.NotEmpty(t, loaded.ManagedByCycleIDs["i-33333333"], "Same cycle ID restart: previously marked resource should preserve marker")
	// But wasRunning should be updated to false from current capture
	require.True(t, loaded.State["i-33333333"].(map[string]any)["wasRunning"].(bool) != false)
	// Stale count should be cleared for all reported resources
	require.Empty(t, loaded.StaleCounts)

	// === Attempt 3: Instance 3 not reported at all (missing from API) ===
	// This tests staleness tracking
	cycle3Data := &Data{
		Target:    targetName,
		Executor:  "ec2",
		Version:   1,
		CreatedAt: metav1.Now(),
		State: map[string]any{
			"i-11111111": map[string]any{"instanceId": "i-11111111", "wasRunning": true, "instanceType": "t3.micro"},
			"i-22222222": map[string]any{"instanceId": "i-22222222", "wasRunning": true, "instanceType": "t3.small"},
		},
	}

	err = mgr.SaveState(ctx, namespace, planName, targetName, cycle3Data, maxStaleCount, "cycle-002")
	require.NoError(t, err)

	loaded, _ = mgr.Load(ctx, namespace, planName, targetName)
	require.Equal(t, "cycle-002", loaded.ManagedByCycleIDs["i-11111111"])
	require.True(t, loaded.State["i-11111111"].(map[string]any)["wasRunning"].(bool) == true)
	require.Equal(t, "cycle-002", loaded.ManagedByCycleIDs["i-22222222"])
	require.True(t, loaded.State["i-22222222"].(map[string]any)["wasRunning"].(bool) == true)
	// i-33333333 should still exist but have staleCount incremented
	require.Equal(t, 1, loaded.StaleCounts["i-33333333"])
	// i-33333333 should still be in state (not evicted yet)
	require.NotNil(t, loaded.State["i-33333333"])

	// === Attempt 4: Instance 3 still not reported ===
	cycle4Data := &Data{
		Target:    targetName,
		Executor:  "ec2",
		Version:   1,
		CreatedAt: metav1.Now(),
		State: map[string]any{
			"i-11111111": map[string]any{"instanceId": "i-11111111", "wasRunning": true, "instanceType": "t3.micro"},
			"i-22222222": map[string]any{"instanceId": "i-22222222", "wasRunning": true, "instanceType": "t3.small"},
		},
	}

	err = mgr.SaveState(ctx, namespace, planName, targetName, cycle4Data, maxStaleCount, "cycle-003")
	require.NoError(t, err)

	loaded, _ = mgr.Load(ctx, namespace, planName, targetName)
	// i-33333333 staleCount now 2
	require.Equal(t, 2, loaded.StaleCounts["i-33333333"])

	// === Attempt 5: Instance 3 reported again (recovered) ===
	cycle5Data := &Data{
		Target:    targetName,
		Executor:  "ec2",
		Version:   1,
		CreatedAt: metav1.Now(),
		State: map[string]any{
			"i-11111111": map[string]any{"instanceId": "i-11111111", "wasRunning": true, "instanceType": "t3.micro"},
			"i-22222222": map[string]any{"instanceId": "i-22222222", "wasRunning": true, "instanceType": "t3.small"},
			"i-33333333": map[string]any{"instanceId": "i-33333333", "wasRunning": true, "instanceType": "t3.medium"}, // Back online!
		},
	}

	err = mgr.SaveState(ctx, namespace, planName, targetName, cycle5Data, maxStaleCount, "cycle-004")
	require.NoError(t, err)

	loaded, _ = mgr.Load(ctx, namespace, planName, targetName)
	// i-33333333 staleCount should be cleared
	require.Empty(t, loaded.StaleCounts["i-33333333"])
	// i-33333333 should have new cycleID (back in demanded state)
	require.Equal(t, "cycle-004", loaded.ManagedByCycleIDs["i-33333333"])

	// === Attempt 6-7: Instance 2 disappears completely (will be evicted) ===
	cycle6Data := &Data{
		Target:    targetName,
		Executor:  "ec2",
		Version:   1,
		CreatedAt: metav1.Now(),
		State: map[string]any{
			"i-11111111": map[string]any{"instanceId": "i-11111111", "wasRunning": true, "instanceType": "t3.micro"},
			"i-33333333": map[string]any{"instanceId": "i-33333333", "wasRunning": true, "instanceType": "t3.medium"},
			// i-22222222 missing
		},
	}

	err = mgr.SaveState(ctx, namespace, planName, targetName, cycle6Data, maxStaleCount, "cycle-005")
	require.NoError(t, err)

	cycle7Data := &Data{
		Target:    targetName,
		Executor:  "ec2",
		Version:   1,
		CreatedAt: metav1.Now(),
		State: map[string]any{
			"i-11111111": map[string]any{"instanceId": "i-11111111", "wasRunning": true, "instanceType": "t3.micro"},
			"i-33333333": map[string]any{"instanceId": "i-33333333", "wasRunning": true, "instanceType": "t3.medium"},
		},
	}

	err = mgr.SaveState(ctx, namespace, planName, targetName, cycle7Data, maxStaleCount, "cycle-006")
	require.NoError(t, err)

	loaded, _ = mgr.Load(ctx, namespace, planName, targetName)
	// i-22222222 should have staleCount=2
	require.Equal(t, 2, loaded.StaleCounts["i-22222222"])

	// === Attempt 8: Final cycle - i-22222222 exceeds maxStaleCount and is evicted ===
	cycle8Data := &Data{
		Target:    targetName,
		Executor:  "ec2",
		Version:   1,
		CreatedAt: metav1.Now(),
		State: map[string]any{
			"i-11111111": map[string]any{"instanceId": "i-11111111", "wasRunning": true, "instanceType": "t3.micro"},
			"i-33333333": map[string]any{"instanceId": "i-33333333", "wasRunning": true, "instanceType": "t3.medium"},
		},
	}

	err = mgr.SaveState(ctx, namespace, planName, targetName, cycle8Data, maxStaleCount, "cycle-007")
	require.NoError(t, err)

	loaded, _ = mgr.Load(ctx, namespace, planName, targetName)
	// i-22222222 should be EVICTED (staleCount reached 3)
	require.Nil(t, loaded.State["i-22222222"])
	require.Empty(t, loaded.StaleCounts["i-22222222"])
	// Only 2 instances remaining
	require.Len(t, loaded.State, 2)

	// === FINAL VERIFICATION: Restart hibernation after eviction ===
	// Simulate a retry after eviction - new instance should be captured fresh
	cycle9Data := &Data{
		Target:    targetName,
		Executor:  "ec2",
		Version:   1,
		CreatedAt: metav1.Now(),
		State: map[string]any{
			"i-11111111": map[string]any{"instanceId": "i-11111111", "wasRunning": true, "instanceType": "t3.micro"},
			"i-33333333": map[string]any{"instanceId": "i-33333333", "wasRunning": true, "instanceType": "t3.medium"},
			"i-22222222": map[string]any{"instanceId": "i-22222222", "wasRunning": true, "instanceType": "t3.small"}, // New instance with same ID
		},
	}

	err = mgr.SaveState(ctx, namespace, planName, targetName, cycle9Data, maxStaleCount, "cycle-008")
	require.NoError(t, err)

	loaded, _ = mgr.Load(ctx, namespace, planName, targetName)
	// i-22222222 should be captured as NEW with fresh cycleID
	require.Equal(t, "cycle-008", loaded.ManagedByCycleIDs["i-22222222"])
	require.Len(t, loaded.State, 3)
}

// TestManager_SaveState_ManagedByCycleIDEdgeCases tests edge cases for ManagedByCycleIDs tracking:
// 1. Resource toggles between demanded and non-demanded state multiple times
// 2. All resources drop out of demanded state (empty tracker)
// 3. Resource re-enters demanded state after being dropped
// 4. Mix of wasRunning and wasScaled executors
// 5. Cycle ID reset behavior verification
func TestManager_SaveState_ManagedByCycleIDEdgeCases(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	mgr := NewManager(fakeClient)

	ctx := context.Background()
	namespace := "test-ns"
	planName := "test-plan"
	targetName := "eks-target"
	maxStaleCount := 3

	// === EDGE CASE 1: Resource toggles between demanded/non-demanded state ===
	t.Run("resource_toggle_demanded_state", func(t *testing.T) {
		// Cycle 1: In demanded state
		cycle1Data := &Data{
			Target:    targetName,
			Executor:  "eks",
			Version:   1,
			CreatedAt: metav1.Now(),
			State: map[string]any{
				"ng-1": map[string]any{"desired": 2, "wasScaled": true},
			},
		}
		err := mgr.SaveState(ctx, namespace, planName, targetName, cycle1Data, maxStaleCount, "cycle-001")
		require.NoError(t, err)

		loaded, _ := mgr.Load(ctx, namespace, planName, targetName)
		require.Equal(t, "cycle-001", loaded.ManagedByCycleIDs["ng-1"])

		// Cycle 2: NOT in demanded state (wasScaled=false)
		cycle2Data := &Data{
			Target:    targetName,
			Executor:  "eks",
			Version:   1,
			CreatedAt: metav1.Now(),
			State: map[string]any{
				"ng-1": map[string]any{"desired": 0, "wasScaled": false},
			},
		}
		// Same cycle ID restart (user restarted operation with same cycle ID)
		err = mgr.SaveState(ctx, namespace, planName, targetName, cycle2Data, maxStaleCount, "cycle-001")
		require.NoError(t, err)

		loaded, _ = mgr.Load(ctx, namespace, planName, targetName)
		// Same cycle ID restart - preserves marker (user responsibility)
		require.Equal(t, "cycle-001", loaded.ManagedByCycleIDs["ng-1"],
			"Same cycle ID restart: previously marked resource should preserve marker (user responsibility)")

		// Cycle 3: Back in demanded state
		cycle3Data := &Data{
			Target:    targetName,
			Executor:  "eks",
			Version:   1,
			CreatedAt: metav1.Now(),
			State: map[string]any{
				"ng-1": map[string]any{"desired": 3, "wasScaled": true},
			},
		}
		err = mgr.SaveState(ctx, namespace, planName, targetName, cycle3Data, maxStaleCount, "cycle-003")
		require.NoError(t, err)

		loaded, _ = mgr.Load(ctx, namespace, planName, targetName)
		// Should UPDATE tracker with new cycle ID (back to demanded state)
		require.Equal(t, "cycle-003", loaded.ManagedByCycleIDs["ng-1"])
	})

	// Reset for next test
	_ = mgr.UnlockRestoreData(ctx, namespace, planName)

	// === EDGE CASE 2: All resources drop out of demanded state ===
	t.Run("all_resources_not_demanded", func(t *testing.T) {
		// Cycle 1: Multiple resources in demanded state
		cycle1Data := &Data{
			Target:    targetName,
			Executor:  "eks",
			Version:   1,
			CreatedAt: metav1.Now(),
			State: map[string]any{
				"ng-1": map[string]any{"desired": 2, "wasScaled": true},
				"ng-2": map[string]any{"desired": 3, "wasScaled": true},
				"ng-3": map[string]any{"desired": 1, "wasScaled": true},
			},
		}
		err := mgr.SaveState(ctx, namespace, planName, targetName, cycle1Data, maxStaleCount, "cycle-001")
		require.NoError(t, err)

		loaded, _ := mgr.Load(ctx, namespace, planName, targetName)
		require.Len(t, loaded.ManagedByCycleIDs, 3)

		// Cycle 2: NONE in demanded state
		cycle2Data := &Data{
			Target:    targetName,
			Executor:  "eks",
			Version:   1,
			CreatedAt: metav1.Now(),
			State: map[string]any{
				"ng-1": map[string]any{"desired": 0, "wasScaled": false},
				"ng-2": map[string]any{"desired": 0, "wasScaled": false},
				"ng-3": map[string]any{"desired": 0, "wasScaled": false},
			},
		}
		// Same cycle ID restart (user restarted operation with same cycle ID)
		err = mgr.SaveState(ctx, namespace, planName, targetName, cycle2Data, maxStaleCount, "cycle-001")
		require.NoError(t, err)

		loaded, _ = mgr.Load(ctx, namespace, planName, targetName)
		// Same cycle ID restart - preserves all markers (user responsibility)
		require.Len(t, loaded.ManagedByCycleIDs, 3)
		require.Equal(t, "cycle-001", loaded.ManagedByCycleIDs["ng-1"])
		require.Equal(t, "cycle-001", loaded.ManagedByCycleIDs["ng-2"])
		require.Equal(t, "cycle-001", loaded.ManagedByCycleIDs["ng-3"])
		// State should still have all resources
		require.Len(t, loaded.State, 3)
	})

	// Reset for next test - use unique target to avoid state leakage
	_ = mgr.UnlockRestoreData(ctx, namespace, planName)
	uniqueTarget := "mixed-types-target"

	// === EDGE CASE 3: Mix of wasRunning (EC2) and wasScaled (EKS) executors ===
	t.Run("mixed_executor_types", func(t *testing.T) {
		// EC2-style: wasRunning
		ec2Data := &Data{
			Target:    uniqueTarget,
			Executor:  "ec2",
			Version:   1,
			CreatedAt: metav1.Now(),
			State: map[string]any{
				"i-123": map[string]any{"instanceId": "i-123", "wasRunning": true},
				"i-456": map[string]any{"instanceId": "i-456", "wasRunning": false}, // not demanded
			},
		}
		err := mgr.SaveState(ctx, namespace, planName, uniqueTarget, ec2Data, maxStaleCount, "cycle-001")
		require.NoError(t, err)

		loaded, _ := mgr.Load(ctx, namespace, planName, uniqueTarget)
		require.Equal(t, "cycle-001", loaded.ManagedByCycleIDs["i-123"])
		require.Empty(t, loaded.ManagedByCycleIDs["i-456"],
			"Resource not in demanded state on first encounter should not be tracked")

		// EKS-style: wasScaled - same target, should preserve i-123, add i-456 if demanded
		eksData := &Data{
			Target:    uniqueTarget,
			Executor:  "eks",
			Version:   1,
			CreatedAt: metav1.Now(),
			State: map[string]any{
				"i-123": map[string]any{"instanceId": "i-123", "wasRunning": true},
				"i-456": map[string]any{"instanceId": "i-456", "wasRunning": true}, // now demanded
			},
		}
		err = mgr.SaveState(ctx, namespace, planName, uniqueTarget, eksData, maxStaleCount, "cycle-002")
		require.NoError(t, err)

		loaded, _ = mgr.Load(ctx, namespace, planName, uniqueTarget)
		require.Equal(t, "cycle-002", loaded.ManagedByCycleIDs["i-123"],
			"Previously tracked resource in demanded state should update cycle ID")
		require.Equal(t, "cycle-002", loaded.ManagedByCycleIDs["i-456"],
			"Newly demanded resource should get current cycle ID")
	})

	// Reset for next test - use unique target to avoid state leakage
	_ = mgr.UnlockRestoreData(ctx, namespace, planName)
	newResourcesTarget := "new-resources-target"

	// === EDGE CASE 4: New resources added in subsequent cycles ===
	t.Run("new_resources_added_later", func(t *testing.T) {
		// Cycle 1: 2 resources
		cycle1Data := &Data{
			Target:    newResourcesTarget,
			Executor:  "ec2",
			Version:   1,
			CreatedAt: metav1.Now(),
			State: map[string]any{
				"i-111": map[string]any{"instanceId": "i-111", "wasRunning": true},
				"i-222": map[string]any{"instanceId": "i-222", "wasRunning": true},
			},
		}
		err := mgr.SaveState(ctx, namespace, planName, newResourcesTarget, cycle1Data, maxStaleCount, "cycle-001")
		require.NoError(t, err)

		loaded, _ := mgr.Load(ctx, namespace, planName, newResourcesTarget)
		require.Len(t, loaded.ManagedByCycleIDs, 2)

		// Cycle 2: New resource added
		cycle2Data := &Data{
			Target:    newResourcesTarget,
			Executor:  "ec2",
			Version:   1,
			CreatedAt: metav1.Now(),
			State: map[string]any{
				"i-111": map[string]any{"instanceId": "i-111", "wasRunning": true},
				"i-222": map[string]any{"instanceId": "i-222", "wasRunning": true},
				"i-333": map[string]any{"instanceId": "i-333", "wasRunning": true}, // NEW
			},
		}
		err = mgr.SaveState(ctx, namespace, planName, newResourcesTarget, cycle2Data, maxStaleCount, "cycle-002")
		require.NoError(t, err)

		loaded, _ = mgr.Load(ctx, namespace, planName, newResourcesTarget)
		// All should have cycle-002 (existing updated, new added with current cycle)
		require.Equal(t, "cycle-002", loaded.ManagedByCycleIDs["i-111"])
		require.Equal(t, "cycle-002", loaded.ManagedByCycleIDs["i-222"])
		require.Equal(t, "cycle-002", loaded.ManagedByCycleIDs["i-333"])
	})

	// Reset for next test
	_ = mgr.UnlockRestoreData(ctx, namespace, planName)

	// === EDGE CASE 5: Verify cycle ID is always current cycle, never preserved ===
	t.Run("cycle_id_never_preserved", func(t *testing.T) {
		// Cycle 1
		cycle1Data := &Data{
			Target:    targetName,
			Executor:  "ec2",
			Version:   1,
			CreatedAt: metav1.Now(),
			State: map[string]any{
				"i-stable": map[string]any{"instanceId": "i-stable", "wasRunning": true},
			},
		}
		err := mgr.SaveState(ctx, namespace, planName, targetName, cycle1Data, maxStaleCount, "old-cycle")
		require.NoError(t, err)

		loaded, _ := mgr.Load(ctx, namespace, planName, targetName)
		require.Equal(t, "old-cycle", loaded.ManagedByCycleIDs["i-stable"])

		// Cycle 2: Different cycle ID
		cycle2Data := &Data{
			Target:    targetName,
			Executor:  "ec2",
			Version:   1,
			CreatedAt: metav1.Now(),
			State: map[string]any{
				"i-stable": map[string]any{"instanceId": "i-stable", "wasRunning": true},
			},
		}
		err = mgr.SaveState(ctx, namespace, planName, targetName, cycle2Data, maxStaleCount, "new-cycle")
		require.NoError(t, err)

		loaded, _ = mgr.Load(ctx, namespace, planName, targetName)
		// Should be UPDATED to new cycle, not preserved as old-cycle
		require.Equal(t, "new-cycle", loaded.ManagedByCycleIDs["i-stable"])
		require.NotEqual(t, "old-cycle", loaded.ManagedByCycleIDs["i-stable"])
	})

	// Reset for next test
	_ = mgr.UnlockRestoreData(ctx, namespace, planName)

	// === EDGE CASE 6: Resource evicted then re-added with same ID ===
	t.Run("evicted_then_readded", func(t *testing.T) {
		// Cycle 1: Resource exists
		cycle1Data := &Data{
			Target:    targetName,
			Executor:  "ec2",
			Version:   1,
			CreatedAt: metav1.Now(),
			State: map[string]any{
				"i-ephemeral": map[string]any{"instanceId": "i-ephemeral", "wasRunning": true},
			},
		}
		err := mgr.SaveState(ctx, namespace, planName, targetName, cycle1Data, maxStaleCount, "cycle-001")
		require.NoError(t, err)

		// Cycles 2-4: Resource missing (will be evicted on cycle 4)
		for i := 2; i <= 4; i++ {
			cycleData := &Data{
				Target:    targetName,
				Executor:  "ec2",
				Version:   1,
				CreatedAt: metav1.Now(),
				State:     map[string]any{}, // Empty - resource missing
			}
			err = mgr.SaveState(ctx, namespace, planName, targetName, cycleData, maxStaleCount, fmt.Sprintf("cycle-00%d", i))
			require.NoError(t, err)
		}

		loaded, _ := mgr.Load(ctx, namespace, planName, targetName)
		require.Nil(t, loaded.State["i-ephemeral"], "Resource should be evicted")
		require.Empty(t, loaded.ManagedByCycleIDs["i-ephemeral"])

		// Cycle 5: New instance with same ID appears
		cycle5Data := &Data{
			Target:    targetName,
			Executor:  "ec2",
			Version:   1,
			CreatedAt: metav1.Now(),
			State: map[string]any{
				"i-ephemeral": map[string]any{"instanceId": "i-ephemeral", "wasRunning": true},
			},
		}
		err = mgr.SaveState(ctx, namespace, planName, targetName, cycle5Data, maxStaleCount, "cycle-005")
		require.NoError(t, err)

		loaded, _ = mgr.Load(ctx, namespace, planName, targetName)
		// Should be treated as NEW with fresh cycle ID
		require.NotNil(t, loaded.State["i-ephemeral"])
		require.Equal(t, "cycle-005", loaded.ManagedByCycleIDs["i-ephemeral"])
	})
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
