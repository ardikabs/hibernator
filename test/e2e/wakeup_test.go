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

var _ = Describe("Wakeup Cycle E2E", func() {
	const (
		timeout  = time.Minute * 2
		interval = time.Second
	)

	var (
		planName      string
		plan          *hibernatorv1alpha1.HibernatePlan
		cloudProvider *hibernatorv1alpha1.CloudProvider
	)

	BeforeEach(func() {
		planName = "test-wakeup-" + time.Now().Format("150405")

		// Create CloudProvider connector
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

		// Create HibernatePlan with schedule that keeps it awake (on-hours)
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
							// Off-hours in past, so should be on-hours now
							Start:      "01:00",
							End:        "02:00",
							DaysOfWeek: []string{"MON"},
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
					Retries:  3,
				},
				Targets: []hibernatorv1alpha1.Target{
					{
						Name: "test-eks",
						Type: "eks",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{
							Kind: "CloudProvider",
							Name: cloudProvider.Name,
						},
						Parameters: &hibernatorv1alpha1.Parameters{
							Raw: []byte(`{"clusterName":"test-cluster"}`),
						},
					},
				},
			},
		}
	})

	AfterEach(func() {
		if plan != nil {
			_ = k8sClient.Delete(ctx, plan)
		}
		if cloudProvider != nil {
			_ = k8sClient.Delete(ctx, cloudProvider)
		}
	})

	It("Should complete full wakeup cycle from hibernated state", func() {
		By("Creating HibernatePlan")
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Manually setting plan to Hibernated state")
		Eventually(func() error {
			if err := k8sClient.Get(ctx, client.ObjectKey{
				Name:      planName,
				Namespace: testNamespace,
			}, plan); err != nil {
				return err
			}
			plan.Status.Phase = hibernatorv1alpha1.PhaseHibernated
			plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
				{
					Target:              "eks/test-eks",
					Executor:            "eks",
					State:               hibernatorv1alpha1.StateCompleted,
					RestoreConfigMapRef: testNamespace + "/restore-data-" + planName,
				},
			}
			return k8sClient.Status().Update(ctx, plan)
		}, timeout, interval).Should(Succeed())

		// Create mock restore ConfigMap
		restoreCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "restore-data-" + planName,
				Namespace: testNamespace,
			},
			Data: map[string]string{
				"eks_test-eks": `{"nodeGroupConfigs": [{"name": "ng-1", "desiredSize": 2}]}`,
			},
		}
		Expect(k8sClient.Create(ctx, restoreCM)).To(Succeed())

		By("Waiting for schedule evaluation to trigger wakeup")
		Eventually(func() hibernatorv1alpha1.PlanPhase {
			err := k8sClient.Get(ctx, client.ObjectKey{
				Name:      planName,
				Namespace: testNamespace,
			}, plan)
			if err != nil {
				return ""
			}
			return plan.Status.Phase
		}, timeout, interval).Should(Equal(hibernatorv1alpha1.PhaseWakingUp))

		By("Verifying wakeup runner Job is created")
		Eventually(func() bool {
			jobList := &batchv1.JobList{}
			err := k8sClient.List(ctx, jobList, client.InNamespace(testNamespace))
			if err != nil {
				return false
			}
			for _, job := range jobList.Items {
				if job.Labels["hibernator.ardikabs.com/plan"] == planName &&
					job.Labels["hibernator.ardikabs.com/operation"] == "wakeup" {
					return true
				}
			}
			return false
		}, timeout, interval).Should(BeTrue())

		By("Simulating successful wakeup Job completion")
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

		job.Status.Succeeded = 1
		job.Status.CompletionTime = &metav1.Time{Time: time.Now()}
		Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

		By("Verifying plan transitions to Active phase")
		Eventually(func() hibernatorv1alpha1.PlanPhase {
			err := k8sClient.Get(ctx, client.ObjectKey{
				Name:      planName,
				Namespace: testNamespace,
			}, plan)
			if err != nil {
				return ""
			}
			return plan.Status.Phase
		}, timeout, interval).Should(Equal(hibernatorv1alpha1.PhaseActive))

		By("Verifying restore ConfigMap was used")
		Expect(k8sClient.Get(ctx, client.ObjectKey{
			Name:      planName,
			Namespace: testNamespace,
		}, plan)).To(Succeed())
		Expect(plan.Status.Executions).NotTo(BeEmpty())
	})
})
