/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package hibernateplan

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/controller/status"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// Reconciler reconciles a HibernatePlan object.
type Reconciler struct {
	client.Client
	APIReader client.Reader
	Clock     clock.Clock

	Log               logr.Logger
	Scheme            *runtime.Scheme
	Planner           *scheduler.Planner
	ScheduleEvaluator *scheduler.ScheduleEvaluator
	RestoreManager    *restore.Manager

	// ControlPlaneEndpoint is the endpoint for runner streaming.
	ControlPlaneEndpoint string

	// RunnerImage is the runner container image.
	RunnerImage string

	// RunnerServiceAccount is the ServiceAccount used by runner pods.
	RunnerServiceAccount string

	// statusUpdater is responsible for updating the status of HibernatePlan resources.
	statusUpdater *status.SyncStatusUpdater
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
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("plan", req.String())

	// Fetch the HibernatePlan
	plan := &hibernatorv1alpha1.HibernatePlan{}
	if err := r.Get(ctx, req.NamespacedName, plan); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !plan.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, log, plan)
	}

	// Ensure finalizer
	if !controllerutil.ContainsFinalizer(plan, wellknown.PlanFinalizerName) {
		orig := plan.DeepCopy()
		controllerutil.AddFinalizer(plan, wellknown.PlanFinalizerName)
		if err := r.Patch(ctx, plan, client.MergeFrom(orig)); err != nil {
			return ctrl.Result{}, fmt.Errorf("add finalizer to plan: %w", err)
		}
		return ctrl.Result{}, nil
	}

	// Initialize status if needed
	if plan.Status.Phase == "" {
		log.Info("initializing plan status")

		if err := r.statusUpdater.Update(ctx, plan, status.MutatorFunc(func(obj client.Object) (new client.Object, ok bool) {
			p := obj.(*hibernatorv1alpha1.HibernatePlan)
			p.Status.Phase = hibernatorv1alpha1.PhaseActive
			p.Status.ObservedGeneration = plan.Generation

			return p, true
		})); err != nil {
			return ctrl.Result{}, err
		}

		if err := r.RestoreManager.PrepareRestorePoint(ctx, plan.Namespace, plan.Name); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	// Handle suspension state transitions (suspend/resume)
	if handled, result, err := r.reconcileSuspension(ctx, log, plan); handled {
		return result, err
	}

	// Query active exception and update status
	if err := r.updateActiveExceptions(ctx, log, plan); err != nil {
		log.Error(err, "failed to update active exceptions")
		// Don't fail reconciliation, continue with base schedule
	}

	// Evaluate schedule (with exceptions if present)
	shouldHibernate, requeueAfter, err := r.evaluateSchedule(ctx, log, plan)
	if err != nil {
		log.Error(err, "failed to evaluate schedule")
		return ctrl.Result{RequeueAfter: wellknown.RequeueIntervalOnScheduleError}, nil
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
			return r.startHibernation(ctx, log, plan)
		}

	case hibernatorv1alpha1.PhaseHibernated:
		if desiredPhase == hibernatorv1alpha1.PhaseHibernating {
			log.V(1).Info("already hibernated, skipping duplicate shutdown")
			return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
		}
		if desiredPhase == hibernatorv1alpha1.PhaseWakingUp {
			return r.startWakeUp(ctx, log, plan)
		}

	case hibernatorv1alpha1.PhaseHibernating:
		return r.reconcileHibernation(ctx, log, plan)

	case hibernatorv1alpha1.PhaseWakingUp:
		return r.reconcileWakeUp(ctx, log, plan)

	case hibernatorv1alpha1.PhaseSuspended:
		// While suspended, skip all operations and wait
		// Running jobs will complete naturally, but no new jobs are created
		log.V(1).Info("plan is suspended, skipping operations")
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil

	case hibernatorv1alpha1.PhaseError:
		// Handle error recovery with retry logic
		return r.handleErrorRecovery(ctx, log, plan)
	}

	// Requeue based on schedule (next hibernate or wake-up time)
	log.Info("reconciliation complete", "nextRequeue", requeueAfter.String())
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager, workers int) error {
	r.statusUpdater = status.NewSyncStatusUpdater(r.Client)

	return ctrl.NewControllerManagedBy(mgr).
		For(&hibernatorv1alpha1.HibernatePlan{}).
		Owns(&batchv1.Job{}).
		Owns(&corev1.ConfigMap{}).
		Watches(
			&hibernatorv1alpha1.ScheduleException{},
			handler.EnqueueRequestsFromMapFunc(r.findPlansForException),
		).
		Watches(
			&batchv1.Job{},
			handler.EnqueueRequestsFromMapFunc(r.findPlansForRunnerJob),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: workers,
		}).
		Complete(r)
}

