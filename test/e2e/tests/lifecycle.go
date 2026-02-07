//go:build e2e

package tests

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
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
	"github.com/ardikabs/hibernator/test/e2e/testutil"
)

var _ = Describe("Lifecycle E2E", func() {
	var (
		planName      string
		plan          *hibernatorv1alpha1.HibernatePlan
		cloudProvider *hibernatorv1alpha1.CloudProvider

		restoreCM corev1.ConfigMap
	)

	BeforeEach(func() {
		planName = "lifecycle-test"

		By("Creating mock CloudProvider")
		cloudProvider = &hibernatorv1alpha1.CloudProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "global-aws",
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.CloudProviderSpec{
				Type: hibernatorv1alpha1.CloudProviderAWS,
				AWS: &hibernatorv1alpha1.AWSConfig{
					AccountId: "123456789012",
					Region:    "us-east-1",
					Auth: hibernatorv1alpha1.AWSAuth{
						ServiceAccount: &hibernatorv1alpha1.ServiceAccountAuth{},
					},
				},
			},
		}
		if err := k8sClient.Create(ctx, cloudProvider); err != nil && !errors.IsAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}
	})

	AfterEach(func() {
		By("Cleaning up resources")
		if !restoreCM.CreationTimestamp.IsZero() {
			_ = k8sClient.Delete(ctx, &restoreCM)
			Eventually(func() bool {
				return errors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(&restoreCM), &restoreCM))
			}, 10*time.Second, time.Second).Should(BeTrue())
		}

		if plan != nil {
			_ = k8sClient.Delete(ctx, plan)
			// Wait for deletion to prevent teardown race conditions
			Eventually(func() bool {
				return errors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan))
			}, 10*time.Second, time.Second).Should(BeTrue())
		}
		if cloudProvider != nil {
			_ = k8sClient.Delete(ctx, cloudProvider)
			Eventually(func() bool {
				return errors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(cloudProvider), cloudProvider))
			}, 10*time.Second, time.Second).Should(BeTrue())
		}
	})

	It("Should successfully execute the golden path: Active -> Hibernated -> WakingUp -> Active", func() {
		// 1. Setup: Monday 08:00 UTC (On-Hours)
		baseTime := time.Date(2026, 2, 9, 8, 0, 0, 0, time.UTC)
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with 20:00-06:00 hibernation window. Clock set to Monday 08:00")
		plan, _ = testutil.NewHibernatePlanBuilder(planName, testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "database",
				Type: "rds",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "global-aws",
				},
			}).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Verifying plan initializes to Active phase")
		Eventually(func() hibernatorv1alpha1.PlanPhase {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			return plan.Status.Phase
		}, 10*time.Second, time.Second).Should(Equal(hibernatorv1alpha1.PhaseActive))

		// Verify execution ledger is NOT yet initialized (only happens during operation)
		Expect(plan.Status.Executions).To(BeEmpty())

		// 2. Transition to Hibernation
		By("Advancing time to hibernation window (20:01)")
		fakeClock.SetTime(time.Date(2026, 2, 9, 20, 1, 0, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan transitions to Hibernating")
		Eventually(func() hibernatorv1alpha1.PlanPhase {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			return plan.Status.Phase
		}, 10*time.Second, time.Second).Should(Equal(hibernatorv1alpha1.PhaseHibernating))

		// Verify execution ledger is now initialized
		Expect(plan.Status.Executions).To(HaveLen(1))
		Expect(plan.Status.Executions[0].Target).To(Equal("rds/database"))
		Expect(plan.Status.Executions[0].State).To(Equal(hibernatorv1alpha1.StatePending))

		By("Verifying runner Job creation")
		var hibernationJob batchv1.Job
		Eventually(func() bool {
			var jobs batchv1.JobList
			_ = k8sClient.List(ctx, &jobs, client.InNamespace(testNamespace), client.MatchingLabels{
				wellknown.LabelPlan:      plan.Name,
				wellknown.LabelOperation: "shutdown",
			})
			if len(jobs.Items) > 0 {
				hibernationJob = jobs.Items[0]
				return true
			}
			return false
		}, 10*time.Second, time.Second).Should(BeTrue())

		By("Simulating Job running")
		hibernationJob.Status.Active = 1
		hibernationJob.Status.StartTime = &metav1.Time{Time: fakeClock.Now().Add(-5 * time.Minute)}
		Expect(k8sClient.Status().Update(ctx, &hibernationJob)).To(Succeed())

		By("Simulating successful Job completion")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(&hibernationJob), &hibernationJob)).To(Succeed())
		hibernationJob.Status.Succeeded = 1
		hibernationJob.Status.Active = 0
		hibernationJob.Status.CompletionTime = &metav1.Time{Time: fakeClock.Now()}
		hibernationJob.Status.Conditions = []batchv1.JobCondition{
			{
				Type:               batchv1.JobSuccessCriteriaMet,
				Status:             corev1.ConditionTrue,
				LastTransitionTime: metav1.Time{Time: fakeClock.Now().Add(-1 * time.Millisecond)},
				LastProbeTime:      metav1.Time{Time: fakeClock.Now().Add(-1 * time.Millisecond)},
			},
			{
				Type:               batchv1.JobComplete,
				Status:             corev1.ConditionTrue,
				LastTransitionTime: metav1.Time{Time: fakeClock.Now().Add(time.Millisecond)},
				LastProbeTime:      metav1.Time{Time: fakeClock.Now().Add(time.Millisecond)},
			},
		}
		Expect(k8sClient.Status().Update(ctx, &hibernationJob)).To(Succeed())

		By("Verifying plan transitions to Hibernated and saves restore data")
		Eventually(func() bool {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			if len(plan.Status.Executions) == 0 {
				return false
			}

			return plan.Status.Phase == hibernatorv1alpha1.PhaseHibernated &&
				plan.Status.Executions[0].State == hibernatorv1alpha1.StateCompleted &&
				plan.Status.Executions[0].RestoreConfigMapRef != ""
		}, 10*time.Second, time.Second).Should(BeTrue())

		// Verify ConfigMap exists
		cmKey, _ := k8sutil.ObjectKeyFromString(plan.Status.Executions[0].RestoreConfigMapRef)
		Expect(k8sClient.Get(ctx, cmKey, &restoreCM)).To(Succeed())
		Expect(restoreManager.Save(ctx, plan.Namespace, plan.Name, plan.Spec.Targets[0].Name, &restore.Data{
			Target: plan.Spec.Targets[0].Name,
		})).To(Succeed())

		// 4. Transition to Wakeup
		By("Advancing time to wakeup window (Tuesday 06:01)")
		fakeClock.SetTime(time.Date(2026, 2, 10, 6, 1, 0, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan transitions to WakingUp")
		Eventually(func() hibernatorv1alpha1.PlanPhase {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			return plan.Status.Phase
		}, 10*time.Second, time.Second).Should(Equal(hibernatorv1alpha1.PhaseWakingUp))

		// 5. Complete Wakeup
		By("Verifying wakeup Job creation")
		var wakeupJob batchv1.Job
		Eventually(func() bool {
			var jobs batchv1.JobList
			_ = k8sClient.List(ctx, &jobs, client.InNamespace(testNamespace), client.MatchingLabels{
				wellknown.LabelPlan:      plan.Name,
				wellknown.LabelOperation: "wakeup",
			})
			if len(jobs.Items) > 0 {
				wakeupJob = jobs.Items[0]
				return true
			}
			return false
		}, 10*time.Second, time.Second).Should(BeTrue())

		By("Simulating successful wakeup Job completion")
		Eventually(func() bool {
			var job batchv1.Job
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(&wakeupJob), &job)

			job.Status.Succeeded = 1
			job.Status.StartTime = &metav1.Time{Time: fakeClock.Now().Add(-5 * time.Minute)}
			job.Status.CompletionTime = &metav1.Time{Time: fakeClock.Now()}
			job.Status.Conditions = []batchv1.JobCondition{
				{
					Type:               batchv1.JobSuccessCriteriaMet,
					Status:             corev1.ConditionTrue,
					LastTransitionTime: metav1.Time{Time: fakeClock.Now().Add(-2 * time.Millisecond)},
					LastProbeTime:      metav1.Time{Time: fakeClock.Now().Add(-2 * time.Millisecond)},
				},
				{
					Type:               batchv1.JobComplete,
					Status:             corev1.ConditionTrue,
					LastTransitionTime: metav1.Time{Time: fakeClock.Now().Add(-1 * time.Millisecond)},
					LastProbeTime:      metav1.Time{Time: fakeClock.Now().Add(-1 * time.Millisecond)},
				},
			}
			if err := k8sClient.Status().Update(ctx, &job); err != nil {
				return false
			}

			if job.Status.CompletionTime.IsZero() {
				return false
			}

			return true
		}).WithTimeout(10 * time.Second).Should(BeTrueBecause("wakeup job must be in complete state"))

		By("Verifying plan returns to Active phase")
		Eventually(func() hibernatorv1alpha1.PlanPhase {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			return plan.Status.Phase
		}, 10*time.Second, time.Second).Should(Equal(hibernatorv1alpha1.PhaseActive))
	})
})
