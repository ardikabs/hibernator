package hibernateplan

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/controller/status"
	"github.com/ardikabs/hibernator/internal/recovery"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/ardikabs/hibernator/pkg/k8sutil"
)

// initializeOperation prepares a plan for a new operation (shutdown or wakeup).
func (r *Reconciler) initializeOperation(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan, operation string) (scheduler.ExecutionPlan, error) {
	log.Info("initializing operation", "operation", operation, "planName", plan.Name, "numTargets", len(plan.Spec.Targets))

	// Build execution plan
	isWakeup := operation == "wakeup"

	log.V(1).Info("building execution plan", "operation", operation, "isWakeup", isWakeup, "strategy", plan.Spec.Execution.Strategy.Type)
	execPlan, err := r.buildExecutionPlan(plan, isWakeup)
	if err != nil {
		log.Error(err, "failed to build execution plan", "operation", operation)
		return scheduler.ExecutionPlan{}, err
	}

	log.V(1).Info("execution plan built", "operation", operation, "numStages", len(execPlan.Stages))
	for i, stage := range execPlan.Stages {
		log.V(1).Info("stage details", "stageIndex", i, "numTargets", len(stage.Targets), "targets", stage.Targets)
	}

	// Initialize execution status - fresh start for each operation
	log.V(1).Info("resetting execution statuses", "operation", operation, "numTargets", len(plan.Spec.Targets))

	if err := r.statusUpdater.Update(ctx, plan, status.MutatorFunc(func(obj client.Object) client.Object {
		p := obj.(*hibernatorv1alpha1.HibernatePlan)

		p.Status.Executions = make([]hibernatorv1alpha1.ExecutionStatus, len(p.Spec.Targets))
		for i, target := range plan.Spec.Targets {
			p.Status.Executions[i] = hibernatorv1alpha1.ExecutionStatus{
				Target:   fmt.Sprintf("%s/%s", target.Type, target.Name),
				Executor: target.Type,
				State:    hibernatorv1alpha1.StatePending,
			}
		}

		// Set phase based on operation
		if operation == "shutdown" {
			p.Status.CurrentCycleID = uuid.New().String()[:8]
			p.Status.Phase = hibernatorv1alpha1.PhaseHibernating
		} else {
			p.Status.Phase = hibernatorv1alpha1.PhaseWakingUp
		}

		p.Status.CurrentStageIndex = 0
		p.Status.CurrentOperation = operation
		p.Status.LastTransitionTime = ptr.To(metav1.NewTime(r.Clock.Now()))
		return p
	})); err != nil {
		return scheduler.ExecutionPlan{}, err
	}

	log.V(1).Info("plan status updated", "operation", operation, "newPhase", plan.Status.Phase)
	return execPlan, nil
}

// buildOperationSummary creates a summary of the current operation from execution statuses.
func (r *Reconciler) buildOperationSummary(ctx context.Context, plan *hibernatorv1alpha1.HibernatePlan, operation string) *hibernatorv1alpha1.ExecutionOperationSummary {
	summary := &hibernatorv1alpha1.ExecutionOperationSummary{
		Operation: operation,
		StartTime: metav1.NewTime(r.Clock.Now()),
		Success:   true,
	}

	// Build target results from execution statuses
	for _, exec := range plan.Status.Executions {
		if exec.State == hibernatorv1alpha1.StateFailed {
			summary.Success = false
		}

		executionID := exec.JobRef
		job := &batchv1.Job{}
		if jobName, err := k8sutil.ObjectKeyFromString(exec.JobRef); err == nil {
			if err := r.Get(ctx, jobName, job); err == nil {
				if id, ok := job.Labels[wellknown.LabelExecutionID]; ok {
					executionID = id
				}
			}
		}

		summary.TargetResults = append(summary.TargetResults, hibernatorv1alpha1.TargetExecutionResult{
			Target:      exec.Target,
			State:       exec.State,
			Attempts:    exec.Attempts,
			ExecutionID: executionID,
		})
	}

	now := metav1.NewTime(r.Clock.Now())
	summary.EndTime = &now

	return summary
}

