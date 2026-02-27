/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package recovery

import (
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"
	testingclock "k8s.io/utils/clock/testing"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/stretchr/testify/require"
)

func TestErrorClassification_Constants(t *testing.T) {
	if string(ErrorTransient) != "Transient" {
		t.Errorf("ErrorTransient: got %q, want Transient", ErrorTransient)
	}
	if string(ErrorPermanent) != "Permanent" {
		t.Errorf("ErrorPermanent: got %q, want Permanent", ErrorPermanent)
	}
	if string(ErrorUnknown) != "Unknown" {
		t.Errorf("ErrorUnknown: got %q, want Unknown", ErrorUnknown)
	}
}

func TestClassifyError_Nil(t *testing.T) {
	got := ClassifyError(nil)
	if got != ErrorUnknown {
		t.Errorf("ClassifyError(nil) = %q, want Unknown", got)
	}
}

func TestClassifyError_Transient(t *testing.T) {
	transientErrors := []string{
		"connection timeout occurred",
		"dial tcp: connection refused",
		"rate limit exceeded",
		"request throttling applied",
		"service unavailable",
		"too many requests",
		"context deadline exceeded",
		"temporary failure in name resolution",
	}

	for _, msg := range transientErrors {
		t.Run(msg, func(t *testing.T) {
			got := ClassifyError(errors.New(msg))
			if got != ErrorTransient {
				t.Errorf("ClassifyError(%q) = %q, want Transient", msg, got)
			}
		})
	}
}

func TestClassifyError_Permanent(t *testing.T) {
	permanentErrors := []string{
		"resource not found",
		"resource already exists",
		"invalid configuration",
		"access forbidden",
		"unauthorized access",
		"permission denied",
	}

	for _, msg := range permanentErrors {
		t.Run(msg, func(t *testing.T) {
			got := ClassifyError(errors.New(msg))
			if got != ErrorPermanent {
				t.Errorf("ClassifyError(%q) = %q, want Permanent", msg, got)
			}
		})
	}
}

func TestClassifyError_Unknown(t *testing.T) {
	got := ClassifyError(errors.New("some random error"))
	if got != ErrorUnknown {
		t.Errorf("ClassifyError(random) = %q, want Unknown", got)
	}
}

func TestCalculateBackoff(t *testing.T) {
	tests := []struct {
		attempt int32
		want    time.Duration
	}{
		{0, 60 * time.Second},
		{1, 120 * time.Second},
		{2, 240 * time.Second},
		{3, 480 * time.Second},
		{4, 960 * time.Second},
		{10, 30 * time.Minute},
		{-5, 60 * time.Second},
	}

	for _, tt := range tests {
		got := CalculateBackoff(tt.attempt)
		if got != tt.want {
			t.Errorf("CalculateBackoff(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

func TestDetermineRecoveryStrategy_FirstRetry(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		Spec: hibernatorv1alpha1.HibernatePlanSpec{
			Behavior: hibernatorv1alpha1.Behavior{Retries: 3},
		},
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			RetryCount: 0,
		},
	}

	strategy := DetermineRecoveryStrategy(plan, errors.New("connection timeout"))

	if !strategy.ShouldRetry {
		t.Error("ShouldRetry should be true for first retry")
	}
	if strategy.Classification != ErrorTransient {
		t.Errorf("Classification = %q, want Transient", strategy.Classification)
	}
}

func TestDetermineRecoveryStrategy_MaxRetries(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		Spec: hibernatorv1alpha1.HibernatePlanSpec{
			Behavior: hibernatorv1alpha1.Behavior{Retries: 3},
		},
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			RetryCount: 3,
		},
	}

	strategy := DetermineRecoveryStrategy(plan, errors.New("connection timeout"))

	if strategy.ShouldRetry {
		t.Error("ShouldRetry should be false when max retries exceeded")
	}
}

func TestDetermineRecoveryStrategy_PermanentError(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		Spec: hibernatorv1alpha1.HibernatePlanSpec{
			Behavior: hibernatorv1alpha1.Behavior{Retries: 5},
		},
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			RetryCount: 0,
		},
	}

	strategy := DetermineRecoveryStrategy(plan, errors.New("resource not found"))

	if strategy.ShouldRetry {
		t.Error("ShouldRetry should be false for permanent errors")
	}
	if strategy.Classification != ErrorPermanent {
		t.Errorf("Classification = %q, want Permanent", strategy.Classification)
	}
}

