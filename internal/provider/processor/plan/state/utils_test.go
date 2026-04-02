/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	statusprocessor "github.com/ardikabs/hibernator/internal/provider/processor/status"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// captureUpdater is a test helper that buffers statusprocessor.Update[T] values
// sent via Send so that tests can inspect Len() or drain the channel.
type captureUpdater[T client.Object] struct {
	ch chan statusprocessor.Update[T]
}

func newCaptureUpdater[T client.Object](cap int) *captureUpdater[T] {
	return &captureUpdater[T]{ch: make(chan statusprocessor.Update[T], cap)}
}

func (u *captureUpdater[T]) Send(upd statusprocessor.Update[T]) {
	if upd.Mutator != nil {
		upd.Mutator.Mutate(upd.Resource)
	}
	u.ch <- upd
}
func (u *captureUpdater[T]) Len() int                            { return len(u.ch) }
func (u *captureUpdater[T]) C() <-chan statusprocessor.Update[T] { return u.ch }

// planStatuses returns the captureUpdater for plan statuses from the given state,
// providing Len() and C() methods for use in tests.
func planStatuses(st *state) *captureUpdater[*hibernatorv1alpha1.HibernatePlan] {
	return st.Statuses.PlanStatuses.(*captureUpdater[*hibernatorv1alpha1.HibernatePlan])
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func targetStage(names ...string) scheduler.ExecutionStage {
	return scheduler.ExecutionStage{Targets: names}
}

func execSt(target string, state hibernatorv1alpha1.ExecutionState) hibernatorv1alpha1.ExecutionStatus {
	return hibernatorv1alpha1.ExecutionStatus{Target: target, Executor: "noop", State: state}
}

func planWithStatuses(execs ...hibernatorv1alpha1.ExecutionStatus) *hibernatorv1alpha1.HibernatePlan {
	p := &hibernatorv1alpha1.HibernatePlan{}
	p.Status.Executions = execs
	return p
}

// ---------------------------------------------------------------------------
// GetStageStatus
// ---------------------------------------------------------------------------

func TestGetStageStatus_AllCompleted(t *testing.T) {
	plan := planWithStatuses(
		execSt("t1", hibernatorv1alpha1.StateCompleted),
		execSt("t2", hibernatorv1alpha1.StateCompleted),
	)
	ss := GetStageStatus(logr.Discard(), plan, targetStage("t1", "t2"))

	assert.True(t, ss.AllTerminal)
	assert.Equal(t, 2, ss.CompletedCount)
	assert.Equal(t, 0, ss.FailedCount)
	assert.False(t, ss.HasRunning)
}

func TestGetStageStatus_SomeFailed(t *testing.T) {
	plan := planWithStatuses(
		execSt("t1", hibernatorv1alpha1.StateCompleted),
		execSt("t2", hibernatorv1alpha1.StateFailed),
	)
	ss := GetStageStatus(logr.Discard(), plan, targetStage("t1", "t2"))

	assert.True(t, ss.AllTerminal)
	assert.Equal(t, 1, ss.FailedCount)
	assert.Equal(t, 1, ss.CompletedCount)
}

func TestGetStageStatus_SomeAborted(t *testing.T) {
	plan := planWithStatuses(
		execSt("t1", hibernatorv1alpha1.StateCompleted),
		execSt("t2", hibernatorv1alpha1.StateAborted),
	)
	ss := GetStageStatus(logr.Discard(), plan, targetStage("t1", "t2"))

	assert.True(t, ss.AllTerminal)
	assert.Equal(t, 1, ss.FailedCount, "aborted targets count as failed")
	assert.Equal(t, 1, ss.CompletedCount)
}

func TestGetStageStatus_SomeRunning_NotAllTerminal(t *testing.T) {
	plan := planWithStatuses(
		execSt("t1", hibernatorv1alpha1.StateCompleted),
		execSt("t2", hibernatorv1alpha1.StateRunning),
	)
	ss := GetStageStatus(logr.Discard(), plan, targetStage("t1", "t2"))

	assert.False(t, ss.AllTerminal)
	assert.True(t, ss.HasRunning)
}

func TestGetStageStatus_MissingTarget_TreatedAsPending(t *testing.T) {
	plan := planWithStatuses(execSt("t1", hibernatorv1alpha1.StateCompleted))
	ss := GetStageStatus(logr.Discard(), plan, targetStage("t1", "t2-missing"))

	assert.False(t, ss.AllTerminal, "missing target should prevent AllTerminal")
	assert.True(t, ss.HasPending)
}

// ---------------------------------------------------------------------------
// FindTarget / FindTargetType
// ---------------------------------------------------------------------------

func TestFindTarget_Exists(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{}
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds"},
		{Name: "app", Type: "eks"},
	}

	target := FindTarget(plan, "db")
	require.NotNil(t, target)
	assert.Equal(t, "rds", target.Type)
}

