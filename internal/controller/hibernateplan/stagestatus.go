package hibernateplan

import (
	"fmt"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/go-logr/logr"
)

// StageStatus provides detailed status information about a stage's execution progress.
type StageStatus struct {
	// AllTerminal is true when all targets in the stage have reached a terminal state (Completed or Failed).
	AllTerminal bool
	// HasRunning is true when at least one target is currently running.
	HasRunning bool
	// HasPending is true when at least one target is still pending (no job created yet).
	HasPending bool
	// FailedCount is the number of targets that have failed.
	FailedCount int
	// CompletedCount is the number of targets that have completed successfully.
	CompletedCount int
}

// getStageStatus returns detailed status information about a stage's execution progress.
func (r *Reconciler) getStageStatus(log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan, stage scheduler.ExecutionStage) StageStatus {
	status := StageStatus{}
	terminalCount := 0

	for _, targetName := range stage.Targets {
		// Find the execution status for this target
		targetType := r.findTargetType(plan, targetName)
		targetID := fmt.Sprintf("%s/%s", targetType, targetName)

		found := false
		for _, exec := range plan.Status.Executions {
			if exec.Target == targetID {
				found = true
				switch exec.State {
				case hibernatorv1alpha1.StateCompleted:
					status.CompletedCount++
					terminalCount++
				case hibernatorv1alpha1.StateFailed:
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
			// Target not yet in execution list - treat as pending
			log.V(1).Info("target not found in execution list", "target", targetID, "stageTargets", stage.Targets)
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
