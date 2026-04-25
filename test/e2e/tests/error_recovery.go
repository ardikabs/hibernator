//go:build e2e

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

package tests

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/ardikabs/hibernator/test/e2e/testutil"
)

var _ = Describe("Error Recovery E2E", func() {
	var (
		plan          *hibernatorv1alpha1.HibernatePlan
		cloudProvider *hibernatorv1alpha1.CloudProvider
	)

	BeforeEach(func() {
		By("Creating mock CloudProvider")
		cloudProvider = &hibernatorv1alpha1.CloudProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "recovery-aws",
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
		testutil.EnsureDeleted(ctx, k8sClient, plan)
		testutil.EnsureDeleted(ctx, k8sClient, cloudProvider)
	})

	It("AutoRetry: should enter PhaseError when a runner Job fails and auto-retry creates a new Job", func() {
		// 1. Setup: Monday 20:01:10 UTC (off-hours, plan triggers shutdown)
		baseTime := time.Date(2026, 5, 4, 20, 1, 10, 0, time.UTC) // Monday
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with 20:00-06:00 hibernation window")
		plan, _ = testutil.NewHibernatePlanBuilder("error-autoretry-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "database",
				Type: "noop",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "recovery-aws",
				},
			}).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Verifying plan starts Hibernating (off-hours)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Waiting for shutdown Job and simulating failure")
		hibernationJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "database")
		testutil.SimulateJobFailure(ctx, k8sClient, hibernationJob, fakeClock.Now())

		By("Verifying ErrorMessage is set in plan status")
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			return plan.Status.ErrorMessage
		}, testutil.DefaultTimeout, testutil.DefaultInterval).ShouldNot(BeEmpty())

		By("Verifying plan auto-retries and transitions back to Hibernating (RetryCount=0, under limit)")
		// On first error entry, RetryCount=0 < maxRetries=3, so plan immediately auto-retries.
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Verifying a new shutdown Job was created for the retry")
		testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "database")
	})

	It("RetryNow: should allow manual retry via retry-now annotation when automatic retries are exhausted", func() {
		// Time set to Monday 20:01:10 UTC (off-hours)
		baseTime := time.Date(2026, 5, 11, 20, 1, 10, 0, time.UTC) // Monday
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan")
		plan, _ = testutil.NewHibernatePlanBuilder("error-retrynow-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithBehavior(hibernatorv1alpha1.Behavior{
				Mode:    hibernatorv1alpha1.BehaviorStrict,
				Retries: ptr.To(int32(1)),
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "database",
				Type: "noop",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "recovery-aws",
				},
			}).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Simulating initial job failure to trigger retry attempts")
		hibernationJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "database")
		testutil.SimulateJobFailure(ctx, k8sClient, hibernationJob, fakeClock.Now())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			return plan.Status.ErrorMessage
		}, testutil.DefaultTimeout, testutil.DefaultInterval).ShouldNot(BeEmpty())

		By("Simulating failure on retry job to enter PhaseError")
		retryJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "database")
		testutil.SimulateJobFailure(ctx, k8sClient, retryJob, fakeClock.Now())

		By("Verifying plan stays in PhaseError (no more auto-retries available)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseError)
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseError, 2*time.Second)

		By("Applying retry-now=true annotation to manually unblock the plan")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		orig := plan.DeepCopy()
		if plan.Annotations == nil {
			plan.Annotations = make(map[string]string)
		}
		plan.Annotations[wellknown.AnnotationRetryNow] = "true"
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())

		By("Verifying plan exits PhaseError and transitions to Hibernating (off-hours schedule)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Verifying retry-now annotation was cleared by the controller")
		Eventually(func() bool {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			_, hasRetryNow := plan.Annotations[wellknown.AnnotationRetryNow]
			return !hasRetryNow
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(BeTrue(), "retry-now annotation should be cleared after processing")

		fakeClock.SetTime(time.Date(2026, 5, 11, 20, 10, 10, 0, time.UTC))
		var jobList batchv1.JobList
		Expect(k8sClient.List(ctx, &jobList, client.InNamespace(testNamespace), client.MatchingLabels{
			wellknown.LabelPlan:      plan.Name,
			wellknown.LabelOperation: string(plan.Status.CurrentOperation),
			wellknown.LabelCycleID:   plan.Status.CurrentCycleID,
		})).Should(Succeed())
		Expect(jobList.Items).To(HaveLen(3), "total number of jobs after include retry-now should be 3")
	})

	It("FullCycle: should complete full error-recovery cycle: failure → retry → success → Active", func() {
		// 1. Setup: Monday 20:01:10 UTC (off-hours)
		baseTime := time.Date(2026, 5, 18, 20, 1, 10, 0, time.UTC) // Monday
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan")
		plan, _ = testutil.NewHibernatePlanBuilder("error-fullcycle-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "database",
				Type: "noop",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "recovery-aws",
				},
			}).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Simulating job failure — plan enters PhaseError")
		hibernationJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "database")
		testutil.SimulateJobFailure(ctx, k8sClient, hibernationJob, fakeClock.Now())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			return plan.Status.ErrorMessage
		}, testutil.DefaultTimeout, testutil.DefaultInterval).ShouldNot(BeEmpty())

		By("Waiting for plan to auto-retry — new Job created as part of auto-recovery")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)
		retryJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "database")

		By("Simulating retry Job success — plan should complete shutdown")
		testutil.SimulateJobSuccess(ctx, k8sClient, retryJob, fakeClock.Now())

		By("Verifying plan transitions to Hibernated after successful retry")
		testutil.EventuallyRestoreDataSaved(ctx, k8sClient, plan, 0)

		// Store restore data for wakeup
		Expect(restoreManager.Save(ctx, plan.Namespace, plan.Name, "database", &restore.Data{
			Target: "database",
		})).To(Succeed())

		By("Advancing clock to wakeup window (Tuesday 07:00)")
		fakeClock.SetTime(time.Date(2026, 5, 19, 7, 0, 0, 0, time.UTC))

		By("Verifying plan transitions to WakingUp and completes cycle")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)
		wakeupJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, wakeupJob, fakeClock.Now())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)
	})

	It("PartialFailure: should handle multi-target partial failure: one target fails while others succeed", func() {
		// Tests BestEffort behavior with multiple targets where partial failure occurs.
		// Time Monday 20:01:10 UTC (off-hours)
		baseTime := time.Date(2026, 5, 25, 20, 1, 10, 0, time.UTC) // Monday
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with two parallel targets")
		plan, _ = testutil.NewHibernatePlanBuilder("error-partial-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type:           hibernatorv1alpha1.StrategyParallel,
				MaxConcurrency: ptr.To[int32](2),
			}).
			WithBehavior(hibernatorv1alpha1.Behavior{
				Mode:    hibernatorv1alpha1.BehaviorBestEffort,
				Retries: ptr.To(int32(1)),
			}).
			WithTarget(
				hibernatorv1alpha1.Target{
					Name: "app",
					Type: "noop",
					ConnectorRef: hibernatorv1alpha1.ConnectorRef{
						Kind: "CloudProvider",
						Name: "recovery-aws",
					},
				},
				hibernatorv1alpha1.Target{
					Name: "database",
					Type: "noop",
					ConnectorRef: hibernatorv1alpha1.ConnectorRef{
						Kind: "CloudProvider",
						Name: "recovery-aws",
					},
				},
			).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Waiting for both parallel shutdown Jobs")
		jobs := testutil.EventuallyMultiJobsCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "app", "database")

		By("Simulating success for 'app' and failure for 'database'")
		for _, job := range jobs {
			switch job.Labels[wellknown.LabelTarget] {
			case "app":
				testutil.SimulateJobSuccess(ctx, k8sClient, job, fakeClock.Now())
			case "database":
				testutil.SimulateJobFailure(ctx, k8sClient, job, fakeClock.Now())
			default:
				Fail("unexpected target label on job: " + job.Labels[wellknown.LabelTarget])
			}
		}

		By("Verifying plan enters PhaseHibernated after partial failure (BestEffort allows completion despite some failures)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernated)

		By("Ensure that the error message is properly populated in the corresponding target to describe the failure.")
		Eventually(func() bool {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)

			for _, exec := range plan.Status.Executions {
				if exec.State == hibernatorv1alpha1.StateFailed {
					return true
				}
			}

			return false
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(BeTrue())
	})

	It("ZeroRetries: should immediately enter PhaseError on first job failure when Retries=0", func() {
		// Retries=0 means no auto-retry is permitted; any job failure must transition
		// the plan directly to PhaseError without spawning a new Job.
		baseTime := time.Date(2026, 6, 8, 20, 1, 10, 0, time.UTC) // Monday, off-hours
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with Retries=0")
		plan, _ = testutil.NewHibernatePlanBuilder("error-zeroretries-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithBehavior(hibernatorv1alpha1.Behavior{
				Mode:    hibernatorv1alpha1.BehaviorStrict,
				Retries: ptr.To(int32(0)),
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "database",
				Type: "noop",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "recovery-aws",
				},
			}).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Verifying plan starts Hibernating (off-hours)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Simulating shutdown Job failure")
		hibernationJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "database")
		testutil.SimulateJobFailure(ctx, k8sClient, hibernationJob, fakeClock.Now())

		By("Verifying plan immediately enters PhaseError — no retry job should be created")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseError)
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseError, 2*time.Second)

		By("Verifying RetryCount is still 0 (no retry was attempted)")
		Eventually(func() int32 {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			return plan.Status.RetryCount
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Equal(int32(0)))

		By("Verifying ErrorMessage is populated")
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			return plan.Status.ErrorMessage
		}, testutil.DefaultTimeout, testutil.DefaultInterval).ShouldNot(BeEmpty())
	})

	It("WakeupError: should enter PhaseError when wakeup Job fails and allow recovery via retry-now", func() {
		// 1. Setup: Monday 20:01:10 UTC (off-hours) — begin by establishing a Hibernated state.
		baseTime := time.Date(2026, 6, 1, 20, 1, 10, 0, time.UTC) // Monday, off-hours
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with Retries=1 to exhaust auto-retries quickly during wakeup failure")
		plan, _ = testutil.NewHibernatePlanBuilder("wakeup-error-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithBehavior(hibernatorv1alpha1.Behavior{
				Mode:    hibernatorv1alpha1.BehaviorStrict,
				Retries: ptr.To(int32(1)),
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "database",
				Type: "noop",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "recovery-aws",
				},
			}).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Simulating successful shutdown to advance plan to Hibernated")
		hibernationJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, hibernationJob, fakeClock.Now())
		testutil.EventuallyRestoreDataSaved(ctx, k8sClient, plan, 0)
		Expect(restoreManager.Save(ctx, plan.Namespace, plan.Name, "database", &restore.Data{
			Target: "database",
		})).To(Succeed())

		By("Advancing clock to wakeup window (Tuesday 07:00)")
		fakeClock.SetTime(time.Date(2026, 6, 2, 7, 0, 0, 0, time.UTC))

		By("Verifying plan transitions to WakingUp")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)

		By("Simulating initial wakeup Job failure")
		wakeupJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "database")
		testutil.SimulateJobFailure(ctx, k8sClient, wakeupJob, fakeClock.Now())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			return plan.Status.ErrorMessage
		}, testutil.DefaultTimeout, testutil.DefaultInterval).ShouldNot(BeEmpty())

		By("Waiting for auto-retry wakeup Job (Retries=1 allows one retry) and simulating failure again")
		retryWakeupJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "database")
		testutil.SimulateJobFailure(ctx, k8sClient, retryWakeupJob, fakeClock.Now())

		By("Verifying plan enters PhaseError (all retries exhausted)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseError)
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseError, 2*time.Second)

		By("Applying retry-now=true annotation to manually trigger wakeup recovery")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		orig := plan.DeepCopy()
		if plan.Annotations == nil {
			plan.Annotations = make(map[string]string)
		}
		plan.Annotations[wellknown.AnnotationRetryNow] = "true"
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())

		By("Verifying plan exits PhaseError and transitions back to WakingUp (still on-hours)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)

		By("Verifying retry-now annotation was cleared by the controller")
		Eventually(func() bool {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			_, hasRetryNow := plan.Annotations[wellknown.AnnotationRetryNow]
			return !hasRetryNow
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(BeTrue(), "retry-now annotation should be cleared after processing")

		By("Verifying a new wakeup Job was created for the recovery attempt")
		testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "database")
	})
})