func TestFindTarget_NotFound_ReturnsNil(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{}
	assert.Nil(t, FindTarget(plan, "missing"))
}

func TestFindTargetType_Exists(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{}
	plan.Spec.Targets = []hibernatorv1alpha1.Target{{Name: "app", Type: "eks"}}
	assert.Equal(t, "eks", FindTargetType(plan, "app"))
}

func TestFindTargetType_NotFound_ReturnsEmpty(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{}
	assert.Empty(t, FindTargetType(plan, "missing"))
}

// ---------------------------------------------------------------------------
// CountRunningJobsInStage
// ---------------------------------------------------------------------------

func TestCountRunningJobsInStage_CountsOnlyActive(t *testing.T) {
	jobs := []batchv1.Job{
		{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{wellknown.LabelTarget: "t1"}},
			Status:     batchv1.JobStatus{Active: 1},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{wellknown.LabelTarget: "t1"}},
			Status: batchv1.JobStatus{
				Active:    0,
				Succeeded: 1,
				Conditions: []batchv1.JobCondition{
					{
						Type:               batchv1.JobSuccessCriteriaMet,
						Status:             corev1.ConditionTrue,
						LastTransitionTime: metav1.Time{Time: time.Now()},
					},
					{
						Type:               batchv1.JobComplete,
						Status:             corev1.ConditionTrue,
						LastTransitionTime: metav1.Time{Time: time.Now()},
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{wellknown.LabelTarget: "t2"}},
			Status:     batchv1.JobStatus{Active: 1},
		},
	}

	count := CountRunningJobsInStage(jobs, targetStage("t1"))
	assert.Equal(t, 1, count, "only active jobs for stage targets should be counted")
}

// ---------------------------------------------------------------------------
// FindExecutionStatus
// ---------------------------------------------------------------------------

func TestFindExecutionStatus_ByTargetAndExecutor(t *testing.T) {
	plan := planWithStatuses(hibernatorv1alpha1.ExecutionStatus{
		Target:   "my-app",
		Executor: "eks",
		State:    hibernatorv1alpha1.StateRunning,
	})

	result := FindExecutionStatus(plan, "eks", "my-app")
	require.NotNil(t, result)
	assert.Equal(t, hibernatorv1alpha1.StateRunning, result.State)
}

func TestFindExecutionStatus_NotFound_ReturnsNil(t *testing.T) {
	plan := planWithStatuses(execSt("t1", hibernatorv1alpha1.StateCompleted))
	assert.Nil(t, FindExecutionStatus(plan, "eks", "unknown"))
}

// ---------------------------------------------------------------------------
// FindFailedUpstream
// ---------------------------------------------------------------------------

func TestFindFailedUpstream_FailedUpstream_Returned(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{}
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds"},
		{Name: "app", Type: "eks"},
	}
	plan.Spec.Execution.Strategy.Dependencies = []hibernatorv1alpha1.Dependency{{From: "db", To: "app"}}
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "db", Executor: "rds", State: hibernatorv1alpha1.StateFailed},
	}

	failed := FindFailedUpstream(plan, "app")
	assert.Equal(t, []string{"db"}, failed)
}

