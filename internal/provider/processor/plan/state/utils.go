/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// StageStatus provides detailed status information about a stage's execution progress.
type StageStatus struct {
	// AllTerminal is true when all targets in the stage have reached a terminal state.
	AllTerminal bool
	// HasRunning is true when at least one target is currently running.
	HasRunning bool
	// HasPending is true when at least one target is still pending.
	HasPending bool
	// FailedCount is the number of targets that have failed.
	FailedCount int
	// CompletedCount is the number of targets that have completed successfully.
	CompletedCount int
}

// GetStageStatus returns detailed status information about a stage's execution progress.
func GetStageStatus(log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan, stage scheduler.ExecutionStage) StageStatus {
	status := StageStatus{}
	terminalCount := 0

	for _, targetName := range stage.Targets {
		found := false
		for _, exec := range plan.Status.Executions {
			if exec.Target == targetName {
				found = true
				switch exec.State {
				case hibernatorv1alpha1.StateCompleted:
					status.CompletedCount++
					terminalCount++
				case hibernatorv1alpha1.StateFailed, hibernatorv1alpha1.StateAborted:
					status.FailedCount++
					terminalCount++
				case hibernatorv1alpha1.StateRunning:
					status.HasRunning = true
				case hibernatorv1alpha1.StatePending:
					status.HasPending = true
				}
				break
			}
		}
		if !found {
			log.V(1).Info("target not found in execution list", "target", targetName, "stageTargets", stage.Targets)
			status.HasPending = true
		}
	}

	status.AllTerminal = terminalCount == len(stage.Targets)

	log.V(1).Info("stage status computed",
		"allTerminal", status.AllTerminal,
		"hasRunning", status.HasRunning,
		"hasPending", status.HasPending,
		"completedCount", status.CompletedCount,
		"failedCount", status.FailedCount,
		"totalTargets", len(stage.Targets))

	return status
}

// FindTarget finds a target by name in the plan's target list.
func FindTarget(plan *hibernatorv1alpha1.HibernatePlan, name string) *hibernatorv1alpha1.Target {
	for i := range plan.Spec.Targets {
		if plan.Spec.Targets[i].Name == name {
			return &plan.Spec.Targets[i]
		}
	}
	return nil
}

// FindTargetType returns the type of a target by name.
func FindTargetType(plan *hibernatorv1alpha1.HibernatePlan, name string) string {
	for _, t := range plan.Spec.Targets {
		if t.Name == name {
			return t.Type
		}
	}
	return ""
}

// CountRunningJobsInStage counts how many non-terminal, non-stale jobs exist for
// targets in the stage. A job occupies a concurrency slot from the moment it is
// created until it reaches a terminal state (complete or failed), regardless of
// whether its pod has been scheduled yet (job.Status.Active may be 0 while the
// pod is still being created by the job controller).
func CountRunningJobsInStage(jobs []batchv1.Job, stage scheduler.ExecutionStage) int {
	count := 0
	targetSet := make(map[string]bool, len(stage.Targets))
	for _, t := range stage.Targets {
		targetSet[t] = true
	}
	for _, job := range jobs {
		// Stale jobs have been superseded; they no longer occupy a concurrency slot.
		if _, stale := job.Labels[wellknown.LabelStaleRunnerJob]; stale {
			continue
		}
		// Terminal jobs (completed or failed) no longer occupy a concurrency slot.
		if isJobTerminal(&job) {
			continue
		}
		if _, ok := targetSet[job.Labels[wellknown.LabelTarget]]; ok {
			count++
		}
	}
	return count
}

// isJobTerminal returns true when the Job has reached a terminal state, i.e. it
// has either completed successfully or exceeded its backoff limit.
func isJobTerminal(job *batchv1.Job) bool {
	for _, cond := range job.Status.Conditions {
		if cond.Status != corev1.ConditionTrue {
			continue
		}

		if cond.Type == batchv1.JobComplete || cond.Type == batchv1.JobFailed {
			return true
		}
	}
	return false
}