// cleanupAfterWakeUp handles restore data cleanup after successful wake-up.
// This is separated from finalizeOperation to keep status updates and restore data
// management concerns cleanly separated.
func (r *Reconciler) cleanupAfterWakeUp(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) error {
	orig := plan.DeepCopy()

	// Extract target names
	targetNames := make([]string, 0, len(plan.Spec.Targets))
	for _, target := range plan.Spec.Targets {
		targetNames = append(targetNames, target.Name)
	}

	// Check if all targets have been marked as restored
	restored, err := r.RestoreManager.MarkAllTargetsRestored(ctx, plan.Namespace, plan.Name, targetNames)
	if err != nil {
		return fmt.Errorf("check restored targets: %w", err)
	}

	if !restored {
		log.V(1).Info("not all targets restored yet, keeping restore data locked")
		return nil
	}

	log.Info("all targets restored, unlocking restore data")

	// Unlock restore data (clear restored-* annotations)
	if err := r.RestoreManager.UnlockRestoreData(ctx, plan.Namespace, plan.Name); err != nil {
		return fmt.Errorf("unlock restore data: %w", err)
	}

	// Clean up suspension tracking annotation
	if _, ok := plan.Annotations[wellknown.AnnotationSuspendedAtPhase]; ok {
		delete(plan.Annotations, wellknown.AnnotationSuspendedAtPhase)

		if err := r.Patch(ctx, plan, client.MergeFrom(orig)); err != nil {
			return fmt.Errorf("remove restore data locked annotation: %w", err)
		}
		log.V(1).Info("removed restore data locked annotation")
	}

	return nil
}

// finalizeOperation completes an operation and transitions the plan phase.
func (r *Reconciler) finalizeOperation(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan, operation string) error {
	// Build summary once (uses current execution statuses)
	summary := r.buildOperationSummary(ctx, plan, operation)
	currentCycleID := plan.Status.CurrentCycleID

	if err := r.statusUpdater.Update(ctx, plan, status.MutatorFunc(func(obj client.Object) client.Object {
		p := obj.(*hibernatorv1alpha1.HibernatePlan)

		// Append operation to execution history (idempotent)
		cycleIndex := -1
		for i, cycle := range p.Status.ExecutionHistory {
			if cycle.CycleID == currentCycleID {
				cycleIndex = i
				break
			}
		}

		if cycleIndex == -1 {
			p.Status.ExecutionHistory = append(p.Status.ExecutionHistory, hibernatorv1alpha1.ExecutionCycle{
				CycleID: currentCycleID,
			})
			cycleIndex = len(p.Status.ExecutionHistory) - 1
		}

		if operation == "shutdown" {
			p.Status.Phase = hibernatorv1alpha1.PhaseHibernated
			if p.Status.ExecutionHistory[cycleIndex].ShutdownExecution == nil {
				p.Status.ExecutionHistory[cycleIndex].ShutdownExecution = summary
			}
		} else if operation == "wakeup" {
			p.Status.Phase = hibernatorv1alpha1.PhaseActive
			if p.Status.ExecutionHistory[cycleIndex].WakeupExecution == nil {
				p.Status.ExecutionHistory[cycleIndex].WakeupExecution = summary
			}
		}

		// Prune old cycles if exceeding max 5
		if len(p.Status.ExecutionHistory) > 5 {
			p.Status.ExecutionHistory = p.Status.ExecutionHistory[len(p.Status.ExecutionHistory)-5:]
		}

		recovery.ResetRetryState(p)
		p.Status.LastTransitionTime = ptr.To(metav1.NewTime(r.Clock.Now()))
		return p
	})); err != nil {
		return err
	}

	log.Info("operation completed", "operation", operation, "cycleID", currentCycleID)
	return nil
}

// isOperationComplete checks if all targets in an operation have reached terminal state.
func (r *Reconciler) isOperationComplete(plan *hibernatorv1alpha1.HibernatePlan) bool {
	for _, exec := range plan.Status.Executions {
		if exec.State != hibernatorv1alpha1.StateCompleted && exec.State != hibernatorv1alpha1.StateFailed {
			return false
		}
	}
	return true
}
