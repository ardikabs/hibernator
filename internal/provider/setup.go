/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package provider

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/internal/notification"
	notificationprocessor "github.com/ardikabs/hibernator/internal/provider/processor/notification"
	planprocessor "github.com/ardikabs/hibernator/internal/provider/processor/plan"
	"github.com/ardikabs/hibernator/internal/provider/processor/plan/state"
	requeueprocessor "github.com/ardikabs/hibernator/internal/provider/processor/requeue"
	scheduleexceptionprocessor "github.com/ardikabs/hibernator/internal/provider/processor/scheduleexception"
	statusprocessor "github.com/ardikabs/hibernator/internal/provider/processor/status"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/internal/wellknown"
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

	// NotificationOptions configures the notification subsystem.
	// E2E tests use this to inject custom sinks via notification.WithSink().
	NotificationOptions []notification.Option
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

	// Register field indexer for ScheduleException.spec.planRef.name.
	// This enables efficient lookups of exceptions by plan name without relying on labels.
	if err := registerFieldIndexes(mgr); err != nil {
		return fmt.Errorf("unable to register field indexes: %w", err)
	}

	restoreMgr := restore.NewManager(mgr.GetClient(), opts.Logger)
	planner := scheduler.NewPlanner()
	schedEvaluator := scheduler.NewScheduleEvaluator(clk, scheduler.WithScheduleBuffer(opts.ScheduleBufferDuration))

	// Shared message bus between providers and processors.
	resources := new(message.ControllerResources)

	// PlanEnqueuer channel — processors write GenericEvents here to trigger
	// a fresh PlanReconciler.Reconcile() without relying on RequeueAfter.
	enqueueCh := make(chan event.GenericEvent, 128)
	enqueuer := &channelEnqueuer{
		logger: opts.Logger.WithName("plan-enqueuer"),
		ch:     enqueueCh,
	}

	planStatusProcessor := statusprocessor.NewUpdateProcessor[*hibernatorv1alpha1.HibernatePlan](
		opts.Logger.WithName("processor").WithName("plan-status"),
		mgr.GetClient(),
		mgr.GetAPIReader())

	exceptionStatusProcessor := statusprocessor.NewUpdateProcessor[*hibernatorv1alpha1.ScheduleException](
		opts.Logger.WithName("processor").WithName("exception-status"),
		mgr.GetClient(),
		mgr.GetAPIReader())

	notifStatusProcessor := statusprocessor.NewUpdateProcessor[*hibernatorv1alpha1.HibernateNotification](
		opts.Logger.WithName("processor").WithName("notification-status"),
		mgr.GetClient(),
		mgr.GetAPIReader())

	statuses := &statusprocessor.ControllerStatuses{
		PlanStatuses:         planStatusProcessor.Writer(),
		ExceptionStatuses:    exceptionStatusProcessor.Writer(),
		NotificationStatuses: notifStatusProcessor.Writer(),
	}

	// --- Providers (K8s reconciler → watchable map) ---
	provider := &PlanReconciler{
		Client:            mgr.GetClient(),
		APIReader:         mgr.GetAPIReader(),
		Clock:             clk,
		Log:               opts.Logger.WithName("hibernateplan"),
		Scheme:            mgr.GetScheme(),
		Planner:           planner,
		ScheduleEvaluator: schedEvaluator,
		RestoreManager:    restoreMgr,
		Resources:         resources,
		EnqueueCh:         enqueueCh,
	}

	if err := provider.SetupWithManager(mgr, opts.Workers); err != nil {
		return fmt.Errorf("unable to create hibernateplan provider: %w", err)
	}

	log.Info("registered provider", "provider", "hibernateplan")

	// --- Processors (watchable map → status updates) ---
	// Registered as Runnables via mgr.Add() — started when the manager starts.

	// --- Notification Lifecycle Processor---
	notifLifecycleProcessor := &notificationprocessor.LifecycleProcessor{
		Client:    mgr.GetClient(),
		Clock:     clk,
		Log:       opts.Logger.WithName("processor").WithName("notification"),
		Resources: resources,
		Statuses:  statuses,
	}

	// --- Notification Dispatcher ---
	notifInstance := notification.New(
		opts.Logger.WithName("processor").WithName("notification"),
		mgr.GetAPIReader(),
		append(opts.NotificationOptions, notification.WithDeliveryCallback(notifLifecycleProcessor.HandleDeliveryResult))...,
	)

	processors := []struct {
		name     string
		runnable manager.Runnable
	}{
		{
			name: "hibernateplan.coordinator",
			runnable: &planprocessor.Coordinator{
				Infrastructure: state.Infrastructure{
					Client:    mgr.GetClient(),
					APIReader: mgr.GetAPIReader(),
					Scheme:    mgr.GetScheme(),
					Clock:     clk,
				},
				ExecutorInfra: state.ExecutorInfra{
					ControlPlaneEndpoint: opts.ControlPlaneEndpoint,
					RunnerImage:          opts.RunnerImage,
					RunnerServiceAccount: opts.RunnerServiceAccount,
				},
				Log:            opts.Logger.WithName("processor").WithName("plan"),
				Planner:        planner,
				Resources:      resources,
				Statuses:       statuses,
				RestoreManager: restoreMgr,
				Notifier:       notifInstance.Notifier,
			},
		},
		{
			name: "plan.requeue",
			runnable: &requeueprocessor.PlanRequeueProcessor{
				Clock:     clk,
				Log:       opts.Logger.WithName("processor").WithName("requeue"),
				Resources: resources,
				Enqueuer:  enqueuer,
			},
		},
		{
			name: "scheduleexception.processor",
			runnable: &scheduleexceptionprocessor.LifecycleProcessor{
				Client:    mgr.GetClient(),
				Clock:     clk,
				Log:       opts.Logger.WithName("processor").WithName("exception"),
				Resources: resources,
				Statuses:  statuses,
			},
		},
		{
			name:     "plan.status",
			runnable: planStatusProcessor,
		},
		{
			name:     "exception.status",
			runnable: exceptionStatusProcessor,
		},
		{
			name:     "notification.lifecycle",
			runnable: notifLifecycleProcessor,
		},
		{
			name:     "notification.status",
			runnable: notifStatusProcessor,
		},
		{
			name:     "notification.dispatcher",
			runnable: notifInstance.Runnable,
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

// registerFieldIndexes sets up field indexes required by the reconciler pipeline.
func registerFieldIndexes(mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&hibernatorv1alpha1.ScheduleException{},
		wellknown.FieldIndexExceptionPlanRef,
		func(obj client.Object) []string {
			exc, ok := obj.(*hibernatorv1alpha1.ScheduleException)
			if !ok {
				return nil
			}
			return []string{exc.Spec.PlanRef.Name}
		},
	)
}
