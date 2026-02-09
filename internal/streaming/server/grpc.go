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

	streamingv1alpha1 "github.com/ardikabs/hibernator/api/streaming/v1alpha1"
	"github.com/ardikabs/hibernator/internal/streaming/auth"
)

// GRPCServer wraps the gRPC server with lifecycle management.
type GRPCServer struct {
	grpcServer  *grpc.Server
	execService *ExecutionServiceServer
	log         logr.Logger
	address     string
}

// NewServer creates a new streaming server.
func NewServer(
	address string,
	clientset *kubernetes.Clientset,
	execService *ExecutionServiceServer,
	log logr.Logger,
) *GRPCServer {
	// Create token validator
	validator := auth.NewTokenValidator(clientset, log)

	// Create gRPC server with auth interceptors
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(auth.GRPCInterceptor(validator, log)),
		grpc.StreamInterceptor(auth.GRPCStreamInterceptor(validator, log)),
	)

	// Register gRPC services
	streamingv1alpha1.RegisterExecutionServiceServer(grpcServer, execService)

	return &GRPCServer{
		grpcServer:  grpcServer,
		execService: execService,
		log:         log.WithName("streaming-server"),
		address:     address,
	}
}

// DefaultStaleExecutionDuration is the default duration after which an execution
// is considered stale if no updates are received (e.g., runner crashed).
const DefaultStaleExecutionDuration = 1 * time.Hour

// Start starts the gRPC server and background cleanup routine.
func (s *GRPCServer) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.address, err)
	}

	s.log.Info("starting gRPC server", "address", s.address)

	// Start execution cleanup routine to handle stale executions (e.g., crashed runners)
	go s.execService.StartCleanupRoutine(ctx, DefaultStaleExecutionDuration)

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
func (s *GRPCServer) NeedLeaderElection() bool {
	return false
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
