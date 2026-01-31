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
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/recovery"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/scheduler"
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
	log := r.Log.WithValues("hibernateplan", req.NamespacedName)

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

	// Evaluate schedule
	shouldHibernate, requeueAfter, err := r.evaluateSchedule(&plan)
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

// evaluateSchedule checks if we should be in hibernation based on schedule.
func (r *HibernatePlanReconciler) evaluateSchedule(plan *hibernatorv1alpha1.HibernatePlan) (bool, time.Duration, error) {
	if r.ScheduleEvaluator == nil {
		// Fallback: always active if no evaluator
		return false, time.Minute, nil
	}

	// Convert OffHourWindows to cron expressions
	offHourWindows := make([]scheduler.OffHourWindow, len(plan.Spec.Schedule.OffHours))
	for i, w := range plan.Spec.Schedule.OffHours {
		offHourWindows[i] = scheduler.OffHourWindow{
			Start:      w.Start,
			End:        w.End,
			DaysOfWeek: w.DaysOfWeek,
		}
	}

	hibernateCron, wakeUpCron, err := scheduler.ConvertOffHoursToCron(offHourWindows)
	if err != nil {
		return false, time.Minute, fmt.Errorf("failed to convert off-hours to cron: %w", err)
	}

	window := scheduler.ScheduleWindow{
		HibernateCron: hibernateCron,
		WakeUpCron:    wakeUpCron,
		Timezone:      plan.Spec.Schedule.Timezone,
	}

	result, err := r.ScheduleEvaluator.Evaluate(window, time.Now())
	if err != nil {
		return false, time.Minute, err
	}

	requeueAfter := r.ScheduleEvaluator.NextRequeueTime(result, time.Now())
	return result.ShouldHibernate, requeueAfter, nil
}

