package hibernateplan

import (
	"context"
	"fmt"
	"sort"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/ardikabs/hibernator/pkg/k8sutil"
	"github.com/go-logr/logr"
)

// createRunnerJob creates a Kubernetes Job for executing a target.
func (r *Reconciler) createRunnerJob(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan, target *hibernatorv1alpha1.Target, operation string) error {
	// Calculate execution ID
	// Format: <plan>-<target>-<timestamp>
	// Max length for label value is 63 characters.
	ts := fmt.Sprintf("%d", r.Clock.Now().Unix())
	baseID := fmt.Sprintf("%s-%s", plan.Name, target.Name)
	// Reserve space for timestamp and hyphen (e.g., -1678900000) -> approx 11 chars
	maxBaseLen := 63 - len(ts) - 1
	executionID := fmt.Sprintf("%s-%s", k8sutil.ShortenName(baseID, maxBaseLen), ts)

	// Serialize target parameters
	var paramsJSON []byte
	if target.Parameters != nil {
		paramsJSON = target.Parameters.Raw
	}

	// Build job spec
	backoffLimit := int32(wellknown.DefaultJobBackoffLimit)
	ttlSeconds := int32(wellknown.DefaultJobTTLSeconds)
	tokenExpiration := int64(wellknown.StreamTokenExpirationSeconds)
	runnerServiceAccount := r.RunnerServiceAccount
	if runnerServiceAccount == "" {
		runnerServiceAccount = "hibernator-runner"
	}

	// Reconstruct name with ShortenName
	// Format: runner-<plan>-<target>-
	// Max length for GenerateName is 63, but K8s appends random suffix (5 chars), so safe limit is 58.
	// Hence, we exclude "runner-" (7 chars) as well as the trailing hyphen (1 char), leaving 50 chars for plan and target.
	generateNameBase := fmt.Sprintf("%s-%s", plan.Name, target.Name)
	generateName := fmt.Sprintf("runner-%s-", k8sutil.ShortenName(generateNameBase, 50))

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: generateName,
			Namespace:    plan.Namespace,
			Labels: map[string]string{
				wellknown.LabelCycleID:     plan.Status.CurrentCycleID,
				wellknown.LabelOperation:   operation,
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
						wellknown.LabelOperation:   operation,
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

	// Set owner reference
	if err := controllerutil.SetControllerReference(plan, job, r.Scheme); err != nil {
		return err
	}

	log.V(1).Info("creating runner job", "target", target.Name, "operation", operation, "jobName", generateName)
	return r.Create(ctx, job)
}

func (r *Reconciler) findTarget(plan *hibernatorv1alpha1.HibernatePlan, name string) *hibernatorv1alpha1.Target {
	for i := range plan.Spec.Targets {
		if plan.Spec.Targets[i].Name == name {
			return &plan.Spec.Targets[i]
		}
	}
	return nil
}

func (r *Reconciler) findTargetType(plan *hibernatorv1alpha1.HibernatePlan, name string) string {
	for _, t := range plan.Spec.Targets {
		if t.Name == name {
			return t.Type
		}
	}
	return ""
}

func (r *Reconciler) getConnectorNamespace(plan *hibernatorv1alpha1.HibernatePlan, ref *hibernatorv1alpha1.ConnectorRef) string {
	if ref.Namespace != "" {
		return ref.Namespace
	}
	return plan.Namespace
}

func (r *Reconciler) getRunnerImage() string {
	if r.RunnerImage != "" {
		return r.RunnerImage
	}
	return wellknown.RunnerImage
}

// countRunningJobsInStage counts how many jobs in the provided list are running for targets in the stage.
func (r *Reconciler) countRunningJobsInStage(plan *hibernatorv1alpha1.HibernatePlan, jobList *batchv1.JobList, stage scheduler.ExecutionStage) int {
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
		targetLabel := job.Labels[wellknown.LabelTarget]
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
func (r *Reconciler) findExecutionStatus(plan *hibernatorv1alpha1.HibernatePlan, targetType, targetName string) *hibernatorv1alpha1.ExecutionStatus {
	for i := range plan.Status.Executions {
		if plan.Status.Executions[i].Target == targetName &&
			plan.Status.Executions[i].Executor == targetType {
			return &plan.Status.Executions[i]
		} else if plan.Status.Executions[i].Target == fmt.Sprintf("%s/%s", targetType, targetName) {
			// Support old format
			return &plan.Status.Executions[i]
		}
	}
	return nil
}

// findFailedDependencies checks if any target in the stage depends on a failed target.
// Returns list of failed target names that are dependencies of targets in the stage.
func (r *Reconciler) findFailedDependencies(plan *hibernatorv1alpha1.HibernatePlan, dependencies []hibernatorv1alpha1.Dependency, stage scheduler.ExecutionStage) []string {
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
func (r *Reconciler) findPlansForException(ctx context.Context, obj client.Object) []reconcile.Request {
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

// findPlansForRunnerJob returns reconcile requests for HibernatePlans when a Runner Job changes.
func (r *Reconciler) findPlansForRunnerJob(ctx context.Context, obj client.Object) []reconcile.Request {
	job, ok := obj.(*batchv1.Job)
	if !ok {
		return nil
	}

	if planName, ok := job.Labels[wellknown.LabelPlan]; ok {
		// Enqueue the referenced plan
		return []reconcile.Request{
			{
				NamespacedName: types.NamespacedName{
					Name:      planName,
					Namespace: job.Namespace,
				},
			},
		}
	}

	return nil
}

// getDetailedErrorFromPod fetches the termination message from the failed pod of a job.
func (r *Reconciler) getDetailedErrorFromPod(ctx context.Context, job *batchv1.Job) string {
	// List pods belonging to the job
	// We use the labels that we know we put on the pods
	var podList corev1.PodList
	if err := r.List(ctx, &podList,
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
		// Check current container statuses
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
