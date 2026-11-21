/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package connector

import (
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"

	statusmanager "github.com/ardikabs/hibernator/internal/provider/status"
)

// Options configures the connector reconcilers registered by SetupWithManager.
type Options struct {
	Clock    clock.Clock
	Log      logr.Logger
	Statuses *statusmanager.ControllerStatuses
	// Workers is the number of concurrent reconciles per connector kind.
	Workers int

	// RunnerImage and RunnerServiceAccount run the probe Jobs, in the namespace
	// of the connector they probe.
	RunnerImage          string
	RunnerServiceAccount string

	// Resync is the interval between probes of an unchanged connector.
	Resync time.Duration
	// MaxConcurrentProbes caps in-flight probe Jobs across all connector kinds.
	MaxConcurrentProbes int
	// ProbeTimeout bounds a single probe.
	ProbeTimeout time.Duration
}

var defaultStrategies = []Strategy{CloudProviderStrategy{}, K8SClusterStrategy{}}

// SetupWithManager registers a reconciler for every connector kind in
// DefaultStrategies.
func SetupWithManager(mgr ctrl.Manager, opts Options) error {
	timeout := opts.ProbeTimeout
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}

	for _, strategy := range defaultStrategies {
		r := &Reconciler{
			Strategy:  strategy,
			Client:    mgr.GetClient(),
			APIReader: mgr.GetAPIReader(),
			Clock:     opts.Clock,
			Log:       opts.Log.WithName("connector").WithName(strategy.Kind()),
			Statuses:  opts.Statuses,
			Job: JobConfig{
				RunnerImage:          opts.RunnerImage,
				RunnerServiceAccount: opts.RunnerServiceAccount,
				Timeout:              timeout,
			},
			Resync:              opts.Resync,
			MaxConcurrentProbes: opts.MaxConcurrentProbes,
		}
		if err := r.SetupWithManager(mgr, opts.Workers); err != nil {
			return fmt.Errorf("unable to create %s connector reconciler: %w", strategy.Kind(), err)
		}
	}
	return nil
}
