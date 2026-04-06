/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/internal/notification"
	statusprocessor "github.com/ardikabs/hibernator/internal/provider/processor/status"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/scheduler"
)

// Infrastructure groups the core Kubernetes client and runtime dependencies
// required by every state handler. Client is embedded so its methods (Get,
// List, Create, Patch, Delete, etc.) promote through to the enclosing state.
type Infrastructure struct {
	client.Client

	APIReader client.Reader
	Scheme    *runtime.Scheme
	Clock     clock.Clock
}

// ExecutorInfra groups the configuration needed to create runner Jobs that
// invoke executors for individual targets.
type ExecutorInfra struct {
	RunnerImage          string
	RunnerServiceAccount string
	ControlPlaneEndpoint string
}

// StateCallbacks groups worker-owned closure pairs that implement the
// consecutive-job-miss safeguard at the state handler level.
type StateCallbacks struct {
	// OnJobMissing increments the counter and returns true when the threshold is
	// reached (job considered lost → reset target to StatePending for re-dispatch).
	OnJobMissing func(target string) bool
	// OnJobFound resets the counter when the job reappears.
	OnJobFound func(target string)
}

// Config holds all infrastructure dependencies and worker-owned callbacks that
// state handlers need. Build it with NewConfig() and wire each dependency using
// the With* builder methods.
//
// All fields listed here mirror the worker's buildConfig() assembly: the worker
// constructs a fresh Config on every handle() call, so handlers are fully
// stateless with respect to the worker's internal state.
type Config struct {
	Log            logr.Logger
	Infrastructure Infrastructure
	ExecutorInfra  ExecutorInfra
	Callbacks      StateCallbacks
	Planner        *scheduler.Planner
	RestoreManager *restore.Manager
	Notifier       notification.Notifier
	Resources      *message.ControllerResources
	Statuses       *statusprocessor.ControllerStatuses
}
