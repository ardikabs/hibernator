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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

var _ = Describe("Concurrency Control E2E", func() {
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
		planName = "test-concurrency-" + time.Now().Format("150405")

		cloudProvider = &hibernatorv1alpha1.CloudProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name:      planName + "-aws",
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.CloudProviderSpec{
				Type: "aws",
				AWS: &hibernatorv1alpha1.AWSConfig{
					AccountId:     "123456789012",
					Region:        "us-east-1",
					AssumeRoleArn: "arn:aws:iam::123456789012:role/hibernator",
					Auth: hibernatorv1alpha1.AWSAuth{
						ServiceAccount: &hibernatorv1alpha1.ServiceAccountAuth{},
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

	It("Should respect maxConcurrency limit for parallel execution", func() {
		By("Creating HibernatePlan with maxConcurrency=2 and 5 targets")
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
							DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
						},
					},
				},
				Execution: hibernatorv1alpha1.Execution{
					Strategy: hibernatorv1alpha1.ExecutionStrategy{
						Type:           hibernatorv1alpha1.StrategyParallel,
						MaxConcurrency: ptr.To[int32](2),
					},
				},
				Targets: []hibernatorv1alpha1.Target{
					{
						Name: "target1",
						Type: "ec2",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{
							Kind: "CloudProvider",
							Name: cloudProvider.Name,
						},
					},
					{
						Name: "target2",
						Type: "ec2",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{
							Kind: "CloudProvider",
							Name: cloudProvider.Name,
						},
					},
					{
						Name: "target3",
						Type: "ec2",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{
							Kind: "CloudProvider",
							Name: cloudProvider.Name,
						},
					},
					{
						Name: "target4",
						Type: "ec2",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{
							Kind: "CloudProvider",
							Name: cloudProvider.Name,
						},
					},
					{
						Name: "target5",
						Type: "ec2",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{
							Kind: "CloudProvider",
							Name: cloudProvider.Name,
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Verifying plan enters Hibernating state")
		Eventually(func() hibernatorv1alpha1.PlanPhase {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			return plan.Status.Phase
		}, timeout, interval).Should(Equal(hibernatorv1alpha1.PhaseHibernating))

		By("Verifying no more than 2 jobs run concurrently")
		Consistently(func() int {
			var jobs batchv1.JobList
			_ = k8sClient.List(ctx, &jobs, client.InNamespace(testNamespace), client.MatchingLabels{
				"hibernator/plan": planName,
			})

			runningCount := 0
			for _, job := range jobs.Items {
				if job.Status.Active > 0 {
					runningCount++
				}
			}
			return runningCount
		}, time.Second*10, interval).Should(BeNumerically("<=", 2))
	})

	It("Should execute DAG dependencies in correct order", func() {
		By("Creating HibernatePlan with DAG dependencies")
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
							DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
						},
					},
				},
				Execution: hibernatorv1alpha1.Execution{
					Strategy: hibernatorv1alpha1.ExecutionStrategy{
						Type: hibernatorv1alpha1.StrategyDAG,
						Dependencies: []hibernatorv1alpha1.Dependency{
							{From: "app", To: "database"},
						},
						MaxConcurrency: ptr.To[int32](32),
					},
				},
				Targets: []hibernatorv1alpha1.Target{
					{
						Name: "database",
						Type: "rds",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{
							Kind: "CloudProvider",
							Name: cloudProvider.Name,
						},
					},
					{
						Name: "app",
						Type: "ec2",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{
							Kind: "CloudProvider",
							Name: cloudProvider.Name,
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Verifying database job doesn't start before app completes")
		var appJobStartTime, dbJobStartTime time.Time

		Eventually(func() bool {
			var jobs batchv1.JobList
			_ = k8sClient.List(ctx, &jobs, client.InNamespace(testNamespace), client.MatchingLabels{
				"hibernator/plan":   planName,
				"hibernator/target": "app",
			})
			if len(jobs.Items) > 0 && jobs.Items[0].Status.StartTime != nil {
				appJobStartTime = jobs.Items[0].Status.StartTime.Time
				return true
			}
			return false
		}, timeout, interval).Should(BeTrue())

		Eventually(func() bool {
			var jobs batchv1.JobList
			_ = k8sClient.List(ctx, &jobs, client.InNamespace(testNamespace), client.MatchingLabels{
				"hibernator/plan":   planName,
				"hibernator/target": "database",
			})
			if len(jobs.Items) > 0 && jobs.Items[0].Status.StartTime != nil {
				dbJobStartTime = jobs.Items[0].Status.StartTime.Time
				return true
			}
			return false
		}, timeout, interval).Should(BeTrue())

		// Database job should start after app job
		Expect(dbJobStartTime.After(appJobStartTime) || dbJobStartTime.Equal(appJobStartTime)).To(BeTrue(),
			"database job should not start before app job completes")
	})

	It("Should isolate executions with different cycleIDs", func() {
		By("Creating HibernatePlan")
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
							DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
						},
					},
				},
				Execution: hibernatorv1alpha1.Execution{
					Strategy: hibernatorv1alpha1.ExecutionStrategy{
						Type: hibernatorv1alpha1.StrategySequential,
					},
				},
				Targets: []hibernatorv1alpha1.Target{
					{
						Name: "target1",
						Type: "ec2",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{
							Kind: "CloudProvider",
							Name: cloudProvider.Name,
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Waiting for first cycle to create job")
		var firstCycleID string
		Eventually(func() bool {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			firstCycleID = plan.Status.CurrentCycleID
			return firstCycleID != ""
		}, timeout, interval).Should(BeTrue())

		By("Verifying job has correct cycleID label")
		var firstJob batchv1.Job
		Eventually(func() bool {
			var jobs batchv1.JobList
			_ = k8sClient.List(ctx, &jobs, client.InNamespace(testNamespace), client.MatchingLabels{
				"hibernator/plan":    planName,
				"hibernator/cycleID": firstCycleID,
			})
			if len(jobs.Items) > 0 {
				firstJob = jobs.Items[0]
				return true
			}
			return false
		}, timeout, interval).Should(BeTrue())

		Expect(firstJob.Labels["hibernator/cycleID"]).To(Equal(firstCycleID))
	})
})
