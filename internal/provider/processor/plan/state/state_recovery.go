/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"fmt"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/notification"
	statusprocessor "github.com/ardikabs/hibernator/internal/provider/processor/status"
	"github.com/ardikabs/hibernator/internal/recovery"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/go-logr/logr"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// recoveryState implements exponential-backoff retry for plans in PhaseError.
type recoveryState struct {
	*state
}

func (state *recoveryState) Handle(ctx context.Context) (StateResult, error) {
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
		// Check for manual retry via annotation.
		if handled, result, err := state.handleManualRetry(ctx, log); handled {
			return result, err
		}
		log.Info("error recovery aborted, manual intervention required",
			"classification", recovery.ClassifyError(lastErr),
			"reason", strategy.Reason)
		return StateResult{}, nil
	}

	if strategy.RetryAfter > 0 {
		// Still within backoff — schedule a timer to re-drive this handler exactly when ready.
		log.Info("backoff in progress", "reason", strategy.Reason, "retryAfter", strategy.RetryAfter.String())
		return StateResult{RequeueAfter: strategy.RetryAfter}, nil
	}

	log.Info("attempting error recovery",
		"classification", strategy.Classification,
		"reason", strategy.Reason,
		"attempt", plan.Status.RetryCount+1,
	)

	// Ready to retry.
	state.clearRetryAtAnnotation(ctx, log, plan)
	return state.handleRetry(ctx, log, lastErr)
}

// handleManualRetry checks for the retry-now annotation and resets retry state if found.
// Returns (handled, result, err) where handled=true means a manual retry was triggered.
func (state *recoveryState) handleManualRetry(ctx context.Context, log logr.Logger) (bool, StateResult, error) {
	plan := state.plan()
	val, ok := plan.Annotations[wellknown.AnnotationRetryNow]
	if !ok || val != "true" {
		return false, StateResult{}, nil
	}

	log.Info("manual retry triggered via annotation")

	orig := plan.DeepCopy()
	delete(plan.Annotations, wellknown.AnnotationRetryNow)
	if err := state.patchAndPreserveStatus(ctx, plan, client.MergeFrom(orig)); err != nil {
		log.Error(err, "failed to clear manual retry annotation")
		return true, StateResult{RequeueAfter: wellknown.RequeueIntervalOnRecoveryError}, nil
	}

	state.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: state.Key,
		Resource:       plan,
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.RetryCount = 0
			p.Status.LastRetryTime = nil
		}),
	})

	return true, StateResult{Requeue: true}, nil
}

func (state *recoveryState) handleRetry(ctx context.Context, log logr.Logger, lastErr error) (StateResult, error) {
	plan := state.plan()

	operation := state.determineRetryOperation(log, plan)
	shouldHibernate := operation == hibernatorv1alpha1.OperationHibernate

	execPlan, err := state.buildExecutionPlan(plan, operation == hibernatorv1alpha1.OperationWakeUp)
	if err != nil {
		log.Error(err, "failed to rebuild execution plan during recovery, repeat may be attempted if this is a transient error")
		return StateResult{}, err
	}

	state.relabelStaleFailedJobs(ctx, log, plan, operation)

	currentPhase := plan.Status.Phase
	targetPhase := hibernatorv1alpha1.PhaseHibernating
	if !shouldHibernate {
		targetPhase = hibernatorv1alpha1.PhaseWakingUp
	}

	state.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: state.Key,
		Resource:       plan,
		PreHook: state.notifyHook(hibernatorv1alpha1.EventRecovery, func(p *hibernatorv1alpha1.HibernatePlan) notification.Payload {
			// PreHook sees pre-mutation state — override with target values.
			payload := buildPayload(p, hibernatorv1alpha1.EventRecovery, state.Clock.Now)
			payload.Phase = string(targetPhase)
			payload.Operation = string(operation)
			return payload
		}),
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			if lastErr == nil {
				lastErr = fmt.Errorf("unknown error (no error message in status)")
			}

			if ok := recovery.RecordRetryAttempt(p, state.Clock, lastErr); !ok {
				log.V(1).Info("retry attempt not recorded", "error", lastErr)
				return
			}

			currentStage := execPlan.Stages[plan.Status.CurrentStageIndex]
			for _, targetName := range currentStage.Targets {
				for i, exec := range p.Status.Executions {
					if exec.Target == targetName && exec.State == hibernatorv1alpha1.StateFailed {
						p.Status.Executions[i].State = hibernatorv1alpha1.StatePending
						p.Status.Executions[i].Message = "Execution state reset for retry after failure"
					}
				}
			}

			p.Status.Phase = targetPhase
		}),
		PostHook: state.phaseChangePostHook(currentPhase),
	})

	log.Info("transitioning on recovery",
		"fromPhase", currentPhase,
		"toPhase", targetPhase,
		"operation", operation,
		"attempt", plan.Status.RetryCount,
	)
	return StateResult{Requeue: true}, nil
}

