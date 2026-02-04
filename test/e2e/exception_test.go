package e2e

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

var _ = Describe("ScheduleException E2E", func() {
	const (
		timeout  = time.Minute * 2
		interval = time.Second
	)

	var (
		planName      string
		plan          *hibernatorv1alpha1.HibernatePlan
		cloudProvider *hibernatorv1alpha1.CloudProvider
		exception     *hibernatorv1alpha1.ScheduleException
	)

	BeforeEach(func() {
		planName = "test-exception-" + time.Now().Format("150405")

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
		if exception != nil {
			_ = k8sClient.Delete(ctx, exception)
		}
		if plan != nil {
			_ = k8sClient.Delete(ctx, plan)
		}
		if cloudProvider != nil {
			_ = k8sClient.Delete(ctx, cloudProvider)
		}
	})

	It("Should apply extend exception to add hibernation windows", func() {
		By("Creating HibernatePlan with weekday schedule")
		plan = &hibernatorv1alpha1.HibernatePlan{
			ObjectMeta: metav1.ObjectMeta{
				Name:      planName,
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.HibernatePlanSpec{
				Schedule: hibernatorv1alpha1.Schedule{
					Timezone: "UTC",
					OffHours: []hibernatorv1alpha1.OffHourWindow{{
						Start:      "20:00",
						End:        "06:00",
						DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
					}},
				},
				Execution: hibernatorv1alpha1.Execution{
					Strategy: hibernatorv1alpha1.ExecutionStrategy{
						Type: hibernatorv1alpha1.StrategySequential,
					},
				},
				Targets: []hibernatorv1alpha1.Target{{
					Name: "target1",
					Type: "ec2",
					ConnectorRef: hibernatorv1alpha1.ConnectorRef{
						Kind: "CloudProvider",
						Name: cloudProvider.Name,
					},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Creating extend exception for weekend windows")
		now := time.Now()
		exception = &hibernatorv1alpha1.ScheduleException{
			ObjectMeta: metav1.ObjectMeta{
				Name:      planName + "-extend",
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
				PlanRef: hibernatorv1alpha1.PlanReference{
					Name:      planName,
					Namespace: testNamespace,
				},
				Type:       hibernatorv1alpha1.ExceptionExtend,
				ValidFrom:  metav1.Time{Time: now},
				ValidUntil: metav1.Time{Time: now.Add(24 * time.Hour)},
				Windows: []hibernatorv1alpha1.OffHourWindow{{
					Start:      "06:00",
					End:        "11:00",
					DaysOfWeek: []string{"SAT", "SUN"},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, exception)).To(Succeed())

		By("Waiting for exception to become active")
		Eventually(func() hibernatorv1alpha1.ExceptionState {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(exception), exception)
			return exception.Status.State
		}, timeout, interval).Should(Equal(hibernatorv1alpha1.ExceptionStateActive))

		By("Verifying plan status includes active exception")
		Eventually(func() int {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			return len(plan.Status.ActiveExceptions)
		}, timeout, interval).Should(BeNumerically(">", 0))

		Expect(plan.Status.ActiveExceptions[0].Name).To(Equal(exception.Name))
		Expect(plan.Status.ActiveExceptions[0].Type).To(Equal(hibernatorv1alpha1.ExceptionExtend))
	})

	It("Should apply suspend exception to prevent hibernation", func() {
		By("Creating HibernatePlan")
		plan = &hibernatorv1alpha1.HibernatePlan{
			ObjectMeta: metav1.ObjectMeta{
				Name:      planName,
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.HibernatePlanSpec{
				Schedule: hibernatorv1alpha1.Schedule{
					Timezone: "UTC",
					OffHours: []hibernatorv1alpha1.OffHourWindow{{
						Start:      "20:00",
						End:        "06:00",
						DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
					}},
				},
				Execution: hibernatorv1alpha1.Execution{
					Strategy: hibernatorv1alpha1.ExecutionStrategy{
						Type: hibernatorv1alpha1.StrategySequential,
					},
				},
				Targets: []hibernatorv1alpha1.Target{{
					Name: "target1",
					Type: "ec2",
					ConnectorRef: hibernatorv1alpha1.ConnectorRef{
						Kind: "CloudProvider",
						Name: cloudProvider.Name,
					},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Creating suspend exception with lead time")
		now := time.Now()
		exception = &hibernatorv1alpha1.ScheduleException{
			ObjectMeta: metav1.ObjectMeta{
				Name:      planName + "-suspend",
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
				PlanRef: hibernatorv1alpha1.PlanReference{
					Name:      planName,
					Namespace: testNamespace,
				},
				Type:       hibernatorv1alpha1.ExceptionSuspend,
				ValidFrom:  metav1.Time{Time: now},
				ValidUntil: metav1.Time{Time: now.Add(24 * time.Hour)},
				LeadTime:   "1h",
				Windows: []hibernatorv1alpha1.OffHourWindow{{
					Start:      "21:00",
					End:        "02:00",
					DaysOfWeek: []string{"SAT"},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, exception)).To(Succeed())

		By("Waiting for exception to become active")
		Eventually(func() hibernatorv1alpha1.ExceptionState {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(exception), exception)
			return exception.Status.State
		}, timeout, interval).Should(Equal(hibernatorv1alpha1.ExceptionStateActive))

		By("Verifying lead time is recorded in status")
		Expect(exception.Spec.LeadTime).To(Equal("1h"))
	})

	It("Should replace schedule with replace exception", func() {
		By("Creating HibernatePlan")
		plan = &hibernatorv1alpha1.HibernatePlan{
			ObjectMeta: metav1.ObjectMeta{
				Name:      planName,
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.HibernatePlanSpec{
				Schedule: hibernatorv1alpha1.Schedule{
					Timezone: "UTC",
					OffHours: []hibernatorv1alpha1.OffHourWindow{{
						Start:      "20:00",
						End:        "06:00",
						DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
					}},
				},
				Execution: hibernatorv1alpha1.Execution{
					Strategy: hibernatorv1alpha1.ExecutionStrategy{
						Type: hibernatorv1alpha1.StrategySequential,
					},
				},
				Targets: []hibernatorv1alpha1.Target{{
					Name: "target1",
					Type: "ec2",
					ConnectorRef: hibernatorv1alpha1.ConnectorRef{
						Kind: "CloudProvider",
						Name: cloudProvider.Name,
					},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Creating replace exception for full hibernation")
		now := time.Now()
		exception = &hibernatorv1alpha1.ScheduleException{
			ObjectMeta: metav1.ObjectMeta{
				Name:      planName + "-replace",
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
				PlanRef: hibernatorv1alpha1.PlanReference{
					Name:      planName,
					Namespace: testNamespace,
				},
				Type:       hibernatorv1alpha1.ExceptionReplace,
				ValidFrom:  metav1.Time{Time: now},
				ValidUntil: metav1.Time{Time: now.Add(24 * time.Hour)},
				Windows: []hibernatorv1alpha1.OffHourWindow{{
					Start:      "00:00",
					End:        "23:59",
					DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, exception)).To(Succeed())

		By("Waiting for exception to become active")
		Eventually(func() hibernatorv1alpha1.ExceptionState {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(exception), exception)
			return exception.Status.State
		}, timeout, interval).Should(Equal(hibernatorv1alpha1.ExceptionStateActive))

		By("Verifying exception type is replace")
		Expect(exception.Spec.Type).To(Equal(hibernatorv1alpha1.ExceptionReplace))
	})

	It("Should enforce single active exception per plan", func() {
		By("Creating HibernatePlan")
		plan = &hibernatorv1alpha1.HibernatePlan{
			ObjectMeta: metav1.ObjectMeta{
				Name:      planName,
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.HibernatePlanSpec{
				Schedule: hibernatorv1alpha1.Schedule{
					Timezone: "UTC",
					OffHours: []hibernatorv1alpha1.OffHourWindow{{
						Start:      "20:00",
						End:        "06:00",
						DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
					}},
				},
				Execution: hibernatorv1alpha1.Execution{
					Strategy: hibernatorv1alpha1.ExecutionStrategy{
						Type: hibernatorv1alpha1.StrategySequential,
					},
				},
				Targets: []hibernatorv1alpha1.Target{{
					Name: "target1",
					Type: "ec2",
					ConnectorRef: hibernatorv1alpha1.ConnectorRef{
						Kind: "CloudProvider",
						Name: cloudProvider.Name,
					},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Creating first exception")
		now := time.Now()
		exception = &hibernatorv1alpha1.ScheduleException{
			ObjectMeta: metav1.ObjectMeta{
				Name:      planName + "-first",
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
				PlanRef: hibernatorv1alpha1.PlanReference{
					Name:      planName,
					Namespace: testNamespace,
				},
				Type:       hibernatorv1alpha1.ExceptionExtend,
				ValidFrom:  metav1.Time{Time: now},
				ValidUntil: metav1.Time{Time: now.Add(24 * time.Hour)},
				Windows: []hibernatorv1alpha1.OffHourWindow{{
					Start:      "12:00",
					End:        "13:00",
					DaysOfWeek: []string{"SAT"},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, exception)).To(Succeed())

		By("Waiting for first exception to become active")
		Eventually(func() hibernatorv1alpha1.ExceptionState {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(exception), exception)
			return exception.Status.State
		}, timeout, interval).Should(Equal(hibernatorv1alpha1.ExceptionStateActive))

		By("Attempting to create second exception should fail validation")
		secondException := &hibernatorv1alpha1.ScheduleException{
			ObjectMeta: metav1.ObjectMeta{
				Name:      planName + "-second",
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
				PlanRef: hibernatorv1alpha1.PlanReference{
					Name:      planName,
					Namespace: testNamespace,
				},
				Type:       hibernatorv1alpha1.ExceptionExtend,
				ValidFrom:  metav1.Time{Time: now},
				ValidUntil: metav1.Time{Time: now.Add(24 * time.Hour)},
				Windows: []hibernatorv1alpha1.OffHourWindow{{
					Start:      "14:00",
					End:        "15:00",
					DaysOfWeek: []string{"SUN"},
				}},
			},
		}
		err := k8sClient.Create(ctx, secondException)
		Expect(err).To(HaveOccurred(), "should reject second active exception")
	})
})
