/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/go-logr/logr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	streamingv1alpha1 "github.com/ardikabs/hibernator/api/streaming/v1alpha1"
	"github.com/ardikabs/hibernator/internal/streaming/auth"
)

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
	eventRecorder record.EventRecorder,
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
	executionService := NewExecutionServiceServer(k8sClient, eventRecorder)

	// Register gRPC services
	streamingv1alpha1.RegisterExecutionServiceServer(grpcServer, executionService)

	return &Server{
		grpcServer:       grpcServer,
		executionService: executionService,
		log:              log.WithName("streaming-server"),
		address:          address,
	}
}

// DefaultStaleExecutionDuration is the default duration after which an execution
// is considered stale if no updates are received (e.g., runner crashed).
const DefaultStaleExecutionDuration = 1 * time.Hour

// Start starts the gRPC server and background cleanup routine.
func (s *Server) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.address, err)
	}

	s.log.Info("starting gRPC server", "address", s.address)

	// Start execution cleanup routine to handle stale executions (e.g., crashed runners)
	go s.executionService.StartCleanupRoutine(ctx, DefaultStaleExecutionDuration)

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

// NeedLeaderElection indicates whether the server requires leader election.
func (s *Server) NeedLeaderElection() bool {
	return false
}

// ExecutionService returns the execution service for direct access.
func (s *Server) ExecutionService() *ExecutionServiceServer {
	return s.executionService
}

// isGracefulStreamClosure checks if the error represents a normal stream closure.
// This includes client-initiated closures, context cancellations, and timeouts.
func isGracefulStreamClosure(err error) bool {
	if err == nil {
		return false
	}

	// Check for EOF (client closed stream normally)
	if errors.Is(err, io.EOF) {
		return true
	}

	// Check for gRPC status codes indicating graceful closure
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.Canceled:
			// Client canceled context (normal for runner failures/exits)
			return true
		case codes.DeadlineExceeded:
			// Timeout - can be considered graceful depending on use case
			return true
		}
	}

	// Check for context errors (when client context is canceled)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	return false
}
