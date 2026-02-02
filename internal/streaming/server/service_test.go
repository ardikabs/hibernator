/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	streamingv1alpha1 "github.com/ardikabs/hibernator/api/streaming/v1alpha1"
	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

func TestNewExecutionServiceServer(t *testing.T) {
	server := NewExecutionServiceServer(nil, nil, nil)

	require.NotNil(t, server, "expected non-nil server")
	assert.NotNil(t, server.executionStatus, "expected executionStatus to be initialized")
}

func TestReportProgress(t *testing.T) {
	server := NewExecutionServiceServer(nil, nil, nil)

	tests := []struct {
		name   string
		report *streamingv1alpha1.ProgressReport
	}{
		{
			name: "new execution",
			report: &streamingv1alpha1.ProgressReport{
				ExecutionId:     "exec-001",
				Phase:           "Starting",
				ProgressPercent: 10,
				Message:         "Initializing",
			},
		},
		{
			name: "progress update",
			report: &streamingv1alpha1.ProgressReport{
				ExecutionId:     "exec-001",
				Phase:           "Running",
				ProgressPercent: 50,
				Message:         "Processing targets",
			},
		},
		{
			name: "near completion",
			report: &streamingv1alpha1.ProgressReport{
				ExecutionId:     "exec-001",
				Phase:           "Finishing",
				ProgressPercent: 90,
				Message:         "Finalizing",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := server.ReportProgress(context.Background(), tt.report)
			require.NoError(t, err)
			assert.True(t, resp.Acknowledged, "expected acknowledged response")

			// Verify state was stored internally
			server.executionStatusMu.RLock()
			state, exists := server.executionStatus[tt.report.ExecutionId]
			server.executionStatusMu.RUnlock()

			require.True(t, exists, "expected state to be stored")
			assert.Equal(t, tt.report.Phase, state.Phase)
			assert.Equal(t, tt.report.ProgressPercent, state.ProgressPercent)
			assert.Equal(t, tt.report.Message, state.Message)
		})
	}
}

func TestReportCompletion(t *testing.T) {
	server := NewExecutionServiceServer(nil, nil, nil)

	tests := []struct {
		name    string
		report  *streamingv1alpha1.CompletionReport
		wantErr bool
	}{
		{
			name: "successful completion",
			report: &streamingv1alpha1.CompletionReport{
				ExecutionId:  "exec-success",
				Success:      true,
				DurationMs:   5000,
				ErrorMessage: "",
			},
			wantErr: false,
		},
		{
			name: "failed completion",
			report: &streamingv1alpha1.CompletionReport{
				ExecutionId:  "exec-failed",
				Success:      false,
				DurationMs:   3000,
				ErrorMessage: "connection timeout",
			},
			wantErr: false,
		},
		{
			name: "completion with restore data",
			report: &streamingv1alpha1.CompletionReport{
				ExecutionId: "exec-restore",
				Success:     true,
				DurationMs:  10000,
				RestoreData: json.RawMessage(`{"instanceId": "i-12345", "state": "stopped"}`),
			},
			wantErr: false,
		},
		{
			name: "completion with invalid restore data",
			report: &streamingv1alpha1.CompletionReport{
				ExecutionId: "exec-invalid-restore",
				Success:     true,
				DurationMs:  1000,
				RestoreData: json.RawMessage(`{invalid json}`),
			},
			wantErr: false, // Should log error but not fail
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := server.ReportCompletion(context.Background(), tt.report)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.True(t, resp.Acknowledged, "expected acknowledged response")

			// Verify state was stored internally
			server.executionStatusMu.RLock()
			state, exists := server.executionStatus[tt.report.ExecutionId]
			server.executionStatusMu.RUnlock()

			require.True(t, exists, "expected state to be stored")
			assert.True(t, state.Completed, "expected completed to be true")
			assert.Equal(t, tt.report.Success, state.Success)
			assert.Equal(t, tt.report.ErrorMessage, state.Error)
		})
	}
}

func TestHeartbeat(t *testing.T) {
	server := NewExecutionServiceServer(nil, nil, nil)

	// First, create an execution state
	_, _ = server.ReportProgress(context.Background(), &streamingv1alpha1.ProgressReport{
		ExecutionId:     "exec-hb",
		Phase:           "Running",
		ProgressPercent: 25,
	})

	// Record initial heartbeat time
	server.executionStatusMu.RLock()
	initialHeartbeat := server.executionStatus["exec-hb"].LastUpdate
	server.executionStatusMu.RUnlock()

	// Wait a bit and send heartbeat
	time.Sleep(10 * time.Millisecond)

	req := &streamingv1alpha1.HeartbeatRequest{
		ExecutionId: "exec-hb",
	}

	resp, err := server.Heartbeat(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, resp.Acknowledged, "expected acknowledged response")
	assert.NotEmpty(t, resp.ServerTime, "expected non-empty server time")

	// Verify heartbeat was updated
	server.executionStatusMu.RLock()
	state := server.executionStatus["exec-hb"]
	server.executionStatusMu.RUnlock()

	assert.True(t, state.LastUpdate.After(initialHeartbeat), "expected heartbeat time to be updated")
}