// FindExecutionStatus finds the execution status for a given target type and name.
func FindExecutionStatus(plan *hibernatorv1alpha1.HibernatePlan, targetType, targetName string) *hibernatorv1alpha1.ExecutionStatus {
	for i := range plan.Status.Executions {
		if plan.Status.Executions[i].Target == targetName &&
			plan.Status.Executions[i].Executor == targetType {
			return &plan.Status.Executions[i]
		} else if plan.Status.Executions[i].Target == fmt.Sprintf("%s/%s", targetType, targetName) {
			return &plan.Status.Executions[i]
		}
	}
	return nil
}

// FindFailedUpstream returns the names of failed upstream dependencies for a single target.
// It checks each dependency where dep.To == targetName and returns the dep.From names
// whose execution state is StateFailed or StateAborted. Returns nil when the target has no failed upstreams.
func FindFailedUpstream(plan *hibernatorv1alpha1.HibernatePlan, targetName string) []string {
	deps := plan.Spec.Execution.Strategy.Dependencies
	if len(deps) == 0 {
		return nil
	}

	var failed []string
	for _, dep := range deps {
		if dep.To != targetName {
			continue
		}
		execStatus := FindExecutionStatus(plan, FindTargetType(plan, dep.From), dep.From)
		if execStatus != nil &&
			(execStatus.State == hibernatorv1alpha1.StateFailed || execStatus.State == hibernatorv1alpha1.StateAborted) {
			failed = append(failed, dep.From)
		}
	}
	return failed
}

// FindFailedDependencies checks if any target in the stage depends on a failed target.
func FindFailedDependencies(plan *hibernatorv1alpha1.HibernatePlan, stage scheduler.ExecutionStage) []string {
	deps := plan.Spec.Execution.Strategy.Dependencies
	if len(deps) == 0 {
		return nil
	}

	var failedDeps []string
	for _, targetName := range stage.Targets {
		failedDeps = append(failedDeps, FindFailedUpstream(plan, targetName)...)
	}
	return failedDeps
}

// BuildOperationSummary creates a summary of the current operation from execution statuses.
func BuildOperationSummary(clk clock.Clock, plan *hibernatorv1alpha1.HibernatePlan, operation string) *hibernatorv1alpha1.ExecutionOperationSummary {
	summary := &hibernatorv1alpha1.ExecutionOperationSummary{
		Operation: operation,
		Success:   true,
		StartTime: metav1.NewTime(clk.Now()),
	}

	for _, exec := range plan.Status.Executions {
		if exec.State == hibernatorv1alpha1.StateFailed || exec.State == hibernatorv1alpha1.StateAborted {
			summary.Success = false
		}

		if exec.StartedAt != nil {
			if summary.StartTime.IsZero() || exec.StartedAt.Before(&summary.StartTime) {
				summary.StartTime = *exec.StartedAt.DeepCopy()
			}
		}

		if exec.FinishedAt != nil {
			if summary.EndTime.IsZero() || exec.FinishedAt.After(summary.EndTime.Time) {
				summary.EndTime = exec.FinishedAt
			}
		}

		summary.TargetResults = append(summary.TargetResults, hibernatorv1alpha1.TargetExecutionResult{
			Target:      exec.Target,
			State:       exec.State,
			Attempts:    exec.Attempts,
			ExecutionID: strings.TrimPrefix(exec.LogsRef, wellknown.ExecutionIDLogPrefix),
			StartedAt:   exec.StartedAt,
			FinishedAt:  exec.FinishedAt,
		})
	}
	return summary
}

// IsOperationComplete checks if all targets in an operation have reached terminal state.
func IsOperationComplete(plan *hibernatorv1alpha1.HibernatePlan) bool {
	for _, exec := range plan.Status.Executions {
		if exec.State != hibernatorv1alpha1.StateCompleted &&
			exec.State != hibernatorv1alpha1.StateFailed &&
			exec.State != hibernatorv1alpha1.StateAborted {
			return false
		}
	}
	return true
}

