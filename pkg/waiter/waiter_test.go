/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package waiter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

func TestNewWaiter_ValidTimeout(t *testing.T) {
	tests := []struct {
		name        string
		timeoutStr  string
		wantTimeout time.Duration
	}{
		{"5 minutes", "5m", 5 * time.Minute},
		{"10 minutes 30 seconds", "10m30s", 10*time.Minute + 30*time.Second},
		{"1 hour", "1h", 1 * time.Hour},
		{"empty string (no timeout)", "", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			log := logr.Discard()

			waiter, err := NewWaiter(ctx, log, tt.timeoutStr)
			if err != nil {
				t.Fatalf("NewWaiter() error = %v, want nil", err)
			}
			if waiter.timeout != tt.wantTimeout {
				t.Errorf("timeout = %v, want %v", waiter.timeout, tt.wantTimeout)
			}
		})
	}
}

func TestNewWaiter_InvalidTimeout(t *testing.T) {
	tests := []struct {
		name       string
		timeoutStr string
	}{
		{"invalid format", "5x"},
		{"alphabetic", "abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			log := logr.Discard()

			_, err := NewWaiter(ctx, log, tt.timeoutStr)
			if err == nil {
				t.Error("NewWaiter() error = nil, want error")
			}
		})
	}
}

func TestPoll_ImmediateSuccess(t *testing.T) {
	ctx := context.Background()
	log := logr.Discard()

	waiter, err := NewWaiter(ctx, log, "1m")
	if err != nil {
		t.Fatalf("NewWaiter() error = %v", err)
	}

	callCount := 0
	checkFunc := func() (bool, string, error) {
		callCount++
		return true, "ready", nil
	}

	err = waiter.Poll("test operation", checkFunc)
	if err != nil {
		t.Errorf("Poll() error = %v, want nil", err)
	}
	if callCount != 1 {
		t.Errorf("checkFunc called %d times, want 1", callCount)
	}
}

func TestPoll_EventualSuccess(t *testing.T) {
	ctx := context.Background()
	log := logr.Discard()

	waiter, err := NewWaiter(ctx, log, "1m")
	if err != nil {
		t.Fatalf("NewWaiter() error = %v", err)
	}

	callCount := 0
	checkFunc := func() (bool, string, error) {
		callCount++
		if callCount < 3 {
			return false, "pending", nil
		}
		return true, "ready", nil
	}

	err = waiter.Poll("test operation", checkFunc)
	if err != nil {
		t.Errorf("Poll() error = %v, want nil", err)
	}
	if callCount < 3 {
		t.Errorf("checkFunc called %d times, want >= 3", callCount)
	}
}

func TestPoll_Timeout(t *testing.T) {
	ctx := context.Background()
	log := logr.Discard()

	waiter, err := NewWaiter(ctx, log, "1s")
	if err != nil {
		t.Fatalf("NewWaiter() error = %v", err)
	}

	checkFunc := func() (bool, string, error) {
		return false, "still pending", nil
	}

	start := time.Now()
	err = waiter.Poll("test operation", checkFunc)
	duration := time.Since(start)

	if err == nil {
		t.Error("Poll() error = nil, want timeout error")
	}
	if duration < 1*time.Second {
		t.Errorf("Poll() duration = %v, want >= 1s", duration)
	}
	if duration > 2*time.Second {
		t.Errorf("Poll() duration = %v, want < 2s", duration)
	}
}

func TestPoll_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	log := logr.Discard()

	waiter, err := NewWaiter(ctx, log, "1m")
	if err != nil {
		t.Fatalf("NewWaiter() error = %v", err)
	}

	callCount := 0
	checkFunc := func() (bool, string, error) {
		callCount++
		if callCount == 2 {
			cancel() // Cancel after second call
		}
		return false, "pending", nil
	}

	err = waiter.Poll("test operation", checkFunc)
	if err == nil {
		t.Error("Poll() error = nil, want context canceled error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Poll() error = %v, want context.Canceled", err)
	}
}

func TestPoll_CheckFuncError(t *testing.T) {
	ctx := context.Background()
	log := logr.Discard()

	waiter, err := NewWaiter(ctx, log, "1m")
	if err != nil {
		t.Fatalf("NewWaiter() error = %v", err)
	}

	expectedErr := errors.New("check error")
	checkFunc := func() (bool, string, error) {
		return false, "", expectedErr
	}

	err = waiter.Poll("test operation", checkFunc)
	if err == nil {
		t.Error("Poll() error = nil, want check error")
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("Poll() error = %v, want %v", err, expectedErr)
	}
}

func TestPoll_NoTimeout(t *testing.T) {
	ctx := context.Background()
	log := logr.Discard()

	waiter, err := NewWaiter(ctx, log, "") // Empty timeout = no timeout
	if err != nil {
		t.Fatalf("NewWaiter() error = %v", err)
	}

	callCount := 0
	checkFunc := func() (bool, string, error) {
		callCount++
		if callCount >= 3 {
			return true, "ready", nil
		}
		return false, "pending", nil
	}

	err = waiter.Poll("test operation", checkFunc)
	if err != nil {
		t.Errorf("Poll() error = %v, want nil", err)
	}
}
