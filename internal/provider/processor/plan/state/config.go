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
	Statuses             *message.ControllerStatuses
	RestoreManager       *restore.Manager
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

// NewConfig returns an empty Config ready for builder-method chaining.
func NewConfig() *Config {
	return &Config{}
}

func (c *Config) WithLog(log logr.Logger) *Config { c.Log = log; return c }

func (c *Config) WithClient(cl client.Client) *Config { c.Client = cl; return c }

func (c *Config) WithAPIReader(r client.Reader) *Config { c.APIReader = r; return c }

func (c *Config) WithClock(clk clock.Clock) *Config { c.Clock = clk; return c }

func (c *Config) WithScheme(s *runtime.Scheme) *Config { c.Scheme = s; return c }

func (c *Config) WithPlanner(p *scheduler.Planner) *Config { c.Planner = p; return c }

func (c *Config) WithStatuses(st *message.ControllerStatuses) *Config { c.Statuses = st; return c }

func (c *Config) WithRestoreManager(rm *restore.Manager) *Config { c.RestoreManager = rm; return c }

func (c *Config) WithControlPlaneEndpoint(ep string) *Config { c.ControlPlaneEndpoint = ep; return c }

func (c *Config) WithRunnerImage(img string) *Config { c.RunnerImage = img; return c }

func (c *Config) WithRunnerServiceAccount(sa string) *Config { c.RunnerServiceAccount = sa; return c }

func (c *Config) WithOnJobMissingFunc(fn func(string) bool) *Config { c.OnJobMissing = fn; return c }

func (c *Config) WithOnJobFoundFunc(fn func(string)) *Config { c.OnJobFound = fn; return c }