// startHibernation initiates the hibernation process.
func (r *HibernatePlanReconciler) startHibernation(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (ctrl.Result, error) {
	log.Info("starting hibernation")

	// Build execution plan
	execPlan, err := r.buildExecutionPlan(plan, false)
	if err != nil {
		return r.setError(ctx, plan, fmt.Errorf("build execution plan: %w", err))
	}

	// Initialize execution status
	plan.Status.Executions = make([]hibernatorv1alpha1.ExecutionStatus, len(plan.Spec.Targets))
	for i, target := range plan.Spec.Targets {
		plan.Status.Executions[i] = hibernatorv1alpha1.ExecutionStatus{
			Target:   fmt.Sprintf("%s/%s", target.Type, target.Name),
			Executor: target.Type,
			State:    hibernatorv1alpha1.StatePending,
		}
	}

	plan.Status.Phase = hibernatorv1alpha1.PhaseHibernating
	now := metav1.Now()
	plan.Status.LastTransitionTime = &now

	if err := r.Status().Update(ctx, plan); err != nil {
		return ctrl.Result{}, err
	}

	// Start first stage
	return r.executeStage(ctx, log, plan, execPlan, 0, "shutdown")
}

// reconcileHibernation continues the hibernation process.
func (r *HibernatePlanReconciler) reconcileHibernation(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (ctrl.Result, error) {
	// Check job statuses and progress through stages
	return r.reconcileExecution(ctx, log, plan, "shutdown")
}

// startWakeUp initiates the wake-up process.
func (r *HibernatePlanReconciler) startWakeUp(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (ctrl.Result, error) {
	log.Info("starting wake-up")

	// Build execution plan (reversed for wake-up)
	execPlan, err := r.buildExecutionPlan(plan, true)
	if err != nil {
		return r.setError(ctx, plan, fmt.Errorf("build execution plan: %w", err))
	}

	// Reset execution states to pending
	for i := range plan.Status.Executions {
		plan.Status.Executions[i].State = hibernatorv1alpha1.StatePending
		plan.Status.Executions[i].StartedAt = nil
		plan.Status.Executions[i].FinishedAt = nil
		plan.Status.Executions[i].JobRef = ""
	}

	plan.Status.Phase = hibernatorv1alpha1.PhaseWakingUp
	now := metav1.Now()
	plan.Status.LastTransitionTime = &now

	if err := r.Status().Update(ctx, plan); err != nil {
		return ctrl.Result{}, err
	}

	// Start first stage
	return r.executeStage(ctx, log, plan, execPlan, 0, "wakeup")
}

// reconcileWakeUp continues the wake-up process.
func (r *HibernatePlanReconciler) reconcileWakeUp(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (ctrl.Result, error) {
	return r.reconcileExecution(ctx, log, plan, "wakeup")
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

// executeStage creates jobs for targets in the current stage.
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
		log.Info("execution complete", "operation", operation)
		return ctrl.Result{}, nil
	}

	stage := execPlan.Stages[stageIndex]
	log.Info("executing stage", "index", stageIndex, "targets", stage.Targets)

	for _, targetName := range stage.Targets {
		target := r.findTarget(plan, targetName)
		if target == nil {
			continue
		}

		// Check if job already exists
		jobName := fmt.Sprintf("%s-%s-%s", plan.Name, targetName, operation[:4])
		var existingJob batchv1.Job
		err := r.Get(ctx, types.NamespacedName{
			Namespace: plan.Namespace,
			Name:      jobName,
		}, &existingJob)

		if err == nil {
			// Job exists, skip creation
			continue
		} else if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		// Create job
		if err := r.createRunnerJob(ctx, log, plan, target, operation); err != nil {
			log.Error(err, "failed to create job", "target", targetName)
			// Continue with other targets in best-effort mode
			if plan.Spec.Behavior.Mode == hibernatorv1alpha1.BehaviorStrict && plan.Spec.Behavior.FailFast {
				return r.setError(ctx, plan, err)
			}
		}
	}

	return ctrl.Result{RequeueAfter: StageRequeueInterval}, nil
}

// reconcileExecution checks job statuses and progresses through stages.
func (r *HibernatePlanReconciler) reconcileExecution(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan, operation string) (ctrl.Result, error) {
	// List all jobs for this plan
	var jobList batchv1.JobList
	if err := r.List(ctx, &jobList, client.InNamespace(plan.Namespace), client.MatchingLabels{
		LabelPlan: plan.Name,
	}); err != nil {
		return ctrl.Result{}, err
	}

	// Update execution statuses
	allComplete := true
	anyFailed := false

	for i := range plan.Status.Executions {
		exec := &plan.Status.Executions[i]

		// Find matching job
		for _, job := range jobList.Items {
			targetLabel := job.Labels[LabelTarget]
			if exec.Target != fmt.Sprintf("%s/%s", r.findTargetType(plan, targetLabel), targetLabel) {
				continue
			}

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
				allComplete = false
			} else {
				allComplete = false
			}

			exec.JobRef = fmt.Sprintf("%s/%s", job.Namespace, job.Name)
			exec.Attempts = job.Status.Failed + job.Status.Succeeded
			break
		}

		if exec.State == hibernatorv1alpha1.StatePending || exec.State == hibernatorv1alpha1.StateRunning {
			allComplete = false
		}
	}

	if err := r.Status().Update(ctx, plan); err != nil {
		return ctrl.Result{}, err
	}

	// Handle completion
	if allComplete {
		if anyFailed && plan.Spec.Behavior.Mode == hibernatorv1alpha1.BehaviorStrict {
			return r.setError(ctx, plan, fmt.Errorf("one or more targets failed"))
		}

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

		log.Info("execution complete", "operation", operation)
		return ctrl.Result{}, nil
	}

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
			GenerateName: fmt.Sprintf("hibernate-runner-%s-%s-", plan.Name, target.Name),
			Namespace:    plan.Namespace,
			Labels: map[string]string{
				LabelPlan:        plan.Name,
				LabelTarget:      target.Name,
				LabelExecutionID: executionID,
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
									Name:  "HIBERNATOR_EXECUTION_ID",
									Value: executionID,
								},
								{
									Name:  "HIBERNATOR_CONTROL_PLANE_ENDPOINT",
									Value: r.ControlPlaneEndpoint,
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

	log.Info("creating runner job", "target", target.Name, "operation", operation)
	return r.Create(ctx, job)
}

// reconcileDelete handles plan deletion.
func (r *HibernatePlanReconciler) reconcileDelete(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (ctrl.Result, error) {
	log.Info("handling deletion")

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
	shouldHibernate, _, err := r.evaluateSchedule(plan)
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
func (r *HibernatePlanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hibernatorv1alpha1.HibernatePlan{}).
		Owns(&batchv1.Job{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}
