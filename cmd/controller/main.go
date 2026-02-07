/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	_ "time/tzdata"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/controller/hibernateplan"
	"github.com/ardikabs/hibernator/internal/controller/scheduleexception"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/internal/streaming/auth"
	"github.com/ardikabs/hibernator/internal/streaming/server"
	"github.com/ardikabs/hibernator/pkg/envutil"
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
	var webhookCertDir string
	var workers int
	var syncPeriod time.Duration
	var leaderElectionNamespace string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", envutil.GetBool("LEADER_ELECTION_ENABLED", true),
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&runnerImage, "runner-image", envutil.GetString("RUNNER_IMAGE", "ghcr.io/ardikabs/hibernator-runner:latest"),
		"The runner container image to use for execution jobs.")
	flag.StringVar(&controlPlaneEndpoint, "control-plane-endpoint", envutil.GetString("CONTROL_PLANE_ENDPOINT", ""),
		"The endpoint for runner streaming callbacks.")
	flag.StringVar(&runnerServiceAccount, "runner-service-account", "hibernator-runner",
		"The ServiceAccount name used by runner pods.")
	flag.StringVar(&grpcServerAddr, "grpc-server-address", ":9444",
		"The address for the gRPC streaming server.")
	flag.StringVar(&websocketServerAddr, "websocket-server-address", ":8082",
		"The address for the WebSocket streaming server.")
	flag.BoolVar(&enableStreaming, "enable-streaming", true,
		"Enable gRPC and WebSocket streaming servers for runner communication.")
	flag.StringVar(&webhookCertDir, "webhook-cert-dir", "/tmp/k8s-webhook-server/serving-certs",
		"The directory where webhook certificates are stored.")
	flag.IntVar(&workers, "workers", envutil.GetInt("WORKERS", 1),
		"The number of concurrent reconcile workers. Controls MaxConcurrentReconciles for controllers.")
	flag.DurationVar(&syncPeriod, "sync-period", envutil.GetDuration("SYNC_PERIOD", 10*time.Hour),
		"The minimum interval at which watched resources are reconciled. Default is 10 hours.")
	flag.StringVar(&leaderElectionNamespace, "leader-election-namespace", envutil.GetString("LEADER_ELECTION_NAMESPACE", "hibernator-system"),
		"The namespace in which the leader election resource will be created.")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Cache: cache.Options{
			SyncPeriod: &syncPeriod,
		},
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		WebhookServer: webhook.NewServer(webhook.Options{
			Port:    9443,
			CertDir: webhookCertDir,
		}),
		HealthProbeBindAddress:  probeAddr,
		LeaderElection:          enableLeaderElection,
		LeaderElectionID:        "hibernator.ardikabs.com",
		LeaderElectionNamespace: leaderElectionNamespace,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Set up HibernatePlan controller
	if err = (&hibernateplan.Reconciler{
		Client:               mgr.GetClient(),
		APIReader:            mgr.GetAPIReader(),
		Log:                  ctrl.Log.WithName("controllers").WithName("HibernatePlan"),
		Scheme:               mgr.GetScheme(),
		Planner:              scheduler.NewPlanner(),
		ScheduleEvaluator:    scheduler.NewScheduleEvaluator(),
		RestoreManager:       restore.NewManager(mgr.GetClient()),
		ControlPlaneEndpoint: controlPlaneEndpoint,
		RunnerImage:          runnerImage,
		RunnerServiceAccount: runnerServiceAccount,
	}).SetupWithManager(mgr, workers); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "HibernatePlan")
		os.Exit(1)
	}

	// Set up ScheduleException controller
	if err = (&scheduleexception.Reconciler{
		Client:    mgr.GetClient(),
		APIReader: mgr.GetAPIReader(),
		Log:       ctrl.Log.WithName("controllers").WithName("ScheduleException"),
		Scheme:    mgr.GetScheme(),
	}).SetupWithManager(mgr, workers); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ScheduleException")
		os.Exit(1)
	}

	// Set up validation webhook
	if err = (&hibernatorv1alpha1.HibernatePlan{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "HibernatePlan")
		os.Exit(1)
	}

	// Set up ScheduleException validation webhook
	if err = (&hibernatorv1alpha1.ScheduleException{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "ScheduleException")
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
	log := ctrl.Log.WithName("streaming")

	// Create Kubernetes clientset for TokenReview
	clientset, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		return err
	}

	// Create event recorder for streaming events
	eventRecorder := mgr.GetEventRecorderFor("hibernator-streaming")

	// Create shared execution service
	// Runners persist restore data directly to ConfigMap - controller only orchestrates
	execService := server.NewExecutionServiceServer(mgr.GetClient(), eventRecorder)

	// Start gRPC server
	grpcServer := server.NewServer(grpcAddr, clientset, mgr.GetClient(), eventRecorder, log)
	if err := mgr.Add(grpcServer); err != nil {
		return fmt.Errorf("failed to add grpc server to manager: %w", err)
	}

	// Start WebSocket server
	validator := auth.NewTokenValidator(clientset, log)
	wsServer := server.NewWebSocketServer(server.WebSocketServerOptions{
		Addr:         wsAddr,
		ExecService:  execService,
		Validator:    validator,
		K8sClientset: clientset,
		Log:          log,
	})

	if err := mgr.Add(wsServer); err != nil {
		return fmt.Errorf("failed to add websocket server to manager: %w", err)
	}

	return nil
}
