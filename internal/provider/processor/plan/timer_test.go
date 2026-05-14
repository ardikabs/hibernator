/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package plan

import (
	"context"
	"testing"
	"time"

	clocktesting "k8s.io/utils/clock/testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"k8s.io/utils/clock"

	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/internal/provider/processor/plan/state"
)

// ---------------------------------------------------------------------------
// Schedule
// ---------------------------------------------------------------------------

func TestSchedule_Arm_CreatesTimer(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	s := NewSchedule(clk, "test")

	s.Arm(time.Hour)

	assert.True(t, s.IsArmed())
	assert.Equal(t, time.Hour, s.Duration())
	assert.NotNil(t, s.C())
	s.Disarm()
}

func TestSchedule_Disarm_StopsTimer(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	s := NewSchedule(clk, "test")
	s.Arm(time.Hour)

	s.Disarm()

	assert.False(t, s.IsArmed())
	assert.Nil(t, s.C())
	assert.Equal(t, time.Duration(0), s.Duration())
}

func TestSchedule_Disarm_NilTimer_IsNoop(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	s := NewSchedule(clk, "test")

	// Must not panic.
	s.Disarm()
	assert.False(t, s.IsArmed())
}

func TestSchedule_Reset_ActiveTimer_ChangesDuration(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	s := NewSchedule(clk, "test")
	s.Arm(time.Hour)

	ok := s.Reset(2 * time.Hour)

	assert.True(t, ok)
	assert.True(t, s.IsArmed())
	s.Disarm()
}

func TestSchedule_Reset_InactiveTimer_Arms(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	s := NewSchedule(clk, "test")

	ok := s.Reset(time.Hour)

	assert.True(t, ok)
	assert.True(t, s.IsArmed())
	s.Disarm()
}

func TestSchedule_C_NotArmed_ReturnsNil(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	s := NewSchedule(clk, "test")

	assert.Nil(t, s.C())
}

// ---------------------------------------------------------------------------
// TimerSet — lifecycle
// ---------------------------------------------------------------------------

func newTestTimerSet(clk clock.Clock) *TimerSet {
	return NewTimerSet(logr.Discard(), clk, defaultWorkerIdleTimeout, TimerHooks{
		OnRequeue:    func(_ context.Context, _ *message.PlanContext) {},
		OnTimeout:    func(_ context.Context, _ *message.PlanContext) {},
		OnDeadline:   func(_ context.Context, _ *message.PlanContext) {},
		OnInactivity: func() {},
	})
}

func TestTimerSet_StartStop_Lifecycle(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	ts := newTestTimerSet(clk)

	ts.Start()
	assert.NotNil(t, ts.C())

	ts.Stop()
	// After Stop, C() should still return the channel but it will no longer receive.
}

func TestTimerSet_Cleanup_DisarmsAll(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	ts := newTestTimerSet(clk)

	ts.SetRequeue(time.Hour)
	ts.SetTimeout(time.Hour)
	ts.SetDeadline(time.Hour)

	ts.Cleanup()

	assert.False(t, ts.Requeue.IsArmed())
	assert.False(t, ts.Timeout.IsArmed())
	assert.False(t, ts.Deadline.IsArmed())
	assert.False(t, ts.Inactivity.IsArmed())
}

// ---------------------------------------------------------------------------
// TimerSet — requeue
// ---------------------------------------------------------------------------

func TestTimerSet_SetRequeue_ArmsTimer(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	ts := newTestTimerSet(clk)

	ts.SetRequeue(time.Hour)

	assert.True(t, ts.Requeue.IsArmed())
	assert.Equal(t, time.Hour, ts.Requeue.Duration())
	ts.Requeue.Disarm()
}

func TestTimerSet_StopRequeue_Disarms(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	ts := newTestTimerSet(clk)
	ts.SetRequeue(time.Hour)

	ts.StopRequeue()

	assert.False(t, ts.Requeue.IsArmed())
}

// ---------------------------------------------------------------------------
// TimerSet — timeout (arm-once)
// ---------------------------------------------------------------------------

