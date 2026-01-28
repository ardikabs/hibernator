/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	streamingv1alpha1 "github.com/ardikabs/hibernator/api/streaming/v1alpha1"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/streaming/auth"
)

// ExecutionServiceServer implements the gRPC ExecutionService.
type ExecutionServiceServer struct {
	log            logr.Logger
	k8sClient      client.Client
	restoreManager *restore.Manager

	// executionLogs stores logs per execution ID.
	executionLogs   map[string][]streamingv1alpha1.LogEntry
	executionLogsMu sync.RWMutex

	// executionStatus tracks execution progress.
	executionStatus   map[string]*ExecutionState
	executionStatusMu sync.RWMutex
}

// ExecutionState tracks the current state of an execution.
type ExecutionState struct {
	ExecutionID     string
	Phase           string
	ProgressPercent int32
	Message         string
	LastHeartbeat   time.Time
	StartedAt       time.Time
	Completed       bool
	Success         bool
	Error           string
}

// NewExecutionServiceServer creates a new execution service server.
func NewExecutionServiceServer(log logr.Logger, k8sClient client.Client, restoreManager *restore.Manager) *ExecutionServiceServer {
	return &ExecutionServiceServer{
		log:             log.WithName("execution-service"),
		k8sClient:       k8sClient,
		restoreManager:  restoreManager,
		executionLogs:   make(map[string][]streamingv1alpha1.LogEntry),
		executionStatus: make(map[string]*ExecutionState),
	}
}

// StreamLogs receives a stream of log entries from a runner.
func (s *ExecutionServiceServer) StreamLogs(stream grpc.ClientStreamingServer[streamingv1alpha1.LogEntry, streamingv1alpha1.StreamLogsResponse]) error {
	var count int64
	var executionID string

	for {
		entry, err := stream.Recv()
		if err == io.EOF {
			s.log.Info("log stream completed", "executionId", executionID, "count", count)
			return stream.SendAndClose(&streamingv1alpha1.StreamLogsResponse{
				ReceivedCount: count,
			})
		}
		if err != nil {
			s.log.Error(err, "error receiving log entry")
			return status.Errorf(codes.Internal, "receive error: %v", err)
		}

		executionID = entry.ExecutionID
		count++

		// Store log entry
		s.executionLogsMu.Lock()
		s.executionLogs[executionID] = append(s.executionLogs[executionID], *entry)
		s.executionLogsMu.Unlock()

		// Log at appropriate level
		switch entry.Level {
		case "ERROR":
			s.log.Error(nil, entry.Message, "executionId", executionID, "fields", entry.Fields)
		case "WARN":
			s.log.Info(entry.Message, "executionId", executionID, "level", "warn", "fields", entry.Fields)
		default:
			s.log.V(1).Info(entry.Message, "executionId", executionID, "level", entry.Level, "fields", entry.Fields)
		}
	}
}

// ReportProgress handles progress updates from runners.
func (s *ExecutionServiceServer) ReportProgress(ctx context.Context, report *streamingv1alpha1.ProgressReport) (*streamingv1alpha1.ProgressResponse, error) {
	s.log.Info("progress report received",
		"executionId", report.ExecutionID,
		"phase", report.Phase,
		"progress", report.ProgressPercent,
		"message", report.Message,
	)

	s.executionStatusMu.Lock()
	state, exists := s.executionStatus[report.ExecutionID]
	if !exists {
		state = &ExecutionState{
			ExecutionID: report.ExecutionID,
			StartedAt:   time.Now(),
		}
		s.executionStatus[report.ExecutionID] = state
	}
	state.Phase = report.Phase
	state.ProgressPercent = report.ProgressPercent
	state.Message = report.Message
	state.LastHeartbeat = time.Now()
	s.executionStatusMu.Unlock()

	return &streamingv1alpha1.ProgressResponse{Acknowledged: true}, nil
}