func TestDetermineRecoveryStrategy_WithinBackoff(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		Spec: hibernatorv1alpha1.HibernatePlanSpec{
			Behavior: hibernatorv1alpha1.Behavior{Retries: 5},
		},
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			RetryCount:    1,
			LastRetryTime: &metav1.Time{Time: time.Now().Add(-30 * time.Second)},
		},
	}

	strategy := DetermineRecoveryStrategy(plan, errors.New("timeout"))

	if !strategy.ShouldRetry {
		t.Error("ShouldRetry should be true")
	}
	if strategy.RetryAfter <= 0 {
		t.Error("RetryAfter should be positive when within backoff period")
	}
}

func TestRecordRetryAttempt(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			RetryCount: 2,
		},
	}

	require.True(t, RecordRetryAttempt(plan, clock.RealClock{}, errors.New("test error")))

	if plan.Status.RetryCount != 3 {
		t.Errorf("RetryCount = %d, want 3", plan.Status.RetryCount)
	}
	if plan.Status.LastRetryTime == nil {
		t.Error("LastRetryTime should be set")
	}
	if plan.Status.ErrorMessage != "test error" {
		t.Errorf("ErrorMessage = %q, want 'test error'", plan.Status.ErrorMessage)
	}
}

func TestRecordRetryAttempt_Idempotent(t *testing.T) {
	now := time.Now()
	fakeClock := testingclock.NewFakeClock(now)

	plan := &hibernatorv1alpha1.HibernatePlan{
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			RetryCount: 2,
		},
	}

	// First call should increment
	RecordRetryAttempt(plan, fakeClock, errors.New("first error"))
	if plan.Status.RetryCount != 3 {
		t.Errorf("After first call: RetryCount = %d, want 3", plan.Status.RetryCount)
	}
	if plan.Status.ErrorMessage != "first error" {
		t.Errorf("After first call: ErrorMessage = %q, want 'first error'", plan.Status.ErrorMessage)
	}

	// Second call within 5s should NOT increment (idempotency guard)
	fakeClock.Step(2 * time.Second)
	RecordRetryAttempt(plan, fakeClock, errors.New("second error"))
	if plan.Status.RetryCount != 3 {
		t.Errorf("After second call (within 5s): RetryCount = %d, want 3", plan.Status.RetryCount)
	}
	if plan.Status.ErrorMessage != "second error" {
		t.Errorf("After second call: ErrorMessage = %q, want 'second error' (should update)", plan.Status.ErrorMessage)
	}

	// Third call after 5s should increment
	fakeClock.Step(5 * time.Second)
	RecordRetryAttempt(plan, fakeClock, errors.New("third error"))
	if plan.Status.RetryCount != 4 {
		t.Errorf("After third call (after 5s): RetryCount = %d, want 4", plan.Status.RetryCount)
	}
	if plan.Status.ErrorMessage != "third error" {
		t.Errorf("After third call: ErrorMessage = %q, want 'third error'", plan.Status.ErrorMessage)
	}
}

func TestResetRetryState(t *testing.T) {
	now := metav1.Now()
	plan := &hibernatorv1alpha1.HibernatePlan{
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			RetryCount:    5,
			LastRetryTime: &now,
			ErrorMessage:  "previous error",
		},
	}

	ResetRetryState(plan)

	if plan.Status.RetryCount != 0 {
		t.Errorf("RetryCount = %d, want 0", plan.Status.RetryCount)
	}
	if plan.Status.LastRetryTime != nil {
		t.Error("LastRetryTime should be nil")
	}
	if plan.Status.ErrorMessage != "" {
		t.Errorf("ErrorMessage = %q, want empty", plan.Status.ErrorMessage)
	}
}

func TestErrorRecoveryStrategy_Fields(t *testing.T) {
	strategy := ErrorRecoveryStrategy{
		ShouldRetry:    true,
		RetryAfter:     5 * time.Minute,
		Classification: ErrorTransient,
		Reason:         "test reason",
	}

	if !strategy.ShouldRetry {
		t.Error("ShouldRetry should be true")
	}
	if strategy.RetryAfter != 5*time.Minute {
		t.Errorf("RetryAfter = %v, want 5m", strategy.RetryAfter)
	}
	if strategy.Classification != ErrorTransient {
		t.Errorf("Classification = %q, want Transient", strategy.Classification)
	}
	if strategy.Reason != "test reason" {
		t.Errorf("Reason = %q, want 'test reason'", strategy.Reason)
	}
}
