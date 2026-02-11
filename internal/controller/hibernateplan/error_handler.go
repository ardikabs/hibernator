/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package hibernateplan

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/controller/status"
	"github.com/ardikabs/hibernator/internal/recovery"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// handleErrorRecovery implements error recovery with exponential backoff.
func (r *Reconciler) handleErrorRecovery(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (ctrl.Result, error) {
	log.Info("handling error recovery",
		"retryCount", plan.Status.RetryCount,
		"errorMessage", plan.Status.ErrorMessage,
	)

	// Create a dummy error from the stored error message
	var lastErr error
	if plan.Status.ErrorMessage != "" {
		lastErr = fmt.Errorf("%s", plan.Status.ErrorMessage)
	}

	// Determine recovery strategy
	strategy := recovery.DetermineRecoveryStrategy(plan, lastErr)

	log.Info("recovery strategy determined",
		"shouldRetry", strategy.ShouldRetry,
		"retryAfter", strategy.RetryAfter,
		"classification", strategy.Classification,
		"reason", strategy.Reason,
	)

	if !strategy.ShouldRetry {
		// Max retries exceeded or permanent error
		log.Info("error recovery aborted", "reason", strategy.Reason, "classification", recovery.ClassifyError(lastErr))

		// Stay in error state, requiring manual intervention
		return ctrl.Result{}, nil
	}

	if strategy.RetryAfter > 0 {
		// Still waiting for backoff period
		log.Info("waiting for backoff period", "retryAfter", strategy.RetryAfter)
		return ctrl.Result{RequeueAfter: strategy.RetryAfter}, nil
	}

	// Ready to retry - determine which phase to transition to
	log.Info("attempting error recovery")

	// Evaluate current schedule to determine target phase
	shouldHibernate, _, err := r.evaluateSchedule(ctx, log, plan)
	if err != nil {
		log.Error(err, "failed to evaluate schedule during recovery")
		return ctrl.Result{RequeueAfter: wellknown.RequeueIntervalOnScheduleError}, nil
	}

	// Determine operation type for job query
	operation := "wakeup"
	if shouldHibernate {
		operation = "shutdown"
	}

	execPlan, err := r.buildExecutionPlan(plan, operation == "wakeup")
	if err != nil {
		log.Error(err, "failed to rebuild execution plan during recovery")
		return ctrl.Result{RequeueAfter: wellknown.RequeueIntervalOnScheduleError}, nil
	}

	// Relabel stale failed jobs from current cycle to unblock retry
	r.relabelStaleFailedJobs(ctx, log, plan, operation)

	if err := r.statusUpdater.Update(ctx, plan, status.MutatorFunc(func(obj client.Object) client.Object {
		p := obj.(*hibernatorv1alpha1.HibernatePlan)

		// Ensure error is not nil before recording retry
		if lastErr == nil {
			lastErr = fmt.Errorf("unknown error (no error message in status)")
		}

		recovery.RecordRetryAttempt(p, r.Clock, lastErr)

		currentStage := execPlan.Stages[p.Status.CurrentStageIndex]
		for _, target := range currentStage.Targets {
			for i, exec := range p.Status.Executions {
				if exec.Target == target {
					if exec.State == hibernatorv1alpha1.StateFailed {
						p.Status.Executions[i].Message = "State reset for retry (on error recovery)"
						p.Status.Executions[i].State = hibernatorv1alpha1.StatePending
					}
				}
			}
		}

		// Transition to appropriate phase
		if shouldHibernate {
			p.Status.Phase = hibernatorv1alpha1.PhaseHibernating
			log.Info("transitioning to hibernating phase for recovery", "attempt", plan.Status.RetryCount)
		} else {
			p.Status.Phase = hibernatorv1alpha1.PhaseWakingUp
			log.Info("transitioning to waking up phase for recovery", "attempt", plan.Status.RetryCount)
		}

		return p
	})); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{Requeue: true}, nil
}

// setError transitions the plan to error state.
func (r *Reconciler) setError(ctx context.Context, plan *hibernatorv1alpha1.HibernatePlan, phaseErr error) (ctrl.Result, error) {
	if err := r.statusUpdater.Update(ctx, plan, status.MutatorFunc(func(obj client.Object) client.Object {
		p := obj.(*hibernatorv1alpha1.HibernatePlan)
		p.Status.Phase = hibernatorv1alpha1.PhaseError
		p.Status.LastTransitionTime = ptr.To(metav1.NewTime(r.Clock.Now()))

		if phaseErr != nil {
			p.Status.ErrorMessage = phaseErr.Error()
		} else {
			p.Status.ErrorMessage = "unknown error"
		}

		return p
	})); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status onError: %w, phaseErr: %v", err, phaseErr)
	}

	return ctrl.Result{RequeueAfter: wellknown.RequeueIntervalOnScheduleError}, nil
}

// relabelStaleFailedJobs identifies and relabels failed jobs from the current cycle to unblock retry.
func (r *Reconciler) relabelStaleFailedJobs(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan, operation string) {
	var staleJobList batchv1.JobList
	if err := r.List(ctx, &staleJobList,
		client.InNamespace(plan.Namespace),
		client.MatchingLabels{
			wellknown.LabelPlan:      plan.Name,
			wellknown.LabelCycleID:   plan.Status.CurrentCycleID,
			wellknown.LabelOperation: operation,
		},
	); err != nil {
		log.Error(err, "failed to list stale jobs for relabeling")
		// Continue anyway - don't fail recovery over this
		return
	}

	staleJobCount := 0
	for i := range staleJobList.Items {
		staleJob := &staleJobList.Items[i]

		// Only relabel failed jobs
		if staleJob.Status.Failed == 0 {
			continue
		}

		// Remove cycle ID label to exclude from future queries
		orig := staleJob.DeepCopy()

		if staleJob.Labels == nil {
			staleJob.Labels = make(map[string]string)
		}

		// Mark as stale for observability
		staleJob.Labels[wellknown.LabelStaleRunnerJob] = "true"
		staleJob.Labels[wellknown.LabelStaleReasonRunnerJob] = "retry-recovery"
		if err := r.Patch(ctx, staleJob, client.MergeFrom(orig)); err != nil {
			log.Error(err, "failed to relabel stale job",
				"jobName", staleJob.Name,
				"jobNamespace", staleJob.Namespace)
		} else {
			staleJobCount++
		}
	}

	if staleJobCount > 0 {
		log.Info("relabeled stale failed jobs",
			"count", staleJobCount,
			"operation", operation,
			"cycleID", plan.Status.CurrentCycleID)
	}
}