func TestTimerSet_SetTimeout_ArmsOnce(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	ts := newTestTimerSet(clk)

	ts.SetTimeout(time.Hour)
	assert.True(t, ts.Timeout.IsArmed())
	assert.Equal(t, time.Hour, ts.Timeout.Duration())

	// Second call should be a no-op (arm-once).
	ts.SetTimeout(30 * time.Minute)
	assert.Equal(t, time.Hour, ts.Timeout.Duration())

	ts.StopTimeout()
}

func TestTimerSet_StopTimeout_Disarms(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	ts := newTestTimerSet(clk)
	ts.SetTimeout(time.Hour)

	ts.StopTimeout()

	assert.False(t, ts.Timeout.IsArmed())
}

// ---------------------------------------------------------------------------
// TimerSet — deadline (always-override)
// ---------------------------------------------------------------------------

func TestTimerSet_SetDeadline_AlwaysReplaces(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	ts := newTestTimerSet(clk)

	ts.SetDeadline(time.Hour)
	assert.True(t, ts.Deadline.IsArmed())
	assert.Equal(t, time.Hour, ts.Deadline.Duration())

	// Second call should replace (always-override).
	ts.SetDeadline(30 * time.Minute)
	assert.Equal(t, 30*time.Minute, ts.Deadline.Duration())

	ts.StopDeadline()
}

func TestTimerSet_StopDeadline_Disarms(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	ts := newTestTimerSet(clk)
	ts.SetDeadline(time.Hour)

	ts.StopDeadline()

	assert.False(t, ts.Deadline.IsArmed())
}

// ---------------------------------------------------------------------------
// TimerSet — inactivity / KeepAlive
// ---------------------------------------------------------------------------

func TestTimerSet_Inactivity_ArmsTimer(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	ts := newTestTimerSet(clk)

	ts.Inactivity.Arm(time.Hour)

	assert.True(t, ts.Inactivity.IsArmed())
	assert.Equal(t, time.Hour, ts.Inactivity.Duration())
	ts.Cleanup()
}

func TestTimerSet_KeepAlive_WithoutDeadline_UsesDefault(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	ts := newTestTimerSet(clk)
	ts.Inactivity.Arm(defaultWorkerIdleTimeout)

	// No deadline set; should reset to default.
	ts.KeepAlive()

	assert.True(t, ts.Inactivity.IsArmed())
}

func TestTimerSet_KeepAlive_WithShortDeadline_NoExtension(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	ts := newTestTimerSet(clk)
	ts.Inactivity.Arm(defaultWorkerIdleTimeout)
	ts.SetDeadline(5 * time.Minute) // shorter than 30 min

	ts.KeepAlive()

	assert.True(t, ts.Inactivity.IsArmed())
	// Inactivity should remain at default since deadline is shorter.
}

func TestTimerSet_KeepAlive_WithLongDeadline_ExtendsInactivity(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	ts := newTestTimerSet(clk)
	ts.Inactivity.Arm(defaultWorkerIdleTimeout)
	ts.SetDeadline(2 * time.Hour) // longer than 30 min

	ts.KeepAlive()

	assert.True(t, ts.Inactivity.IsArmed())
	// Inactivity should be extended to 2h + 1m to cover the deadline.
}

func TestTimerSet_KeepAlive_InactiveTimer_IsNoop(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	ts := newTestTimerSet(clk)

	// Must not panic when inactivity timer is not armed.
	ts.KeepAlive()
	assert.False(t, ts.Inactivity.IsArmed())
}

// ---------------------------------------------------------------------------
// TimerSet — Apply
// ---------------------------------------------------------------------------

func TestTimerSet_Apply_RequeueAfter_ArmsRequeue(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	ts := newTestTimerSet(clk)

	ts.Apply(state.StateResult{RequeueAfter: time.Hour})

	assert.True(t, ts.Requeue.IsArmed())
	assert.False(t, ts.Timeout.IsArmed())
	assert.False(t, ts.Deadline.IsArmed())
	ts.Cleanup()
}

