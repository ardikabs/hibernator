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
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/ardikabs/hibernator/test/e2e/testutil"
)

var _ = Describe("Plan Suspension E2E", func() {
	var (
		plan          *hibernatorv1alpha1.HibernatePlan
		cloudProvider *hibernatorv1alpha1.CloudProvider
	)

	BeforeEach(func() {
		By("Creating mock CloudProvider")
		cloudProvider = &hibernatorv1alpha1.CloudProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "suspension-aws",
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

	It("SpecSuspend: should transition to PhaseSuspended when spec.suspend=true and resume to Active when spec.suspend=false", func() {
		// 1. Setup: Monday 08:00 UTC (on-hours, plan starts Active)
		baseTime := time.Date(2026, 4, 6, 8, 0, 0, 0, time.UTC) // Monday
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with 20:00-06:00 hibernation window")
		plan, _ = testutil.NewHibernatePlanBuilder("suspend-resume-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "database",
				Type: "rds",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "suspension-aws",
				},
			}).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Verifying plan initializes to Active phase")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Patching spec.suspend=true")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		orig := plan.DeepCopy()
		plan.Spec.Suspend = true
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())

		By("Verifying plan transitions to PhaseSuspended")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseSuspended)

		By("Patching spec.suspend=false to resume the plan")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		orig = plan.DeepCopy()
		plan.Spec.Suspend = false
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())

		By("Verifying plan resumes to Active phase (clock is 08:00 — on-hours)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)
	})

	It("SuspendUntil: should auto-resume plan when the deadline annotation passes", func() {
		// 1. Setup: Monday 08:00 UTC (on-hours, plan stays Active)
		baseTime := time.Date(2026, 4, 13, 8, 0, 0, 0, time.UTC) // Monday
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan at 20:00-06:00 window")
		plan, _ = testutil.NewHibernatePlanBuilder("suspend-until-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "database",
				Type: "rds",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "suspension-aws",
				},
			}).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		// Deadline set 2 hours from now (at 10:00 UTC)
		deadline := baseTime.Add(2 * time.Hour)

		By("Suspending plan with spec.suspend=true and AnnotationSuspendUntil annotation")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		orig := plan.DeepCopy()
		plan.Spec.Suspend = true
		if plan.Annotations == nil {
			plan.Annotations = make(map[string]string)
		}
		plan.Annotations[wellknown.AnnotationSuspendUntil] = deadline.Format(time.RFC3339)
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())

		By("Verifying plan transitions to PhaseSuspended")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseSuspended)

		By("Verifying suspend-until annotation is present on the plan")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		Expect(plan.Annotations).To(HaveKey(wellknown.AnnotationSuspendUntil))

		By("Advancing clock past the suspend deadline (10:05 UTC — on-hours)")
		fakeClock.SetTime(baseTime.Add(2*time.Hour + 5*time.Minute))
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan auto-resumes to Active phase after deadline")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Verifying suspend-until annotation was cleared after resume")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		Expect(plan.Annotations).NotTo(HaveKey(wellknown.AnnotationSuspendUntil))
	})

	It("ForceWakeup: should force wakeup when plan is suspended while Hibernated and resumes during on-hours", func() {
		// This tests the grace scenario: plan was hibernating when operator suspended it,
		// then operator resumes during on-hours — system should force wakeup.
		baseTime := time.Date(2026, 4, 20, 22, 0, 0, 0, time.UTC) // Monday 22:00 (off-hours)
		fakeClock.SetTime(baseTime)

		By("Creating plan with 20:00-06:00 hibernation window at 22:00 (off-hours)")
		plan, _ = testutil.NewHibernatePlanBuilder("suspend-force-wakeup", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "database",
				Type: "rds",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "suspension-aws",
				},
			}).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Waiting for plan to start Hibernating (22:00 is off-hours)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Simulating successful hibernation job")
		hibernationJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "shutdown", "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, hibernationJob, fakeClock.Now())
		testutil.EventuallyRestoreDataSaved(ctx, k8sClient, plan, 0)

		// Save actual restore data so shouldForceWakeUpOnResume can detect it
		Expect(restoreManager.Save(ctx, plan.Namespace, plan.Name, "database", &restore.Data{
			Target: "database",
		})).To(Succeed())

		By("Verifying plan reaches Hibernated phase")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernated)

		By("Suspending plan while it's hibernated (operator suspends infrastructure management)")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		orig := plan.DeepCopy()
		plan.Spec.Suspend = true
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())

		By("Verifying plan transitions to PhaseSuspended (was hibernated)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseSuspended)

		By("Verifying suspended-at-phase annotation records Hibernated")
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			return plan.Annotations[wellknown.AnnotationSuspendedAtPhase]
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Equal(string(hibernatorv1alpha1.PhaseHibernated)))

		By("Advancing clock to on-hours (09:00 next day) and resuming suspension")
		fakeClock.SetTime(time.Date(2026, 4, 21, 9, 0, 0, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		orig = plan.DeepCopy()
		plan.Spec.Suspend = false
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())

		By("Verifying plan force-wakes up and transitions to WakingUp (restore data present, on-hours)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)

		By("Simulating successful wakeup job")
		wakeupJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "wakeup", "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, wakeupJob, fakeClock.Now())

		By("Verifying plan returns to Active phase")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)
	})

	It("SuspendUntilExpiresOffHours: should transition to Hibernating (not Active) when suspend deadline expires while still in off-hours", func() {
		// The existing SuspendUntil test resumes at on-hours after the deadline.
		// This test validates the opposite: deadline expires while the clock is still inside
		// the hibernation window → plan must resume schedule evaluation → Hibernating, not Active.
		baseTime := time.Date(2026, 5, 4, 8, 0, 0, 0, time.UTC) // Monday 08:00 — on-hours
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with 20:00-06:00 hibernation window")
		plan, _ = testutil.NewHibernatePlanBuilder("suspend-offhours-expiry", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "database",
				Type: "rds",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "suspension-aws",
				},
			}).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		// Suspend with a deadline of 21:00 the same evening (well inside the 20:00-06:00 window).
		deadline := time.Date(2026, 5, 4, 21, 0, 0, 0, time.UTC)

		By("Suspending plan with spec.suspend=true and AnnotationSuspendUntil=21:00")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		orig := plan.DeepCopy()
		plan.Spec.Suspend = true
		if plan.Annotations == nil {
			plan.Annotations = make(map[string]string)
		}
		plan.Annotations[wellknown.AnnotationSuspendUntil] = deadline.Format(time.RFC3339)
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())

		By("Verifying plan transitions to PhaseSuspended")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseSuspended)

		By("Advancing clock to 21:05 UTC — deadline has passed AND it is still inside off-hours (20:00-06:00)")
		fakeClock.SetTime(time.Date(2026, 5, 4, 21, 5, 0, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan transitions to Hibernating (not Active) because it is still off-hours")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Verifying suspend-until annotation was cleared after auto-resume")
		Eventually(func() bool {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			_, hasDeadline := plan.Annotations[wellknown.AnnotationSuspendUntil]
			return !hasDeadline
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(BeTrue(), "suspend-until annotation should be cleared after deadline passes")
	})

	It("SuspendWhileHibernatingOnSameWindow: should drain in-flight Jobs before suspending, then resume hibernation from where it left off when unsuspended within the same off-hours window", func() {
		// A plan suspended mid-hibernation (with active shutdown Jobs) must not interrupt those Jobs.
		// The transition to PhaseSuspended is deferred until every in-flight Job reaches a terminal state,
		// preserving the execution bookmark (cycle, stage, target progress) in status.
		// When unsuspended while still inside the same off-hours window the plan resumes to
		// PhaseHibernating — picking up from the next target — rather than restarting from PhaseActive.

		baseTime := time.Date(2026, 5, 11, 22, 0, 0, 0, time.UTC) // Monday 22:00 — off-hours
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan at 22:00 (off-hours) so it starts Hibernating immediately")
		plan, _ = testutil.NewHibernatePlanBuilder("suspend-mid-hibernating", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(
				hibernatorv1alpha1.Target{
					Name: "database",
					Type: "rds",
					ConnectorRef: hibernatorv1alpha1.ConnectorRef{
						Kind: "CloudProvider",
						Name: "suspension-aws",
					},
				},
				hibernatorv1alpha1.Target{
					Name: "eks-cluster",
					Type: "eks",
					ConnectorRef: hibernatorv1alpha1.ConnectorRef{
						Kind: "CloudProvider",
						Name: "suspension-aws",
					},
				},
			).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Capturing the first in-flight shutdown Job before suspension (Job is running but not yet complete)")
		firstInFlightJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "shutdown", "database")

		By("Suspending plan via spec.suspend=true while the shutdown Job is still in flight")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		orig := plan.DeepCopy()
		plan.Spec.Suspend = true
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())

		By("Verifying plan will wait for in-flight Job to terminal state before transitioning to PhaseSuspended")
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating, 5*time.Second)

		By("Simulating completion of the first in-flight shutdown Job")
		testutil.SimulateJobSuccess(ctx, k8sClient, firstInFlightJob, fakeClock.Now())

		By("Verifying plan transitions to PhaseSuspended")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseSuspended)

		By("Advancing clock to off-hours (next day 01:00) and resuming the plan")
		fakeClock.SetTime(time.Date(2026, 5, 12, 1, 0, 0, 0, time.UTC))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		orig = plan.DeepCopy()
		plan.Spec.Suspend = false
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan resumes to Hibernating phase, continuing the schedule evaluation without starting from Active (clock is still off-hours)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Capturing the second in-flight shutdown Job before suspension (Job is running but not yet complete)")
		secondInFlightJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "shutdown", "eks-cluster")

		By("Simulating completion of the second in-flight shutdown Job")
		testutil.SimulateJobSuccess(ctx, k8sClient, secondInFlightJob, fakeClock.Now())

		By("Verifying plan transitions to PhaseHibernated")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernated)
	})

	It("SuspendWhileHibernatingOnDifferentWindow: should drain in-flight Jobs before suspending, then route to PhaseActive when unsuspended outside the original off-hours window", func() {
		// Same drain-then-suspend contract as the same-window variant, but the plan is resumed
		// during on-hours (a different schedule window than when it was suspended).
		// Because the clock is no longer inside the off-hours window, the plan cannot
		// continue the interrupted hibernation — it routes to PhaseActive instead.
		baseTime := time.Date(2026, 5, 11, 22, 0, 0, 0, time.UTC) // Monday 22:00 — off-hours
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan at 22:00 (off-hours) so it starts Hibernating immediately")
		plan, _ = testutil.NewHibernatePlanBuilder("suspend-mid-hibernating", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(
				hibernatorv1alpha1.Target{
					Name: "database",
					Type: "rds",
					ConnectorRef: hibernatorv1alpha1.ConnectorRef{
						Kind: "CloudProvider",
						Name: "suspension-aws",
					},
				},
				hibernatorv1alpha1.Target{
					Name: "eks-cluster",
					Type: "eks",
					ConnectorRef: hibernatorv1alpha1.ConnectorRef{
						Kind: "CloudProvider",
						Name: "suspension-aws",
					},
				},
			).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Capturing the first in-flight shutdown Job before suspension (Job is running but not yet complete)")
		firstInFlightJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "shutdown", "database")

		By("Suspending plan via spec.suspend=true while the shutdown Job is still in flight")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		orig := plan.DeepCopy()
		plan.Spec.Suspend = true
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())

		By("Verifying plan will wait for in-flight Job to terminal state before transitioning to PhaseSuspended")
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating, 5*time.Second)

		By("Simulating completion of the first in-flight shutdown Job")
		testutil.SimulateJobSuccess(ctx, k8sClient, firstInFlightJob, fakeClock.Now())

		By("Verifying plan transitions to PhaseSuspended")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseSuspended)

		By("Advancing clock to off-hours (next day 09:00) and resuming the plan")
		fakeClock.SetTime(time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		orig = plan.DeepCopy()
		plan.Spec.Suspend = false
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan transition to PhaseActive (clock is now on-hours, different from when it was suspended)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)
	})

	It("SuspendFromErrorOnShutdown: should clear error state and route to Active when suspended while in PhaseError during shutdown", func() {
		// Plan enters PhaseError after exhausting retries during shutdown.
		// Operator suspends to acknowledge the error, then resumes during on-hours.
		// Expected: error state cleared, plan routes to Active (shutdown never completed,
		// resource is still running).
		baseTime := time.Date(2026, 6, 1, 20, 1, 10, 0, time.UTC) // Monday 20:01 — off-hours
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with retries=1 so it enters PhaseError after two failures")
		plan, _ = testutil.NewHibernatePlanBuilder("suspend-from-error-shutdown", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithBehavior(hibernatorv1alpha1.Behavior{
				Mode:    hibernatorv1alpha1.BehaviorStrict,
				Retries: 1,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "database",
				Type: "rds",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "suspension-aws",
				},
			}).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Failing the initial shutdown Job")
		firstJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "shutdown", "database")
		testutil.SimulateJobFailure(ctx, k8sClient, firstJob, fakeClock.Now())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			return plan.Status.ErrorMessage
		}, testutil.DefaultTimeout, testutil.DefaultInterval).ShouldNot(BeEmpty())

		By("Failing the auto-retry Job to exhaust all retries")
		retryJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "shutdown", "database")
		testutil.SimulateJobFailure(ctx, k8sClient, retryJob, fakeClock.Now())

		By("Verifying plan enters PhaseError (no more auto-retries)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseError)

		By("Suspending plan via spec.suspend=true to acknowledge the error")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		orig := plan.DeepCopy()
		plan.Spec.Suspend = true
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())

		By("Verifying plan transitions to PhaseSuspended")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseSuspended)

		By("Verifying suspended-at-phase annotation records PhaseError")
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			return plan.Annotations[wellknown.AnnotationSuspendedAtPhase]
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Equal(string(hibernatorv1alpha1.PhaseError)))

		By("Advancing clock to 2 hours after off-hours (Monday 22:00) and resuming the plan, assuming fix was applied during suspension")
		fakeClock.SetTime(time.Date(2026, 6, 1, 22, 0, 0, 0, time.UTC))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		orig = plan.DeepCopy()
		plan.Spec.Suspend = false
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan resumes to Hibernating phase (internally it moved from Active→Hibernating)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Verifying error state was cleared after suspend-from-error resume")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		Expect(plan.Status.ErrorMessage).To(BeEmpty())
		Expect(plan.Status.RetryCount).To(BeZero())

		By("Verifying suspended-at-phase annotation was cleaned up")
		Expect(plan.Annotations).NotTo(HaveKey(wellknown.AnnotationSuspendedAtPhase))
	})

	It("SuspendFromErrorOnWakeup: should clear error state and route to Hibernated when suspended while in PhaseError during wakeup", func() {
		// Plan reaches Hibernated, then wakeup fails and exhausts retries → PhaseError.
		// Operator suspends to acknowledge the error, then resumes.
		// Expected: error state cleared, plan routes to Hibernated (wakeup never completed,
		// resource is still hibernated).
		baseTime := time.Date(2026, 6, 8, 22, 0, 0, 0, time.UTC) // Monday 22:00 — off-hours
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with retries=1 at off-hours so shutdown proceeds immediately")
		plan, _ = testutil.NewHibernatePlanBuilder("suspend-from-error-wakeup", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithBehavior(hibernatorv1alpha1.Behavior{
				Mode:    hibernatorv1alpha1.BehaviorStrict,
				Retries: 1,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "database",
				Type: "rds",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "suspension-aws",
				},
			}).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Simulating successful shutdown Job so plan reaches Hibernated")
		shutdownJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "shutdown", "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, shutdownJob, fakeClock.Now())
		testutil.EventuallyRestoreDataSaved(ctx, k8sClient, plan, 0)

		Expect(restoreManager.Save(ctx, plan.Namespace, plan.Name, "database", &restore.Data{
			Target: "database",
		})).To(Succeed())

		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernated)

		By("Advancing clock to on-hours (Tuesday 09:00) to trigger wakeup")
		fakeClock.SetTime(time.Date(2026, 6, 9, 9, 0, 0, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)

		By("Failing the initial wakeup Job")
		firstWakeupJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "wakeup", "database")
		testutil.SimulateJobFailure(ctx, k8sClient, firstWakeupJob, fakeClock.Now())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			return plan.Status.ErrorMessage
		}, testutil.DefaultTimeout, testutil.DefaultInterval).ShouldNot(BeEmpty())

		By("Failing the auto-retry wakeup Job to exhaust all retries")
		retryWakeupJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "wakeup", "database")
		testutil.SimulateJobFailure(ctx, k8sClient, retryWakeupJob, fakeClock.Now())

		By("Verifying plan enters PhaseError (no more auto-retries)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseError)

		By("Suspending plan via spec.suspend=true to acknowledge the error")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		orig := plan.DeepCopy()
		plan.Spec.Suspend = true
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())

		By("Verifying plan transitions to PhaseSuspended")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseSuspended)

		By("Verifying suspended-at-phase annotation records PhaseError")
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			return plan.Annotations[wellknown.AnnotationSuspendedAtPhase]
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Equal(string(hibernatorv1alpha1.PhaseError)))

		By("Advancing clock to on off-hours period (Tuesday 22:00) and resuming the plan, assuming Plan will stay on Hibernated phase because wakeup never completed and resource is still hibernated")
		fakeClock.SetTime(time.Date(2026, 6, 1, 22, 0, 0, 0, time.UTC))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		orig = plan.DeepCopy()
		plan.Spec.Suspend = false
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan routes to Hibernated (wakeup never completed, resource still hibernated)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernated)

		By("Verifying error state was cleared after suspend-from-error resume")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		Expect(plan.Status.ErrorMessage).To(BeEmpty())
		Expect(plan.Status.RetryCount).To(BeZero())

		By("Verifying suspended-at-phase annotation was cleaned up")
		Expect(plan.Annotations).NotTo(HaveKey(wellknown.AnnotationSuspendedAtPhase))
	})
})
