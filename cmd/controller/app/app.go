/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package app

import (
	"flag"
	"fmt"
	"os"
	"time"

	_ "time/tzdata"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"
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
	"github.com/ardikabs/hibernator/internal/streaming"
	"github.com/ardikabs/hibernator/internal/validationwebhook"
	"github.com/ardikabs/hibernator/internal/version"
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

// Options contains configuration for the controller app.
type Options struct {
	MetricsAddr             string
	ProbeAddr               string
	EnableLeaderElection    bool
	RunnerImage             string
	ControlPlaneEndpoint    string
	RunnerServiceAccount    string
	GRPCServerAddr          string
	WebSocketServerAddr     string
	EnableStreaming         bool
	WebhookCertDir          string
	Workers                 int
	SyncPeriod              time.Duration
	LeaderElectionNamespace string
	ScheduleBufferDuration  string
}

// ParseFlags parses command-line flags and environment variables.
func ParseFlags() Options {
	var opts Options
	var showVersion bool

	flag.BoolVar(&showVersion, "version", false, "Print version and exit.")
	flag.StringVar(&opts.MetricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&opts.ProbeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&opts.EnableLeaderElection, "leader-elect", envutil.GetBool("LEADER_ELECTION_ENABLED", true),
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&opts.RunnerImage, "runner-image", envutil.GetString("RUNNER_IMAGE", "ghcr.io/ardikabs/hibernator-runner:latest"),
		"The runner container image to use for execution jobs.")
	flag.StringVar(&opts.ControlPlaneEndpoint, "control-plane-endpoint", envutil.GetString("CONTROL_PLANE_ENDPOINT", ""),
		"The endpoint for runner streaming callbacks.")
	flag.StringVar(&opts.RunnerServiceAccount, "runner-service-account", "hibernator-runner",
		"The ServiceAccount name used by runner pods.")
	flag.StringVar(&opts.GRPCServerAddr, "grpc-server-address", ":9444",
		"The address for the gRPC streaming server.")
	flag.StringVar(&opts.WebSocketServerAddr, "websocket-server-address", ":8082",
		"The address for the WebSocket streaming server.")
	flag.BoolVar(&opts.EnableStreaming, "enable-streaming", true,
		"Enable gRPC and WebSocket streaming servers for runner communication.")
	flag.StringVar(&opts.WebhookCertDir, "webhook-cert-dir", "/tmp/k8s-webhook-server/serving-certs",
		"The directory where webhook certificates are stored.")
	flag.IntVar(&opts.Workers, "workers", envutil.GetInt("WORKERS", 1),
		"The number of concurrent reconcile workers. Controls MaxConcurrentReconciles for controllers.")
	flag.DurationVar(&opts.SyncPeriod, "sync-period", envutil.GetDuration("SYNC_PERIOD", 10*time.Hour),
		"The minimum interval at which watched resources are reconciled. Default is 10 hours.")
	flag.StringVar(&opts.LeaderElectionNamespace, "leader-election-namespace", envutil.GetString("LEADER_ELECTION_NAMESPACE", "hibernator-system"),
		"The namespace in which the leader election resource will be created.")
	flag.StringVar(&opts.ScheduleBufferDuration, "schedule-buffer-duration", envutil.GetString("SCHEDULE_BUFFER_DURATION", "1m"),
		"The buffer duration added to schedule evaluation windows. Defaults to 1m (1-minute) buffer duration to allow full-day operation both for shutdown and wakeup.")

	zapOpts := zap.Options{
		Development: true,
	}
	zapOpts.BindFlags(flag.CommandLine)
	flag.Parse()

	// Check if version flag is set
	if showVersion {
		fmt.Println("hibernator-controller", version.GetVersion())
		os.Exit(0)
	}

	logger := zap.New(zap.UseFlagOptions(&zapOpts))
	ctrl.SetLogger(logger)
	klog.SetLogger(logger)

	return opts
}

// Run starts the hibernator controller manager.
func Run(opts Options) error {
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Logger: ctrl.Log.WithName("controller-runtime"),
		Cache: cache.Options{
			SyncPeriod: &opts.SyncPeriod,
		},
		Metrics: metricsserver.Options{
			BindAddress: opts.MetricsAddr,
		},
		WebhookServer: webhook.NewServer(webhook.Options{
			Port:    9443,
			CertDir: opts.WebhookCertDir,
		}),
		HealthProbeBindAddress:  opts.ProbeAddr,
		LeaderElection:          opts.EnableLeaderElection,
		LeaderElectionID:        "hibernator.ardikabs.com",
		LeaderElectionNamespace: opts.LeaderElectionNamespace,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		return err
	}

	clk := clock.RealClock{}

	// Set up HibernatePlan controller
	if err = (&hibernateplan.Reconciler{
		Client:               mgr.GetClient(),
		APIReader:            mgr.GetAPIReader(),
		Clock:                clk,
		Log:                  ctrl.Log.WithName("controllers").WithName("HibernatePlan"),
		Scheme:               mgr.GetScheme(),
		Planner:              scheduler.NewPlanner(),
		ScheduleEvaluator:    scheduler.NewScheduleEvaluator(clk, scheduler.WithScheduleBuffer(opts.ScheduleBufferDuration)),
		RestoreManager:       restore.NewManager(mgr.GetClient()),
		ControlPlaneEndpoint: opts.ControlPlaneEndpoint,
		RunnerImage:          opts.RunnerImage,
		RunnerServiceAccount: opts.RunnerServiceAccount,
	}).SetupWithManager(mgr, opts.Workers); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "HibernatePlan")
		return err
	}

	// Set up ScheduleException controller
	if err = (&scheduleexception.Reconciler{
		Client:    mgr.GetClient(),
		APIReader: mgr.GetAPIReader(),
		Clock:     clk,
		Log:       ctrl.Log.WithName("controllers").WithName("ScheduleException"),
		Scheme:    mgr.GetScheme(),
	}).SetupWithManager(mgr, opts.Workers); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ScheduleException")
		return err
	}

	// Set up validation webhooks
	if err = validationwebhook.SetupWithManager(mgr, ctrl.Log.WithName("validationwebhook")); err != nil {
		setupLog.Error(err, "unable to setup webhooks")
		return err
	}

	// Add health checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		return err
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		return err
	}

	// Start streaming servers if enabled
	if opts.EnableStreaming {
		if err := streaming.SetupStreamingServerWithManager(mgr, streaming.Options{
			GRPCAddr:      opts.GRPCServerAddr,
			WebSocketAddr: opts.WebSocketServerAddr,
			Clock:         clk,
		}); err != nil {
			setupLog.Error(err, "unable to initialize streaming servers")
			return err
		}
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		return err
	}

	return nil
}
