/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/go-logr/logr"
	"github.com/samber/lo"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/metrics"
	statusprocessor "github.com/ardikabs/hibernator/internal/provider/processor/status"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/ardikabs/hibernator/pkg/k8sutil"
)

// ---------------------------------------------------------------------------
// Stage execution
// ---------------------------------------------------------------------------

// execute executes the given operation (hibernatorv1alpha1.OperationHibernate or hibernatorv1alpha1.OperationWakeUp)
// starting from the current stage index in the plan status.
func (s *state) execute(
	ctx context.Context,
	log logr.Logger,
	operation hibernatorv1alpha1.PlanOperation,
	reverse bool,
	onAdvanceStageCallback func(int),
	onFinalizeCallback func(context.Context, scheduler.ExecutionPlan),
) (StateResult, error) {
	plan := s.plan()

	jobs, err := s.getCurrentCycleJobs(ctx, plan)
	if err != nil {
		log.Error(err, "failed to get current cycle jobs")
		return StateResult{}, err
	}
	log.V(1).Info("job list fetched", "operation", operation, "jobCount", len(jobs))

	s.updateExecutionStatuses(ctx, log, plan, jobs)

	execPlan, err := s.buildExecutionPlan(plan, reverse)
	if err != nil {
		return StateResult{}, AsPlanError(fmt.Errorf("failed to build execution plan: %w", err))
	}

	log.V(1).Info("execution plan built",
		"totalStages", len(execPlan.Stages),
		"currentStageIndex", plan.Status.CurrentStageIndex)

	if plan.Status.CurrentStageIndex >= len(execPlan.Stages) {
		onFinalizeCallback(ctx, execPlan)
		return StateResult{}, nil
	}

	targetStage := execPlan.Stages[plan.Status.CurrentStageIndex]
	stageStatus := GetStageStatus(log, plan, targetStage)

	log.V(1).Info("current stage status",
		"stageIndex", plan.Status.CurrentStageIndex,
		"targets", targetStage.Targets,
		"allTerminal", stageStatus.AllTerminal,
		"completedCount", stageStatus.CompletedCount,
		"failedCount", stageStatus.FailedCount)

	if stageStatus.AllTerminal {
		log.Info("stage reached terminal state",
			"stageIndex", plan.Status.CurrentStageIndex,
			"completedCount", stageStatus.CompletedCount,
			"failedCount", stageStatus.FailedCount)

		if stageStatus.FailedCount > 0 &&
			plan.Spec.Behavior.Mode == hibernatorv1alpha1.BehaviorStrict {
			var failedTargets []string
			for _, exec := range plan.Status.Executions {
				if exec.State != hibernatorv1alpha1.StateFailed {
					continue
				}

				if slices.Contains(targetStage.Targets, exec.Target) {
					failedTargets = append(failedTargets, exec.Target)
				}
			}
			return StateResult{}, AsPlanError(fmt.Errorf("one or more targets failed: %s", strings.Join(failedTargets, ", ")))
		}

		nextStageIndex := plan.Status.CurrentStageIndex + 1
		if nextStageIndex < len(execPlan.Stages) {
			log.V(1).Info("advancing to next stage", "currentStage", plan.Status.CurrentStageIndex, "nextStage", nextStageIndex)
			onAdvanceStageCallback(nextStageIndex)

			targetStage = execPlan.Stages[nextStageIndex]
			return s.executeForStage(ctx, log, plan, jobs, targetStage, operation)
		}

		onFinalizeCallback(ctx, execPlan)
		return StateResult{}, nil
	}

	if stageStatus.HasPending {
		log.V(1).Info("filling pending slots in current stage", "stageIndex", plan.Status.CurrentStageIndex, "targetStages", targetStage.Targets)
		return s.executeForStage(ctx, log, plan, jobs, targetStage, operation)
	}

	return StateResult{RequeueAfter: wellknown.RequeueIntervalDuringStage}, nil
}

