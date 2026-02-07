package server

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	streamingv1alpha1 "github.com/ardikabs/hibernator/api/streaming/v1alpha1"
	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// ExecutionMetadata holds metadata about an execution extracted from the runner Job
type ExecutionMetadata struct {
	Namespace   string
	PlanName    string
	TargetName  string
	ExecutionID string
}

// ExecutionState holds the current state of an execution
type ExecutionState struct {
	ExecutionID     string
	Phase           string
	ProgressPercent int32
	Message         string
	LastUpdate      time.Time
	Completed       bool
	Success         bool
	Error           string
}

// ExecutionServiceServer implements the business logic for execution tracking
type ExecutionServiceServer struct {
	streamingv1alpha1.UnimplementedExecutionServiceServer

	clock clock.Clock

	log           logr.Logger
	k8sClient     client.Client
	eventRecorder record.EventRecorder

	executionStatus   map[string]*ExecutionState
	executionStatusMu sync.RWMutex

	// metadataCache caches execution metadata to avoid repeated K8s API queries.
	// Metadata is cached on first access and evicted when execution completes.
	metadataCache   map[string]*ExecutionMetadata
	metadataCacheMu sync.RWMutex
}

// NewExecutionServiceServer creates a new ExecutionServiceServer
func NewExecutionServiceServer(
	k8sClient client.Client,
	eventRecorder record.EventRecorder,
	clk clock.Clock,
) *ExecutionServiceServer {
	logger := log.Log.WithName("execution-service")

	return &ExecutionServiceServer{
		clock:           clk,
		log:             logger,
		k8sClient:       k8sClient,
		eventRecorder:   eventRecorder,
		executionStatus: make(map[string]*ExecutionState),
		metadataCache:   make(map[string]*ExecutionMetadata),
	}
}

// StreamLogs receives a stream of log entries from a runner via gRPC.
// This is a transport-layer method that delegates to ExecutionServiceServer.
func (s *ExecutionServiceServer) StreamLogs(stream grpc.ClientStreamingServer[streamingv1alpha1.LogEntry, streamingv1alpha1.StreamLogsResponse]) error {
	ctx := stream.Context()
	var count int64
	var executionID string
	var lastLogLevel string
	startTime := time.Now()

	// Cleanup tracking on stream exit
	defer func() {
		duration := time.Since(startTime)
		s.log.V(1).Info("stream closed",
			"executionId", executionID,
			"count", count,
			"duration", duration,
		)
	}()

	for {
		entry, err := stream.Recv()
		if err == io.EOF {
			s.log.V(1).Info("log stream completed", "executionId", executionID, "count", count)
			return stream.SendAndClose(&streamingv1alpha1.StreamLogsResponse{
				ReceivedCount: count,
			})
		}
		if err != nil {
			// Check if this is a graceful stream closure
			if isGracefulStreamClosure(err) {
				// Detect if runner failed (last log was ERROR)
				if lastLogLevel == "ERROR" {
					s.log.Info("runner closed stream after error",
						"executionId", executionID,
						"count", count,
						"reason", err.Error(),
					)
				} else {
					s.log.Info("runner closed stream normally",
						"executionId", executionID,
						"count", count,
						"reason", err.Error(),
					)
				}
				return nil
			}

			// Log unexpected errors at ERROR level
			s.log.Error(err, "unexpected stream error",
				"executionId", executionID,
				"count", count,
			)
			return status.Errorf(codes.Internal, "receive error: %v", err)
		}

		executionID = entry.ExecutionId
		lastLogLevel = entry.Level
		count++

		// Delegate to business logic layer (EmitLog pipes logs with full context)
		if err := s.EmitLog(ctx, entry); err != nil {
			s.log.Error(err, "failed to process log entry")
			return status.Errorf(codes.Internal, "process error: %v", err)
		}
	}
}

