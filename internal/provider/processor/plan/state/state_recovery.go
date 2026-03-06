/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"fmt"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/internal/recovery"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/go-logr/logr"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// recoveryState implements exponential-backoff retry for plans in PhaseError.
type recoveryState struct {
	*State
}

func (state *recoveryState) Handle(ctx context.Context) {
	plan := state.plan()
	log := state.Log.
		WithName("recovery").
		WithValues(
			"plan", state.Key.String(),
			"errorMessage", plan.Status.ErrorMessage)

	log.V(1).Info("processing error recovery",
		"cycleID", plan.Status.CurrentCycleID,
		"currentOperation", plan.Status.CurrentOperation)

	var lastErr error
	if plan.Status.ErrorMessage != "" {
		lastErr = fmt.Errorf("%s", plan.Status.ErrorMessage)
	}

	strategy := recovery.DetermineRecoveryStrategy(plan, state.Clock, lastErr)
	if !strategy.ShouldRetry {
		// Cancel any pending timer — no more auto-retries.
		state.CancelRequeue()

		// Check for manual retry via annotation.
		if state.handleManualRetry(ctx, log) {
			return
		}
		log.Info("error recovery aborted, manual intervention required",
			"classification", recovery.ClassifyError(lastErr),
			"reason", strategy.Reason)
		return
	}

	if strategy.RetryAfter > 0 {
		// Still within backoff — schedule a timer to re-drive this handler exactly when ready.
		log.Info(strategy.Reason, "retryAfter", strategy.RetryAfter.String())
		state.RequeueAfter(strategy.RetryAfter)
		return
	}

	log.Info("attempting error recovery",
		"classification", strategy.Classification,
		"reason", strategy.Reason,
		"attempt", plan.Status.RetryCount+1,
	)

	// Ready to retry.
	state.CancelRequeue()
	state.clearRetryAtAnnotation(ctx, log, plan)

	state.handleRetry(ctx, log, lastErr)
}

// handleManualRetry checks for the retry-now annotation and resets retry state if found.
// Returns true if a manual retry was triggered.
func (state *recoveryState) handleManualRetry(ctx context.Context, log logr.Logger) bool {
	plan := state.plan()
	val, ok := plan.Annotations[wellknown.AnnotationRetryNow]
	if !ok || val != "true" {
		return false
	}

	log.Info("manual retry triggered via annotation")

	orig := plan.DeepCopy()
	delete(plan.Annotations, wellknown.AnnotationRetryNow)
	if err := state.patchPreservingStatus(ctx, plan, client.MergeFrom(orig)); err != nil {
		log.Error(err, "failed to clear manual retry annotation")
		state.RequeueAfter(wellknown.RequeueIntervalOnRecoveryError)
		return false
	}

	mutate := func(st *hibernatorv1alpha1.HibernatePlanStatus) {
		st.RetryCount = 0
		st.LastRetryTime = nil
	}

	mutate(&plan.Status)
	state.Statuses.PlanStatuses.Send(&message.PlanStatusUpdate{
		NamespacedName: state.Key,
		Mutate:         mutate,
	})

	state.dispatch(ctx)
	return true
}

func (state *recoveryState) handleRetry(ctx context.Context, log logr.Logger, lastErr error) {
	plan := state.plan()

	shouldHibernate := false
	if state.PlanCtx.ScheduleResult != nil {
		shouldHibernate = state.PlanCtx.ScheduleResult.ShouldHibernate
	}

	operation := "wakeup"
	if shouldHibernate {
		operation = "shutdown"
	}

	execPlan, err := state.buildExecutionPlan(plan, operation == "wakeup")
	if err != nil {
		log.Error(err, "failed to rebuild execution plan during recovery, repeat may be attempted if this is a transient error")
		state.RequeueAfter(wellknown.RequeueIntervalOnRecoveryError)
		return
	}

	state.relabelStaleFailedJobs(ctx, log, plan, operation)

	mutate := func(st *hibernatorv1alpha1.HibernatePlanStatus) {
		if lastErr == nil {
			lastErr = fmt.Errorf("unknown error (no error message in status)")
		}

		if ok := recovery.RecordRetryAttemptOnStatus(st, state.Clock, lastErr); !ok {
			log.V(1).Info("retry attempt not recorded", "error", lastErr)
			return
		}

		currentStage := execPlan.Stages[plan.Status.CurrentStageIndex]
		for _, targetName := range currentStage.Targets {
			for i, exec := range st.Executions {
				if exec.Target == targetName && exec.State == hibernatorv1alpha1.StateFailed {
					st.Executions[i].State = hibernatorv1alpha1.StatePending
					st.Executions[i].Message = "State reset for retry (on error recovery)"
				}
			}
		}

		if shouldHibernate {
			st.Phase = hibernatorv1alpha1.PhaseHibernating
		} else {
			st.Phase = hibernatorv1alpha1.PhaseWakingUp
		}
	}

	mutate(&plan.Status)
	state.Statuses.PlanStatuses.Send(&message.PlanStatusUpdate{
		NamespacedName: state.Key,
		Mutate:         mutate,
	})

	log.Info("transitioning on recovery", "phase", plan.Status.Phase, "attempt", plan.Status.RetryCount)
	state.dispatch(ctx)
}

func (state *recoveryState) clearRetryAtAnnotation(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) {
	if _, ok := plan.Annotations[wellknown.AnnotationRetryAt]; !ok {
		return
	}
	orig := plan.DeepCopy()
	delete(plan.Annotations, wellknown.AnnotationRetryAt)
	if err := state.patchPreservingStatus(ctx, plan, client.MergeFrom(orig)); err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "failed to clear retry-at annotation (non-fatal)")
		state.RequeueAfter(wellknown.RequeueIntervalOnRecoveryError)
		return
	}
}

func (state *recoveryState) relabelStaleFailedJobs(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan, operation string) {
	var jobList batchv1.JobList
	if err := state.List(ctx, &jobList,
		client.InNamespace(plan.Namespace),
		client.MatchingLabels{
			wellknown.LabelPlan:      plan.Name,
			wellknown.LabelCycleID:   plan.Status.CurrentCycleID,
			wellknown.LabelOperation: operation,
		},
	); err != nil {
		log.Error(err, "failed to list stale jobs for relabeling")
		state.RequeueAfter(wellknown.RequeueIntervalOnRecoveryError)
		return
	}

	count := 0
	for i := range jobList.Items {
		job := &jobList.Items[i]

		if _, ok := job.Labels[wellknown.LabelStaleRunnerJob]; ok {
			continue
		}

		if job.Status.Failed == 0 {
			continue
		}
		orig := job.DeepCopy()
		if job.Labels == nil {
			job.Labels = make(map[string]string)
		}
		job.Labels[wellknown.LabelStaleRunnerJob] = "true"
		job.Labels[wellknown.LabelStaleReasonRunnerJob] = "retry-recovery"
		if err := state.Patch(ctx, job, client.MergeFrom(orig)); err != nil {
			log.Error(err, "failed to relabel stale job", "job", job.Name)
			state.RequeueAfter(wellknown.RequeueIntervalOnRecoveryError)
			return
		} else {
			count++
		}
	}
	if count > 0 {
		log.Info("relabeled stale failed jobs", "count", count, "operation", operation)
	}
}
