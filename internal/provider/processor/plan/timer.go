/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package plan

import (
	"context"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/utils/clock"

	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/internal/provider/processor/plan/state"
)

// Schedule is a single named timer with safe lifecycle.
// All methods are safe for concurrent use.
type Schedule struct {
	Name     string
	duration time.Duration
	timer    clock.Timer
	clock    clock.Clock
	mu       sync.Mutex
}

// NewSchedule creates a new Schedule with the given clock and name.
func NewSchedule(clock clock.Clock, name string) *Schedule {
	return &Schedule{Name: name, clock: clock}
}

// Duration returns the most recent duration the schedule was armed with.
func (s *Schedule) Duration() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.duration
}

// Arm creates a new timer for the given duration.
// Any existing timer is stopped and replaced.
func (s *Schedule) Arm(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.disarmLocked()
	s.duration = d
	s.timer = s.clock.NewTimer(d)
}

// Disarm stops the timer and clears its state.
// It drains the channel if the timer has already fired to prevent leaks.
func (s *Schedule) Disarm() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.disarmLocked()
}

func (s *Schedule) disarmLocked() {
	if s.timer == nil {
		return
	}
	if !s.timer.Stop() {
		select {
		case <-s.timer.C():
		default:
		}
	}
	s.timer = nil
	s.duration = 0
}

// Reset changes the timer's duration. If the timer is not armed, it arms it.
func (s *Schedule) Reset(d time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil {
		return s.timer.Reset(d)
	}
	s.duration = d
	s.timer = s.clock.NewTimer(d)
	return true
}

// C returns the timer's channel, or nil if not armed.
func (s *Schedule) C() <-chan time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer == nil {
		return nil
	}
	return s.timer.C()
}

// IsArmed reports whether the timer is currently active.
func (s *Schedule) IsArmed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.timer != nil
}

// ---------------------------------------------------------------------------
// TimerHooks
// ---------------------------------------------------------------------------

// TimerHooks are called by TimerSet when a timer fires.
type TimerHooks struct {
	// OnRequeue is called when the requeue timer fires.
	// The worker should re-drive the current phase.
	OnRequeue func(ctx context.Context, planCtx *message.PlanContext)

	// OnTimeout is called when the timeout timer fires.
	// The worker should handle the timeout event.
	OnTimeout func(ctx context.Context, planCtx *message.PlanContext)

	// OnDeadline is called when the deadline timer fires.
	// The worker should handle the deadline event.
	OnDeadline func(ctx context.Context, planCtx *message.PlanContext)

	// OnInactivity is called when the inactivity timeout is reached.
	// It signals the worker should terminate.
	OnInactivity func()
}

// ---------------------------------------------------------------------------
// TimerSet
// ---------------------------------------------------------------------------

// TimerSet multiplexes 4 timer channels (requeue, timeout, deadline, inactivity)
// into a single channel of closures. The background goroutine is started with
// Start() and stopped with Stop().
type TimerSet struct {
	clock             clock.Clock
	inactivityTimeout time.Duration
	hooks             TimerHooks

	Requeue    *Schedule
	Timeout    *Schedule
	Deadline   *Schedule
	Inactivity *Schedule

	log    logr.Logger
	out    chan func(ctx context.Context, planCtx *message.PlanContext) bool
	cancel context.CancelFunc
	wg     sync.WaitGroup
	notify chan struct{}
}

// NewTimerSet creates a new TimerSet with the given clock, inactivity timeout, and hooks.
func NewTimerSet(log logr.Logger, clock clock.Clock, inactivityTimeout time.Duration, hooks TimerHooks) *TimerSet {
	ts := &TimerSet{
		clock:             clock,
		inactivityTimeout: inactivityTimeout,
		hooks:             hooks,
		log:               log,
		out:               make(chan func(ctx context.Context, planCtx *message.PlanContext) bool, 1),
		notify:            make(chan struct{}, 1),
	}
	ts.Requeue = NewSchedule(clock, "requeue")
	ts.Timeout = NewSchedule(clock, "timeout")
	ts.Deadline = NewSchedule(clock, "deadline")
	ts.Inactivity = NewSchedule(clock, "inactivity")
	return ts
}

// Start begins the background goroutine that multiplexes timer channels.
func (ts *TimerSet) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	ts.cancel = cancel

	ts.Inactivity.Arm(ts.inactivityTimeout)
	ts.wg.Add(1)
	go ts.loop(ctx)
}

// Stop signals the background goroutine to exit and waits for it.
// It also cleans up all timers.
func (ts *TimerSet) Stop() {
	if ts.cancel != nil {
		ts.cancel()
		ts.wg.Wait()
		ts.cancel = nil
	}
	ts.Cleanup()
}

// C returns the multiplexed channel of timer callbacks.
func (ts *TimerSet) C() <-chan func(ctx context.Context, planCtx *message.PlanContext) bool {
	return ts.out
}

