//go:build e2e

package testutil

import (
	"context"
	"reflect"
	"sort"
	"time"

	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/ardikabs/hibernator/pkg/k8sutil"
)

const (
	DefaultTimeout        = 30 * time.Second
	DefaultInterval       = 200 * time.Millisecond
	MinConsistentDuration = 5 * time.Second // Minimum duration for negative assertions to avoid flaky tests
)

// ConsistentllyAtPhase asserts that the HibernatePlan remains at the expected phase for the specified duration.
// Uses MinConsistentDuration as a minimum to avoid flaky tests on slower CI runners.
func ConsistentllyAtPhase(ctx context.Context, k8sClient client.Client, plan *hibernatorv1alpha1.HibernatePlan, phase hibernatorv1alpha1.PlanPhase, duration time.Duration) {
	// Enforce minimum duration for reliable negative assertions
	if duration < MinConsistentDuration {
		duration = MinConsistentDuration
	}
	Consistently(func() hibernatorv1alpha1.PlanPhase {
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
		return plan.Status.Phase
	}).
		WithTimeout(duration).
		WithPolling(time.Second).
		Should(Equal(phase))
}

// EventuallyPhase waits until the HibernatePlan reaches the expected phase.
func EventuallyPhase(ctx context.Context, k8sClient client.Client, plan *hibernatorv1alpha1.HibernatePlan, phase hibernatorv1alpha1.PlanPhase) {
	Eventually(func() hibernatorv1alpha1.PlanPhase {
		TriggerReconcile(ctx, k8sClient, plan)

		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
		return plan.Status.Phase
	}).
		WithTimeout(DefaultTimeout).
		WithPolling(DefaultInterval).
		Should(Equal(phase))
}

// EventuallyJobCreated waits until a Job with specified labels is created.
func EventuallyJobCreated(ctx context.Context, k8sClient client.Client, namespace, planName string, operation hibernatorv1alpha1.PlanOperation, target string) *batchv1.Job {
	var job batchv1.Job
	Eventually(func() bool {
		var jobList batchv1.JobList
		_ = k8sClient.List(ctx, &jobList, client.InNamespace(namespace), client.MatchingLabels{
			wellknown.LabelPlan:      planName,
			wellknown.LabelOperation: string(operation),
			wellknown.LabelTarget:    target,
		})

		jobs := jobList.Items
		sort.Slice(jobs, func(i, j int) bool {
			return jobs[j].CreationTimestamp.Before(&jobs[i].CreationTimestamp)
		})

		for _, j := range jobs {
			if _, ok := j.Labels[wellknown.LabelStaleRunnerJob]; ok {
				continue
			}

			if j.Status.CompletionTime != nil {
				continue
			}

			job = j
			return true
		}

		return false
	}, DefaultTimeout, DefaultInterval).Should(BeTrueBecause("Job for plan %s and operation %s should be created", planName, operation))
	return &job
}

// EventuallyMultiJobsCreated waits until few Jobs with target labels are created.
func EventuallyMultiJobsCreated(ctx context.Context, k8sClient client.Client, namespace, planName string, operation hibernatorv1alpha1.PlanOperation, targets ...string) []*batchv1.Job {
	jobs := []*batchv1.Job{}
	Eventually(func() bool {
		var jobList batchv1.JobList
		_ = k8sClient.List(ctx, &jobList, client.InNamespace(namespace), client.MatchingLabels{
			wellknown.LabelPlan:      planName,
			wellknown.LabelOperation: string(operation),
		})

		for _, job := range jobList.Items {
			if _, ok := job.Labels[wellknown.LabelStaleRunnerJob]; ok {
				continue
			}

			if job.Status.CompletionTime != nil {
				continue
			}

			for _, target := range targets {
				if job.Labels[wellknown.LabelTarget] == target {
					jobs = append(jobs, &job)
				}
			}
		}

		if len(jobs) == len(targets) {
			return true
		}

		return false
	}, DefaultTimeout, DefaultInterval).Should(BeTrueBecause("Job for plan %s and operation %s should be created", planName, operation))
	return jobs
}

