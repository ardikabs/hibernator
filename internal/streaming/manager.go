/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package streaming

import (
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/ardikabs/hibernator/internal/streaming/auth"
	"github.com/ardikabs/hibernator/internal/streaming/server"
)

// Options configuration for streaming servers
type Options struct {
	GRPCAddr      string
	WebSocketAddr string
	Clock         clock.Clock
}

// SetupStreamingServerWithManager sets up the streaming servers to the controller manager
func SetupStreamingServerWithManager(mgr ctrl.Manager, opts Options) error {
	log := ctrl.Log.WithName("streaming")

	// Create Kubernetes clientset for TokenReview
	clientset, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		return fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	// Create event recorder for streaming events
	eventRecorder := mgr.GetEventRecorderFor("hibernator-streaming")

	// Create shared execution service
	// Runners persist restore data directly to ConfigMap - controller only orchestrates
	execService := server.NewExecutionServiceServer(mgr.GetClient(), eventRecorder, opts.Clock)

	if opts.GRPCAddr != "" {
		// Start gRPC server
		grpcServer := server.NewServer(opts.GRPCAddr, clientset, execService, log)

		if err := mgr.Add(grpcServer); err != nil {
			return fmt.Errorf("failed to add grpc server to manager: %w", err)
		}
	}

	if opts.WebSocketAddr != "" {
		// Start WebSocket server
		validator := auth.NewTokenValidator(clientset, log)
		wsServer := server.NewWebSocketServer(server.WebSocketServerOptions{
			Addr:         opts.WebSocketAddr,
			Clock:        opts.Clock,
			ExecService:  execService,
			Validator:    validator,
			K8sClientset: clientset,
			Log:          log,
		})

		if err := mgr.Add(wsServer); err != nil {
			return fmt.Errorf("failed to add websocket server to manager: %w", err)
		}
	}

	return nil
}