func TestTimerSet_Apply_ZeroRequeueAfter_StopsRequeue(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	ts := newTestTimerSet(clk)
	ts.SetRequeue(time.Hour)

	ts.Apply(state.StateResult{})

	assert.False(t, ts.Requeue.IsArmed())
}

func TestTimerSet_Apply_TimeoutAfter_ArmsTimeout(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	ts := newTestTimerSet(clk)

	ts.Apply(state.StateResult{TimeoutAfter: time.Hour})

	assert.True(t, ts.Timeout.IsArmed())
	assert.Equal(t, time.Hour, ts.Timeout.Duration())
	assert.False(t, ts.Deadline.IsArmed())
	ts.Cleanup()
}

func TestTimerSet_Apply_ZeroTimeoutAfter_StopsTimeout(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	ts := newTestTimerSet(clk)
	ts.SetTimeout(time.Hour)

	ts.Apply(state.StateResult{})

	assert.False(t, ts.Timeout.IsArmed())
}

func TestTimerSet_Apply_DeadlineAfter_SetsDeadline(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	ts := newTestTimerSet(clk)

	ts.Apply(state.StateResult{DeadlineAfter: time.Hour})

	assert.True(t, ts.Deadline.IsArmed())
	assert.Equal(t, time.Hour, ts.Deadline.Duration())
	assert.False(t, ts.Timeout.IsArmed())
	ts.Cleanup()
}

func TestTimerSet_Apply_DeadlineAfter_OverridesExisting(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	ts := newTestTimerSet(clk)
	ts.SetDeadline(time.Hour)

	ts.Apply(state.StateResult{DeadlineAfter: 30 * time.Minute})

	assert.True(t, ts.Deadline.IsArmed())
	assert.Equal(t, 30*time.Minute, ts.Deadline.Duration())
	ts.Cleanup()
}

func TestTimerSet_Apply_ZeroDeadline_StopsDeadline(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	ts := newTestTimerSet(clk)
	ts.SetDeadline(time.Hour)

	ts.Apply(state.StateResult{})

	assert.False(t, ts.Deadline.IsArmed())
}

func TestTimerSet_Apply_SeparatesTimeoutAndDeadline(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	ts := newTestTimerSet(clk)

	// TimeoutAfter and DeadlineAfter should arm different schedules.
	ts.Apply(state.StateResult{
		TimeoutAfter:  time.Hour,
		DeadlineAfter: 30 * time.Minute,
	})

	assert.True(t, ts.Timeout.IsArmed())
	assert.Equal(t, time.Hour, ts.Timeout.Duration())
	assert.True(t, ts.Deadline.IsArmed())
	assert.Equal(t, 30*time.Minute, ts.Deadline.Duration())
	ts.Cleanup()
}

// ---------------------------------------------------------------------------
// TimerSet — multiplexed loop
// ---------------------------------------------------------------------------

func TestTimerSet_Loop_RequeueFires_SendsCallback(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	requeueCalled := false
	ts := NewTimerSet(logr.Discard(), clk, defaultWorkerIdleTimeout, TimerHooks{
		OnRequeue: func(_ context.Context, _ *message.PlanContext) {
			requeueCalled = true
		},
		OnTimeout:    func(_ context.Context, _ *message.PlanContext) {},
		OnDeadline:   func(_ context.Context, _ *message.PlanContext) {},
		OnInactivity: func() {},
	})

	ts.Start()
	defer ts.Stop()

	ts.SetRequeue(time.Millisecond)
	clk.Step(time.Millisecond)

	select {
	case fn := <-ts.C():
		ok := fn(context.Background(), nil)
		assert.True(t, ok)
		assert.True(t, requeueCalled)
	case <-time.After(time.Second):
		t.Fatal("expected requeue callback")
	}
}