// executeForStage executes the operation for the targets in the given stage.
func (s *state) executeForStage(
	ctx context.Context,
	log logr.Logger,
	plan *hibernatorv1alpha1.HibernatePlan,
	jobs []batchv1.Job,
	stage scheduler.ExecutionStage,
	operation hibernatorv1alpha1.PlanOperation,
) (StateResult, error) {
	runningCount := CountRunningJobsInStage(jobs, stage)
	maxConcurrency := stage.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = int32(len(stage.Targets))
	}

	log.V(1).Info("evaluating stage targets for dispatch",
		"targetCount", len(stage.Targets),
		"runningCount", runningCount,
		"maxConcurrency", maxConcurrency)

	isDAG := plan.Spec.Execution.Strategy.Type == hibernatorv1alpha1.StrategyDAG

	jobsCreated := 0
	for _, targetName := range stage.Targets {
		target := FindTarget(plan, targetName)
		if target == nil {
			continue
		}

		// Skip targets already in a terminal state (previously pruned or completed).
		execStatus := FindExecutionStatus(plan, target.Type, targetName)
		if execStatus != nil &&
			(execStatus.State == hibernatorv1alpha1.StateFailed ||
				execStatus.State == hibernatorv1alpha1.StateCompleted ||
				execStatus.State == hibernatorv1alpha1.StateAborted) {
			continue
		}

		// DAG per-target dependency check: evaluate failed upstream for this specific target.
		if isDAG {
			if failedUpstream := FindFailedUpstream(plan, targetName); len(failedUpstream) > 0 {
				if plan.Spec.Behavior.Mode == hibernatorv1alpha1.BehaviorStrict {
					return StateResult{}, AsPlanError(fmt.Errorf(
						"target %q blocked: upstream dependency %v failed", targetName, failedUpstream))
				}

				// BestEffort: prune this target — mark as aborted so downstream cascades naturally.
				pruneMsg := fmt.Sprintf("Aborted: upstream dependency %v failed", failedUpstream)
				log.Info("aborting target with failed upstream dependency",
					"target", targetName, "failedUpstream", failedUpstream)
				s.pruneTarget(plan, targetName, pruneMsg)
				continue
			}
		}

		if JobExistsForTarget(jobs, targetName, operation, plan.Status.CurrentCycleID) {
			log.V(1).Info("job already exists for target, skipping", "target", targetName)
			continue
		}

		if int32(runningCount+jobsCreated) >= maxConcurrency {
			log.V(1).Info("reached maxConcurrency limit", "maxConcurrency", maxConcurrency)
			break
		}

		log.Info("dispatching job for target", "target", targetName, "operation", operation)
		if err := s.createRunnerJob(ctx, log,
			s.Clock, plan, target, operation,
			s.ExecutorInfra); err != nil {

			log.Error(err, "failed to create runner job", "target", targetName)
			metrics.JobFailuresTotal.WithLabelValues(s.Key.String(), targetName).Inc()

			if plan.Spec.Behavior.Mode == hibernatorv1alpha1.BehaviorStrict && plan.Spec.Behavior.FailFast {
				return StateResult{}, AsPlanError(fmt.Errorf("failed to create job for target %s: %w", targetName, err))
			}
		} else {
			metrics.JobsCreatedTotal.WithLabelValues(s.Key.String(), targetName).Inc()
		}
		jobsCreated++
	}
	return StateResult{RequeueAfter: wellknown.RequeueIntervalDuringStage}, nil
}

// pruneTarget marks a target as StateAborted with an abort message.
// This is used during DAG BestEffort execution to skip targets whose upstream
// dependencies have failed, while allowing independent branches to proceed.
func (s *state) pruneTarget(plan *hibernatorv1alpha1.HibernatePlan, targetName, message string) {
	// Snapshot before mutation so the PostHook can detect the transition.
	prevSnapshot := snapshotExecutionStates(plan.Status.Executions)

	for i := range plan.Status.Executions {
		if plan.Status.Executions[i].Target == targetName {
			plan.Status.Executions[i].State = hibernatorv1alpha1.StateAborted
			plan.Status.Executions[i].Message = message
			break
		}
	}

	s.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: s.Key,
		Resource:       plan,
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			for i := range p.Status.Executions {
				if p.Status.Executions[i].Target == targetName {
					p.Status.Executions[i].State = hibernatorv1alpha1.StateAborted
					p.Status.Executions[i].Message = message
					break
				}
			}
		}),
		PostHook: s.executionProgressPostHook(prevSnapshot),
	})
}