func TestFindFailedUpstream_NoDeps_ReturnsNil(t *testing.T) {
	plan := planWithStatuses(execSt("t1", hibernatorv1alpha1.StateCompleted))
	assert.Nil(t, FindFailedUpstream(plan, "t1"))
}

func TestFindFailedUpstream_AllDepsCompleted_ReturnsNil(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{}
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds"},
		{Name: "app", Type: "eks"},
	}
	plan.Spec.Execution.Strategy.Dependencies = []hibernatorv1alpha1.Dependency{{From: "db", To: "app"}}
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "db", Executor: "rds", State: hibernatorv1alpha1.StateCompleted},
	}

	assert.Nil(t, FindFailedUpstream(plan, "app"))
}

func TestFindFailedUpstream_MultipleDeps_OneFailed(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{}
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds"},
		{Name: "cache", Type: "ec2"},
		{Name: "app", Type: "eks"},
	}
	plan.Spec.Execution.Strategy.Dependencies = []hibernatorv1alpha1.Dependency{
		{From: "db", To: "app"},
		{From: "cache", To: "app"},
	}
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "db", Executor: "rds", State: hibernatorv1alpha1.StateCompleted},
		{Target: "cache", Executor: "ec2", State: hibernatorv1alpha1.StateFailed},
	}

	failed := FindFailedUpstream(plan, "app")
	assert.Equal(t, []string{"cache"}, failed)
}

func TestFindFailedUpstream_IndependentTarget_ReturnsNil(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{}
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "web", Type: "eks"},
		{Name: "metrics-db", Type: "rds"},
	}
	plan.Spec.Execution.Strategy.Dependencies = []hibernatorv1alpha1.Dependency{{From: "web", To: "app"}}
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "web", Executor: "eks", State: hibernatorv1alpha1.StateFailed},
	}

	// metrics-db has no dependency on web
	assert.Nil(t, FindFailedUpstream(plan, "metrics-db"))
}

func TestFindFailedUpstream_AbortedUpstream_Returned(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{}
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db", Type: "rds"},
		{Name: "app", Type: "eks"},
	}
	plan.Spec.Execution.Strategy.Dependencies = []hibernatorv1alpha1.Dependency{{From: "db", To: "app"}}
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "db", Executor: "rds", State: hibernatorv1alpha1.StateAborted},
	}

	failed := FindFailedUpstream(plan, "app")
	assert.Equal(t, []string{"db"}, failed, "aborted upstream should cascade to downstream")
}

// ---------------------------------------------------------------------------
// IsOperationComplete
// ---------------------------------------------------------------------------

func TestIsOperationComplete_AllTerminal_True(t *testing.T) {
	plan := planWithStatuses(
		execSt("t1", hibernatorv1alpha1.StateCompleted),
		execSt("t2", hibernatorv1alpha1.StateFailed),
	)
	assert.True(t, IsOperationComplete(plan))
}

func TestIsOperationComplete_WithAborted_True(t *testing.T) {
	plan := planWithStatuses(
		execSt("t1", hibernatorv1alpha1.StateCompleted),
		execSt("t2", hibernatorv1alpha1.StateAborted),
	)
	assert.True(t, IsOperationComplete(plan), "aborted is a terminal state")
}

func TestIsOperationComplete_SomeRunning_False(t *testing.T) {
	plan := planWithStatuses(
		execSt("t1", hibernatorv1alpha1.StateCompleted),
		execSt("t2", hibernatorv1alpha1.StateRunning),
	)
	assert.False(t, IsOperationComplete(plan))
}

func TestIsOperationComplete_Empty_True(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{}
	assert.True(t, IsOperationComplete(plan))
}

// ---------------------------------------------------------------------------
// JobExistsForTarget
// ---------------------------------------------------------------------------

func makeTestJob(target string, operation hibernatorv1alpha1.PlanOperation, cycleID string, stale bool) batchv1.Job {
	labels := map[string]string{
		wellknown.LabelTarget:    target,
		wellknown.LabelOperation: string(operation),
		wellknown.LabelCycleID:   cycleID,
	}
	if stale {
		labels[wellknown.LabelStaleRunnerJob] = "true"
	}
	return batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Labels: labels},
	}
}

