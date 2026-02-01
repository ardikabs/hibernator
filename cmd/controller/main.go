/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package main

import (
	"context"
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/controller"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/internal/streaming/auth"
	"github.com/ardikabs/hibernator/internal/streaming/server"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(hibernatorv1alpha1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var probeAddr string
	var enableLeaderElection bool
	var runnerImage string
	var controlPlaneEndpoint string
	var runnerServiceAccount string
	var grpcServerAddr string
	var websocketServerAddr string
	var enableStreaming bool

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&runnerImage, "runner-image", "ghcr.io/ardikabs/hibernator-runner:latest",
		"The runner container image to use for execution jobs.")
	flag.StringVar(&controlPlaneEndpoint, "control-plane-endpoint", "",
		"The endpoint for runner streaming callbacks.")
	flag.StringVar(&runnerServiceAccount, "runner-service-account", "hibernator-runner",
		"The ServiceAccount name used by runner pods.")
	flag.StringVar(&grpcServerAddr, "grpc-server-address", ":9443",
		"The address for the gRPC streaming server.")
	flag.StringVar(&websocketServerAddr, "websocket-server-address", ":8082",
		"The address for the WebSocket streaming server.")
	flag.BoolVar(&enableStreaming, "enable-streaming", true,
		"Enable gRPC and WebSocket streaming servers for runner communication.")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "hibernator.ardikabs.com",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Set up HibernatePlan controller
	if err = (&controller.HibernatePlanReconciler{
		Client:               mgr.GetClient(),
		Log:                  ctrl.Log.WithName("controllers").WithName("HibernatePlan"),
		Scheme:               mgr.GetScheme(),
		Planner:              scheduler.NewPlanner(),
		ScheduleEvaluator:    scheduler.NewScheduleEvaluator(),
		RestoreManager:       restore.NewManager(mgr.GetClient()),
		ControlPlaneEndpoint: controlPlaneEndpoint,
		RunnerImage:          runnerImage,
		RunnerServiceAccount: runnerServiceAccount,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "HibernatePlan")
		os.Exit(1)
	}

	// Set up validation webhook
	if err = (&hibernatorv1alpha1.HibernatePlan{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "HibernatePlan")
		os.Exit(1)
	}

	// Add health checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	// Start streaming servers if enabled
	if enableStreaming {
		if err := startStreamingServers(mgr, grpcServerAddr, websocketServerAddr); err != nil {
			setupLog.Error(err, "unable to start streaming servers")
			os.Exit(1)
		}
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// startStreamingServers initializes and starts gRPC and WebSocket streaming servers.
func startStreamingServers(mgr ctrl.Manager, grpcAddr, wsAddr string) error {
	ctx := context.Background()
	log := ctrl.Log.WithName("streaming")

	// Create Kubernetes clientset for TokenReview
	clientset, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		return err
	}

	// Create event recorder for streaming events
	eventRecorder := mgr.GetEventRecorderFor("hibernator-streaming")

	// Create shared execution service
	restoreManager := restore.NewManager(mgr.GetClient())
	execService := server.NewExecutionServiceServer(mgr.GetClient(), restoreManager, eventRecorder)

	// Start gRPC server
	grpcServer := server.NewServer(grpcAddr, clientset, mgr.GetClient(), restoreManager, eventRecorder, log)
	go func() {
		if err := grpcServer.Start(ctx); err != nil {
			log.Error(err, "gRPC server failed")
		}
	}()
	log.Info("started gRPC streaming server", "address", grpcAddr)

	// Start WebSocket server
	validator := auth.NewTokenValidator(clientset, log)
	wsServer := server.NewWebSocketServer(server.WebSocketServerOptions{
		Addr:         wsAddr,
		ExecService:  execService,
		Validator:    validator,
		K8sClientset: clientset,
		Log:          log,
	})
	go func() {
		if err := wsServer.Start(ctx); err != nil {
			log.Error(err, "WebSocket server failed")
		}
	}()
	log.Info("started WebSocket streaming server", "address", wsAddr)

	return nil
}