// loop multiplexes timer channels into ts.out.
//
// IMPORTANT: Go's select evaluates channel expressions once per iteration.
// If a timer is not armed when select is entered, its C() returns nil and that
// case is permanently blocked for that iteration. The notify channel (written
// to by poke()) forces the loop to re-iterate whenever a timer is armed or
// disarmed, ensuring the select picks up newly created timer channels.
func (ts *TimerSet) loop(ctx context.Context) {
	defer ts.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ts.notify:
			// A timer was armed or disarmed; re-evaluate all channels.
			ts.log.V(1).Info("poke received, re-evaluating timer channels")
		case <-ts.Requeue.C():
			ts.send(ts.Requeue.Name, func(ctx context.Context, planCtx *message.PlanContext) bool {
				ts.Requeue.Disarm()
				if ts.hooks.OnRequeue != nil {
					ts.hooks.OnRequeue(ctx, planCtx)
				}
				ts.KeepAlive()
				return true
			})
		case <-ts.Timeout.C():
			ts.send(ts.Timeout.Name, func(ctx context.Context, planCtx *message.PlanContext) bool {
				ts.Timeout.Disarm()
				if ts.hooks.OnTimeout != nil {
					ts.hooks.OnTimeout(ctx, planCtx)
				}
				ts.KeepAlive()
				return true
			})
		case <-ts.Deadline.C():
			ts.send(ts.Deadline.Name, func(ctx context.Context, planCtx *message.PlanContext) bool {
				ts.Deadline.Disarm()
				if ts.hooks.OnDeadline != nil {
					ts.hooks.OnDeadline(ctx, planCtx)
				}
				ts.KeepAlive()
				return true
			})
		case <-ts.Inactivity.C():
			ts.send(ts.Inactivity.Name, func(ctx context.Context, planCtx *message.PlanContext) bool {
				ts.Inactivity.Disarm()
				if ts.hooks.OnInactivity != nil {
					ts.hooks.OnInactivity()
				}
				return false
			})
		}
	}
}

func (ts *TimerSet) send(name string, fn func(ctx context.Context, planCtx *message.PlanContext) bool) {
	select {
	case ts.out <- fn:
		ts.log.V(1).Info("timer dispatched to worker", "timer", name)
	default:
		// Channel is full (previous event not yet consumed). Drop silently.
		// This should not happen in normal operation since the worker processes
		// one event at a time, but we guard against it to prevent goroutine leak.
		ts.log.V(1).Info("timer event dropped; worker busy", "timer", name)
	}
}

// ---------------------------------------------------------------------------
// Lifecycle helpers
// ---------------------------------------------------------------------------

// poke signals the loop goroutine to re-evaluate timer channels.
// It uses a non-blocking send on a buffered(1) channel, so multiple
// rapid pokes coalesce into a single wakeup.
func (ts *TimerSet) poke() {
	select {
	case ts.notify <- struct{}{}:
	default:
	}
}

// SetRequeue arms the requeue timer with the given duration.
func (ts *TimerSet) SetRequeue(d time.Duration) {
	ts.Requeue.Arm(d)
	ts.poke()
}

// StopRequeue disarms the requeue timer.
func (ts *TimerSet) StopRequeue() {
	ts.Requeue.Disarm()
	ts.poke()
}

// SetTimeout arms the timeout timer only if it is not already armed (arm-once).
func (ts *TimerSet) SetTimeout(d time.Duration) {
	if !ts.Timeout.IsArmed() {
		ts.Timeout.Arm(d)
		ts.poke()
	}
}

// StopTimeout disarms the timeout timer.
func (ts *TimerSet) StopTimeout() {
	ts.Timeout.Disarm()
	ts.poke()
}

// SetDeadline arms the deadline timer, always replacing any existing deadline.
func (ts *TimerSet) SetDeadline(d time.Duration) {
	ts.Deadline.Arm(d)
	ts.poke()
}

// StopDeadline disarms the deadline timer.
func (ts *TimerSet) StopDeadline() {
	ts.Deadline.Disarm()
	ts.poke()
}

// KeepAlive resets the inactivity timer. If a deadline is active and its duration
// exceeds the inactivity timeout duration, the inactivity timer is extended to cover
// the deadline plus a small buffer.
func (ts *TimerSet) KeepAlive() {
	d := ts.inactivityTimeout
	deadlineDur := ts.Deadline.Duration()
	if deadlineDur > d {
		d = deadlineDur + ts.inactivityTimeout
	}
	if ts.Inactivity.IsArmed() {
		ts.Inactivity.Reset(d)
	}
}

// Cleanup disarms all timers.
func (ts *TimerSet) Cleanup() {
	ts.Reset()
	ts.Inactivity.Disarm()
}

// Reset disarms all operational timers (requeue, timeout, deadline) but leaves
// the inactivity timer untouched.
func (ts *TimerSet) Reset() {
	ts.Requeue.Disarm()
	ts.Timeout.Disarm()
	ts.Deadline.Disarm()
}

// Apply translates StateResult timer directives into TimerSet operations.
func (ts *TimerSet) Apply(result state.StateResult) {
	if result.RequeueAfter > 0 {
		ts.SetRequeue(result.RequeueAfter)
	} else {
		ts.StopRequeue()
	}

	if result.TimeoutAfter > 0 {
		ts.SetTimeout(result.TimeoutAfter)
	} else {
		ts.StopTimeout()
	}

	if result.DeadlineAfter > 0 {
		ts.SetDeadline(result.DeadlineAfter)
	} else {
		ts.StopDeadline()
	}
}
