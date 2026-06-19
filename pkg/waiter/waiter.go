/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package waiter provides a generic polling mechanism for long-running operations.
// It implements asynchronous wait-to-complete verification with progress logging.
//
// Future enhancements:
// - Support detailed per-resource status tracking (e.g., instance-level states)
// - Configurable polling intervals per operation type
// - Exponential backoff for transient errors
package waiter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
)

const (
	// DefaultPollInterval is the global polling interval during operation
	DefaultPollInterval = 15 * time.Second
)

// Waiter provides asynchronous polling with timeout and progress logging.
type Waiter struct {
	ctx      context.Context
	log      logr.Logger
	timeout  time.Duration
	interval time.Duration
}

// Option configures a Waiter.
type Option func(*Waiter) error

// WithInterval sets the polling interval.
func WithInterval(d time.Duration) Option {
	return func(w *Waiter) error {
		w.interval = d
		return nil
	}
}

// WithTimeout sets the timeout as a time.Duration.
func WithTimeout(d time.Duration) Option {
	return func(w *Waiter) error {
		w.timeout = d
		return nil
	}
}

// WithTimeoutString sets the timeout by parsing a duration string (e.g., "5m", "200ms").
// An empty string means no timeout (wait until the context is canceled).
func WithTimeoutString(s string) Option {
	return func(w *Waiter) error {
		if s == "" {
			return nil
		}

		d, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("invalid timeout format %q: %w", s, err)

		}

		w.timeout = d
		return nil
	}
}

// NewWaiter creates a new Waiter.
// If no timeout option is provided, the waiter will wait indefinitely (until the context is canceled).
// If multiple timeout options are supplied, they are applied in order and the last one wins.
func NewWaiter(ctx context.Context, log logr.Logger, opts ...Option) (*Waiter, error) {
	w := &Waiter{
		ctx:      ctx,
		log:      log,
		interval: DefaultPollInterval,
	}

	for _, opt := range opts {
		if err := opt(w); err != nil {
			return nil, err
		}
	}

	return w, nil
}

// CheckFunc is called on each poll iteration to check operation status.
// Returns:
//   - done: true if operation completed successfully
//   - status: human-readable status string for logging (e.g., "3/5 stopped, 2 stopping")
//   - err: error if check failed (stops polling immediately)
type CheckFunc func() (done bool, status string, err error)

// Poll repeatedly calls checkFunc until it returns done=true, timeout is reached, or context is canceled.
// Logs INFO at start/completion, DEBUG on each iteration, WARN on context cancellation.
func (w *Waiter) Poll(description string, checkFunc CheckFunc) error {
	w.log.Info("waiting for operation", "description", description, "timeout", w.timeout)

	// Create timeout context if timeout is set
	ctx := w.ctx
	var cancel context.CancelFunc
	if w.timeout > 0 {
		ctx, cancel = context.WithTimeout(w.ctx, w.timeout)
		defer cancel()
	}

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// Check immediately before first tick
	done, status, err := checkFunc()
	if err != nil {
		return fmt.Errorf("check failed: %w", err)
	}
	if done {
		w.log.Info("operation completed", "description", description)
		return nil
	}
	w.log.Info("polling operation (initial)", "description", description, "status", status)

	for {
		select {
		case <-ctx.Done():
			// Check if it's parent context cancellation or timeout
			if errors.Is(ctx.Err(), context.DeadlineExceeded) && w.timeout > 0 {
				return fmt.Errorf("timeout waiting for %s after %v", description, w.timeout)
			}
			w.log.Info("wait interrupted by context cancellation", "description", description)
			return fmt.Errorf("wait for %s interrupted: %w", description, ctx.Err())

		case <-ticker.C:
			done, status, err := checkFunc()
			if err != nil {
				return fmt.Errorf("check failed: %w", err)
			}
			w.log.Info("polling operation", "description", description, "status", status)

			if done {
				w.log.Info("operation completed", "description", description)
				return nil
			}
		}
	}
}