// JobExistsForTarget checks if a non-stale job already exists for a given target/operation/cycleID.
// Stale runner jobs (LabelStaleRunnerJob) are excluded — their presence does not block new job dispatch.
func JobExistsForTarget(jobs []batchv1.Job, targetName, operation, cycleID string) bool {
	for _, job := range jobs {
		// Skip stale runner jobs marked during retry/recovery.
		if _, ok := job.Labels[wellknown.LabelStaleRunnerJob]; ok {
			continue
		}
		if job.Labels[wellknown.LabelTarget] == targetName &&
			job.Labels[wellknown.LabelOperation] == operation &&
			job.Labels[wellknown.LabelCycleID] == cycleID {
			return true
		}
	}
	return false
}

// FilterJobsForStage returns jobs that match targets in the given stage.
func FilterJobsForStage(jobs []batchv1.Job, stage scheduler.ExecutionStage) []batchv1.Job {
	targetSet := make(map[string]bool, len(stage.Targets))
	for _, t := range stage.Targets {
		targetSet[t] = true
	}

	var filtered []batchv1.Job
	for _, job := range jobs {
		if targetSet[job.Labels[wellknown.LabelTarget]] {
			filtered = append(filtered, job)
		}
	}
	return filtered
}

// ---------------------------------------------------------------------------
// Package-level helpers (no receiver state needed)
// ---------------------------------------------------------------------------

// findOrAppendCycle looks for the given cycleID in the plan status history and returns its index
func findOrAppendCycle(st *hibernatorv1alpha1.HibernatePlanStatus, cycleID string) int {
	for i, c := range st.ExecutionHistory {
		if c.CycleID == cycleID {
			return i
		}
	}
	st.ExecutionHistory = append(st.ExecutionHistory, hibernatorv1alpha1.ExecutionCycle{CycleID: cycleID})
	return len(st.ExecutionHistory) - 1
}

// pruneCycleHistory keeps only the most recent 5 cycles in the plan status history to prevent unbounded growth
func pruneCycleHistory(st *hibernatorv1alpha1.HibernatePlanStatus) {
	if len(st.ExecutionHistory) > wellknown.MaxCycleHistorySize {
		st.ExecutionHistory = st.ExecutionHistory[len(st.ExecutionHistory)-5:]
	}
}

// executionSnapshot captures the progress-relevant fields of an ExecutionStatus
// for producer-side dedup in the execute() hot loop. Fields that change only on
// state transitions (State) and fields that change during Running (Attempts,
// StartedAt, JobRef, LogsRef, Message) are all included so that incremental
// progress within a phase is persisted to K8s, not just terminal transitions.
type executionSnapshot struct {
	State    hibernatorv1alpha1.ExecutionState
	Attempts int32
	Message  string
	JobRef   string
	LogsRef  string
}

// snapshotExecutionStates creates a map of target name to execution snapshot
// for the given list of execution statuses.
func snapshotExecutionStates(execs []hibernatorv1alpha1.ExecutionStatus) map[string]executionSnapshot {
	m := make(map[string]executionSnapshot, len(execs))
	for _, e := range execs {
		m[e.Target] = executionSnapshot{
			State:    e.State,
			Attempts: e.Attempts,
			Message:  e.Message,
			JobRef:   e.JobRef,
			LogsRef:  e.LogsRef,
		}
	}
	return m
}

// executionStatesEqual compares the previous execution snapshots with the current
// execution statuses to determine if there has been any meaningful progress change.
func executionStatesEqual(prev map[string]executionSnapshot, current []hibernatorv1alpha1.ExecutionStatus) bool {
	if len(prev) != len(current) {
		return false
	}
	for _, e := range current {
		p, ok := prev[e.Target]
		if !ok {
			return false
		}
		if p.State != e.State || p.Attempts != e.Attempts ||
			p.Message != e.Message || p.JobRef != e.JobRef || p.LogsRef != e.LogsRef {
			return false
		}
	}
	return true
}
