package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/log"

	streamingv1alpha1 "github.com/ardikabs/hibernator/api/streaming/v1alpha1"
	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/restore"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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
	log            logr.Logger
	k8sClient      client.Client
	restoreManager *restore.Manager
	eventRecorder  record.EventRecorder

	executionLogs   map[string][]*streamingv1alpha1.LogEntry
	executionLogsMu sync.RWMutex

	executionStatus   map[string]*ExecutionState
	executionStatusMu sync.RWMutex
}

// NewExecutionServiceServer creates a new ExecutionServiceServer
func NewExecutionServiceServer(
	k8sClient client.Client,
	restoreManager *restore.Manager,
	eventRecorder record.EventRecorder,
) *ExecutionServiceServer {
	logger := log.Log.WithName("execution-service")

	return &ExecutionServiceServer{
		log:             logger,
		k8sClient:       k8sClient,
		restoreManager:  restoreManager,
		eventRecorder:   eventRecorder,
		executionLogs:   make(map[string][]*streamingv1alpha1.LogEntry),
		executionStatus: make(map[string]*ExecutionState),
	}
}

// StoreLog stores a log entry for the given execution ID
func (s *ExecutionServiceServer) StoreLog(entry *streamingv1alpha1.LogEntry) error {
	if entry == nil {
		return fmt.Errorf("log entry is nil")
	}

	s.executionLogsMu.Lock()
	defer s.executionLogsMu.Unlock()

	s.executionLogs[entry.ExecutionId] = append(s.executionLogs[entry.ExecutionId], entry)
	s.log.V(1).Info("Stored log entry",
		"executionId", entry.ExecutionId,
		"level", entry.Level,
		"message", entry.Message,
	)

	return nil
}

// ReportProgress handles progress reporting for an execution
func (s *ExecutionServiceServer) ReportProgress(ctx context.Context, req *streamingv1alpha1.ProgressReport) (*streamingv1alpha1.ProgressResponse, error) {
	logger := log.FromContext(ctx)

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
	state.LastUpdate = time.Now()
	s.executionStatusMu.Unlock()

	logger.Info("Progress reported",
		"executionId", req.ExecutionId,
		"progress", req.ProgressPercent,
		"message", req.Message,
	)

	// Get HibernatePlan for event recording
	plan, err := s.getHibernatePlan(ctx, req.ExecutionId)
	if err != nil {
		logger.Error(err, "Failed to get HibernatePlan for progress event")
	} else if plan != nil {
		s.eventRecorder.Eventf(plan, corev1.EventTypeNormal, "ExecutionProgress",
			"Execution %s: %d%% - %s", req.ExecutionId, req.ProgressPercent, req.Message)
	}

	return &streamingv1alpha1.ProgressResponse{
		Acknowledged: true,
	}, nil
}

// ReportCompletion handles completion reporting for an execution
func (s *ExecutionServiceServer) ReportCompletion(ctx context.Context, req *streamingv1alpha1.CompletionReport) (*streamingv1alpha1.CompletionResponse, error) {
	logger := log.FromContext(ctx)

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
	state.LastUpdate = time.Now()
	s.executionStatusMu.Unlock()

	logger.Info("Completion reported",
		"executionId", req.ExecutionId,
		"success", req.Success,
		"message", req.ErrorMessage,
	)

	// Get HibernatePlan for event recording
	plan, err := s.getHibernatePlan(ctx, req.ExecutionId)
	if err != nil {
		logger.Error(err, "Failed to get HibernatePlan for completion event")
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
			"Execution %s: %s", req.ExecutionId, message)
	}

	// Handle restore data if provided
	if len(req.RestoreData) > 0 {
		logger.Info("Storing restore data", "executionId", req.ExecutionId)
		if err := s.storeRestoreData(ctx, req.ExecutionId, string(req.RestoreData)); err != nil {
			logger.Error(err, "Failed to store restore data")
			return &streamingv1alpha1.CompletionResponse{
				Acknowledged: false,
			}, err
		}
	}

	return &streamingv1alpha1.CompletionResponse{
		Acknowledged: true,
	}, nil
}

// Heartbeat handles heartbeat requests
func (s *ExecutionServiceServer) Heartbeat(ctx context.Context, req *streamingv1alpha1.HeartbeatRequest) (*streamingv1alpha1.HeartbeatResponse, error) {
	logger := log.FromContext(ctx)

	s.executionStatusMu.Lock()
	state, exists := s.executionStatus[req.ExecutionId]
	if !exists {
		state = &ExecutionState{
			ExecutionID: req.ExecutionId,
		}
		s.executionStatus[req.ExecutionId] = state
	}
	state.LastUpdate = time.Now()
	s.executionStatusMu.Unlock()

	logger.V(2).Info("Heartbeat received", "executionId", req.ExecutionId)

	return &streamingv1alpha1.HeartbeatResponse{
		Acknowledged: true,
		ServerTime:   time.Now().Format(time.RFC3339),
	}, nil
}

// GetExecutionLogs retrieves all logs for a given execution ID
func (s *ExecutionServiceServer) GetExecutionLogs(executionID string) []*streamingv1alpha1.LogEntry {
	s.executionLogsMu.RLock()
	defer s.executionLogsMu.RUnlock()

	logs, exists := s.executionLogs[executionID]
	if !exists {
		return nil
	}

	// Return a deep copy to avoid concurrent modification
	result := make([]*streamingv1alpha1.LogEntry, len(logs))
	for i, entry := range logs {
		// Create a new LogEntry with copied values
		entryCopy := &streamingv1alpha1.LogEntry{
			ExecutionId: entry.ExecutionId,
			Timestamp:   entry.Timestamp,
			Level:       entry.Level,
			Message:     entry.Message,
		}
		// Copy fields map if present
		if entry.Fields != nil {
			entryCopy.Fields = make(map[string]string, len(entry.Fields))
			for k, v := range entry.Fields {
				entryCopy.Fields[k] = v
			}
		}
		result[i] = entryCopy
	}
	return result
}

// GetExecutionState retrieves the current state for a given execution ID
func (s *ExecutionServiceServer) GetExecutionState(executionID string) *ExecutionState {
	s.executionStatusMu.RLock()
	defer s.executionStatusMu.RUnlock()

	state, exists := s.executionStatus[executionID]
	if !exists {
		return nil
	}

	// Return a copy to avoid concurrent modification
	stateCopy := *state
	return &stateCopy
}

// getHibernatePlan retrieves the HibernatePlan associated with an execution ID
func (s *ExecutionServiceServer) getHibernatePlan(ctx context.Context, executionID string) (*hibernatorv1alpha1.HibernatePlan, error) {
	// Parse execution ID to extract plan namespace and name
	// Format: namespace/planName/targetName/timestamp
	// For now, we'll just return nil - this needs proper implementation
	// based on how execution IDs are structured in the system
	return nil, nil
}

// storeRestoreData stores restore data for an execution
func (s *ExecutionServiceServer) storeRestoreData(ctx context.Context, executionID string, restoreData string) error {
	// This is a placeholder - actual implementation would use restoreManager
	// to persist the restore data to ConfigMap or other storage
	logger := log.FromContext(ctx)
	logger.Info("Restore data storage requested",
		"executionId", executionID,
		"dataLength", len(restoreData),
	)
	return nil
}
