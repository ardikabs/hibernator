/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package server

import (
	"context"
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

	for {
		entry, err := stream.Recv()
		if err == io.EOF {
			s.log.V(1).Info("log stream completed", "executionId", executionID, "count", count)
			return stream.SendAndClose(&streamingv1alpha1.StreamLogsResponse{
				ReceivedCount: count,
			})
		}
		if err != nil {
			s.log.Error(err, "error receiving log entry")
			return status.Errorf(codes.Internal, "receive error: %v", err)
		}

		executionID = entry.ExecutionId
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

	// Note: In a real implementation, you would register the generated protobuf service
	// For now, we use the manual types and handle via the webhook fallback or custom registration

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

// ExecutionService returns the execution service for direct access.
func (s *Server) ExecutionService() *ExecutionServiceServer {
	return s.executionService
}
