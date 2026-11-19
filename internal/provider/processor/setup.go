/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package processor wires the HibernatePlan reconciler and every processor of the
// async phase-driven pipeline into a manager.
package processor

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
	statusmanager "github.com/ardikabs/hibernator/internal/provider/status"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// Options configures the HibernatePlan reconciler and its processors.
type Options struct {
	// Logger is the base logger used by the reconciler and processors.
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

	// Statuses sends status updates to the status managers.
	Statuses *statusmanager.ControllerStatuses
}

// SetupWithManager registers PlanReconciler, the single aggregate reconciler for
// HibernatePlan, ScheduleException and HibernateNotification, and adds every
// processor to mgr.
//
// PlanReconciler is anchored on HibernatePlan: a ScheduleException or
// HibernateNotification change is mapped to the plans it affects, and each
// reconcile publishes one plan snapshot (plan, exceptions, notifications) that is
// distributed to the processors. Owned Jobs and ConfigMaps also trigger it.
//
// Pipeline:
//
//	K8s watch → [PlanReconciler: HibernatePlan + ScheduleException + HibernateNotification] → watchable.Map
//	          → [Processors: Coordinator → Workers, ...] → status updates → [Status Writer] → K8s
func SetupWithManager(mgr ctrl.Manager, clk clock.Clock, opts Options) error {
	log := opts.Logger.WithName("setup")

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

	// --- Providers (K8s reconciler → watchable map) ---
	planReconciler := &PlanReconciler{
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

	if err := planReconciler.SetupWithManager(mgr, opts.Workers); err != nil {
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
		Statuses:  opts.Statuses,
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
				Statuses:       opts.Statuses,
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
				Statuses:  opts.Statuses,
			},
		},
		{
			name:     "notification.lifecycle",
			runnable: notifLifecycleProcessor,
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

// registerFieldIndexes indexes ScheduleException by spec.planRef.name, so exceptions
// are looked up by plan name without relying on labels.
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
