package state

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// preSuspensionState Helpers
// ---------------------------------------------------------------------------

// newPreSuspensionState creates a preSuspensionState wrapper for testing.
func newPreSuspensionState(plan *hibernatorv1alpha1.HibernatePlan, c client.Client) *preSuspensionState {
	st := newHandlerState(plan, c)
	return &preSuspensionState{state: st}
}

// ---------------------------------------------------------------------------
// preSuspensionState.Handle()
// ---------------------------------------------------------------------------

func TestPreSuspensionState_Handle_NotInExecutingPhase_PerformsSuspension(t *testing.T) {
	// When phase is Active (not Hibernating or WakingUp), Handle should skip
	// the drain check and directly perform suspension.
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	c := newHandlerFakeClient(plan)
	ps := newPreSuspensionState(plan, c)

	result, err := ps.Handle(context.Background())
	require.NoError(t, err)

	// Should requeue immediately and queue a status update
	assert.True(t, result.Requeue)
	assert.GreaterOrEqual(t, planStatuses(ps.state).Len(), 1)

	// Verify annotation was recorded
	assert.Equal(t, string(hibernatorv1alpha1.PhaseActive),
		plan.Annotations[wellknown.AnnotationSuspendedAtPhase],
		"should record current phase in annotation before suspension")
}

func TestPreSuspensionState_Handle_InExecutingPhaseWithActiveTasks_DefersSuspension(t *testing.T) {
	// When phase is Hibernating/WakingUp with active executions,
	// Handle should return a poll result and not perform suspension.
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernating)
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{
			Target:   "app",
			Executor: "eks",
			State:    hibernatorv1alpha1.StateRunning,
			LogsRef:  "logs-ref", // Indicates Job was dispatched
			// FinishedAt is nil, indicating execution is still active
		},
	}
	c := newHandlerFakeClient(plan)
	ps := newPreSuspensionState(plan, c)

	result, err := ps.Handle(context.Background())
	require.NoError(t, err)

	// Should NOT requeue, but should have a requeue-after time
	assert.False(t, result.Requeue)
	assert.Greater(t, result.RequeueAfter, time.Duration(0),
		"should reschedule poll while executions are active")
	assert.Greater(t, result.TimeoutAfter, time.Duration(0),
		"should arm a timeout for the drain")

	// Status update should NOT have been queued yet
	assert.Equal(t, 0, planStatuses(ps.state).Len(),
		"should not queue suspension status update while executions are running")

	// Annotation should NOT have been written yet
	assert.NotContains(t, plan.Annotations, wellknown.AnnotationSuspendedAtPhase,
		"should not record suspended-at-phase annotation until drain completes")
}

func TestPreSuspensionState_Handle_InExecutingPhaseWithoutActiveTasks_PerformsSuspension(t *testing.T) {
	// When phase is Hibernating/WakingUp but all executions are terminal,
	// Handle should proceed directly to suspension.
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseWakingUp)
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{
			Target:     "db",
			Executor:   "rds",
			State:      hibernatorv1alpha1.StateCompleted,
			LogsRef:    "logs-ref",
			FinishedAt: ptr.To(metav1.NewTime(time.Now())),
		},
	}
	c := newHandlerFakeClient(plan)
	ps := newPreSuspensionState(plan, c)

	result, err := ps.Handle(context.Background())
	require.NoError(t, err)

	// Should requeue immediately (drain is complete, suspension follows)
	assert.True(t, result.Requeue)

	// Status update should be queued (suspension update)
	assert.GreaterOrEqual(t, planStatuses(ps.state).Len(), 1)

	// Annotation should be recorded
	assert.Equal(t, string(hibernatorv1alpha1.PhaseWakingUp),
		plan.Annotations[wellknown.AnnotationSuspendedAtPhase])
}

// ---------------------------------------------------------------------------
// preSuspensionState.OnDeadline()
// ---------------------------------------------------------------------------

func TestPreSuspensionState_OnDeadline_BypassesDrainAndSuspends(t *testing.T) {
	// When the deadline fires, suspension should proceed immediately,
	// bypassing any pending execution drain check.
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernating)
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{
			Target:   "app",
			Executor: "eks",
			State:    hibernatorv1alpha1.StateRunning,
			LogsRef:  "logs-ref", // Active execution
			// FinishedAt is nil
		},
	}
	c := newHandlerFakeClient(plan)
	ps := newPreSuspensionState(plan, c)

	// Call OnDeadline (deadline has fired)
	result, err := ps.OnDeadline(context.Background())
	require.NoError(t, err)

	// Should requeue immediately (suspension is forced)
	assert.True(t, result.Requeue)

	// Status update should be queued despite active executions
	assert.GreaterOrEqual(t, planStatuses(ps.state).Len(), 1)

	// Annotation should be recorded despite active executions
	assert.Equal(t, string(hibernatorv1alpha1.PhaseHibernating),
		plan.Annotations[wellknown.AnnotationSuspendedAtPhase],
		"OnDeadline should force suspension regardless of execution state")
}

// ---------------------------------------------------------------------------
// preSuspensionState suspension side-effects
// ---------------------------------------------------------------------------

func TestPreSuspensionState_Suspension_ClearsErrorMessageAndSetsPhase(t *testing.T) {
	// Verify that suspension correctly updates all status fields.
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Status.ErrorMessage = "some previous error"
	c := newHandlerFakeClient(plan)
	ps := newPreSuspensionState(plan, c)

	_, err := ps.Handle(context.Background())
	require.NoError(t, err)

	// Extract the queued status update to verify the mutations
	statusUpdates := planStatuses(ps.state)
	assert.Equal(t, 1, statusUpdates.Len())

	// Since the captureUpdater applies mutations automatically in Send(),
	// the plan's status should reflect the suspended state
	assert.Equal(t, hibernatorv1alpha1.PhaseSuspended, plan.Status.Phase)
	assert.Empty(t, plan.Status.ErrorMessage, "should clear error message on suspension")
}

func TestPreSuspensionState_Suspension_RecordsTimestampInStatus(t *testing.T) {
	// Verify that suspension records LastTransitionTime.
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	c := newHandlerFakeClient(plan)
	ps := newPreSuspensionState(plan, c)

	_, err := ps.Handle(context.Background())
	require.NoError(t, err)

	require.NotNil(t, plan.Status.LastTransitionTime)
	assert.NotZero(t, plan.Status.LastTransitionTime.Time)
}