// determineRetryOperation determines which operation should be retried.
//
// It reads plan.Status.CurrentOperation as the source of truth — the plan
// failed mid-operation and the retry must resume from that same operation,
// regardless of where the schedule window currently sits. Dispatching the
// opposite operation would corrupt resource state (e.g. waking up resources
// that were never fully hibernated).
//
// If CurrentOperation is absent (edge case for very old plans that pre-date
// the field), it falls back to the current schedule as a best-effort.
// When the persisted operation conflicts with the current schedule window, a
// structured warning is logged to guide the operator.
func (state *recoveryState) determineRetryOperation(log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) hibernatorv1alpha1.PlanOperation {
	operation := hibernatorv1alpha1.PlanOperation(plan.Status.CurrentOperation)
	if operation == "" {
		if state.PlanCtx.Schedule != nil && state.PlanCtx.Schedule.ShouldHibernate {
			operation = hibernatorv1alpha1.OperationHibernate
		} else {
			operation = hibernatorv1alpha1.OperationWakeUp
		}
		log.Info("CurrentOperation is absent, falling back to schedule-derived operation", "operation", operation)
		return operation
	}

	scheduledShouldHibernate := state.PlanCtx.Schedule != nil && state.PlanCtx.Schedule.ShouldHibernate
	operationShouldHibernate := operation == hibernatorv1alpha1.OperationHibernate
	if operationShouldHibernate != scheduledShouldHibernate {
		// The failed operation no longer aligns with the current schedule window.
		// We still retry the original operation as-is: a recovery attempt must
		// resume from the point of failure. If the operator wants the plan to
		// follow the current schedule instead, they should suspend the plan and
		// perform manual intervention, or delete and resubmit it to reset the
		// status entirely.
		scheduleOperation := hibernatorv1alpha1.OperationWakeUp
		if scheduledShouldHibernate {
			scheduleOperation = hibernatorv1alpha1.OperationHibernate
		}
		log.Info("retrying failed operation that conflicts with current schedule window — "+
			"proceeding with original operation; to follow the current schedule, "+
			"suspend the plan and perform manual intervention or resubmit the plan",
			"failedOperation", operation,
			"scheduleOperation", scheduleOperation,
		)
	}

	return operation
}

func (state *recoveryState) clearRetryAtAnnotation(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) {
	if _, ok := plan.Annotations[wellknown.AnnotationRetryAt]; !ok {
		return
	}
	orig := plan.DeepCopy()
	delete(plan.Annotations, wellknown.AnnotationRetryAt)
	if err := state.patchAndPreserveStatus(ctx, plan, client.MergeFrom(orig)); err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "failed to clear retry-at annotation (non-fatal)")
		return
	}
}

func (state *recoveryState) relabelStaleFailedJobs(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan, operation hibernatorv1alpha1.PlanOperation) {
	var jobList batchv1.JobList
	if err := state.List(ctx, &jobList,
		client.InNamespace(plan.Namespace),
		client.MatchingLabels{
			wellknown.LabelPlan:      plan.Name,
			wellknown.LabelCycleID:   plan.Status.CurrentCycleID,
			wellknown.LabelOperation: string(operation),
		},
	); err != nil {
		log.Error(err, "failed to list stale jobs for relabeling")
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
			return
		} else {
			count++
		}
	}
	if count > 0 {
		log.Info("relabeled stale failed jobs", "count", count, "operation", operation)
	}
}