// getCurrentCycleJobs returns the runner Jobs for the current execution cycle of a plan.
// It reads from the API server directly via client.Reader (typically mgr.GetAPIReader())
// to avoid cache staleness that could cause phantom "job missing" resets during
// informer re-list gaps. Returns nil if the plan has no active cycle.
func (s *state) getCurrentCycleJobs(ctx context.Context, plan *hibernatorv1alpha1.HibernatePlan) ([]batchv1.Job, error) {
	if plan.Status.CurrentCycleID == "" || plan.Status.CurrentOperation == "" {
		return nil, nil
	}
	var jobList batchv1.JobList
	if err := s.APIReader.List(ctx, &jobList,
		client.InNamespace(plan.Namespace),
		client.MatchingLabels{
			wellknown.LabelPlan:      plan.Name,
			wellknown.LabelCycleID:   plan.Status.CurrentCycleID,
			wellknown.LabelOperation: string(plan.Status.CurrentOperation),
		},
	); err != nil {
		return nil, err
	}
	return jobList.Items, nil
}

// updateExecutionStatuses updates execution statuses in the plan based on job conditions.
// It mirrors updatePlanExecutionStatuses in the legacy controller exactly:
//   - Iterates by execution status (not by job) to preserve ordering.
//   - Skips stale runner jobs marked during retry/recovery (LabelStaleRunnerJob).
//   - Uses JobComplete/JobFailed conditions rather than Succeeded/Failed counts.
//   - Handles GC'd jobs: if no job is found and FinishedAt is set while state is Running,
//     the state is inferred as Completed.
//   - Handles lost jobs: if no job is found, FinishedAt is nil, and state is Running,
//     onJobMissing is called. Once the miss threshold is reached the target is reset to
//     StatePending so executeStageTargets will re-dispatch a new runner Job.
//   - Sets RestoreConfigMapRef, JobRef, and Attempts fields.
//
// Instead of writing directly to the status sub-resource (which clobbers the worker's
// optimistic in-memory state), this method snapshots Executions before the mutation,
// mutates in-place, and only queues a PlanStatuses.Send when drift is detected.
// This keeps the write path through the StatusWriter — the single point of status
// persistence — and avoids redundant writes on poll ticks where nothing changed.
//
// onJobMissing and onJobFound are optional closures (nil disables the safeguard).
func (s *state) updateExecutionStatuses(ctx context.Context,
	log logr.Logger,
	plan *hibernatorv1alpha1.HibernatePlan,
	jobs []batchv1.Job,
) {
	prevSnapshot := snapshotExecutionStates(plan.Status.Executions)
	log.V(1).Info("updating execution statuses", "totalExecutions", len(plan.Status.Executions))

	for i := range plan.Status.Executions {
		exec := &plan.Status.Executions[i]
		log.V(1).Info("processing execution status", "target", exec.Target, "currentState", exec.State, "index", i)

		found := false
		for _, job := range jobs {
			// Skip stale runner jobs marked during retry/recovery.
			if _, ok := job.Labels[wellknown.LabelStaleRunnerJob]; ok {
				continue
			}

			targetLabel := job.Labels[wellknown.LabelTarget]
			executorLabel := job.Labels[wellknown.LabelExecutor]
			if exec.Target != targetLabel || exec.Executor != executorLabel {
				continue
			}

			prevState := exec.State

			found = true
			log.V(1).Info("found matching job for target",
				"target", exec.Target,
				"jobName", job.Name,
				"succeeded", job.Status.Succeeded,
				"failed", job.Status.Failed,
				"active", job.Status.Active)

			// Job reappeared — reset consecutive-miss counter.
			if s.Callbacks.OnJobFound != nil {
				s.Callbacks.OnJobFound(exec.Target)
			}

			if exec.StartedAt == nil && job.Status.StartTime != nil {
				exec.StartedAt = job.Status.StartTime
			}

			if job.Status.Active > 0 {
				exec.State = hibernatorv1alpha1.StateRunning
				exec.Message = fmt.Sprintf("A %s operation is already in progress", plan.Status.CurrentOperation)
			}

			for _, cond := range job.Status.Conditions {
				if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
					exec.State = hibernatorv1alpha1.StateCompleted
					if msg := s.getTerminationMessageFromPod(ctx, &job); msg != "" {
						exec.Message = msg
					}
					exec.FinishedAt = cond.LastTransitionTime.DeepCopy()
					break
				}

				if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
					exec.State = hibernatorv1alpha1.StateFailed
					if msg := s.getTerminationMessageFromPod(ctx, &job); msg != "" {
						exec.Message = msg
					}
					exec.FinishedAt = cond.LastTransitionTime.DeepCopy()
					break
				}
			}
			// Emit per-target execution metrics on first transition to a terminal state.
			if prevState != exec.State &&
				(exec.State == hibernatorv1alpha1.StateCompleted || exec.State == hibernatorv1alpha1.StateFailed) {

				operation := plan.Status.CurrentOperation
				status := "success"
				if exec.State == hibernatorv1alpha1.StateFailed {
					status = "failed"
				}
				metrics.ExecutionTotal.WithLabelValues(s.Key.String(), string(operation), exec.Executor, status).Inc()
				if exec.StartedAt != nil && exec.FinishedAt != nil {
					duration := exec.FinishedAt.Sub(exec.StartedAt.Time).Seconds()
					metrics.ExecutionDuration.WithLabelValues(s.Key.String(), string(operation), exec.Executor, status).Observe(duration)
				}
			}

			if execID, ok := job.Labels[wellknown.LabelExecutionID]; ok {
				exec.LogsRef = fmt.Sprintf("%s%s", wellknown.ExecutionIDLogPrefix, execID)
			}
			exec.RestoreConfigMapRef = fmt.Sprintf("%s/%s", job.Namespace, restore.GetRestoreConfigMap(plan.Name))
			exec.JobRef = fmt.Sprintf("%s/%s", job.Namespace, job.Name)
			exec.Attempts = job.Status.Failed + job.Status.Succeeded
			break
		}

		// GC'd job handling: if no job is found but FinishedAt is set and state is still
		// Running, infer completion. The job completed and was garbage-collected by TTL.
		if !found {
			if exec.FinishedAt != nil && exec.State == hibernatorv1alpha1.StateRunning {
				exec.State = hibernatorv1alpha1.StateCompleted
				log.V(1).Info("inferred completed state for execution (job GC'd)", "target", exec.Target)
			} else if exec.FinishedAt == nil && exec.State == hibernatorv1alpha1.StateRunning {
				// Job missing while still in-flight — track consecutive misses.
				// Once the threshold is reached the target is reset to StatePending so
				// executeStageTargets will re-dispatch a replacement runner Job.
				if s.Callbacks.OnJobMissing != nil && s.Callbacks.OnJobMissing(exec.Target) {
					exec.State = hibernatorv1alpha1.StatePending
					log.Info("consecutive job-miss threshold reached, resetting target to pending for re-dispatch", "target", exec.Target)
				} else {
					log.V(1).Info("job not found for running target, tracking consecutive misses", "target", exec.Target)
				}
			} else {
				log.V(1).Info("no job found for execution", "target", exec.Target, "currentState", exec.State)
			}
		}
	}

	// Only queue a status write if execution statuses actually changed.
	// This avoids flooding the StatusWriter on every poll tick when jobs
	// haven't progressed, while still capturing incremental changes
	// (state transitions, attempt bumps, JobRef/LogsRef assignment).
	if !executionStatesEqual(prevSnapshot, plan.Status.Executions) {
		executions := make([]hibernatorv1alpha1.ExecutionStatus, len(plan.Status.Executions))
		copy(executions, plan.Status.Executions)

		s.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
			NamespacedName: s.Key,
			Resource:       s.plan(),
			Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
				p.Status.Executions = executions
			}),
			PostHook: s.executionProgressPostHook(prevSnapshot),
		})
	}
}

