/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package main

import (
	"os"
	"strings"

	"github.com/go-logr/logr"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/internal/executor/cloudsql"
	"github.com/ardikabs/hibernator/internal/executor/ec2"
	"github.com/ardikabs/hibernator/internal/executor/eks"
	"github.com/ardikabs/hibernator/internal/executor/gke"
	"github.com/ardikabs/hibernator/internal/executor/karpenter"
	"github.com/ardikabs/hibernator/internal/executor/rds"
)

// ExecutorFactory creates executor instances.
type ExecutorFactory func() executor.Executor

// executorRegistration holds metadata for each executor type.
type executorRegistration struct {
	factory        ExecutorFactory
	defaultEnabled bool
	description    string
}

// executorFactoryRegistry manages all available executor factories.
type executorFactoryRegistry struct {
	registrations map[string]executorRegistration
}

// newExecutorFactoryRegistry creates a registry with all known executors.
func newExecutorFactoryRegistry() *executorFactoryRegistry {
	return &executorFactoryRegistry{
		registrations: map[string]executorRegistration{
			"eks": {
				factory:        func() executor.Executor { return eks.New() },
				defaultEnabled: true,
				description:    "AWS EKS managed node groups",
			},
			"rds": {
				factory:        func() executor.Executor { return rds.New() },
				defaultEnabled: true,
				description:    "AWS RDS instances and clusters",
			},
			"ec2": {
				factory:        func() executor.Executor { return ec2.New() },
				defaultEnabled: true,
				description:    "AWS EC2 instances",
			},
			"karpenter": {
				factory:        func() executor.Executor { return karpenter.New() },
				defaultEnabled: true,
				description:    "Karpenter node pools",
			},
			"gke": {
				factory:        func() executor.Executor { return gke.New() },
				defaultEnabled: false,
				description:    "GCP GKE clusters (pending API integration)",
			},
			"cloudsql": {
				factory:        func() executor.Executor { return cloudsql.New() },
				defaultEnabled: false,
				description:    "GCP Cloud SQL instances (pending API integration)",
			},
		},
	}
}

// resolveEnabledExecutors returns the set of enabled executor types,
// considering environment overrides.
func (r *executorFactoryRegistry) resolveEnabledExecutors() map[string]bool {
	enabled := make(map[string]bool, len(r.registrations))

	// Start with default settings
	for name, reg := range r.registrations {
		enabled[name] = reg.defaultEnabled
	}

	// Override from HIBERNATOR_ACTIVE_EXECUTORS environment variable
	if envValue := os.Getenv("HIBERNATOR_ACTIVE_EXECUTORS"); envValue != "" {
		// Disable all first
		for name := range enabled {
			enabled[name] = false
		}
		// Enable only the specified executors
		for _, name := range strings.Split(envValue, ",") {
			name = strings.TrimSpace(name)
			if _, exists := r.registrations[name]; exists {
				enabled[name] = true
			}
		}
	}

	return enabled
}

// registerTo populates the executor.Registry with enabled executors.
func (r *executorFactoryRegistry) registerTo(registry *executor.Registry, log logr.Logger) {
	enabled := r.resolveEnabledExecutors()

	var registered []string
	for name, reg := range r.registrations {
		if enabled[name] {
			registry.Register(reg.factory())
			registered = append(registered, name)
		}
	}

	log.Info("registered executors", "count", len(registered), "types", registered)
}
