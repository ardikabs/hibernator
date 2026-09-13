/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestResourceStatusExcludedRoundTrip(t *testing.T) {
	// Unset markers stay absent on the wire (backward compatible).
	raw, err := json.Marshal(ResourceStatus{})
	require.NoError(t, err)
	require.Equal(t, "{}", string(raw))

	raw, err = json.Marshal(ResourceStatus{Excluded: true})
	require.NoError(t, err)
	require.Contains(t, string(raw), `"excluded":true`)

	var back ResourceStatus
	require.NoError(t, json.Unmarshal(raw, &back))
	require.True(t, back.Excluded)
}

// markExcluded simulates CLI prune: flip the marker on persisted data
// without touching state.
func markExcluded(t *testing.T, mgr *Manager, ctx context.Context, namespace, planName, targetName, key string) {
	t.Helper()
	data, err := mgr.Load(ctx, namespace, planName, targetName)
	require.NoError(t, err)
	require.NotNil(t, data)
	st := data.Status[key]
	st.Excluded = true
	if data.Status == nil {
		data.Status = map[string]ResourceStatus{}
	}
	data.Status[key] = st
	require.NoError(t, mgr.Save(ctx, namespace, planName, targetName, data))
}

// TestManager_SaveState_ExcludedLifecycle pins the marker lifecycle:
// a fresh capture in a new cycle clears Excluded (Monday full run is
// whole again), while a same-cycle preserve keeps operator intent.
func TestManager_SaveState_ExcludedLifecycle(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	newMgr := func() (*Manager, context.Context) {
		return NewManager(fake.NewClientBuilder().WithScheme(scheme).Build(), logr.Discard()), context.Background()
	}
	now := metav1.Now()
	demanded := func(id string) map[string]interface{} {
		return map[string]interface{}{"instanceId": id, "wasRunning": true}
	}

	t.Run("fresh capture in a new cycle clears the marker", func(t *testing.T) {
		mgr, ctx := newMgr()
		data := &Data{
			Target: "db", Executor: "rds", Version: 1,
			CreatedAt: now, CapturedAt: &now,
			State: map[string]interface{}{"i-1": demanded("i-1"), "i-2": demanded("i-2")},
		}
		require.NoError(t, mgr.SaveState(ctx, "ns", "p", "db", data, 3, "cycle-1"))

		markExcluded(t, mgr, ctx, "ns", "p", "db", "i-2")

		data2 := &Data{
			Target: "db", Executor: "rds", Version: 1,
			CreatedAt: now, CapturedAt: &now,
			State: map[string]interface{}{"i-1": demanded("i-1"), "i-2": demanded("i-2")},
		}
		require.NoError(t, mgr.SaveState(ctx, "ns", "p", "db", data2, 3, "cycle-2"))

		loaded, err := mgr.Load(ctx, "ns", "p", "db")
		require.NoError(t, err)
		require.False(t, loaded.Status["i-2"].Excluded, "fresh capture must clear the marker")
		require.Contains(t, loaded.State, "i-2")
	})

	t.Run("same-cycle preserve keeps the marker", func(t *testing.T) {
		mgr, ctx := newMgr()
		idle := map[string]interface{}{"instanceId": "i-1", "wasRunning": false}
		data := &Data{
			Target: "db", Executor: "rds", Version: 1,
			CreatedAt: now, CapturedAt: &now,
			State: map[string]interface{}{"i-1": idle},
		}
		require.NoError(t, mgr.SaveState(ctx, "ns", "p", "db", data, 3, "cycle-1"))

		markExcluded(t, mgr, ctx, "ns", "p", "db", "i-1")

		// Same cycle, re-reported without demanded state: preserve path.
		data2 := &Data{
			Target: "db", Executor: "rds", Version: 1,
			CreatedAt: now, CapturedAt: &now,
			State: map[string]interface{}{"i-1": idle},
		}
		require.NoError(t, mgr.SaveState(ctx, "ns", "p", "db", data2, 3, "cycle-1"))

		loaded, err := mgr.Load(ctx, "ns", "p", "db")
		require.NoError(t, err)
		require.True(t, loaded.Status["i-1"].Excluded, "same-cycle preserve must keep operator intent")
	})
}
