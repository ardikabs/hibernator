/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package provider

import (
	"context"
	"fmt"

	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/ardikabs/hibernator/internal/message"
	planprocessor "github.com/ardikabs/hibernator/internal/provider/processor/plan"
	scheduleexceptionprocessor "github.com/ardikabs/hibernator/internal/provider/processor/scheduleexception"
	statusprocessor "github.com/ardikabs/hibernator/internal/provider/processor/status"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/go-logr/logr"
)

// ProviderOptions contains the configuration needed to wire the full async reconciler pipeline.
// It captures the subset of controller options relevant to providers and processors,
// allowing both the production binary and e2e test suites to initialise the pipeline
// with a single call to Setup.
type ProviderOptions struct {
	// Logger is the base logger used by providers and processors.
	// If zero, ctrl.Log is used as the fallback.
	Logger logr.Logger

	// Workers is the number of concurrent reconciler workers.
	Workers int
	// ScheduleBufferDuration is passed to scheduler.WithScheduleBuffer.
	// Empty string disables the schedule buffer.
	ScheduleBufferDuration string
	// ControlPlaneEndpoint is the address of the hibernator control-plane gRPC/webhook server,
	// used by runner Jobs for streaming callbacks.
	ControlPlaneEndpoint string
	// RunnerImage is the container image used for executor runner Jobs.
	RunnerImage string
	// RunnerServiceAccount is the ServiceAccount name used by runner Jobs.
	RunnerServiceAccount string
}

// Setup wires the full async phase-driven reconciler pipeline and registers all providers
// and processors with mgr. It is the single entry point for both the production controller
// binary and e2e test suites.
//
// Pipeline:
//
//	K8s watch → [HibernatePlan/ScheduleException Providers] → watchable.Map
//	         → [Coordinator → Workers] → status updates → [Status Writer] → K8s
func Setup(mgr ctrl.Manager, clk clock.Clock, opts ProviderOptions) error {
	log := opts.Logger.WithName("setup")
	restoreMgr := restore.NewManager(mgr.GetClient())
	planner := scheduler.NewPlanner()
	schedEvaluator := scheduler.NewScheduleEvaluator(clk, scheduler.WithScheduleBuffer(opts.ScheduleBufferDuration))

	// Shared message bus between providers and processors.
	resources := new(message.ControllerResources)
	statuses := message.NewControllerStatuses()

	// --- Providers (K8s reconciler → watchable map) ---

	providers := []struct {
		name       string
		reconciler interface {
			reconcile.Reconciler
			SetupWithManager(ctrl.Manager, int) error
		}
	}{
		{
			name: "hibernateplan",
			reconciler: &PlanReconciler{
				Client:            mgr.GetClient(),
				APIReader:         mgr.GetAPIReader(),
				Clock:             clk,
				Log:               opts.Logger.WithName("hibernateplan"),
				Scheme:            mgr.GetScheme(),
				Planner:           planner,
				ScheduleEvaluator: schedEvaluator,
				RestoreManager:    restoreMgr,
				Resources:         resources,
			},
		},
		{
			name: "scheduleexception",
			reconciler: &ExceptionReconciler{
				Client:    mgr.GetClient(),
				APIReader: mgr.GetAPIReader(),
				Clock:     clk,
				Log:       opts.Logger.WithName("scheduleexception"),
				Scheme:    mgr.GetScheme(),
				Resources: resources,
			},
		},
	}

	for _, p := range providers {
		if err := p.reconciler.SetupWithManager(mgr, opts.Workers); err != nil {
			return fmt.Errorf("unable to create %s provider: %w", p.name, err)
		}

		log.Info("registered provider", "provider", p.name)
	}

	// --- Processors (watchable map → status updates) ---
	// Registered as Runnables via mgr.Add() — started when the manager starts.

	processors := []struct {
		name     string
		runnable interface {
			Start(context.Context) error
			NeedLeaderElection() bool
		}
	}{
		{
			name: "hibernateplan.coordinator",
			runnable: &planprocessor.Coordinator{
				Client:               mgr.GetClient(),
				APIReader:            mgr.GetAPIReader(),
				Clock:                clk,
				Log:                  opts.Logger.WithName("processor").WithName("plan"),
				Scheme:               mgr.GetScheme(),
				Planner:              planner,
				Resources:            resources,
				Statuses:             statuses,
				RestoreManager:       restoreMgr,
				ControlPlaneEndpoint: opts.ControlPlaneEndpoint,
				RunnerImage:          opts.RunnerImage,
				RunnerServiceAccount: opts.RunnerServiceAccount,
			},
		},
		{
			name: "scheduleexception.processor",
			runnable: &scheduleexceptionprocessor.LifecycleProcessor{
				Client:    mgr.GetClient(),
				APIReader: mgr.GetAPIReader(),
				Clock:     clk,
				Log:       opts.Logger.WithName("processor").WithName("exception"),
				Resources: resources,
				Statuses:  statuses,
			},
		},
		{
			name: "status.writer",
			runnable: &statusprocessor.Writer{
				Client:    mgr.GetClient(),
				APIReader: mgr.GetAPIReader(),
				Log:       opts.Logger.WithName("processor").WithName("status"),
				Statuses:  statuses,
				Resources: resources,
			},
		},
	}

	for _, p := range processors {
		if err := mgr.Add(p.runnable); err != nil {
			return fmt.Errorf("unable to add processor %s: %w", p.name, err)
		}
		log.Info("registered processor", "processor", p.name)
	}

	return nil
}