// ReportCompletion handles completion reports from runners.
func (s *ExecutionServiceServer) ReportCompletion(ctx context.Context, report *streamingv1alpha1.CompletionReport) (*streamingv1alpha1.CompletionResponse, error) {
	s.log.Info("completion report received",
		"executionId", report.ExecutionID,
		"success", report.Success,
		"duration", report.DurationMs,
	)

	// Update execution state
	s.executionStatusMu.Lock()
	state, exists := s.executionStatus[report.ExecutionID]
	if !exists {
		state = &ExecutionState{
			ExecutionID: report.ExecutionID,
			StartedAt:   time.Now(),
		}
		s.executionStatus[report.ExecutionID] = state
	}
	state.Completed = true
	state.Success = report.Success
	state.Error = report.ErrorMessage
	s.executionStatusMu.Unlock()

	var restoreRef string

	// Store restore data if present
	if len(report.RestoreData) > 0 && s.restoreManager != nil {
		var restoreState map[string]interface{}
		if err := json.Unmarshal(report.RestoreData, &restoreState); err != nil {
			s.log.Error(err, "failed to unmarshal restore data", "executionId", report.ExecutionID)
		} else {
			// Extract plan and target from execution ID
			// Format: <plan>-<target>-<timestamp>
			// For now, we'll need the caller to provide this context
			// TODO: Parse from execution ID or require in completion report
			s.log.Info("restore data received", "executionId", report.ExecutionID, "dataSize", len(report.RestoreData))
		}
	}

	return &streamingv1alpha1.CompletionResponse{
		Acknowledged: true,
		RestoreRef:   restoreRef,
	}, nil
}

// Heartbeat handles heartbeat messages from runners.
func (s *ExecutionServiceServer) Heartbeat(ctx context.Context, req *streamingv1alpha1.HeartbeatRequest) (*streamingv1alpha1.HeartbeatResponse, error) {
	s.executionStatusMu.Lock()
	if state, exists := s.executionStatus[req.ExecutionID]; exists {
		state.LastHeartbeat = time.Now()
	}
	s.executionStatusMu.Unlock()

	return &streamingv1alpha1.HeartbeatResponse{
		Acknowledged: true,
		ServerTime:   time.Now(),
	}, nil
}

// GetExecutionLogs returns the logs for an execution.
func (s *ExecutionServiceServer) GetExecutionLogs(executionID string) []streamingv1alpha1.LogEntry {
	s.executionLogsMu.RLock()
	defer s.executionLogsMu.RUnlock()

	if logs, exists := s.executionLogs[executionID]; exists {
		result := make([]streamingv1alpha1.LogEntry, len(logs))
		copy(result, logs)
		return result
	}
	return nil
}

// GetExecutionState returns the current state of an execution.
func (s *ExecutionServiceServer) GetExecutionState(executionID string) *ExecutionState {
	s.executionStatusMu.RLock()
	defer s.executionStatusMu.RUnlock()

	if state, exists := s.executionStatus[executionID]; exists {
		// Return a copy
		stateCopy := *state
		return &stateCopy
	}
	return nil
}

// Server wraps the gRPC server with lifecycle management.
type Server struct {
	grpcServer       *grpc.Server
	executionService *ExecutionServiceServer
	log              logr.Logger
	address          string
}

// NewServer creates a new streaming server.
func NewServer(
	address string,
	clientset *kubernetes.Clientset,
	k8sClient client.Client,
	restoreManager *restore.Manager,
	log logr.Logger,
) *Server {
	// Create token validator
	validator := auth.NewTokenValidator(clientset, log)

	// Create gRPC server with auth interceptors
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(auth.GRPCInterceptor(validator, log)),
		grpc.StreamInterceptor(auth.GRPCStreamInterceptor(validator, log)),
	)

	// Create and register execution service
	executionService := NewExecutionServiceServer(log, k8sClient, restoreManager)

	// Note: In a real implementation, you would register the generated protobuf service
	// For now, we use the manual types and handle via the webhook fallback or custom registration

	return &Server{
		grpcServer:       grpcServer,
		executionService: executionService,
		log:              log.WithName("streaming-server"),
		address:          address,
	}
}

// Start starts the gRPC server.
func (s *Server) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.address, err)
	}

	s.log.Info("starting gRPC server", "address", s.address)

	// Handle graceful shutdown
	go func() {
		<-ctx.Done()
		s.log.Info("shutting down gRPC server")
		s.grpcServer.GracefulStop()
	}()

	if err := s.grpcServer.Serve(listener); err != nil {
		return fmt.Errorf("gRPC server error: %w", err)
	}

	return nil
}

// ExecutionService returns the execution service for direct access.
func (s *Server) ExecutionService() *ExecutionServiceServer {
	return s.executionService
}