// getTerminationMessageFromPod fetches the termination message from a pod of a job.
// This captures both error messages (from failed pods) and success messages (from completed pods)
// written to /dev/termination-log by the runner.
func (s *state) getTerminationMessageFromPod(ctx context.Context, job *batchv1.Job) string {
	var podList corev1.PodList
	if err := s.List(ctx, &podList,
		client.InNamespace(job.Namespace),
		client.MatchingLabels(job.Spec.Template.Labels),
	); err != nil {
		return ""
	}

	pods := podList.Items
	sort.Slice(pods, func(i, j int) bool {
		return pods[j].CreationTimestamp.Before(&pods[i].CreationTimestamp)
	})

	for _, pod := range pods {
		for _, status := range pod.Status.ContainerStatuses {
			if status.State.Terminated != nil && status.State.Terminated.Message != "" {
				return status.State.Terminated.Message
			}
			if status.LastTerminationState.Terminated != nil && status.LastTerminationState.Terminated.Message != "" {
				return status.LastTerminationState.Terminated.Message
			}
		}
	}
	return ""
}

// buildExecutionPlan creates a scheduler.ExecutionPlan from the plan's strategy.
func (s *state) buildExecutionPlan(plan *hibernatorv1alpha1.HibernatePlan, reverse bool) (scheduler.ExecutionPlan, error) {
	strategy := plan.Spec.Execution.Strategy
	maxConcurrency := ptr.Deref(strategy.MaxConcurrency, 0)

	targets := lo.Map(plan.Spec.Targets, func(t hibernatorv1alpha1.Target, _ int) string {
		return t.Name
	})

	var (
		execPlan scheduler.ExecutionPlan
		err      error
	)

	switch strategy.Type {
	case hibernatorv1alpha1.StrategySequential:
		execPlan = s.Planner.PlanSequential(ReverseIf(reverse, targets))
	case hibernatorv1alpha1.StrategyParallel:
		execPlan = s.Planner.PlanParallel(ReverseIf(reverse, targets), maxConcurrency)
	case hibernatorv1alpha1.StrategyStaged:
		stages := lo.Map(strategy.Stages, func(s hibernatorv1alpha1.Stage, _ int) scheduler.Stage {
			return scheduler.Stage{
				Name:           s.Name,
				Parallel:       s.Parallel,
				MaxConcurrency: ptr.Deref(s.MaxConcurrency, 0),
				Targets:        s.Targets,
			}
		})

		execPlan = s.Planner.PlanStaged(ReverseIf(reverse, stages), maxConcurrency)
	case hibernatorv1alpha1.StrategyDAG:
		deps := lo.Map(strategy.Dependencies, func(d hibernatorv1alpha1.Dependency, _ int) scheduler.Dependency {
			return scheduler.Dependency{
				From: lo.Ternary(reverse, d.To, d.From),
				To:   lo.Ternary(reverse, d.From, d.To),
			}
		})

		execPlan, err = s.Planner.PlanDAG(targets, deps, maxConcurrency)
		if err != nil {
			return scheduler.ExecutionPlan{}, fmt.Errorf("build DAG execution plan: %w", err)
		}
	default:
		return scheduler.ExecutionPlan{}, fmt.Errorf("unknown strategy type: %s", strategy.Type)
	}

	return execPlan, nil
}

