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
	"github.com/stretchr/testify/require"
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
	st := newHandlerState(plan, c)

	h := &recoveryState{state: st}
	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.Zero(t, result.RequeueAfter, "no retry should be scheduled when max retries exhausted")
	assert.False(t, result.Requeue)
}

func TestRecoveryState_Handle_BackoffPending_SchedulesRetryTimer(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseError)
	plan.Status.RetryCount = 0
	plan.Status.ErrorMessage = "transient error"
	// Set LastRetryTime to now so the backoff window has not yet elapsed.
	now := metav1.NewTime(time.Now())
	plan.Status.LastRetryTime = ptr.To(now)

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := &recoveryState{state: st}
	result, err := h.Handle(context.Background())
	require.NoError(t, err)

	assert.True(t, result.RequeueAfter > 0, "retry timer should be scheduled while within backoff window")
}