func TestJobExistsForTarget_Exists_True(t *testing.T) {
	jobs := []batchv1.Job{makeTestJob("app", hibernatorv1alpha1.OperationHibernate, "c1", false)}
	assert.True(t, JobExistsForTarget(jobs, "app", hibernatorv1alpha1.OperationHibernate, "c1"))
}

func TestJobExistsForTarget_WrongCycle_False(t *testing.T) {
	jobs := []batchv1.Job{makeTestJob("app", hibernatorv1alpha1.OperationHibernate, "c1", false)}
	assert.False(t, JobExistsForTarget(jobs, "app", hibernatorv1alpha1.OperationHibernate, "c2"))
}

func TestJobExistsForTarget_StaleIgnored(t *testing.T) {
	jobs := []batchv1.Job{makeTestJob("app", hibernatorv1alpha1.OperationHibernate, "c1", true)}
	assert.False(t, JobExistsForTarget(jobs, "app", hibernatorv1alpha1.OperationHibernate, "c1"),
		"stale job should not count as existing")
}

// ---------------------------------------------------------------------------
// FilterJobsForStage
// ---------------------------------------------------------------------------

func TestFilterJobsForStage_FiltersCorrectly(t *testing.T) {
	jobs := []batchv1.Job{
		makeTestJob("t1", hibernatorv1alpha1.OperationHibernate, "c1", false),
		makeTestJob("t2", hibernatorv1alpha1.OperationHibernate, "c1", false),
		makeTestJob("t3", hibernatorv1alpha1.OperationHibernate, "c1", false),
	}

	filtered := FilterJobsForStage(jobs, targetStage("t1", "t3"))
	assert.Len(t, filtered, 2)

	names := []string{
		filtered[0].Labels[wellknown.LabelTarget],
		filtered[1].Labels[wellknown.LabelTarget],
	}
	assert.ElementsMatch(t, []string{"t1", "t3"}, names)
}

// ---------------------------------------------------------------------------
// BuildOperationSummary
// ---------------------------------------------------------------------------

func TestBuildOperationSummary_SuccessPath(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	plan := planWithStatuses(
		execSt("t1", hibernatorv1alpha1.StateCompleted),
		execSt("t2", hibernatorv1alpha1.StateCompleted),
	)

	summary := BuildOperationSummary(clk, plan, hibernatorv1alpha1.OperationHibernate)

	assert.Equal(t, hibernatorv1alpha1.OperationHibernate, summary.Operation)
	assert.True(t, summary.Success)
	assert.Len(t, summary.TargetResults, 2)
}

func TestBuildOperationSummary_FailedTarget_SetsSuccessFalse(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	plan := planWithStatuses(
		execSt("t1", hibernatorv1alpha1.StateCompleted),
		execSt("t2", hibernatorv1alpha1.StateFailed),
	)

	summary := BuildOperationSummary(clk, plan, hibernatorv1alpha1.OperationWakeUp)

	assert.False(t, summary.Success)
}

func TestBuildOperationSummary_AbortedTarget_SetsSuccessFalse(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	plan := planWithStatuses(
		execSt("t1", hibernatorv1alpha1.StateCompleted),
		execSt("t2", hibernatorv1alpha1.StateAborted),
	)

	summary := BuildOperationSummary(clk, plan, hibernatorv1alpha1.OperationHibernate)

	assert.False(t, summary.Success, "aborted target should set success=false")
}

// ---------------------------------------------------------------------------
// snapshotExecutionStates / executionStatesEqual
// ---------------------------------------------------------------------------

func TestSnapshotExecutionStates_CapturesAllFields(t *testing.T) {
	execs := []hibernatorv1alpha1.ExecutionStatus{
		{Target: "t1", State: hibernatorv1alpha1.StateRunning, Attempts: 2, Message: "running"},
		{Target: "t2", State: hibernatorv1alpha1.StateCompleted, Attempts: 1},
	}

	snap := snapshotExecutionStates(execs)
	require.Len(t, snap, 2)
	assert.Equal(t, hibernatorv1alpha1.StateRunning, snap["t1"].State)
	assert.Equal(t, int32(2), snap["t1"].Attempts)
}