// CreateRunnerJob creates a Kubernetes Job for executing a target.
func (s *state) createRunnerJob(ctx context.Context, log logr.Logger, clk clock.Clock,
	plan *hibernatorv1alpha1.HibernatePlan,
	target *hibernatorv1alpha1.Target,
	operation hibernatorv1alpha1.PlanOperation,
	infra ExecutorInfra) error {

	ts := fmt.Sprintf("%d", clk.Now().Unix())
	baseID := fmt.Sprintf("%s-%s", plan.Name, target.Name)
	maxBaseLen := 63 - len(ts) - 1
	executionID := fmt.Sprintf("%s-%s", k8sutil.ShortenName(baseID, maxBaseLen), ts)

	var paramsJSON []byte
	if target.Parameters != nil {
		paramsJSON = target.Parameters.Raw
	}

	backoffLimit := int32(wellknown.DefaultJobBackoffLimit)
	ttlSeconds := int32(wellknown.DefaultJobTTLSeconds)
	tokenExpiration := int64(wellknown.StreamTokenExpirationSeconds)

	if infra.RunnerServiceAccount == "" {
		infra.RunnerServiceAccount = "hibernator-runner"
	}
	if infra.RunnerImage == "" {
		infra.RunnerImage = wellknown.RunnerImage
	}

	connectorNamespace := target.ConnectorRef.Namespace
	if connectorNamespace == "" {
		connectorNamespace = plan.Namespace
	}

	generateNameBase := fmt.Sprintf("%s-%s", plan.Name, target.Name)
	generateName := fmt.Sprintf("runner-%s-", k8sutil.ShortenName(generateNameBase, 50))

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: generateName,
			Namespace:    plan.Namespace,
			Labels: map[string]string{
				wellknown.LabelCycleID:     plan.Status.CurrentCycleID,
				wellknown.LabelOperation:   string(operation),
				wellknown.LabelPlan:        plan.Name,
				wellknown.LabelExecutionID: executionID,
				wellknown.LabelExecutor:    target.Type,
				wellknown.LabelTarget:      target.Name,
			},
			Annotations: map[string]string{
				wellknown.AnnotationPlan:   plan.Name,
				wellknown.AnnotationTarget: target.Name,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttlSeconds,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						wellknown.LabelCycleID:     plan.Status.CurrentCycleID,
						wellknown.LabelOperation:   string(operation),
						wellknown.LabelPlan:        plan.Name,
						wellknown.LabelExecutionID: executionID,
						wellknown.LabelExecutor:    target.Type,
						wellknown.LabelTarget:      target.Name,
					},
					Annotations: map[string]string{
						wellknown.AnnotationPlan:   plan.Name,
						wellknown.AnnotationTarget: target.Name,
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: infra.RunnerServiceAccount,
					Containers: []corev1.Container{
						{
							Name:  "runner",
							Image: infra.RunnerImage,
							Args: []string{
								"--operation", string(operation),
								"--target", target.Name,
								"--target-type", target.Type,
								"--plan", plan.Name,
							},
						Env: []corev1.EnvVar{
							{Name: "POD_NAMESPACE", Value: plan.Namespace},
							{Name: "HIBERNATOR_EXECUTION_ID", Value: executionID},
							{Name: "HIBERNATOR_CYCLE_ID", Value: plan.Status.CurrentCycleID},
							{Name: "HIBERNATOR_CONTROL_PLANE_ENDPOINT", Value: infra.ControlPlaneEndpoint},
							{Name: "HIBERNATOR_USE_TLS", Value: "false"},
							{Name: "HIBERNATOR_GRPC_ENDPOINT", Value: fmt.Sprintf("%s:9444", infra.ControlPlaneEndpoint)},
							{Name: "HIBERNATOR_WEBSOCKET_ENDPOINT", Value: fmt.Sprintf("ws://%s:8082", infra.ControlPlaneEndpoint)},
							{Name: "HIBERNATOR_HTTP_CALLBACK_ENDPOINT", Value: fmt.Sprintf("http://%s:8082", infra.ControlPlaneEndpoint)},
							{Name: "HIBERNATOR_TARGET_PARAMS", Value: string(paramsJSON)},
							{Name: "HIBERNATOR_CONNECTOR_KIND", Value: target.ConnectorRef.Kind},
							{Name: "HIBERNATOR_CONNECTOR_NAME", Value: target.ConnectorRef.Name},
							{Name: "HIBERNATOR_CONNECTOR_NAMESPACE", Value: connectorNamespace},
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
												Audience:          wellknown.StreamTokenAudience,
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

	if err := controllerutil.SetControllerReference(plan, job, s.Scheme); err != nil {
		return fmt.Errorf("set owner reference: %w", err)
	}

	log.V(1).Info("creating runner job", "target", target.Name, "operation", operation, "jobName", generateName)
	return s.Create(ctx, job)
}