// SimulateJobRunning updates the Job status to running.
func SimulateJobRunning(ctx context.Context, k8sClient client.Client, job *batchv1.Job, completionTime time.Time) {
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(job), job)).To(Succeed())

	patch := client.MergeFrom(job.DeepCopy())
	job.Status.Active = 1
	if job.Status.StartTime == nil {
		job.Status.StartTime = &metav1.Time{Time: completionTime.Add(-5 * time.Minute)}
	}
	Expect(k8sClient.Status().Patch(ctx, job, patch)).To(Succeed())

	// Verify status is actually updated in the API server
	Eventually(func() bool {
		var j batchv1.Job
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(job), &j); err != nil {
			return false
		}
		return j.Status.Active > 0
	}, DefaultTimeout, DefaultInterval).Should(BeTrueBecause("Job status should be reflected in API server"))
}

// SimulateJobSuccess updates the Job status to successful.
func SimulateJobSuccess(ctx context.Context, k8sClient client.Client, job *batchv1.Job, completionTime time.Time) {
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(job), job)).To(Succeed())

	patch := client.MergeFrom(job.DeepCopy())
	job.Status.Succeeded = 1
	job.Status.Active = 0
	if job.Status.StartTime == nil {
		job.Status.StartTime = &metav1.Time{Time: completionTime.Add(-5 * time.Minute)}
	}
	if job.Status.CompletionTime == nil {
		job.Status.CompletionTime = &metav1.Time{Time: completionTime}
	}
	job.Status.Conditions = []batchv1.JobCondition{
		{
			Type:               batchv1.JobSuccessCriteriaMet,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Time{Time: completionTime.Add(-time.Millisecond)},
		},
		{
			Type:               batchv1.JobComplete,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Time{Time: completionTime},
		},
	}
	Expect(k8sClient.Status().Patch(ctx, job, patch)).To(Succeed())

	// Verify status is actually updated in the API server
	Eventually(func() bool {
		var j batchv1.Job
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(job), &j); err != nil {
			return false
		}
		isComplete := false
		for _, cond := range j.Status.Conditions {
			if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
				isComplete = true
				break
			}
		}
		return j.Status.Succeeded == 1 && isComplete
	}, DefaultTimeout, DefaultInterval).Should(BeTrueBecause("Job status should be reflected in API server"))
}

// EnsureDeleted deletes the object and waits until it's gone.
func EnsureDeleted(ctx context.Context, k8sClient client.Client, obj client.Object) {
	if obj == nil || (reflect.ValueOf(obj).Kind() == reflect.Ptr && reflect.ValueOf(obj).IsNil()) {
		return
	}

	if obj.GetNamespace() == "" && obj.GetName() == "" {
		return
	}

	_ = k8sClient.Delete(ctx, obj)
	Eventually(func() bool {
		return errors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj))
	}, DefaultTimeout, DefaultInterval).Should(BeTrue())
}

// EnsureDeletedAll deletes all objects in the slice and waits until they're gone.
func EnsureDeletedAll[T client.Object](ctx context.Context, k8sClient client.Client, objs []T) {
	for _, obj := range objs {
		if obj.GetNamespace() == "" && obj.GetName() == "" {
			continue
		}
		_ = k8sClient.Delete(ctx, obj)
		Eventually(func() bool {
			return errors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj))
		}, DefaultTimeout, DefaultInterval).Should(BeTrue())
	}
}

// SimulateJobFailure updates the Job status to failed (backoff limit exceeded).
func SimulateJobFailure(ctx context.Context, k8sClient client.Client, job *batchv1.Job, failureTime time.Time) {
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(job), job)).To(Succeed())

	patch := client.MergeFrom(job.DeepCopy())
	job.Status.Failed = 4 // exceed default BackoffLimit of 3
	job.Status.Active = 0
	if job.Status.StartTime == nil {
		job.Status.StartTime = &metav1.Time{Time: failureTime.Add(-5 * time.Minute)}
	}
	job.Status.Conditions = []batchv1.JobCondition{
		{
			Type:               batchv1.JobFailureTarget,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Time{Time: failureTime},
		},
		{
			Type:               batchv1.JobFailed,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Time{Time: failureTime},
			Reason:             "BackoffLimitExceeded",
			Message:            "Job has reached the specified backoff limit",
		},
	}
	Expect(k8sClient.Status().Patch(ctx, job, patch)).To(Succeed())

	// Verify status is actually updated in the API server
	Eventually(func() bool {
		var j batchv1.Job
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(job), &j); err != nil {
			return false
		}
		for _, cond := range j.Status.Conditions {
			if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
				return true
			}
		}
		return false
	}, DefaultTimeout, DefaultInterval).Should(BeTrueBecause("Job status should reflect failure in API server"))
}