func TestHeartbeat_NonExistentExecution(t *testing.T) {
	server := NewExecutionServiceServer(nil, nil, nil)

	req := &streamingv1alpha1.HeartbeatRequest{
		ExecutionId: "non-existent",
	}

	resp, err := server.Heartbeat(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, resp.Acknowledged, "expected acknowledged response even for non-existent execution")
}

func TestStoreLog(t *testing.T) {
	// Create a fake client with a runner Job
	scheme := runtime.NewScheme()
	require.NoError(t, batchv1.AddToScheme(scheme))
	require.NoError(t, hibernatorv1alpha1.AddToScheme(scheme))

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hibernate-runner-test-plan-test-target-abcd",
			Namespace: "default",
			Labels: map[string]string{
				LabelExecutionID: "test-plan-test-target-1234567890",
				LabelPlan:        "test-plan",
				LabelTarget:      "test-target",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(job).
		Build()

	server := NewExecutionServiceServer(fakeClient, nil, nil)

	// Test storing a log entry
	entry := &streamingv1alpha1.LogEntry{
		ExecutionId: "test-plan-test-target-1234567890",
		Timestamp:   time.Now().Format(time.RFC3339),
		Level:       "INFO",
		Message:     "Test log message",
		Fields: map[string]string{
			"key1": "value1",
		},
	}

	err := server.StoreLog(context.Background(), entry)
	require.NoError(t, err)
}

func TestStoreLog_NilEntry(t *testing.T) {
	server := NewExecutionServiceServer(nil, nil, nil)

	err := server.StoreLog(context.Background(), nil)
	require.Error(t, err, "expected error for nil entry")
}

func TestStoreLog_JobNotFound(t *testing.T) {
	// Create a fake client without any Jobs
	scheme := runtime.NewScheme()
	require.NoError(t, batchv1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	server := NewExecutionServiceServer(fakeClient, nil, nil)

	// Test storing a log entry when Job doesn't exist
	entry := &streamingv1alpha1.LogEntry{
		ExecutionId: "nonexistent-execution",
		Timestamp:   time.Now().Format(time.RFC3339),
		Level:       "INFO",
		Message:     "Test log message",
	}

	// Should not error - just logs with "unknown" metadata
	err := server.StoreLog(context.Background(), entry)
	require.NoError(t, err, "StoreLog() should not error when Job not found")
}

func TestGetExecutionMetadata(t *testing.T) {
	// Create a fake client with a runner Job
	scheme := runtime.NewScheme()
	require.NoError(t, batchv1.AddToScheme(scheme))

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hibernate-runner-my-plan-my-target-xyz",
			Namespace: "test-namespace",
			Labels: map[string]string{
				LabelExecutionID: "my-plan-my-target-1234567890",
				LabelPlan:        "my-plan",
				LabelTarget:      "my-target",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(job).
		Build()

	server := NewExecutionServiceServer(fakeClient, nil, nil)

	meta, err := server.getExecutionMetadata(context.Background(), "my-plan-my-target-1234567890")
	require.NoError(t, err)

	assert.Equal(t, "test-namespace", meta.Namespace)
	assert.Equal(t, "my-plan", meta.PlanName)
	assert.Equal(t, "my-target", meta.TargetName)
	assert.Equal(t, "my-plan-my-target-1234567890", meta.ExecutionID)
}

func TestGetExecutionMetadata_NotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, batchv1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	server := NewExecutionServiceServer(fakeClient, nil, nil)

	_, err := server.getExecutionMetadata(context.Background(), "nonexistent-execution")
	require.Error(t, err, "expected error for non-existent execution")
}

func TestGetExecutionMetadata_MissingPlanLabel(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, batchv1.AddToScheme(scheme))

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hibernate-runner-broken",
			Namespace: "default",
			Labels: map[string]string{
				LabelExecutionID: "broken-execution",
				// Missing LabelPlan
				LabelTarget: "my-target",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(job).
		Build()

	server := NewExecutionServiceServer(fakeClient, nil, nil)

	_, err := server.getExecutionMetadata(context.Background(), "broken-execution")
	require.Error(t, err, "expected error for missing plan label")
}

func TestGetOrCacheExecutionMetadata(t *testing.T) {
	// Create a fake client with a runner Job
	scheme := runtime.NewScheme()
	require.NoError(t, batchv1.AddToScheme(scheme))

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hibernate-runner-cached",
			Namespace: "test-namespace",
			Labels: map[string]string{
				LabelExecutionID: "cached-exec-123",
				LabelPlan:        "my-plan",
				LabelTarget:      "my-target",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(job).
		Build()

	server := NewExecutionServiceServer(fakeClient, nil, nil)

	// First call should query K8s API and cache the result
	meta1, err := server.getOrCacheExecutionMetadata(context.Background(), "cached-exec-123")
	require.NoError(t, err)
	assert.Equal(t, "my-plan", meta1.PlanName)

	// Verify metadata is cached
	server.metadataCacheMu.RLock()
	cachedMeta, exists := server.metadataCache["cached-exec-123"]
	server.metadataCacheMu.RUnlock()
	require.True(t, exists, "expected metadata to be cached")
	assert.Equal(t, "my-plan", cachedMeta.PlanName)

	// Second call should return cached result (same pointer)
	meta2, err := server.getOrCacheExecutionMetadata(context.Background(), "cached-exec-123")
	require.NoError(t, err)
	assert.Same(t, meta1, meta2, "expected same cached object")
}

func TestEvictMetadataCache(t *testing.T) {
	server := NewExecutionServiceServer(nil, nil, nil)

	// Manually add metadata to cache
	server.metadataCacheMu.Lock()
	server.metadataCache["exec-to-evict"] = &ExecutionMetadata{
		Namespace:   "test-ns",
		PlanName:    "test-plan",
		TargetName:  "test-target",
		ExecutionID: "exec-to-evict",
	}
	server.metadataCacheMu.Unlock()

	// Verify it's in cache
	server.metadataCacheMu.RLock()
	_, exists := server.metadataCache["exec-to-evict"]
	server.metadataCacheMu.RUnlock()
	require.True(t, exists, "expected metadata to be in cache before eviction")

	// Evict
	server.evictMetadataCache("exec-to-evict")

	// Verify it's gone
	server.metadataCacheMu.RLock()
	_, exists = server.metadataCache["exec-to-evict"]
	server.metadataCacheMu.RUnlock()
	assert.False(t, exists, "expected metadata to be evicted from cache")
}

func TestFetchHibernatePlan(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, hibernatorv1alpha1.AddToScheme(scheme))

	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-plan",
			Namespace: "test-namespace",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(plan).
		Build()

	server := NewExecutionServiceServer(fakeClient, nil, nil)

	result, err := server.fetchHibernatePlan(context.Background(), "test-namespace", "test-plan")
	require.NoError(t, err)
	require.NotNil(t, result, "expected non-nil plan")
	assert.Equal(t, "test-plan", result.Name)
}

func TestFetchHibernatePlan_NotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, hibernatorv1alpha1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	server := NewExecutionServiceServer(fakeClient, nil, nil)

	_, err := server.fetchHibernatePlan(context.Background(), "test-namespace", "nonexistent")
	require.Error(t, err, "expected error for non-existent plan")
}

func TestConcurrentAccess(t *testing.T) {
	server := NewExecutionServiceServer(nil, nil, nil)

	done := make(chan bool)

	// Start multiple goroutines accessing the server
	for i := 0; i < 10; i++ {
		go func(id int) {
			execID := "exec-concurrent"
			for j := 0; j < 100; j++ {
				// Progress updates
				_, _ = server.ReportProgress(context.Background(), &streamingv1alpha1.ProgressReport{
					ExecutionId:     execID,
					ProgressPercent: int32((id*100 + j) % 100),
				})

				// Heartbeats
				_, _ = server.Heartbeat(context.Background(), &streamingv1alpha1.HeartbeatRequest{
					ExecutionId: execID,
				})
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestMultipleExecutions(t *testing.T) {
	server := NewExecutionServiceServer(nil, nil, nil)

	execIDs := []string{"exec-1", "exec-2", "exec-3"}

	// Create progress for each execution
	for i, id := range execIDs {
		_, err := server.ReportProgress(context.Background(), &streamingv1alpha1.ProgressReport{
			ExecutionId:     id,
			Phase:           "Running",
			ProgressPercent: int32((i + 1) * 25),
		})
		require.NoError(t, err)
	}

	// Verify each execution has its own state
	for i, id := range execIDs {
		server.executionStatusMu.RLock()
		state, exists := server.executionStatus[id]
		server.executionStatusMu.RUnlock()

		require.True(t, exists, "expected state for %s", id)
		expectedProgress := int32((i + 1) * 25)
		assert.Equal(t, expectedProgress, state.ProgressPercent, "execution %s progress mismatch", id)
	}
}
