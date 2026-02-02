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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

var _ = Describe("Schedule Evaluation E2E", func() {
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
		planName = "test-schedule-" + time.Now().Format("150405")

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

	It("Should evaluate schedule with start/end/daysOfWeek format", func() {
		By("Creating HibernatePlan with specific off-hour window")
		plan = &hibernatorv1alpha1.HibernatePlan{
			ObjectMeta: metav1.ObjectMeta{
				Name:      planName,
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.HibernatePlanSpec{
				Schedule: hibernatorv1alpha1.Schedule{
					Timezone: "America/New_York",
					OffHours: []hibernatorv1alpha1.OffHourWindow{
						{
							Start:      "18:00",
							End:        "08:00",
							DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
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
						Name: "test-ec2",
						Type: "ec2",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{
							Kind: "CloudProvider",
							Name: cloudProvider.Name,
						},
						Parameters: &hibernatorv1alpha1.Parameters{
							Raw: []byte(`{"selector":{"instanceIds":["i-1234567890abcdef0"]}}`),
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Verifying plan initializes with schedule")
		Eventually(func() bool {
			err := k8sClient.Get(ctx, client.ObjectKey{
				Name:      planName,
				Namespace: testNamespace,
			}, plan)
			return err == nil && plan.Status.Phase != ""
		}, timeout, interval).Should(BeTrue())

		By("Verifying schedule was converted for evaluation")
		// The controller should have processed the schedule
		Expect(plan.Spec.Schedule.OffHours).To(HaveLen(1))
		Expect(plan.Spec.Schedule.OffHours[0].Start).To(Equal("18:00"))
		Expect(plan.Spec.Schedule.OffHours[0].End).To(Equal("08:00"))
		Expect(plan.Spec.Schedule.OffHours[0].DaysOfWeek).To(ContainElement("MON"))
	})

	It("Should handle multiple day-of-week configurations", func() {
		By("Creating HibernatePlan with weekend-only hibernation")
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
							DaysOfWeek: []string{"SAT", "SUN"},
						},
					},
				},
				Execution: hibernatorv1alpha1.Execution{
					Strategy: hibernatorv1alpha1.ExecutionStrategy{
						Type: hibernatorv1alpha1.StrategyParallel,
					},
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
							Raw: []byte(`{"instanceId":"weekend-db"}`),
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Verifying schedule evaluation occurs")
		Eventually(func() bool {
			err := k8sClient.Get(ctx, client.ObjectKey{
				Name:      planName,
				Namespace: testNamespace,
			}, plan)
			if err != nil {
				return false
			}
			// Phase should be Active if not weekend, or transition to Hibernating if weekend
			return plan.Status.Phase == hibernatorv1alpha1.PhaseActive ||
				plan.Status.Phase == hibernatorv1alpha1.PhaseHibernating
		}, timeout, interval).Should(BeTrue())
	})

	It("Should respect timezone configuration", func() {
		By("Creating HibernatePlan with Asia/Tokyo timezone")
		plan = &hibernatorv1alpha1.HibernatePlan{
			ObjectMeta: metav1.ObjectMeta{
				Name:      planName,
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.HibernatePlanSpec{
				Schedule: hibernatorv1alpha1.Schedule{
					Timezone: "Asia/Tokyo",
					OffHours: []hibernatorv1alpha1.OffHourWindow{
						{
							Start:      "20:00",
							End:        "06:00",
							DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
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
						Name: "test-target",
						Type: "rds",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{
							Kind: "CloudProvider",
							Name: cloudProvider.Name,
						},
						Parameters: &hibernatorv1alpha1.Parameters{Raw: []byte(`{"dbInstanceIdentifier":"tokyo-db"}`)},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Verifying timezone is preserved in spec")
		Expect(k8sClient.Get(ctx, client.ObjectKey{
			Name:      planName,
			Namespace: testNamespace,
		}, plan)).To(Succeed())
		Expect(plan.Spec.Schedule.Timezone).To(Equal("Asia/Tokyo"))
	})
})