func TestExecutionStatesEqual_NoChange_True(t *testing.T) {
	execs := []hibernatorv1alpha1.ExecutionStatus{
		{Target: "t1", State: hibernatorv1alpha1.StateCompleted, Attempts: 1},
	}
	snap := snapshotExecutionStates(execs)
	assert.True(t, executionStatesEqual(snap, execs))
}

func TestExecutionStatesEqual_StateChanged_False(t *testing.T) {
	old := []hibernatorv1alpha1.ExecutionStatus{
		{Target: "t1", State: hibernatorv1alpha1.StateRunning},
	}
	snap := snapshotExecutionStates(old)

	current := []hibernatorv1alpha1.ExecutionStatus{
		{Target: "t1", State: hibernatorv1alpha1.StateCompleted},
	}
	assert.False(t, executionStatesEqual(snap, current))
}

func TestExecutionStatesEqual_DifferentLength_False(t *testing.T) {
	snap := snapshotExecutionStates([]hibernatorv1alpha1.ExecutionStatus{
		{Target: "t1", State: hibernatorv1alpha1.StateCompleted},
	})
	current := []hibernatorv1alpha1.ExecutionStatus{
		{Target: "t1", State: hibernatorv1alpha1.StateCompleted},
		{Target: "t2", State: hibernatorv1alpha1.StateCompleted},
	}
	assert.False(t, executionStatesEqual(snap, current))
}

// ---------------------------------------------------------------------------
// pruneCycleHistory / findOrAppendCycle
// ---------------------------------------------------------------------------

func TestPruneCycleHistory_UnderLimit_NoChange(t *testing.T) {
	st := &hibernatorv1alpha1.HibernatePlanStatus{}
	for i := 0; i < wellknown.MaxCycleHistorySize; i++ {
		st.ExecutionHistory = append(st.ExecutionHistory, hibernatorv1alpha1.ExecutionCycle{
			CycleID: "c" + string(rune('0'+i)),
		})
	}

	pruneCycleHistory(st)
	assert.Len(t, st.ExecutionHistory, wellknown.MaxCycleHistorySize)
}

func TestPruneCycleHistory_OverLimit_KeepsNewest(t *testing.T) {
	st := &hibernatorv1alpha1.HibernatePlanStatus{}
	for i := 0; i < wellknown.MaxCycleHistorySize+3; i++ {
		st.ExecutionHistory = append(st.ExecutionHistory, hibernatorv1alpha1.ExecutionCycle{
			CycleID: string(rune('a' + i)),
		})
	}
	total := len(st.ExecutionHistory)
	lastFive := st.ExecutionHistory[total-5:]

	pruneCycleHistory(st)

	require.Len(t, st.ExecutionHistory, 5)
	for i := range lastFive {
		assert.Equal(t, lastFive[i].CycleID, st.ExecutionHistory[i].CycleID)
	}
}

func TestFindOrAppendCycle_NewCycle_Appended(t *testing.T) {
	st := &hibernatorv1alpha1.HibernatePlanStatus{}

	idx := findOrAppendCycle(st, "c1")

	assert.Equal(t, 0, idx)
	require.Len(t, st.ExecutionHistory, 1)
	assert.Equal(t, "c1", st.ExecutionHistory[0].CycleID)
}

func TestFindOrAppendCycle_ExistingCycle_ReturnsIndex(t *testing.T) {
	st := &hibernatorv1alpha1.HibernatePlanStatus{
		ExecutionHistory: []hibernatorv1alpha1.ExecutionCycle{
			{CycleID: "c1"},
			{CycleID: "c2"},
		},
	}

	idx := findOrAppendCycle(st, "c2")
	assert.Equal(t, 1, idx)
	assert.Len(t, st.ExecutionHistory, 2, "should not append a duplicate")
}
