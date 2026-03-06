/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// ---------------------------------------------------------------------------
// recoveryState
// ---------------------------------------------------------------------------

func TestRecoveryState_Handle_MaxRetriesExceeded_CancelsRetry(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseError)
	plan.Status.RetryCount = wellknown.DefaultRecoveryMaxRetryAttempts
	plan.Status.ErrorMessage = "something went wrong"

	c := newHandlerFakeClient(plan)
	tt := &timerTracker{}
	state := newHandlerState(plan, c, tt)

	h := &recoveryState{State: state}
	h.Handle(context.Background())

	assert.True(t, tt.cancelRequeueCalled, "retry timer must be cancelled when max retries exhausted")
	assert.Zero(t, tt.requeueDuration, "no retry should be scheduled")
}

func TestRecoveryState_Handle_BackoffPending_SchedulesRetryTimer(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseError)
	plan.Status.RetryCount = 0
	plan.Status.ErrorMessage = "transient error"
	// Set LastRetryTime to now so the backoff window has not yet elapsed.
	now := metav1.NewTime(time.Now())
	plan.Status.LastRetryTime = ptr.To(now)

	c := newHandlerFakeClient(plan)
	tt := &timerTracker{}
	state := newHandlerState(plan, c, tt)

	h := &recoveryState{State: state}
	h.Handle(context.Background())

	assert.True(t, tt.requeueDuration > 0, "retry timer should be scheduled while within backoff window")
}