// EmitLog forwards a log entry to the controller's logging sink with execution context.
// Logs are piped to the same output as controller logs, allowing them to be
// viewed via "kubectl logs" on the controller pod with full execution context.
func (s *ExecutionServiceServer) EmitLog(ctx context.Context, entry *streamingv1alpha1.LogEntry) error {
	if entry == nil {
		return fmt.Errorf("log entry is nil")
	}

	log := s.log.WithName("runner-logs")

	// Get execution metadata from cache (queries K8s API only on first access)
	meta, err := s.getOrCacheExecutionMetadata(ctx, entry.ExecutionId)
	if err != nil {
		// Log with unknown metadata but include the error
		meta = &ExecutionMetadata{
			Namespace:   "unknown",
			PlanName:    "unknown",
			TargetName:  "unknown",
			ExecutionID: entry.ExecutionId,
		}
	}

	// Build key-value pairs for structured logging
	kvs := []interface{}{
		"namespace", meta.Namespace,
		"plan", meta.PlanName,
		"target", meta.TargetName,
		"executionId", entry.ExecutionId,
		"timestamp", entry.Timestamp,
	}

	// Append any additional fields from the log entry
	for k, v := range entry.Fields {
		kvs = append(kvs, k, v)
	}

	// Add metadata lookup error if present
	if err != nil {
		kvs = append(kvs, "metadataError", err.Error())
	}

	// Emit log at appropriate level
	switch entry.Level {
	case "ERROR":
		log.Info(entry.Message, kvs...)
	case "WARN", "INFO":
		log.Info(entry.Message, kvs...)
	default:
		log.V(1).Info(entry.Message, kvs...)
	}

	return nil
}

// ReportProgress handles progress reporting for an execution
func (s *ExecutionServiceServer) ReportProgress(ctx context.Context, req *streamingv1alpha1.ProgressReport) (*streamingv1alpha1.ProgressResponse, error) {

	s.executionStatusMu.Lock()
	state, exists := s.executionStatus[req.ExecutionId]
	if !exists {
		state = &ExecutionState{
			ExecutionID: req.ExecutionId,
		}
		s.executionStatus[req.ExecutionId] = state
	}
	state.Phase = req.Phase
	state.ProgressPercent = req.ProgressPercent
	state.Message = req.Message
	state.LastUpdate = s.clock.Now()
	s.executionStatusMu.Unlock()

	// Get execution metadata from cache (queries K8s API only on first access)
	meta, err := s.getOrCacheExecutionMetadata(ctx, req.ExecutionId)
	if err != nil {
		s.log.Error(err, "Failed to get execution metadata for progress event",
			"executionId", req.ExecutionId)
		// Continue without metadata - just log with executionId
		s.log.Info("Progress reported",
			"executionId", req.ExecutionId,
			"progress", req.ProgressPercent,
			"message", req.Message,
		)
		return &streamingv1alpha1.ProgressResponse{Acknowledged: true}, nil
	}

	s.log.Info("Progress reported",
		"namespace", meta.Namespace,
		"plan", meta.PlanName,
		"target", meta.TargetName,
		"executionId", req.ExecutionId,
		"progress", req.ProgressPercent,
		"message", req.Message,
	)

	// Fetch HibernatePlan for event recording
	plan, err := s.fetchHibernatePlan(ctx, meta.Namespace, meta.PlanName)
	if err != nil {
		s.log.Error(err, "Failed to fetch HibernatePlan for progress event",
			"namespace", meta.Namespace,
			"plan", meta.PlanName)
	} else if plan != nil {
		s.eventRecorder.Eventf(plan, corev1.EventTypeNormal, "ExecutionProgress",
			"[%s/%s] target=%s: %d%% - %s",
			meta.Namespace, meta.PlanName, meta.TargetName, req.ProgressPercent, req.Message)
	}

	return &streamingv1alpha1.ProgressResponse{
		Acknowledged: true,
	}, nil
}

