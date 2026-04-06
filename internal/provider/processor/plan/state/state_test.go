/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
	statusprocessor "github.com/ardikabs/hibernator/internal/provider/processor/status"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// newHandlerScheme returns a runtime.Scheme with the types required by state handler tests.
func newHandlerScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = hibernatorv1alpha1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}

// newHandlerFakeClient returns a fake controller-runtime client pre-populated with objs.
func newHandlerFakeClient(objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(newHandlerScheme()).
		WithObjects(objs...).
		WithStatusSubresource(&hibernatorv1alpha1.HibernatePlan{}).
		Build()
}

// buildTestConfig constructs a Config wired to the supplied fake client.
func buildTestConfig(c client.Client) *Config {
	return &Config{
		Log: logr.Discard(),
		Infrastructure: Infrastructure{
			Client:    c,
			APIReader: c,
			Clock:     clocktesting.NewFakeClock(time.Now()),
			Scheme:    newHandlerScheme(),
		},
		Planner:   scheduler.NewPlanner(),
		Resources: new(message.ControllerResources),
		Statuses: &statusprocessor.ControllerStatuses{
			PlanStatuses:      newCaptureUpdater[*hibernatorv1alpha1.HibernatePlan](64),
			ExceptionStatuses: newCaptureUpdater[*hibernatorv1alpha1.ScheduleException](16),
		},
		RestoreManager: restore.NewManager(c),
		Callbacks: StateCallbacks{
			OnJobMissing: func(target string) bool { return false },
			OnJobFound:   func(target string) {},
		},
	}
}

// newHandlerState constructs a state wired to the supplied plan and fake client.
func newHandlerState(plan *hibernatorv1alpha1.HibernatePlan, c client.Client) *state {
	key := types.NamespacedName{Name: plan.Name, Namespace: plan.Namespace}
	planCtx := &message.PlanContext{Plan: plan}
	return newState(key, planCtx, buildTestConfig(c))
}

// basePlanForState returns a minimal HibernatePlan with the given name and phase.
func basePlanForState(name string, phase hibernatorv1alpha1.PlanPhase) *hibernatorv1alpha1.HibernatePlan {
	return &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			Phase: phase,
		},
	}
}

// ---------------------------------------------------------------------------
// New()
// ---------------------------------------------------------------------------

func TestNew_EmptyPhase_ReturnsLifecycleState(t *testing.T) {
	plan := basePlanForState("p", "")
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*lifecycleState)
	assert.True(t, ok, "expected *lifecycleState for empty phase")
}

func TestNew_PhaseActive_ReturnsIdleState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*idleState)
	assert.True(t, ok, "expected *idleState for PhaseActive")
}

func TestNew_PhaseHibernated_ReturnsIdleState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*idleState)
	assert.True(t, ok, "expected *idleState for PhaseHibernated")
}

func TestNew_PhaseHibernating_ReturnsHibernatingState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernating)
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*hibernatingState)
	assert.True(t, ok, "expected *hibernatingState for PhaseHibernating")
}

func TestNew_PhaseWakingUp_ReturnsWakingUpState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseWakingUp)
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*wakingUpState)
	assert.True(t, ok, "expected *wakingUpState for PhaseWakingUp")
}

func TestNew_PhaseSuspended_ReturnsSuspendedState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseSuspended)
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*suspendedState)
	assert.True(t, ok, "expected *suspendedState for PhaseSuspended")
}

func TestNew_PhaseError_ReturnsRecoveryState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseError)
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*recoveryState)
	assert.True(t, ok, "expected *recoveryState for PhaseError")
}

func TestNew_UnknownPhase_ReturnsNil(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PlanPhase("unknown-phase"))
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	assert.Nil(t, h, "expected nil for unknown phase")
}

func TestNew_DeletionTimestamp_ReturnsLifecycleStateDeleteMode(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	now := metav1.NewTime(time.Now())
	plan.DeletionTimestamp = &now
	// fake client requires a finalizer to not immediately delete the object
	plan.Finalizers = []string{"hibernator.ardikabs.com/finalizer"}
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	ls, ok := h.(*lifecycleState)
	require.True(t, ok, "expected *lifecycleState for plan with DeletionTimestamp")
	assert.True(t, ls.delete, "expected delete=true for lifecycle state with DeletionTimestamp")
}

func TestNew_SuspendRequested_NotYetSuspended_ReturnsPreSuspensionState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Spec.Suspend = true
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*preSuspensionState)
	assert.True(t, ok, "expected *preSuspensionState when Suspend=true and phase != PhaseSuspended")
}

func TestNew_SuspendRequested_AlreadySuspended_ReturnsSuspendedState(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseSuspended)
	plan.Spec.Suspend = true
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*suspendedState)
	assert.True(t, ok, "expected *suspendedState when already in PhaseSuspended")
}

// ---------------------------------------------------------------------------
// HandlerFunc
// ---------------------------------------------------------------------------

func TestPreSuspensionState_Handle_PatchesSuspendedAtPhaseAnnotation(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Spec.Suspend = true
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	h := New(st.Key, st.PlanCtx, buildTestConfig(c))
	require.NotNil(t, h)
	_, ok := h.(*preSuspensionState)
	require.True(t, ok)

	// Calling Handle should patch the suspended-at-phase annotation.
	_, err := h.Handle(context.Background())
	require.NoError(t, err)
	assert.Equal(t, string(hibernatorv1alpha1.PhaseActive), plan.Annotations[wellknown.AnnotationSuspendedAtPhase])
}

// ---------------------------------------------------------------------------
// State.plan()
// ---------------------------------------------------------------------------

func TestState_Plan_ReturnsPlan(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	got := st.plan()
	assert.Same(t, plan, got)
}

// ---------------------------------------------------------------------------
// State.nextStage()
// ---------------------------------------------------------------------------

func TestState_NextStage_UpdatesInMemoryAndQueues(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernating)
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	st.nextStage(1)

	assert.Equal(t, 1, plan.Status.CurrentStageIndex)
	assert.GreaterOrEqual(t, planStatuses(st).Len(), 1)
}

// ---------------------------------------------------------------------------
// State.setError()
// ---------------------------------------------------------------------------

func TestState_SetError_SetsPhaseErrorAndQueues(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernating)
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	st.setError(context.Background(), errors.New("something failed"))

	assert.Equal(t, hibernatorv1alpha1.PhaseError, plan.Status.Phase)
	assert.NotEmpty(t, plan.Status.ErrorMessage)
}

// ---------------------------------------------------------------------------
// State.patchPreservingStatus()
// ---------------------------------------------------------------------------

func TestState_PatchPreservingStatus_PreservesStatus(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseActive)
	plan.Status.ErrorMessage = "should-be-preserved"
	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	orig := plan.DeepCopy()
	if plan.Annotations == nil {
		plan.Annotations = make(map[string]string)
	}
	plan.Annotations["test-key"] = "test-value"

	err := st.patchAndPreserveStatus(context.Background(), plan, client.MergeFrom(orig))
	require.NoError(t, err)

	// Status must be preserved in-memory even after the patch (server might have older status).
	assert.Equal(t, "should-be-preserved", plan.Status.ErrorMessage)
}