// updateActiveExceptions queries for active exceptions and updates plan status.
func (r *Reconciler) updateActiveExceptions(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) error {
	orig := plan.DeepCopy()

	// Query for exceptions referencing this plan
	var exceptions hibernatorv1alpha1.ScheduleExceptionList
	if err := r.List(ctx, &exceptions,
		client.InNamespace(plan.Namespace),
		client.MatchingLabels{wellknown.LabelPlan: plan.Name},
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
		if err := r.Status().Patch(ctx, plan, client.MergeFrom(orig)); err != nil {
			return fmt.Errorf("update plan status: %w", err)
		}
		log.Info("updated active exceptions", "count", len(activeExceptions))
	}

	return nil
}

// evaluateSchedule checks if we should be in hibernation based on schedule and active exceptions.
func (r *Reconciler) evaluateSchedule(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (bool, time.Duration, error) {
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
	result, err := r.ScheduleEvaluator.Evaluate(baseWindows, plan.Spec.Schedule.Timezone, exception)
	if err != nil {
		return false, time.Minute, err
	}

	log.Info(
		"schedule evaluation result",
		"nextHibernateTime", result.NextHibernateTime,
		"nextWakeUpTime", result.NextWakeUpTime,
		"shouldHibernate", result.ShouldHibernate,
	)

	requeueAfter := r.ScheduleEvaluator.NextRequeueTime(result)
	return result.ShouldHibernate, requeueAfter, nil
}

// getActiveException queries for an active ScheduleException for this plan.
// Returns nil if no active exception exists.
func (r *Reconciler) getActiveException(ctx context.Context, plan *hibernatorv1alpha1.HibernatePlan) (*scheduler.Exception, error) {
	// List exceptions with matching plan label
	var exceptions hibernatorv1alpha1.ScheduleExceptionList
	if err := r.List(ctx, &exceptions,
		client.InNamespace(plan.Namespace),
		client.MatchingLabels{wellknown.LabelPlan: plan.Name},
	); err != nil {
		return nil, fmt.Errorf("list schedule exceptions: %w", err)
	}

	var activeExceptions []hibernatorv1alpha1.ScheduleException
	now := r.Clock.Now()

	// Filter for active exceptions
	for _, exc := range exceptions.Items {
		if exc.Status.State != hibernatorv1alpha1.ExceptionStateActive {
			continue
		}

		// Verify it's within valid period
		if now.Before(exc.Spec.ValidFrom.Time) || now.After(exc.Spec.ValidUntil.Time) {
			continue
		}

		activeExceptions = append(activeExceptions, exc)
	}

	if len(activeExceptions) == 0 {
		return nil, nil
	}

	// If multiple active exceptions exist (e.g. webhook bypassed), pick the newest one (latest intent)
	if len(activeExceptions) > 1 {
		r.Log.Info("multiple active exceptions found, picking newest",
			"count", len(activeExceptions),
			"plan", plan.Name)

		// Sort by CreationTimestamp descending (newest first)
		for i := 0; i < len(activeExceptions)-1; i++ {
			for j := i + 1; j < len(activeExceptions); j++ {
				if activeExceptions[i].CreationTimestamp.Before(&activeExceptions[j].CreationTimestamp) {
					activeExceptions[i], activeExceptions[j] = activeExceptions[j], activeExceptions[i]
				}
			}
		}
	}

	exc := activeExceptions[0]

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

// buildExecutionPlan creates an execution plan based on the strategy.
func (r *Reconciler) buildExecutionPlan(plan *hibernatorv1alpha1.HibernatePlan, reverse bool) (scheduler.ExecutionPlan, error) {
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

// executeStage creates jobs for targets in the current stage, respecting maxConcurrency.
func (r *Reconciler) executeStage(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan, execPlan scheduler.ExecutionPlan, stageIndex int, operation string) (ctrl.Result, error) {
	if err := r.APIReader.Get(ctx, types.NamespacedName{Name: plan.Name, Namespace: plan.Namespace}, plan); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, err
	}

	if stageIndex >= len(execPlan.Stages) {
		if err := r.statusUpdater.Update(ctx, plan, status.MutatorFunc(func(obj client.Object) (new client.Object, ok bool) {
			p := obj.(*hibernatorv1alpha1.HibernatePlan)

			// All stages complete
			if operation == "shutdown" {
				p.Status.Phase = hibernatorv1alpha1.PhaseHibernated
			} else {
				p.Status.Phase = hibernatorv1alpha1.PhaseActive
			}
			now := metav1.NewTime(r.Clock.Now())
			p.Status.LastTransitionTime = &now

			return p, true
		})); err != nil {
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
		wellknown.LabelPlan:      plan.Name,
		wellknown.LabelOperation: operation,
		wellknown.LabelCycleID:   plan.Status.CurrentCycleID,
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
		if err := r.List(ctx, &existingJobs,
			client.InNamespace(plan.Namespace),
			client.MatchingLabels{
				wellknown.LabelPlan:      plan.Name,
				wellknown.LabelTarget:    targetName,
				wellknown.LabelOperation: operation,
				wellknown.LabelCycleID:   plan.Status.CurrentCycleID,
			}); err != nil {
			return ctrl.Result{}, err
		}

		exists := false
		for _, job := range existingJobs.Items {
			if _, ok := job.Labels[wellknown.LabelStaleRunnerJob]; !ok {
				exists = true
			}
		}

		if exists {
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
				return r.setError(ctx, plan, fmt.Errorf("failed to create job for target %s: %w", targetName, err))
			}
		}

		jobsCreated++
	}

	return ctrl.Result{RequeueAfter: wellknown.RequeueIntervalDuringStage}, nil
}

// reconcileExecution checks job statuses and progresses through stages.
func (r *Reconciler) reconcileExecution(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan, operation string) (ctrl.Result, error) {
	log.Info("reconciling execution", "operation", operation, "currentPhase", plan.Status.Phase, "currentStageIndex", plan.Status.CurrentStageIndex)

	if err := r.updatePlanExecutionStatuses(ctx, log, plan, operation); err != nil {
		log.Error(err, "failed to update plan execution statuses")
		return ctrl.Result{}, err
	}

	// Rebuild execution plan to access stage structure
	execPlan, err := r.buildExecutionPlan(plan, operation == "wakeup")
	if err != nil {
		log.Error(err, "failed to rebuild execution plan")
		return r.setError(ctx, plan, fmt.Errorf("failed to rebuild execution plan: %w", err))
	}

	// Get detailed stage status
	log.V(1).Info("checking stage status", "stageIndex", plan.Status.CurrentStageIndex, "totalStages", len(execPlan.Stages))
	currentStage := execPlan.Stages[plan.Status.CurrentStageIndex]
	stageStatus := r.getStageStatus(log, plan, currentStage)

	// Handle stage completion
	if stageStatus.AllTerminal {
		log.Info("stage reached terminal state",
			"stageIndex", plan.Status.CurrentStageIndex,
			"completedCount", stageStatus.CompletedCount,
			"failedCount", stageStatus.FailedCount)

		// Check for failures in strict mode
		if stageStatus.FailedCount > 0 && plan.Spec.Behavior.Mode == hibernatorv1alpha1.BehaviorStrict {
			return r.setError(ctx, plan, fmt.Errorf("one or more targets in stage %d failed", plan.Status.CurrentStageIndex))
		}

		// Check if there are more stages
		nextStageIndex := plan.Status.CurrentStageIndex + 1
		if nextStageIndex < len(execPlan.Stages) {
			// Progress to next stage
			log.V(1).Info("stage complete, progressing to next stage", "currentStage", plan.Status.CurrentStageIndex, "nextStage", nextStageIndex)
			if err := r.statusUpdater.Update(ctx, plan, status.MutatorFunc(func(obj client.Object) (new client.Object, ok bool) {
				p := obj.(*hibernatorv1alpha1.HibernatePlan)
				p.Status.CurrentStageIndex = nextStageIndex
				return p, true
			})); err != nil {
				log.Error(err, "failed to update plan status for next stage")
				return ctrl.Result{}, err
			}

			// Execute next stage
			return r.executeStage(ctx, log, plan, execPlan, nextStageIndex, operation)
		}

		// All stages complete - verify all targets are in terminal state
		if !r.isOperationComplete(plan) {
			// Some targets still in progress, requeue to wait
			log.V(1).Info("targets still in progress, not completing operation yet", "operation", operation)
			return ctrl.Result{RequeueAfter: wellknown.RequeueIntervalOnExecution}, nil
		}

		// All stages and all targets completed - finalize the operation
		log.V(1).Info("all stages are completed, finalizing operation ...", "operation", operation)

		if err := r.finalizeOperation(ctx, log, plan, operation); err != nil {
			return ctrl.Result{}, err
		}

		// Cleanup restore data after successful wake-up
		if operation == "wakeup" {
			if err := r.cleanupAfterWakeUp(ctx, log, plan); err != nil {
				log.Error(err, "failed to cleanup after wake-up (non-fatal)")
				// Continue - cleanup failure is non-fatal
			}
		}

		return ctrl.Result{}, nil
	}

	// Stage not complete - check if we need to fill pending slots
	if stageStatus.HasPending {
		// There are pending targets that need jobs created - execute current stage to fill slots
		log.V(1).Info("filling pending slots in current stage", "stageIndex", plan.Status.CurrentStageIndex)
		return r.executeStage(ctx, log, plan, execPlan, plan.Status.CurrentStageIndex, operation)
	}

	// Only running jobs remain - wait for them to complete
	log.V(1).Info("waiting for running jobs to complete", "stageIndex", plan.Status.CurrentStageIndex)
	return ctrl.Result{RequeueAfter: wellknown.RequeueIntervalOnExecution}, nil
}

// reconcileDelete handles plan deletion.
func (r *Reconciler) reconcileDelete(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (ctrl.Result, error) {
	log.V(1).Info("reconciling plan deletion")
	orig := plan.DeepCopy()

	// Clean up jobs
	var jobList batchv1.JobList
	if err := r.List(ctx, &jobList, client.InNamespace(plan.Namespace), client.MatchingLabels{
		wellknown.LabelPlan: plan.Name,
	}); err != nil {
		return ctrl.Result{}, err
	}

	propagation := metav1.DeletePropagationBackground
	for _, job := range jobList.Items {
		if err := r.Delete(ctx, &job, &client.DeleteOptions{
			PropagationPolicy: &propagation,
		}); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}

	// Remove finalizer
	controllerutil.RemoveFinalizer(plan, wellknown.PlanFinalizerName)
	if err := r.Patch(ctx, plan, client.MergeFrom(orig)); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return ctrl.Result{}, nil
}

// updatePlanExecutionStatuses updates the execution statuses based on job states.
func (r *Reconciler) updatePlanExecutionStatuses(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan, operation string) error {
	orig := plan.DeepCopy()

	// List all jobs for this plan, operation, and cycle
	var jobList batchv1.JobList
	if err := r.APIReader.List(ctx, &jobList, client.InNamespace(plan.Namespace), client.MatchingLabels{
		wellknown.LabelPlan:      plan.Name,
		wellknown.LabelOperation: operation,
		wellknown.LabelCycleID:   plan.Status.CurrentCycleID,
	}); err != nil {
		return err
	}
	log.V(1).Info("job list fetched", "operation", operation, "jobCount", len(jobList.Items))

	// Update execution statuses
	log.V(1).Info("updating execution statuses", "totalExecutions", len(plan.Status.Executions))

	for i := range plan.Status.Executions {
		exec := &plan.Status.Executions[i]
		log.V(1).Info("processing execution status", "target", exec.Target, "currentState", exec.State, "index", i)

		// Find matching job
		found := false
		for _, job := range jobList.Items {
			if _, ok := job.Labels[wellknown.LabelStaleRunnerJob]; ok {
				continue
			}

			targetLabel := job.Labels[wellknown.LabelTarget]
			executorLabel := job.Labels[wellknown.LabelExecutor]
			if exec.Target != targetLabel || exec.Executor != executorLabel {
				continue
			}

			isJobComplete := false
			isJobFailed := false
			for _, cond := range job.Status.Conditions {
				if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
					isJobComplete = true
					exec.FinishedAt = cond.LastTransitionTime.DeepCopy()
				}
				if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
					isJobFailed = true
					exec.FinishedAt = cond.LastTransitionTime.DeepCopy()
				}
			}

			found = true
			log.V(1).Info("found matching job for target", "target", exec.Target, "jobName", job.Name, "succeeded", job.Status.Succeeded, "failed", job.Status.Failed, "active", job.Status.Active)

			// Update status based on job
			if isJobComplete {
				exec.State = hibernatorv1alpha1.StateCompleted
			} else if isJobFailed {
				exec.State = hibernatorv1alpha1.StateFailed
				if msg := r.getDetailedErrorFromPod(ctx, &job); msg != "" {
					exec.Message = msg
				}
			} else if job.Status.Active > 0 {
				exec.State = hibernatorv1alpha1.StateRunning
				exec.StartedAt = job.Status.StartTime
			}

			if execId, ok := job.Labels[wellknown.LabelExecutionID]; ok {
				exec.LogsRef = fmt.Sprintf("%s%s", wellknown.ExecutionIDLogPrefix, execId)
			}

			exec.RestoreConfigMapRef = fmt.Sprintf("%s/%s", job.Namespace, restore.GetRestoreConfigMap(plan.Name))
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

	if err := r.Status().Patch(ctx, plan, client.MergeFrom(orig)); err != nil {
		return err
	}

	return nil
}
