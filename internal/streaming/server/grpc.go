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

	"github.com/go-logr/logr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	streamingv1alpha1 "github.com/ardikabs/hibernator/api/streaming/v1alpha1"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/streaming/auth"
)

// StreamLogs receives a stream of log entries from a runner via gRPC.
// This is a transport-layer method that delegates to ExecutionServiceServer.
func (s *ExecutionServiceServer) StreamLogs(stream grpc.ClientStreamingServer[streamingv1alpha1.LogEntry, streamingv1alpha1.StreamLogsResponse]) error {
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

		// Delegate to business logic layer
		if err := s.StoreLog(entry); err != nil {
			s.log.Error(err, "failed to store log entry")
			return status.Errorf(codes.Internal, "store error: %v", err)
		}

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
	executionService := NewExecutionServiceServer(k8sClient, restoreManager, eventRecorder)

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
