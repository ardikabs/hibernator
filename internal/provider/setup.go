/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package provider

import (
	"time"

	"github.com/go-logr/logr"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/ardikabs/hibernator/internal/notification"
	"github.com/ardikabs/hibernator/internal/provider/connector"
	"github.com/ardikabs/hibernator/internal/provider/processor"
	"github.com/ardikabs/hibernator/internal/provider/status"
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

	// Probe configures connector probing.
	Probe ProbeOptions
}

// ProbeOptions configures connector probe scheduling.
type ProbeOptions struct {
	// Resync is the interval between probes of an unchanged connector.
	Resync time.Duration
	// MaxConcurrent caps in-flight probe Jobs across all connectors.
	MaxConcurrent int
	// Timeout bounds a single probe.
	Timeout time.Duration
	// Unreferenced probes connectors that no active HibernatePlan uses.
	// By default they are skipped and report NotInUse.
	Unreferenced bool
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

	statuses, err := status.SetupWithManager(mgr, opts.Logger)
	if err != nil {
		return err
	}

	if err := processor.SetupWithManager(mgr, clk, processor.Options{
		Logger:                 opts.Logger,
		Workers:                opts.Workers,
		ScheduleBufferDuration: opts.ScheduleBufferDuration,
		ControlPlaneEndpoint:   opts.ControlPlaneEndpoint,
		RunnerImage:            opts.RunnerImage,
		RunnerServiceAccount:   opts.RunnerServiceAccount,
		NotificationOptions:    opts.NotificationOptions,
		Statuses:               statuses,
	}); err != nil {
		return err
	}

	if err := connector.SetupWithManager(mgr, connector.Options{
		Clock:                clk,
		Log:                  opts.Logger,
		Statuses:             statuses,
		Workers:              opts.Workers,
		RunnerImage:          opts.RunnerImage,
		RunnerServiceAccount: opts.RunnerServiceAccount,
		Resync:               opts.Probe.Resync,
		MaxConcurrentProbes:  opts.Probe.MaxConcurrent,
		ProbeTimeout:         opts.Probe.Timeout,
		ProbeUnreferenced:    opts.Probe.Unreferenced,
	}); err != nil {
		return err
	}
	log.Info("registered provider", "provider", "connector")

	return nil
}