// EventuallyExceptionState waits until the ScheduleException reaches the expected state.
func EventuallyExceptionState(ctx context.Context, k8sClient client.Client, exc *hibernatorv1alpha1.ScheduleException, state hibernatorv1alpha1.ExceptionState) {
	Eventually(func() hibernatorv1alpha1.ExceptionState {
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(exc), exc)
		return exc.Status.State
	}, DefaultTimeout, DefaultInterval).Should(Equal(state))
}

// SimulateHibernation drives a plan from Hibernating → Hibernated by simulating successful
// completion of the hibernation Job for each target.
// It waits for the plan to enter Hibernating, creates and succeeds each job in order,
// then waits for the terminal Hibernated phase.
// Use this wherever a test needs to cross the execution boundary without caring about the
// job internals — the "golden-path execution" helper.
func SimulateHibernation(ctx context.Context, k8sClient client.Client, plan *hibernatorv1alpha1.HibernatePlan, restoreManager *restore.Manager, now time.Time, targets ...string) {
	TriggerReconcile(ctx, k8sClient, plan)
	EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)
	for _, target := range targets {
		job := EventuallyJobCreated(ctx, k8sClient, plan.Namespace, plan.Name, hibernatorv1alpha1.OperationHibernate, target)
		SimulateJobSuccess(ctx, k8sClient, job, now)
	}
	EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernated)
	EventuallyRestoreDataSaved(ctx, k8sClient, plan, 0)

	cmKey, _ := k8sutil.ObjectKeyFromString(plan.Status.Executions[0].RestoreConfigMapRef)
	var restoreCM corev1.ConfigMap
	Expect(k8sClient.Get(ctx, cmKey, &restoreCM)).To(Succeed())

	// Manually inject some restore data to simulate real-world usage if needed
	Expect(restoreManager.Save(ctx, plan.Namespace, plan.Name, plan.Spec.Targets[0].Name, &restore.Data{
		Target: plan.Spec.Targets[0].Name,
	})).To(Succeed())
}

// SimulateWakeup drives a plan from WakingUp → Active by simulating successful completion of
// the wakeup Job for each target.
// It waits for the plan to enter WakingUp, creates and succeeds each job in order,
// then waits for the terminal Active phase.
// Use this wherever a test needs to cross the execution boundary without caring about the
// job internals — the "golden-path execution" helper.
func SimulateWakeup(ctx context.Context, k8sClient client.Client, plan *hibernatorv1alpha1.HibernatePlan, now time.Time, targets ...string) {
	TriggerReconcile(ctx, k8sClient, plan)
	EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)
	for _, target := range targets {
		job := EventuallyJobCreated(ctx, k8sClient, plan.Namespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, target)
		SimulateJobSuccess(ctx, k8sClient, job, now)
	}
	EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)
}

// EventuallyTargetState waits until the specific target in the plan reaches the expected state.
func EventuallyTargetState(ctx context.Context, k8sClient client.Client, plan *hibernatorv1alpha1.HibernatePlan, targetIndex int, state hibernatorv1alpha1.ExecutionState) {
	Eventually(func() hibernatorv1alpha1.ExecutionState {
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
		if len(plan.Status.Executions) <= targetIndex {
			return ""
		}
		return plan.Status.Executions[targetIndex].State
	}, DefaultTimeout, DefaultInterval).Should(Equal(state))
}

// EventuallyRestoreDataSaved waits until the plan transitions to Hibernated and has restore data reference.
func EventuallyRestoreDataSaved(ctx context.Context, k8sClient client.Client, plan *hibernatorv1alpha1.HibernatePlan, targetIndex int) {
	Eventually(func() bool {
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
		if len(plan.Status.Executions) <= targetIndex {
			return false
		}
		exec := plan.Status.Executions[targetIndex]
		return plan.Status.Phase == hibernatorv1alpha1.PhaseHibernated &&
			exec.State == hibernatorv1alpha1.StateCompleted &&
			exec.RestoreConfigMapRef != ""
	}, DefaultTimeout, DefaultInterval).Should(BeTrueBecause("Restore data should be saved for target %d", targetIndex))
}
