//go:build e2e

package testutil

import (
	"context"
	"time"

	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

const (
	DefaultTimeout  = 10 * time.Second
	DefaultInterval = 250 * time.Millisecond
)

// ConsistentllyAtPhase asserts that the HibernatePlan remains at the expected phase for the specified duration.
func ConsistentllyAtPhase(ctx context.Context, k8sClient client.Client, plan *hibernatorv1alpha1.HibernatePlan, phase hibernatorv1alpha1.PlanPhase, duration time.Duration) {
	Consistently(func() hibernatorv1alpha1.PlanPhase {
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
		return plan.Status.Phase
	}, duration, DefaultInterval).Should(Equal(phase))
}

// EventuallyPhase waits until the HibernatePlan reaches the expected phase.
func EventuallyPhase(ctx context.Context, k8sClient client.Client, plan *hibernatorv1alpha1.HibernatePlan, phase hibernatorv1alpha1.PlanPhase) {
	Eventually(func() hibernatorv1alpha1.PlanPhase {
		_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
		return plan.Status.Phase
	}, DefaultTimeout, DefaultInterval).Should(Equal(phase))
}

// EventuallyJobCreated waits until a Job with specified labels is created.
func EventuallyJobCreated(ctx context.Context, k8sClient client.Client, namespace, planName, operation, target string) *batchv1.Job {
	var job batchv1.Job
	Eventually(func() bool {
		var jobs batchv1.JobList
		_ = k8sClient.List(ctx, &jobs, client.InNamespace(namespace), client.MatchingLabels{
			wellknown.LabelPlan:      planName,
			wellknown.LabelOperation: operation,
			wellknown.LabelTarget:    target,
		})
		if len(jobs.Items) > 0 {
			job = jobs.Items[0]
			return true
		}
		return false
	}, DefaultTimeout, DefaultInterval).Should(BeTrueBecause("Job for plan %s and operation %s should be created", planName, operation))
	return &job
}

// EventuallyMultiJobsCreated waits until few Jobs with target labels are created.
func EventuallyMultiJobsCreated(ctx context.Context, k8sClient client.Client, namespace, planName, operation string, targets ...string) []*batchv1.Job {
	jobs := []*batchv1.Job{}
	Eventually(func() bool {
		var jobList batchv1.JobList
		_ = k8sClient.List(ctx, &jobList, client.InNamespace(namespace), client.MatchingLabels{
			wellknown.LabelPlan:      planName,
			wellknown.LabelOperation: operation,
		})

		for _, item := range jobList.Items {
			for _, target := range targets {
				if item.Labels[wellknown.LabelTarget] == target {
					jobs = append(jobs, &item)
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

// SimulateJobSuccess updates the Job status to successful.
func SimulateJobSuccess(ctx context.Context, k8sClient client.Client, job *batchv1.Job, completionTime time.Time) {
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(job), job)).To(Succeed())

	patch := client.MergeFrom(job.DeepCopy())
	job.Status.Succeeded = 1
	job.Status.Active = 0
	if job.Status.StartTime == nil {
		job.Status.StartTime = &metav1.Time{Time: completionTime.Add(-5 * time.Minute)}
	}
	job.Status.CompletionTime = &metav1.Time{Time: completionTime}
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
	if obj == nil || (obj.GetNamespace() == "" && obj.GetName() == "") {
		return
	}

	_ = k8sClient.Delete(ctx, obj)
	Eventually(func() bool {
		return errors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj))
	}, DefaultTimeout, DefaultInterval).Should(BeTrue())
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
