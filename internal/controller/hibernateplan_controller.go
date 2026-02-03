/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/recovery"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/pkg/k8sutil"
)

const (
	// FinalizerName is the finalizer for HibernatePlan resources.
	FinalizerName = "hibernator.ardikabs.com/finalizer"

	// LabelPlan is the label key for the plan name.
	LabelPlan = "hibernator.ardikabs.com/plan"

	// LabelTarget is the label key for the target name.
	LabelTarget = "hibernator.ardikabs.com/target"

	// LabelExecutionID is the label key for the execution ID.
	LabelExecutionID = "hibernator.ardikabs.com/execution-id"

	// LabelOperation is the label key for the operation type (shutdown or wakeup).
	LabelOperation = "hibernator.ardikabs.com/operation"

	// LabelCycleID is the label key for the cycle ID (isolates jobs by cycle).
	LabelCycleID = "hibernator.ardikabs.com/cycle-id"

	// AnnotationPlan is the annotation for plan name.
	AnnotationPlan = "hibernator/plan"

	// AnnotationTarget is the annotation for target name.
	AnnotationTarget = "hibernator/target"

	// RunnerImage is the default runner image.
	RunnerImage = "ghcr.io/ardikabs/hibernator-runner:latest"

	// StreamTokenAudience is the audience for projected SA tokens.
	StreamTokenAudience = "hibernator-control-plane"

	// StreamTokenExpirationSeconds is the token expiration time.
	StreamTokenExpirationSeconds = 600

	// DefaultJobTTLSeconds is the TTL for completed runner jobs (1 hour).
	DefaultJobTTLSeconds = 3600

	// DefaultJobBackoffLimit is the maximum retries for runner jobs.
	DefaultJobBackoffLimit = 3

	// StageRequeueInterval is the requeue interval during stage execution.
	StageRequeueInterval = 5 * time.Second

	// ExecutionRequeueInterval is the requeue interval during execution reconciliation.
	ExecutionRequeueInterval = 10 * time.Second

	// ScheduleErrorRequeueInterval is the requeue interval when schedule evaluation fails.
	ScheduleErrorRequeueInterval = time.Minute
)

// HibernatePlanReconciler reconciles a HibernatePlan object.
type HibernatePlanReconciler struct {
	client.Client
	APIReader         client.Reader
	Log               logr.Logger
	Scheme            *runtime.Scheme
	Planner           *scheduler.Planner
	ScheduleEvaluator *scheduler.ScheduleEvaluator

	// ControlPlaneEndpoint is the endpoint for runner streaming.
	ControlPlaneEndpoint string

	// RunnerImage is the runner container image.
	RunnerImage string

	// RunnerServiceAccount is the ServiceAccount used by runner pods.
	RunnerServiceAccount string
}

// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=hibernateplans,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=hibernateplans/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=hibernateplans/finalizers,verbs=update
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=cloudproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=k8sclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile handles HibernatePlan reconciliation.
func (r *HibernatePlanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("hibernateplan", req.NamespacedName.String())

	// Fetch the HibernatePlan
	var plan hibernatorv1alpha1.HibernatePlan
	if err := r.Get(ctx, req.NamespacedName, &plan); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !plan.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, log, &plan)
	}

	// Ensure finalizer
	if !controllerutil.ContainsFinalizer(&plan, FinalizerName) {
		controllerutil.AddFinalizer(&plan, FinalizerName)
		if err := r.Update(ctx, &plan); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Initialize status if needed
	if plan.Status.Phase == "" {
		plan.Status.Phase = hibernatorv1alpha1.PhaseActive
		plan.Status.ObservedGeneration = plan.Generation
		if err := r.Status().Update(ctx, &plan); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Query active exception and update status
	if err := r.updateActiveExceptions(ctx, log, &plan); err != nil {
		log.Error(err, "failed to update active exceptions")
		// Don't fail reconciliation, continue with base schedule
	}

	// Re-fetch plan to get updated resourceVersion after status update
	if err := r.Get(ctx, req.NamespacedName, &plan); err != nil {
		return ctrl.Result{}, err
	}

	// Evaluate schedule (with exceptions if present)
	shouldHibernate, requeueAfter, err := r.evaluateSchedule(ctx, &plan)
	if err != nil {
		log.Error(err, "failed to evaluate schedule")
		return ctrl.Result{RequeueAfter: ScheduleErrorRequeueInterval}, nil
	}

	// Determine desired phase based on schedule
	var desiredPhase hibernatorv1alpha1.PlanPhase
	if shouldHibernate {
		switch plan.Status.Phase {
		case hibernatorv1alpha1.PhaseActive:
			desiredPhase = hibernatorv1alpha1.PhaseHibernating
		case hibernatorv1alpha1.PhaseHibernated:
			// Stay hibernated
			desiredPhase = hibernatorv1alpha1.PhaseHibernated
		default:
			// Continue current operation
			desiredPhase = plan.Status.Phase
		}
	} else {
		switch plan.Status.Phase {
		case hibernatorv1alpha1.PhaseHibernated:
			desiredPhase = hibernatorv1alpha1.PhaseWakingUp
		case hibernatorv1alpha1.PhaseActive:
			// Stay active
			desiredPhase = hibernatorv1alpha1.PhaseActive
		default:
			// Continue current operation
			desiredPhase = plan.Status.Phase
		}
	}

	// Handle phase transitions
	switch plan.Status.Phase {
	case hibernatorv1alpha1.PhaseActive:
		if desiredPhase == hibernatorv1alpha1.PhaseHibernating {
			return r.startHibernation(ctx, log, &plan)
		}

	case hibernatorv1alpha1.PhaseHibernating:
		return r.reconcileHibernation(ctx, log, &plan)

	case hibernatorv1alpha1.PhaseHibernated:
		if desiredPhase == hibernatorv1alpha1.PhaseWakingUp {
			return r.startWakeUp(ctx, log, &plan)
		}

	case hibernatorv1alpha1.PhaseWakingUp:
		return r.reconcileWakeUp(ctx, log, &plan)

	case hibernatorv1alpha1.PhaseError:
		// Handle error recovery with retry logic
		return r.handleErrorRecovery(ctx, log, &plan)
	}

	// Requeue based on schedule (next hibernate or wake-up time)
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// updateActiveExceptions queries for active exceptions and updates plan status.
func (r *HibernatePlanReconciler) updateActiveExceptions(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) error {
	// Query for exceptions referencing this plan
	var exceptions hibernatorv1alpha1.ScheduleExceptionList
	if err := r.List(ctx, &exceptions,
		client.InNamespace(plan.Namespace),
		client.MatchingLabels{LabelPlan: plan.Name},
	); err != nil {
		return fmt.Errorf("list exceptions: %w", err)
	}

	// Build exception references from current exceptions
	var activeExceptions []hibernatorv1alpha1.ExceptionReference
	for _, exc := range exceptions.Items {
		ref := hibernatorv1alpha1.ExceptionReference{
			Name:       exc.Name,
			Type:       exc.Spec.Type,
			ValidFrom:  exc.Spec.ValidFrom,
			ValidUntil: exc.Spec.ValidUntil,
			State:      exc.Status.State,
			AppliedAt:  exc.Status.AppliedAt,
			ExpiredAt:  exc.Status.ExpiredAt,
		}
		activeExceptions = append(activeExceptions, ref)
	}

	// Prune old exceptions (max 10, prune expired first, then oldest by expiredAt)
	if len(activeExceptions) > 10 {
		// Separate active and expired
		var active, expired []hibernatorv1alpha1.ExceptionReference
		for _, ref := range activeExceptions {
			if ref.State == hibernatorv1alpha1.ExceptionStateActive {
				active = append(active, ref)
			} else {
				expired = append(expired, ref)
			}
		}

		// Sort expired by expiredAt (newest first)
		for i := 0; i < len(expired)-1; i++ {
			for j := i + 1; j < len(expired); j++ {
				if expired[i].ExpiredAt != nil && expired[j].ExpiredAt != nil {
					if expired[i].ExpiredAt.Before(expired[j].ExpiredAt) {
						expired[i], expired[j] = expired[j], expired[i]
					}
				}
			}
		}

		// Keep all active + newest expired (up to 10 total)
		activeExceptions = active
		if len(activeExceptions) < 10 {
			remaining := 10 - len(activeExceptions)
			if remaining > len(expired) {
				remaining = len(expired)
			}
			activeExceptions = append(activeExceptions, expired[:remaining]...)
		}
	}

	// Update status if changed
	if !hibernatorv1alpha1.ExceptionReferencesEqual(plan.Status.ActiveExceptions, activeExceptions) {
		plan.Status.ActiveExceptions = activeExceptions
		if err := r.Status().Update(ctx, plan); err != nil {
			return fmt.Errorf("update plan status: %w", err)
		}
		log.Info("updated active exceptions", "count", len(activeExceptions))
	}

	return nil
}

// evaluateSchedule checks if we should be in hibernation based on schedule and active exceptions.
func (r *HibernatePlanReconciler) evaluateSchedule(ctx context.Context, plan *hibernatorv1alpha1.HibernatePlan) (bool, time.Duration, error) {
	if r.ScheduleEvaluator == nil {
		// Fallback: always active if no evaluator
		return false, time.Minute, nil
	}

	// Convert OffHourWindows to scheduler format
	baseWindows := make([]scheduler.OffHourWindow, len(plan.Spec.Schedule.OffHours))
	for i, w := range plan.Spec.Schedule.OffHours {
		baseWindows[i] = scheduler.OffHourWindow{
			Start:      w.Start,
			End:        w.End,
			DaysOfWeek: w.DaysOfWeek,
		}
	}

	// Query for active exception
	exception, err := r.getActiveException(ctx, plan)
	if err != nil {
		r.Log.Error(err, "failed to get active exception, evaluating base schedule only")
		// Fall through to evaluate base schedule
		exception = nil
	}

	// Evaluate schedule with exception (if any)
	now := time.Now()
	result, err := r.ScheduleEvaluator.EvaluateWithException(baseWindows, plan.Spec.Schedule.Timezone, exception, now)
	if err != nil {
		return false, time.Minute, err
	}

	requeueAfter := r.ScheduleEvaluator.NextRequeueTime(result, now)
	return result.ShouldHibernate, requeueAfter, nil
}

// getActiveException queries for an active ScheduleException for this plan.
// Returns nil if no active exception exists.
func (r *HibernatePlanReconciler) getActiveException(ctx context.Context, plan *hibernatorv1alpha1.HibernatePlan) (*scheduler.Exception, error) {
	// List exceptions with matching plan label
	var exceptions hibernatorv1alpha1.ScheduleExceptionList
	if err := r.List(ctx, &exceptions,
		client.InNamespace(plan.Namespace),
		client.MatchingLabels{LabelPlan: plan.Name},
	); err != nil {
		return nil, fmt.Errorf("list schedule exceptions: %w", err)
	}

	// Find the first active exception
	for _, exc := range exceptions.Items {
		if exc.Status.State != hibernatorv1alpha1.ExceptionStateActive {
			continue
		}

		// Verify it's within valid period
		now := time.Now()
		if now.Before(exc.Spec.ValidFrom.Time) || now.After(exc.Spec.ValidUntil.Time) {
			continue
		}

		// Convert to scheduler.Exception
		windows := make([]scheduler.OffHourWindow, len(exc.Spec.Windows))
		for i, w := range exc.Spec.Windows {
			windows[i] = scheduler.OffHourWindow{
				Start:      w.Start,
				End:        w.End,
				DaysOfWeek: w.DaysOfWeek,
			}
		}

		// Parse lead time
		var leadTime time.Duration
		if exc.Spec.LeadTime != "" {
			var err error
			leadTime, err = time.ParseDuration(exc.Spec.LeadTime)
			if err != nil {
				r.Log.Error(err, "failed to parse lead time, ignoring", "leadTime", exc.Spec.LeadTime)
			}
		}

		return &scheduler.Exception{
			Type:       scheduler.ExceptionType(exc.Spec.Type),
			ValidFrom:  exc.Spec.ValidFrom.Time,
			ValidUntil: exc.Spec.ValidUntil.Time,
			LeadTime:   leadTime,
			Windows:    windows,
		}, nil
	}

	return nil, nil
}

// buildExecutionPlan creates an execution plan based on the strategy.
func (r *HibernatePlanReconciler) buildExecutionPlan(plan *hibernatorv1alpha1.HibernatePlan, reverse bool) (scheduler.ExecutionPlan, error) {
	targets := make([]string, len(plan.Spec.Targets))
	for i, t := range plan.Spec.Targets {
		targets[i] = t.Name
	}

	var execPlan scheduler.ExecutionPlan
	var err error

	strategy := plan.Spec.Execution.Strategy
	maxConcurrency := int32(1)
	if strategy.MaxConcurrency != nil {
		maxConcurrency = *strategy.MaxConcurrency
	}

	switch strategy.Type {
	case hibernatorv1alpha1.StrategySequential:
		execPlan = r.Planner.PlanSequential(targets)

	case hibernatorv1alpha1.StrategyParallel:
		execPlan = r.Planner.PlanParallel(targets, maxConcurrency)

	case hibernatorv1alpha1.StrategyDAG:
		deps := make([]scheduler.Dependency, len(strategy.Dependencies))
		for i, d := range strategy.Dependencies {
			deps[i] = scheduler.Dependency{From: d.From, To: d.To}
		}
		execPlan, err = r.Planner.PlanDAG(targets, deps, maxConcurrency)
		if err != nil {
			return scheduler.ExecutionPlan{}, err
		}

	case hibernatorv1alpha1.StrategyStaged:
		stages := make([]scheduler.Stage, len(strategy.Stages))
		for i, s := range strategy.Stages {
			mc := int32(1)
			if s.MaxConcurrency != nil {
				mc = *s.MaxConcurrency
			}
			stages[i] = scheduler.Stage{
				Name:           s.Name,
				Parallel:       s.Parallel,
				MaxConcurrency: mc,
				Targets:        s.Targets,
			}
		}
		execPlan = r.Planner.PlanStaged(stages, maxConcurrency)

	default:
		return scheduler.ExecutionPlan{}, fmt.Errorf("unknown strategy type: %s", strategy.Type)
	}

	// Reverse for wake-up (dependencies are reversed)
	if reverse {
		reversed := make([]scheduler.ExecutionStage, len(execPlan.Stages))
		for i, stage := range execPlan.Stages {
			reversed[len(execPlan.Stages)-1-i] = stage
		}
		execPlan.Stages = reversed
	}

	return execPlan, nil
}

func executionsStatusToList(statuses []hibernatorv1alpha1.ExecutionStatus) []string {
	result := make([]string, len(statuses))
	for i, status := range statuses {
		result[i] = fmt.Sprintf("%s/%s", status.Target, status.State)
	}

	return result
}

// executeStage creates jobs for targets in the current stage, respecting maxConcurrency.
func (r *HibernatePlanReconciler) executeStage(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan, execPlan scheduler.ExecutionPlan, stageIndex int, operation string) (ctrl.Result, error) {
	if stageIndex >= len(execPlan.Stages) {
		// All stages complete
		if operation == "shutdown" {
			plan.Status.Phase = hibernatorv1alpha1.PhaseHibernated
		} else {
			plan.Status.Phase = hibernatorv1alpha1.PhaseActive
		}
		now := metav1.Now()
		plan.Status.LastTransitionTime = &now
		if err := r.Status().Update(ctx, plan); err != nil {
			return ctrl.Result{}, err
		}

		// Re-fetch plan after status update to prevent conflicts
		if err := r.Get(ctx, types.NamespacedName{Name: plan.Name, Namespace: plan.Namespace}, plan); err != nil {
			return ctrl.Result{}, err
		}

		log.Info("execution complete", "operation", operation)
		return ctrl.Result{}, nil
	}

	stage := execPlan.Stages[stageIndex]
	log.V(1).Info("executing stage",
		"cycleID", plan.Status.CurrentCycleID,
		"stageIndex", stageIndex,
		"operation", operation,
		"stageTargets", stage.Targets,
		"totalTargets", len(plan.Spec.Targets))

	// List jobs to count running ones for this stage - filter by operation and cycle to avoid seeing old cycle jobs
	var jobList batchv1.JobList
	if err := r.List(ctx, &jobList, client.InNamespace(plan.Namespace), client.MatchingLabels{
		LabelPlan:      plan.Name,
		LabelOperation: operation,
		LabelCycleID:   plan.Status.CurrentCycleID,
	}); err != nil {
		return ctrl.Result{}, err
	}
	log.V(1).Info("job list for stage", "operation", operation, "jobCount", len(jobList.Items))

	// For DAG strategy, check if any target in current stage depends on a failed target
	if plan.Spec.Execution.Strategy.Type == hibernatorv1alpha1.StrategyDAG {
		if failedTargets := r.findFailedDependencies(plan, plan.Spec.Execution.Strategy.Dependencies, stage); len(failedTargets) > 0 {
			return r.setError(ctx, plan, fmt.Errorf("cannot execute stage %d: targets depend on failed targets %v", stageIndex, failedTargets))
		}
	}

	// Count running jobs for targets in this stage
	runningCount := r.countRunningJobsInStage(plan, &jobList, stage)
	maxConcurrency := stage.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = int32(len(stage.Targets)) // Default to all targets
	}

	jobsCreated := 0
	for _, targetName := range stage.Targets {
		target := r.findTarget(plan, targetName)
		if target == nil {
			log.V(1).Info("target not found in spec", "targetName", targetName)
			continue
		}

		log.V(1).Info("processing target for job creation", "targetName", targetName)

		// Check concurrency limit before creating new job
		if int32(runningCount+jobsCreated) >= maxConcurrency {
			log.Info("reached maxConcurrency limit, will retry later", "maxConcurrency", maxConcurrency, "running", runningCount, "queued", jobsCreated)
			break
		}

		// Check if job already exists for this target in current cycle
		// This is the source of truth - use jobList instead of stale execution status
		var existingJobs batchv1.JobList
		err := r.List(ctx, &existingJobs,
			client.InNamespace(plan.Namespace),
			client.MatchingLabels{
				LabelPlan:      plan.Name,
				LabelTarget:    targetName,
				LabelOperation: operation,
				LabelCycleID:   plan.Status.CurrentCycleID,
			})
		if err != nil {
			return ctrl.Result{}, err
		}

		if len(existingJobs.Items) > 0 {
			// Job already exists - skip creation regardless of state
			job := existingJobs.Items[0]
			log.V(1).Info("skipping target, job already exists",
				"targetName", targetName,
				"jobName", job.Name,
				"active", job.Status.Active,
				"succeeded", job.Status.Succeeded,
				"failed", job.Status.Failed)
			continue
		}

		log.Info("dispatching job for target", "target", targetName, "operation", operation)

		// Create job
		if err := r.createRunnerJob(ctx, log, plan, target, operation); err != nil {
			log.Error(err, "failed to create job", "target", targetName)
			// Continue with other targets in best-effort mode
			if plan.Spec.Behavior.Mode == hibernatorv1alpha1.BehaviorStrict && plan.Spec.Behavior.FailFast {
				return r.setError(ctx, plan, err)
			}
		}

		jobsCreated++
	}

	return ctrl.Result{RequeueAfter: StageRequeueInterval}, nil
}

// reconcileExecution checks job statuses and progresses through stages.
func (r *HibernatePlanReconciler) reconcileExecution(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan, operation string) (ctrl.Result, error) {
	log.Info("reconciling execution", "operation", operation, "currentPhase", plan.Status.Phase, "currentStageIndex", plan.Status.CurrentStageIndex)

	// List all jobs for this plan, operation, and cycle
	var jobList batchv1.JobList
	if err := r.List(ctx, &jobList, client.InNamespace(plan.Namespace), client.MatchingLabels{
		LabelPlan:      plan.Name,
		LabelOperation: operation,
		LabelCycleID:   plan.Status.CurrentCycleID,
	}); err != nil {
		return ctrl.Result{}, err
	}
	log.V(1).Info("job list fetched", "operation", operation, "jobCount", len(jobList.Items))

	// Rebuild execution plan to access stage structure
	execPlan, err := r.buildExecutionPlan(plan, operation == "wakeup")
	if err != nil {
		log.Error(err, "failed to rebuild execution plan")
		return r.setError(ctx, plan, err)
	}

	// Update execution statuses
	anyFailed := false
	log.V(1).Info("updating execution statuses", "totalExecutions", len(plan.Status.Executions))

	for i := range plan.Status.Executions {
		exec := &plan.Status.Executions[i]
		log.V(1).Info("processing execution status", "target", exec.Target, "currentState", exec.State, "index", i)

		// Find matching job
		found := false
		for _, job := range jobList.Items {
			targetLabel := job.Labels[LabelTarget]
			expectedTarget := fmt.Sprintf("%s/%s", r.findTargetType(plan, targetLabel), targetLabel)
			if exec.Target != expectedTarget {
				continue
			}

			found = true
			log.V(1).Info("found matching job for target", "target", exec.Target, "jobName", job.Name, "succeeded", job.Status.Succeeded, "failed", job.Status.Failed, "active", job.Status.Active)

			// Update status based on job
			if job.Status.Succeeded > 0 {
				exec.State = hibernatorv1alpha1.StateCompleted
				now := metav1.Now()
				exec.FinishedAt = &now
			} else if job.Status.Failed > 0 {
				exec.State = hibernatorv1alpha1.StateFailed
				anyFailed = true
				now := metav1.Now()
				exec.FinishedAt = &now
			} else if job.Status.Active > 0 {
				exec.State = hibernatorv1alpha1.StateRunning
				if exec.StartedAt == nil {
					now := metav1.Now()
					exec.StartedAt = &now
				}
			}

			exec.JobRef = fmt.Sprintf("%s/%s", job.Namespace, job.Name)
			exec.Attempts = job.Status.Failed + job.Status.Succeeded
			break
		}

		// If no job found but finishedAt is set, infer final state
		// This handles the case where job was completed and then garbage collected
		if !found && exec.FinishedAt != nil && exec.State == hibernatorv1alpha1.StateRunning {
			// Job has finished (finishedAt set) but state wasn't updated
			// Infer as Completed (assume successful completion)
			// If there was an error message, it would have been set during execution
			exec.State = hibernatorv1alpha1.StateCompleted
			log.V(1).Info("inferred completed state for execution (job not found)", "target", exec.Target)
		} else if !found {
			log.V(1).Info("no job found for execution", "target", exec.Target, "currentState", exec.State)
		}
	}

	if err := r.Status().Update(ctx, plan); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.APIReader.Get(ctx, types.NamespacedName{Name: plan.Name, Namespace: plan.Namespace}, plan); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, err
	}

	// Check if current stage is complete
	log.V(1).Info("checking if stage is complete", "stageIndex", plan.Status.CurrentStageIndex, "totalStages", len(execPlan.Stages))
	currentStage := execPlan.Stages[plan.Status.CurrentStageIndex]
	if r.isStageComplete(log, plan, currentStage) {
		log.Info("current stage is complete", "stageIndex", plan.Status.CurrentStageIndex)
		if anyFailed && plan.Spec.Behavior.Mode == hibernatorv1alpha1.BehaviorStrict {
			return r.setError(ctx, plan, fmt.Errorf("one or more targets in stage %d failed", plan.Status.CurrentStageIndex))
		}

		// Check if there are more stages
		nextStageIndex := plan.Status.CurrentStageIndex + 1
		if nextStageIndex < len(execPlan.Stages) {
			// Progress to next stage
			log.V(1).Info("stage complete, progressing to next stage", "currentStage", plan.Status.CurrentStageIndex, "nextStage", nextStageIndex)
			plan.Status.CurrentStageIndex = nextStageIndex
			if err := r.Status().Update(ctx, plan); err != nil {
				return ctrl.Result{}, err
			}

			// Re-fetch plan before proceeding to next stage
			if err := r.APIReader.Get(ctx, types.NamespacedName{Name: plan.Name, Namespace: plan.Namespace}, plan); err != nil {
				if errors.IsNotFound(err) {
					return ctrl.Result{}, nil
				}

				return ctrl.Result{}, err
			}

			// Execute next stage
			return r.executeStage(ctx, log, plan, execPlan, nextStageIndex, operation)
		}

		// All stages complete - verify all targets are in terminal state
		if !r.isOperationComplete(plan) {
			// Some targets still in progress, requeue to wait
			log.V(1).Info("targets still in progress, not completing operation yet", "operation", operation)
			return ctrl.Result{RequeueAfter: ExecutionRequeueInterval}, nil
		}

		// All stages and all targets complete - finalize the operation
		log.V(1).Info("all stages complete", "operation", operation)

		if err := r.finalizeOperation(ctx, log, plan, operation); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	// Current stage still running, requeue
	return ctrl.Result{RequeueAfter: ExecutionRequeueInterval}, nil
}

// createRunnerJob creates a Kubernetes Job for executing a target.
func (r *HibernatePlanReconciler) createRunnerJob(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan, target *hibernatorv1alpha1.Target, operation string) error {
	executionID := fmt.Sprintf("%s-%s-%d", plan.Name, target.Name, time.Now().Unix())

	// Serialize target parameters
	var paramsJSON []byte
	if target.Parameters != nil {
		paramsJSON = target.Parameters.Raw
	}

	// Build job spec
	backoffLimit := int32(DefaultJobBackoffLimit)
	ttlSeconds := int32(DefaultJobTTLSeconds)
	tokenExpiration := int64(StreamTokenExpirationSeconds)
	runnerServiceAccount := r.RunnerServiceAccount
	if runnerServiceAccount == "" {
		runnerServiceAccount = "hibernator-runner"
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("runner-%s-%s-", plan.Name, target.Name),
			Namespace:    plan.Namespace,
			Labels: map[string]string{
				LabelPlan:        plan.Name,
				LabelTarget:      target.Name,
				LabelExecutionID: executionID,
				LabelOperation:   operation,
				LabelCycleID:     plan.Status.CurrentCycleID,
			},
			Annotations: map[string]string{
				AnnotationPlan:   plan.Name,
				AnnotationTarget: target.Name,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttlSeconds,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelPlan:        plan.Name,
						LabelTarget:      target.Name,
						LabelExecutionID: executionID,
						LabelOperation:   operation,
						LabelCycleID:     plan.Status.CurrentCycleID,
					},
					Annotations: map[string]string{
						AnnotationPlan:   plan.Name,
						AnnotationTarget: target.Name,
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: runnerServiceAccount,
					Containers: []corev1.Container{
						{
							Name:  "runner",
							Image: r.getRunnerImage(),
							Args: []string{
								"--operation", operation,
								"--target", target.Name,
								"--target-type", target.Type,
								"--plan", plan.Name,
							},
							Env: []corev1.EnvVar{
								{
									Name:  "POD_NAMESPACE",
									Value: plan.Namespace,
								},
								{
									Name:  "HIBERNATOR_EXECUTION_ID",
									Value: executionID,
								},
								{
									Name:  "HIBERNATOR_CONTROL_PLANE_ENDPOINT",
									Value: r.ControlPlaneEndpoint,
								},
								{
									Name:  "HIBERNATOR_USE_TLS",
									Value: "false",
								},
								{
									Name:  "HIBERNATOR_GRPC_ENDPOINT",
									Value: fmt.Sprintf("%s:9444", r.ControlPlaneEndpoint),
								},
								{
									Name:  "HIBERNATOR_WEBSOCKET_ENDPOINT",
									Value: fmt.Sprintf("ws://%s:8082", r.ControlPlaneEndpoint),
								},
								{
									Name:  "HIBERNATOR_HTTP_CALLBACK_ENDPOINT",
									Value: fmt.Sprintf("http://%s:8082", r.ControlPlaneEndpoint),
								},
								{
									Name:  "HIBERNATOR_TARGET_PARAMS",
									Value: string(paramsJSON),
								},
								{
									Name:  "HIBERNATOR_CONNECTOR_KIND",
									Value: target.ConnectorRef.Kind,
								},
								{
									Name:  "HIBERNATOR_CONNECTOR_NAME",
									Value: target.ConnectorRef.Name,
								},
								{
									Name:  "HIBERNATOR_CONNECTOR_NAMESPACE",
									Value: r.getConnectorNamespace(plan, &target.ConnectorRef),
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "stream-token",
									MountPath: "/var/run/secrets/stream",
									ReadOnly:  true,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "stream-token",
							VolumeSource: corev1.VolumeSource{
								Projected: &corev1.ProjectedVolumeSource{
									Sources: []corev1.VolumeProjection{
										{
											ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
												Audience:          StreamTokenAudience,
												ExpirationSeconds: &tokenExpiration,
												Path:              "token",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Set owner reference
	if err := controllerutil.SetControllerReference(plan, job, r.Scheme); err != nil {
		return err
	}

	log.V(1).Info("creating runner job", "target", target.Name, "operation", operation)
	return r.Create(ctx, job)
}

// reconcileDelete handles plan deletion.
func (r *HibernatePlanReconciler) reconcileDelete(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (ctrl.Result, error) {
	log.V(1).Info("handling deletion")

	// Clean up jobs
	var jobList batchv1.JobList
	if err := r.List(ctx, &jobList, client.InNamespace(plan.Namespace), client.MatchingLabels{
		LabelPlan: plan.Name,
	}); err != nil {
		return ctrl.Result{}, err
	}

	propagation := metav1.DeletePropagationBackground
	for _, job := range jobList.Items {
		if err := r.Delete(ctx, &job, &client.DeleteOptions{
			PropagationPolicy: &propagation,
		}); err != nil && !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}

	// Remove finalizer
	controllerutil.RemoveFinalizer(plan, FinalizerName)
	if err := r.Update(ctx, plan); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// setError transitions the plan to error state.
func (r *HibernatePlanReconciler) setError(ctx context.Context, plan *hibernatorv1alpha1.HibernatePlan, err error) (ctrl.Result, error) {
	plan.Status.Phase = hibernatorv1alpha1.PhaseError
	now := metav1.Now()
	plan.Status.LastTransitionTime = &now
	if updateErr := r.Status().Update(ctx, plan); updateErr != nil {
		return ctrl.Result{}, updateErr
	}
	return ctrl.Result{}, err
}

func (r *HibernatePlanReconciler) findTarget(plan *hibernatorv1alpha1.HibernatePlan, name string) *hibernatorv1alpha1.Target {
	for i := range plan.Spec.Targets {
		if plan.Spec.Targets[i].Name == name {
			return &plan.Spec.Targets[i]
		}
	}
	return nil
}

func (r *HibernatePlanReconciler) findTargetType(plan *hibernatorv1alpha1.HibernatePlan, name string) string {
	for _, t := range plan.Spec.Targets {
		if t.Name == name {
			return t.Type
		}
	}
	return ""
}

func (r *HibernatePlanReconciler) getConnectorNamespace(plan *hibernatorv1alpha1.HibernatePlan, ref *hibernatorv1alpha1.ConnectorRef) string {
	if ref.Namespace != "" {
		return ref.Namespace
	}
	return plan.Namespace
}

func (r *HibernatePlanReconciler) getRunnerImage() string {
	if r.RunnerImage != "" {
		return r.RunnerImage
	}
	return RunnerImage
}

// handleErrorRecovery implements error recovery with exponential backoff.
func (r *HibernatePlanReconciler) handleErrorRecovery(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (ctrl.Result, error) {
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
		log.Error(lastErr, "error recovery aborted", "reason", strategy.Reason)

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
	recovery.RecordRetryAttempt(plan, lastErr)

	// Evaluate current schedule to determine target phase
	shouldHibernate, _, err := r.evaluateSchedule(ctx, plan)
	if err != nil {
		log.Error(err, "failed to evaluate schedule during recovery")
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// Transition to appropriate phase
	if shouldHibernate {
		plan.Status.Phase = hibernatorv1alpha1.PhaseHibernating
		log.Info("transitioning to hibernating phase for recovery", "attempt", plan.Status.RetryCount)
	} else {
		plan.Status.Phase = hibernatorv1alpha1.PhaseWakingUp
		log.Info("transitioning to waking up phase for recovery", "attempt", plan.Status.RetryCount)
	}

	if err := r.Status().Update(ctx, plan); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{Requeue: true}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HibernatePlanReconciler) SetupWithManager(mgr ctrl.Manager, workers int) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hibernatorv1alpha1.HibernatePlan{}).
		Owns(&batchv1.Job{}).
		Owns(&corev1.ConfigMap{}).
		Watches(
			&hibernatorv1alpha1.ScheduleException{},
			handler.EnqueueRequestsFromMapFunc(r.findPlansForException),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: workers,
		}).
		Complete(r)
}

// countRunningJobsInStage counts how many jobs in the provided list are running for targets in the stage.
func (r *HibernatePlanReconciler) countRunningJobsInStage(plan *hibernatorv1alpha1.HibernatePlan, jobList *batchv1.JobList, stage scheduler.ExecutionStage) int {
	count := 0
	for _, job := range jobList.Items {
		// Only count active jobs
		if job.Status.Active == 0 {
			continue
		}

		// Note: jobList is already filtered by LabelPlan and LabelOperation in executeStage,
		// so we don't need to check LabelPlan again. This ensures we only see jobs from
		// the current operation in the current cycle.

		// Check if this job belongs to a target in this stage
		targetLabel := job.Labels[LabelTarget]
		for _, stageName := range stage.Targets {
			if targetLabel == stageName {
				count++
				break
			}
		}
	}
	return count
}

// findExecutionStatus finds the execution status for a given target type and name.
func (r *HibernatePlanReconciler) findExecutionStatus(plan *hibernatorv1alpha1.HibernatePlan, targetType, targetName string) *hibernatorv1alpha1.ExecutionStatus {
	targetID := fmt.Sprintf("%s/%s", targetType, targetName)
	for i := range plan.Status.Executions {
		if plan.Status.Executions[i].Target == targetID {
			return &plan.Status.Executions[i]
		}
	}
	return nil
}

// isStageComplete checks if all targets in a stage have completed execution.
func (r *HibernatePlanReconciler) isStageComplete(log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan, stage scheduler.ExecutionStage) bool {
	for _, targetName := range stage.Targets {
		// Find the execution status for this target
		targetType := r.findTargetType(plan, targetName)
		targetID := fmt.Sprintf("%s/%s", targetType, targetName)

		found := false
		for _, exec := range plan.Status.Executions {
			if exec.Target == targetID {
				// Target must be completed (not pending or running)
				if exec.State != hibernatorv1alpha1.StateCompleted {
					log.Info("target not completed in stage", "target", targetID, "state", exec.State)
					return false
				}
				found = true
				break
			}
		}
		if !found {
			// Target not yet in execution list
			log.V(1).Info("target not found in execution list", "target", targetID, "stageTargets", stage.Targets)
			return false
		}
	}
	return true
}

// findFailedDependencies checks if any target in the stage depends on a failed target.
// Returns list of failed target names that are dependencies of targets in the stage.
func (r *HibernatePlanReconciler) findFailedDependencies(plan *hibernatorv1alpha1.HibernatePlan, dependencies []hibernatorv1alpha1.Dependency, stage scheduler.ExecutionStage) []string {
	if len(dependencies) == 0 {
		return nil
	}

	var failedDeps []string

	// For each target in the current stage
	for _, targetName := range stage.Targets {
		// Check if this target depends on any failed target
		for _, dep := range dependencies {
			if dep.To == targetName {
				// This target depends on dep.From
				// Check if dep.From has failed
				execStatus := r.findExecutionStatus(plan, r.findTargetType(plan, dep.From), dep.From)
				if execStatus != nil && execStatus.State == hibernatorv1alpha1.StateFailed {
					failedDeps = append(failedDeps, dep.From)
				}
			}
		}
	}

	return failedDeps
}

// findPlansForException returns reconcile requests for HibernatePlans when a ScheduleException changes.
func (r *HibernatePlanReconciler) findPlansForException(ctx context.Context, obj client.Object) []reconcile.Request {
	exception, ok := obj.(*hibernatorv1alpha1.ScheduleException)
	if !ok {
		return nil
	}

	// Enqueue the referenced plan
	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Name:      exception.Spec.PlanRef.Name,
				Namespace: exception.Namespace,
			},
		},
	}
}

// appendExecutionToHistory appends a completed operation to the execution history.
// Returns the cycle ID for tracking (either new or existing cycle).
func (r *HibernatePlanReconciler) appendExecutionToHistory(plan *hibernatorv1alpha1.HibernatePlan, operation string) string {
	summary := r.buildOperationSummary(plan, operation)

	// Determine if we need to create a new cycle
	newCycle := true
	if len(plan.Status.ExecutionHistory) > 0 {
		lastCycle := &plan.Status.ExecutionHistory[len(plan.Status.ExecutionHistory)-1]
		// If last cycle doesn't have this operation yet, reuse it
		if operation == "shutdown" && lastCycle.ShutdownExecution == nil {
			newCycle = false
		} else if operation == "wakeup" && lastCycle.WakeupExecution == nil {
			newCycle = false
		}
	}

	// Create a new cycle if needed
	if newCycle {
		plan.Status.ExecutionHistory = append(plan.Status.ExecutionHistory, hibernatorv1alpha1.ExecutionCycle{
			CycleID: plan.Status.CurrentCycleID,
		})
	}

	// Append the operation summary to the last cycle (always the last element)
	if operation == "shutdown" {
		plan.Status.ExecutionHistory[len(plan.Status.ExecutionHistory)-1].ShutdownExecution = summary
	} else if operation == "wakeup" {
		plan.Status.ExecutionHistory[len(plan.Status.ExecutionHistory)-1].WakeupExecution = summary
	}

	// Prune old cycles if exceeding max 5
	if len(plan.Status.ExecutionHistory) > 5 {
		plan.Status.ExecutionHistory = plan.Status.ExecutionHistory[len(plan.Status.ExecutionHistory)-5:]
	}

	return plan.Status.CurrentCycleID
}

// buildOperationSummary creates a summary of the current operation from execution statuses.
func (r *HibernatePlanReconciler) buildOperationSummary(plan *hibernatorv1alpha1.HibernatePlan, operation string) *hibernatorv1alpha1.ExecutionOperationSummary {
	summary := &hibernatorv1alpha1.ExecutionOperationSummary{
		Operation: operation,
		StartTime: metav1.Now(),
		Success:   true,
	}

	// Build target results from execution statuses
	for _, exec := range plan.Status.Executions {
		if exec.State == hibernatorv1alpha1.StateFailed {
			summary.Success = false
		}

		executionID := exec.JobRef
		job := &batchv1.Job{}
		if jobName, err := k8sutil.ParseNamespacedName(exec.JobRef); err == nil {
			if err := r.Get(context.Background(), jobName, job); err == nil {
				if id, ok := job.Labels[LabelExecutionID]; ok {
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

	now := metav1.Now()
	summary.EndTime = &now

	return summary
}

// initializeOperation prepares a plan for a new operation (shutdown or wakeup).
func (r *HibernatePlanReconciler) initializeOperation(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan, operation string) (scheduler.ExecutionPlan, error) {
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
	plan.Status.Executions = make([]hibernatorv1alpha1.ExecutionStatus, len(plan.Spec.Targets))
	for i, target := range plan.Spec.Targets {
		plan.Status.Executions[i] = hibernatorv1alpha1.ExecutionStatus{
			Target:   fmt.Sprintf("%s/%s", target.Type, target.Name),
			Executor: target.Type,
			State:    hibernatorv1alpha1.StatePending,
		}
	}

	// Set phase based on operation
	if operation == "shutdown" {
		plan.Status.CurrentCycleID = uuid.New().String()[:8]
		plan.Status.Phase = hibernatorv1alpha1.PhaseHibernating
	} else {
		plan.Status.Phase = hibernatorv1alpha1.PhaseWakingUp
	}

	plan.Status.CurrentStageIndex = 0
	plan.Status.CurrentOperation = operation
	now := metav1.Now()
	plan.Status.LastTransitionTime = &now

	if err := r.Status().Update(ctx, plan); err != nil {
		log.Error(err, "failed to update plan status during initialization", "operation", operation)
		return scheduler.ExecutionPlan{}, err
	}

	log.V(1).Info("plan status updated", "operation", operation, "newPhase", plan.Status.Phase)
	return execPlan, nil
}

// finalizeOperation completes an operation and transitions the plan phase.
func (r *HibernatePlanReconciler) finalizeOperation(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan, operation string) error {
	// Append operation to execution history
	cycleID := r.appendExecutionToHistory(plan, operation)
	log.Info("execution appended to history", "operation", operation, "cycleID", cycleID)

	// Set final phase based on operation
	if operation == "shutdown" {
		plan.Status.Phase = hibernatorv1alpha1.PhaseHibernated
	} else {
		plan.Status.Phase = hibernatorv1alpha1.PhaseActive
	}

	now := metav1.Now()
	plan.Status.LastTransitionTime = &now

	if err := r.Status().Update(ctx, plan); err != nil {
		return err
	}

	log.Info("operation completed", "operation", operation)
	return nil
}

// isOperationComplete checks if all targets in an operation have reached terminal state.
func (r *HibernatePlanReconciler) isOperationComplete(plan *hibernatorv1alpha1.HibernatePlan) bool {
	for _, exec := range plan.Status.Executions {
		if exec.State != hibernatorv1alpha1.StateCompleted && exec.State != hibernatorv1alpha1.StateFailed {
			return false
		}
	}
	return true
}
