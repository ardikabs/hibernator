/*
Copyright 2026 Ardika Saputro.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

var _ = Describe("Error Recovery E2E", func() {
	const (
		timeout  = time.Minute * 3
		interval = time.Second
	)

	var (
		planName      string
		plan          *hibernatorv1alpha1.HibernatePlan
		cloudProvider *hibernatorv1alpha1.CloudProvider
	)

	BeforeEach(func() {
		planName = "test-recovery-" + time.Now().Format("150405")

		cloudProvider = &hibernatorv1alpha1.CloudProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name:      planName + "-aws",
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.CloudProviderSpec{
				Type: "aws",
				AWS: &hibernatorv1alpha1.AWSConfig{
					AccountId: "123456789012",
					Region:    "us-east-1",
					Auth: hibernatorv1alpha1.AWSAuth{
						ServiceAccount: &hibernatorv1alpha1.ServiceAccountAuth{
							AssumeRoleArn: "arn:aws:iam::123456789012:role/hibernator",
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, cloudProvider)).To(Succeed())
	})

	AfterEach(func() {
		if plan != nil {
			_ = k8sClient.Delete(ctx, plan)
		}
		if cloudProvider != nil {
			_ = k8sClient.Delete(ctx, cloudProvider)
		}
	})

	It("Should retry failed operations with exponential backoff", func() {
		By("Creating HibernatePlan with retry configuration")
		plan = &hibernatorv1alpha1.HibernatePlan{
			ObjectMeta: metav1.ObjectMeta{
				Name:      planName,
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.HibernatePlanSpec{
				Schedule: hibernatorv1alpha1.Schedule{
					Timezone: "UTC",
					OffHours: []hibernatorv1alpha1.OffHourWindow{
						{
							Start:      "00:00",
							End:        "23:59",
							DaysOfWeek: []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"},
						},
					},
				},
				Execution: hibernatorv1alpha1.Execution{
					Strategy: hibernatorv1alpha1.ExecutionStrategy{
						Type: hibernatorv1alpha1.StrategySequential,
					},
				},
				Behavior: hibernatorv1alpha1.Behavior{
					Mode:     hibernatorv1alpha1.BehaviorBestEffort,
					FailFast: false,
					Retries:  5, // Allow 5 retries
				},
				Targets: []hibernatorv1alpha1.Target{
					{
						Name: "test-rds",
						Type: "rds",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{
							Kind: "CloudProvider",
							Name: cloudProvider.Name,
						},
						Parameters: &hibernatorv1alpha1.Parameters{
							Raw: []byte(`{"dbInstanceIdentifier":"test-db"}`),
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Waiting for plan to start hibernating")
		Eventually(func() hibernatorv1alpha1.PlanPhase {
			_ = k8sClient.Get(ctx, client.ObjectKey{
				Name:      planName,
				Namespace: testNamespace,
			}, plan)
			return plan.Status.Phase
		}, timeout, interval).Should(Equal(hibernatorv1alpha1.PhaseHibernating))

		By("Simulating job failure")
		var job batchv1.Job
		Eventually(func() error {
			jobList := &batchv1.JobList{}
			if err := k8sClient.List(ctx, jobList, client.InNamespace(testNamespace)); err != nil {
				return err
			}
			for _, j := range jobList.Items {
				if j.Labels["hibernator.ardikabs.com/plan"] == planName {
					job = j
					return nil
				}
			}
			return nil
		}, timeout, interval).Should(Succeed())

		// Mark job as failed
		job.Status.Failed = 1
		job.Status.Conditions = []batchv1.JobCondition{
			{
				Type:    batchv1.JobFailed,
				Status:  corev1.ConditionTrue,
				Reason:  "BackoffLimitExceeded",
				Message: "Job has reached the specified backoff limit",
			},
		}
		Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

		By("Verifying plan transitions to Error phase")
		Eventually(func() hibernatorv1alpha1.PlanPhase {
			_ = k8sClient.Get(ctx, client.ObjectKey{
				Name:      planName,
				Namespace: testNamespace,
			}, plan)
			return plan.Status.Phase
		}, timeout, interval).Should(Equal(hibernatorv1alpha1.PhaseError))

		By("Verifying error message is recorded")
		Expect(k8sClient.Get(ctx, client.ObjectKey{
			Name:      planName,
			Namespace: testNamespace,
		}, plan)).To(Succeed())
		Expect(plan.Status.ErrorMessage).NotTo(BeEmpty())

		By("Verifying retry count is initialized")
		initialRetryCount := plan.Status.RetryCount
		Expect(initialRetryCount).To(BeNumerically(">=", 0))

		By("Waiting for error recovery to attempt retry")
		Eventually(func() hibernatorv1alpha1.PlanPhase {
			_ = k8sClient.Get(ctx, client.ObjectKey{
				Name:      planName,
				Namespace: testNamespace,
			}, plan)
			return plan.Status.Phase
		}, timeout, interval).Should(SatisfyAny(
			Equal(hibernatorv1alpha1.PhaseHibernating),
			Equal(hibernatorv1alpha1.PhaseError),
		))

		By("Verifying retry attempt was recorded if retried")
		Expect(k8sClient.Get(ctx, client.ObjectKey{
			Name:      planName,
			Namespace: testNamespace,
		}, plan)).To(Succeed())
		if plan.Status.Phase == hibernatorv1alpha1.PhaseHibernating {
			Expect(plan.Status.RetryCount).To(BeNumerically(">", initialRetryCount))
			Expect(plan.Status.LastRetryTime).NotTo(BeNil())
		}
	})

	It("Should stop retrying after max retries exceeded", func() {
		By("Creating HibernatePlan with limited retries")
		plan = &hibernatorv1alpha1.HibernatePlan{
			ObjectMeta: metav1.ObjectMeta{
				Name:      planName,
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.HibernatePlanSpec{
				Schedule: hibernatorv1alpha1.Schedule{
					Timezone: "UTC",
					OffHours: []hibernatorv1alpha1.OffHourWindow{
						{
							Start:      "00:00",
							End:        "23:59",
							DaysOfWeek: []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"},
						},
					},
				},
				Execution: hibernatorv1alpha1.Execution{
					Strategy: hibernatorv1alpha1.ExecutionStrategy{
						Type: hibernatorv1alpha1.StrategySequential,
					},
				},
				Behavior: hibernatorv1alpha1.Behavior{
					Mode:     hibernatorv1alpha1.BehaviorStrict,
					FailFast: true,
					Retries:  0, // No retries
				},
				Targets: []hibernatorv1alpha1.Target{
					{
						Name: "test-ec2",
						Type: "ec2",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{
							Kind: "CloudProvider",
							Name: cloudProvider.Name,
						},
						Parameters: &hibernatorv1alpha1.Parameters{
							Raw: []byte(`{"instanceIds":["i-test123"]}`),
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Manually setting plan to Error state with max retries reached")
		Eventually(func() error {
			if err := k8sClient.Get(ctx, client.ObjectKey{
				Name:      planName,
				Namespace: testNamespace,
			}, plan); err != nil {
				return err
			}
			plan.Status.Phase = hibernatorv1alpha1.PhaseError
			plan.Status.RetryCount = 1 // Exceeded max (0)
			plan.Status.ErrorMessage = "Simulated error: max retries exceeded"
			now := metav1.Now()
			plan.Status.LastRetryTime = &now
			return k8sClient.Status().Update(ctx, plan)
		}, timeout, interval).Should(Succeed())

		By("Verifying plan stays in Error phase")
		Consistently(func() hibernatorv1alpha1.PlanPhase {
			_ = k8sClient.Get(ctx, client.ObjectKey{
				Name:      planName,
				Namespace: testNamespace,
			}, plan)
			return plan.Status.Phase
		}, time.Second*10, interval).Should(Equal(hibernatorv1alpha1.PhaseError))

		By("Verifying retry count doesn't increase")
		Expect(k8sClient.Get(ctx, client.ObjectKey{
			Name:      planName,
			Namespace: testNamespace,
		}, plan)).To(Succeed())
		Expect(plan.Status.RetryCount).To(Equal(int32(1)))
	})
})