// ReportCompletion handles completion reporting for an execution
func (s *ExecutionServiceServer) ReportCompletion(ctx context.Context, req *streamingv1alpha1.CompletionReport) (*streamingv1alpha1.CompletionResponse, error) {
	s.log.V(1).Info("Received completion report",
		"executionId", req.ExecutionId,
		"success", req.Success,
		"errorMsg", req.ErrorMessage,
	)

	s.executionStatusMu.Lock()
	state, exists := s.executionStatus[req.ExecutionId]
	if !exists {
		state = &ExecutionState{
			ExecutionID: req.ExecutionId,
		}
		s.executionStatus[req.ExecutionId] = state
	}
	state.Completed = true
	state.Success = req.Success
	state.Error = req.ErrorMessage
	state.LastUpdate = s.clock.Now()
	s.executionStatusMu.Unlock()

	// Get execution metadata from cache (queries K8s API only on first access)
	meta, err := s.getOrCacheExecutionMetadata(ctx, req.ExecutionId)
	if err != nil {
		s.log.Error(err, "Failed to get execution metadata for completion event",
			"executionId", req.ExecutionId)

		// Continue without metadata - just log with executionId
		s.log.Info("Completion reported",
			"executionId", req.ExecutionId,
			"success", req.Success,
			"message", req.ErrorMessage,
		)
	} else {
		s.log.Info("Completion reported",
			"namespace", meta.Namespace,
			"plan", meta.PlanName,
			"target", meta.TargetName,
			"executionId", req.ExecutionId,
			"success", req.Success,
			"message", req.ErrorMessage,
		)

		// Fetch HibernatePlan for event recording
		plan, fetchErr := s.fetchHibernatePlan(ctx, meta.Namespace, meta.PlanName)
		if fetchErr != nil {
			s.log.Error(fetchErr, "Failed to fetch HibernatePlan for completion event",
				"namespace", meta.Namespace,
				"plan", meta.PlanName)
		} else if plan != nil {
			eventType := corev1.EventTypeNormal
			reason := "ExecutionCompleted"
			if !req.Success {
				eventType = corev1.EventTypeWarning
				reason = "ExecutionFailed"
			}
			message := "Completed successfully"
			if req.ErrorMessage != "" {
				message = req.ErrorMessage
			}
			s.eventRecorder.Eventf(plan, eventType, reason,
				"[%s/%s] target=%s: %s",
				meta.Namespace, meta.PlanName, meta.TargetName, message)
		}
	}

	// Clean up all execution state (metadata cache + execution status)
	s.cleanupExecution(req.ExecutionId)

	return &streamingv1alpha1.CompletionResponse{
		Acknowledged: true,
	}, nil
}

// Heartbeat handles heartbeat requests
func (s *ExecutionServiceServer) Heartbeat(ctx context.Context, req *streamingv1alpha1.HeartbeatRequest) (*streamingv1alpha1.HeartbeatResponse, error) {
	s.executionStatusMu.Lock()
	state, exists := s.executionStatus[req.ExecutionId]
	if !exists {
		state = &ExecutionState{
			ExecutionID: req.ExecutionId,
		}
		s.executionStatus[req.ExecutionId] = state
	}
	state.LastUpdate = s.clock.Now()
	s.executionStatusMu.Unlock()

	s.log.V(2).Info("Heartbeat received", "executionId", req.ExecutionId)

	return &streamingv1alpha1.HeartbeatResponse{
		Acknowledged: true,
		ServerTime:   s.clock.Now().Format(time.RFC3339),
	}, nil
}

// getOrCacheExecutionMetadata retrieves metadata from cache or queries K8s API on cache miss.
// This prevents repeated API calls for the same execution during log streaming.
func (s *ExecutionServiceServer) getOrCacheExecutionMetadata(ctx context.Context, executionID string) (*ExecutionMetadata, error) {
	// Check cache first (read lock)
	s.metadataCacheMu.RLock()
	if meta, exists := s.metadataCache[executionID]; exists {
		s.metadataCacheMu.RUnlock()
		return meta, nil
	}
	s.metadataCacheMu.RUnlock()

	// Cache miss - query K8s API
	meta, err := s.getExecutionMetadata(ctx, executionID)
	if err != nil {
		return nil, err
	}

	// Store in cache (write lock)
	s.metadataCacheMu.Lock()
	s.metadataCache[executionID] = meta
	s.metadataCacheMu.Unlock()

	return meta, nil
}