func TestTimerSet_Loop_TimeoutFires_SendsCallback(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	timeoutCalled := false
	ts := NewTimerSet(logr.Discard(), clk, defaultWorkerIdleTimeout, TimerHooks{
		OnRequeue:    func(_ context.Context, _ *message.PlanContext) {},
		OnTimeout:    func(_ context.Context, _ *message.PlanContext) { timeoutCalled = true },
		OnDeadline:   func(_ context.Context, _ *message.PlanContext) {},
		OnInactivity: func() {},
	})

	ts.Start()
	defer ts.Stop()

	ts.SetTimeout(time.Millisecond)
	clk.Step(time.Millisecond)

	select {
	case fn := <-ts.C():
		ok := fn(context.Background(), nil)
		assert.True(t, ok)
		assert.True(t, timeoutCalled)
	case <-time.After(time.Second):
		t.Fatal("expected timeout callback")
	}
}

func TestTimerSet_Loop_DeadlineFires_SendsCallback(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	deadlineCalled := false
	ts := NewTimerSet(logr.Discard(), clk, defaultWorkerIdleTimeout, TimerHooks{
		OnRequeue:    func(_ context.Context, _ *message.PlanContext) {},
		OnTimeout:    func(_ context.Context, _ *message.PlanContext) {},
		OnDeadline:   func(_ context.Context, _ *message.PlanContext) { deadlineCalled = true },
		OnInactivity: func() {},
	})

	ts.Start()
	defer ts.Stop()

	ts.SetDeadline(time.Millisecond)
	clk.Step(time.Millisecond)

	select {
	case fn := <-ts.C():
		ok := fn(context.Background(), nil)
		assert.True(t, ok)
		assert.True(t, deadlineCalled)
	case <-time.After(time.Second):
		t.Fatal("expected deadline callback")
	}
}

func TestTimerSet_Loop_InactivityFires_SendsExitCallback(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	inactivityCalled := false
	ts := NewTimerSet(logr.Discard(), clk, defaultWorkerIdleTimeout, TimerHooks{
		OnRequeue:    func(_ context.Context, _ *message.PlanContext) {},
		OnTimeout:    func(_ context.Context, _ *message.PlanContext) {},
		OnDeadline:   func(_ context.Context, _ *message.PlanContext) {},
		OnInactivity: func() { inactivityCalled = true },
	})

	ts.Start()
	defer ts.Stop()

	// Override the inactivity timer with a very short duration so it fires quickly.
	ts.Inactivity.Arm(time.Millisecond)
	clk.Step(time.Millisecond)

	select {
	case fn := <-ts.C():
		ok := fn(context.Background(), nil)
		assert.False(t, ok) // inactivity returns false to signal exit
		assert.True(t, inactivityCalled)
	case <-time.After(time.Second):
		t.Fatal("expected inactivity callback")
	}
}

func TestTimerSet_Loop_Stop_CancelsLoop(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	ts := newTestTimerSet(clk)

	ts.Start()
	ts.Stop()

	// After Stop, no callbacks should be sent even if we arm and fire a timer.
	ts.SetRequeue(time.Millisecond)
	clk.Step(time.Millisecond)

	select {
	case <-ts.C():
		t.Fatal("expected no callback after Stop")
	case <-time.After(50 * time.Millisecond):
		// Good — channel is quiet.
	}
}

// ---------------------------------------------------------------------------
// TimerSet — send buffer overflow
// ---------------------------------------------------------------------------

func TestTimerSet_Send_BufferFull_DropsSilently(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	ts := newTestTimerSet(clk)

	ts.Start()
	defer ts.Stop()

	// Arm two timers that both fire, but don't read from C().
	// The first should be buffered, the second dropped.
	ts.SetRequeue(time.Millisecond)
	ts.SetDeadline(2 * time.Millisecond)
	clk.Step(2 * time.Millisecond)

	// Give the loop a moment to process.
	time.Sleep(10 * time.Millisecond)

	// We should have exactly one callback buffered.
	select {
	case <-ts.C():
		// Consumed the buffered one.
	default:
		t.Fatal("expected one buffered callback")
	}

	// No second callback.
	select {
	case <-ts.C():
		t.Fatal("expected no second callback")
	default:
		// Good.
	}
}
