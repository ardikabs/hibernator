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

// Config holds all infrastructure dependencies and worker-owned callbacks that
// state handlers need. Build it with NewConfig() and wire each dependency using
// the With* builder methods.
//
// All fields listed here mirror the worker's buildConfig() assembly: the worker
// constructs a fresh Config on every handle() call, so handlers are fully
// stateless with respect to the worker's internal state.
type Config struct {
	Log                  logr.Logger
	Client               client.Client
	APIReader            client.Reader
	Clock                clock.Clock
	Scheme               *runtime.Scheme
	Planner              *scheduler.Planner
	Resources            *message.ControllerResources
	Statuses             *statusprocessor.ControllerStatuses
	RestoreManager       *restore.Manager
	Notifier             notification.Notifier
	ControlPlaneEndpoint string
	RunnerImage          string
	RunnerServiceAccount string

	// Job-miss safeguard — closures owned by the Worker that track how many
	// consecutive poll cycles a running target's Job has been absent.
	// OnJobMissing increments the counter and returns true when the threshold is
	// reached (job considered lost → reset target to StatePending for re-dispatch).
	// OnJobFound resets the counter when the job reappears.
	// Both are nil-safe; passing nil disables the safeguard.
	OnJobMissing func(target string) bool
	OnJobFound   func(target string)
}