// evictMetadataCache removes metadata from cache when execution completes.
func (s *ExecutionServiceServer) evictMetadataCache(executionID string) {
	s.metadataCacheMu.Lock()
	delete(s.metadataCache, executionID)
	s.metadataCacheMu.Unlock()
}

// cleanupExecution removes all state for a completed or failed execution.
// This prevents memory leaks by cleaning both metadataCache and executionStatus.
func (s *ExecutionServiceServer) cleanupExecution(executionID string) {
	s.evictMetadataCache(executionID)

	s.executionStatusMu.Lock()
	delete(s.executionStatus, executionID)
	s.executionStatusMu.Unlock()
}

// StartCleanupRoutine starts a background goroutine to clean up stale executions.
// This handles cases where runners crash without calling ReportCompletion.
// staleDuration defines how long an execution can be idle before cleanup (e.g., 1 hour).
func (s *ExecutionServiceServer) StartCleanupRoutine(ctx context.Context, staleDuration time.Duration) {
	ticker := time.NewTicker(staleDuration / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.log.Info("stopping cleanup routine")
			return
		case <-ticker.C:
			s.cleanupStaleExecutions(staleDuration)
		}
	}
}

// cleanupStaleExecutions removes executions that haven't been updated within staleDuration.
func (s *ExecutionServiceServer) cleanupStaleExecutions(staleDuration time.Duration) {
	now := s.clock.Now()
	var staleIDs []string

	// Collect stale execution IDs (read lock)
	s.executionStatusMu.RLock()
	for id, state := range s.executionStatus {
		if now.Sub(state.LastUpdate) > staleDuration {
			staleIDs = append(staleIDs, id)
		}
	}
	s.executionStatusMu.RUnlock()

	// Clean up stale executions
	for _, id := range staleIDs {
		s.log.Info("cleaning up stale execution", "executionId", id, "staleDuration", staleDuration)
		s.cleanupExecution(id)
	}
}

// getExecutionMetadata retrieves metadata about an execution by querying the runner Job.
// It queries Jobs by the execution ID label and extracts namespace, plan name, and target name.
func (s *ExecutionServiceServer) getExecutionMetadata(ctx context.Context, executionID string) (*ExecutionMetadata, error) {
	// If no k8s client available, return unknown metadata to avoid panic
	if s.k8sClient == nil {
		return &ExecutionMetadata{
			Namespace:   "unknown",
			PlanName:    "unknown",
			TargetName:  "unknown",
			ExecutionID: executionID,
		}, nil
	}

	// List Jobs with matching execution ID label
	var jobList batchv1.JobList
	if err := s.k8sClient.List(ctx, &jobList, client.MatchingLabels{
		wellknown.LabelExecutionID: executionID,
	}); err != nil {
		return nil, fmt.Errorf("failed to list jobs for execution %s: %w", executionID, err)
	}

	if len(jobList.Items) == 0 {
		return nil, fmt.Errorf("no job found for execution %s", executionID)
	}

	// Use the first matching job (there should only be one per execution)
	job := &jobList.Items[0]

	// Extract metadata from job labels and namespace
	meta := &ExecutionMetadata{
		Namespace:   job.Namespace,
		PlanName:    job.Labels[wellknown.LabelPlan],
		TargetName:  job.Labels[wellknown.LabelTarget],
		ExecutionID: executionID,
	}

	if meta.PlanName == "" {
		return nil, fmt.Errorf("job %s/%s missing plan label", job.Namespace, job.Name)
	}

	return meta, nil
}

// fetchHibernatePlan retrieves a HibernatePlan by namespace and name.
// Returns nil if the k8s client is not available.
func (s *ExecutionServiceServer) fetchHibernatePlan(ctx context.Context, namespace, name string) (*hibernatorv1alpha1.HibernatePlan, error) {
	// If no k8s client available, return nil to skip event emission
	if s.k8sClient == nil {
		return nil, nil
	}

	plan := &hibernatorv1alpha1.HibernatePlan{}
	if err := s.k8sClient.Get(ctx, client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}, plan); err != nil {
		return nil, err
	}
	return plan, nil
}
