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

var _ = Describe("Force-Action E2E", func() {
	var (
		plan          *hibernatorv1alpha1.HibernatePlan
		cloudProvider *hibernatorv1alpha1.CloudProvider
	)

	BeforeEach(func() {
		By("Creating mock CloudProvider")
		cloudProvider = &hibernatorv1alpha1.CloudProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "force-action-aws",
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

	// -----------------------------------------------------------------------
	// Test 1: force-action=hibernate during active window
	//
	// Validates that:
	//   a) force-action=hibernate overrides the schedule (ShouldHibernate=false) and triggers
	//      immediate hibernation from PhaseActive.
	//   b) Once Hibernated, the annotation suppresses the schedule's wakeup signal —
	//      the plan stays Hibernated through the entire active window.
	// -----------------------------------------------------------------------
	It("ForceHibernate: should hibernate during active window and suppress schedule-driven wakeup", func() {
		// Monday 08:00 UTC — on-hours (schedule ShouldHibernate=false).
		fakeClock.SetTime(time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC))

		By("Creating plan with 20:00-06:00 off-hours; clock is in the active window")
		plan, _ = testutil.NewHibernatePlanBuilder("force-hib-active-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name:         "database",
				Type:         "rds",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "force-action-aws"},
			}).
			Build()
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Verifying plan initialises to Active (on-hours)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Setting force-action=hibernate while still in the active window")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		orig := plan.DeepCopy()
		if plan.Annotations == nil {
			plan.Annotations = make(map[string]string)
		}
		plan.Annotations[wellknown.AnnotationForceAction] = wellknown.ForceActionHibernate
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())

		By("Verifying plan transitions to Hibernating (force-action overrides the schedule)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Simulating successful hibernation job")
		hibernationJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, hibernationJob, fakeClock.Now())
		testutil.EventuallyRestoreDataSaved(ctx, k8sClient, plan, 0)

		By("Injecting restore data to simulate what the runner would have written")
		Expect(restoreManager.Save(ctx, plan.Namespace, plan.Name, plan.Spec.Targets[0].Name, &restore.Data{
			Target: plan.Spec.Targets[0].Name,
			IsLive: true,
		})).To(Succeed())

		By("Triggering reconcile (clock still at 08:00, schedule says wake up)")
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Asserting plan stays Hibernated: annotation suppresses the schedule wakeup signal")
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernated, 3*time.Second)
	})

	// -----------------------------------------------------------------------
	// Test 2: Removing the annotation restores schedule control
	//
	// Continues from a force-hibernated Active-window state. After removing the
	// annotation, idleState takes over and the schedule's wakeup signal proceeds.
	// -----------------------------------------------------------------------
	It("ForceHibernate: removing the annotation restores schedule-driven wakeup", func() {
		// Monday 08:00 UTC — active window.
		fakeClock.SetTime(time.Date(2026, 6, 8, 8, 0, 0, 0, time.UTC))

		By("Creating plan and letting it initialise to Active")
		plan, _ = testutil.NewHibernatePlanBuilder("force-hib-remove-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name:         "app",
				Type:         "rds",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "force-action-aws"},
			}).
			Build()
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Force-hibernating the plan")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		orig := plan.DeepCopy()
		if plan.Annotations == nil {
			plan.Annotations = make(map[string]string)
		}
		plan.Annotations[wellknown.AnnotationForceAction] = wellknown.ForceActionHibernate
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		hibernationJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "app")
		testutil.SimulateJobSuccess(ctx, k8sClient, hibernationJob, fakeClock.Now())
		testutil.EventuallyRestoreDataSaved(ctx, k8sClient, plan, 0)
		Expect(restoreManager.Save(ctx, plan.Namespace, plan.Name, plan.Spec.Targets[0].Name, &restore.Data{
			Target: plan.Spec.Targets[0].Name,
			IsLive: true,
		})).To(Succeed())
		testutil.TriggerReconcile(ctx, k8sClient, plan)
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernated, 2*time.Second)

		By("Removing the force-action annotation")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		orig = plan.DeepCopy()
		delete(plan.Annotations, wellknown.AnnotationForceAction)
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())

		By("Triggering reconcile at 08:00 — schedule now has control and wakeup should proceed")
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan transitions to WakingUp (schedule-driven, annotation removed)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)

		wakeupJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "app")
		testutil.SimulateJobSuccess(ctx, k8sClient, wakeupJob, fakeClock.Now())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)
	})

	// -----------------------------------------------------------------------
	// Test 3: force-action=wakeup during the hibernated window
	//
	// Plan hibernates via the schedule. While still inside the off-hours window
	// (ShouldHibernate=true), force-action=wakeup is set. The plan must wake up
	// despite the schedule saying it should stay hibernated.
	// -----------------------------------------------------------------------
	It("ForceWakeup: should wake up during the hibernated window, overriding the schedule", func() {
		// Monday 20:05 UTC — off-hours (schedule ShouldHibernate=true).
		fakeClock.SetTime(time.Date(2026, 6, 15, 20, 5, 0, 0, time.UTC))

		By("Creating plan in the off-hours window; should start hibernating immediately")
		plan, _ = testutil.NewHibernatePlanBuilder("force-wake-offhours-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name:         "cache",
				Type:         "rds",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "force-action-aws"},
			}).
			Build()
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		hibernationJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "cache")
		testutil.SimulateJobSuccess(ctx, k8sClient, hibernationJob, fakeClock.Now())
		testutil.EventuallyRestoreDataSaved(ctx, k8sClient, plan, 0)

		By("Injecting restore data")
		Expect(restoreManager.Save(ctx, plan.Namespace, plan.Name, plan.Spec.Targets[0].Name, &restore.Data{
			Target: plan.Spec.Targets[0].Name,
			IsLive: true,
		})).To(Succeed())

		By("Setting force-action=wakeup while still in the off-hours window")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		Expect(plan.Status.Phase).To(Equal(hibernatorv1alpha1.PhaseHibernated))

		orig := plan.DeepCopy()
		if plan.Annotations == nil {
			plan.Annotations = make(map[string]string)
		}
		plan.Annotations[wellknown.AnnotationForceAction] = wellknown.ForceActionWakeup
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())

		By("Verifying plan transitions to WakingUp (force-action overrides the hibernated-window signal)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)

		wakeupJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "cache")
		testutil.SimulateJobSuccess(ctx, k8sClient, wakeupJob, fakeClock.Now())

		By("Verifying plan reaches Active despite the hibernated window (schedule overridden)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Asserting plan stays Active: force-action=wakeup from Active is a no-op (loop prevention)")
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive, 3*time.Second)
	})

	// -----------------------------------------------------------------------
	// Test 4: force-action on Error — recovery drives the plan; annotation fires afterwards
	//
	// Validates that:
	//   a) When the plan enters PhaseError (hibernation job failure), force-action
	//      does NOT intercept the error recovery (recoveryState is selected, not forceActionState).
	//   b) After recovery succeeds (new job completes), the plan reaches PhaseHibernated,
	//      and force-action=hibernate is then a harmless no-op (already at target).
	// -----------------------------------------------------------------------
	It("ForceAction on Error: recovery proceeds normally; annotation is invisible to recoveryState", func() {
		// Monday 20:05 UTC — off-hours; schedule and force-action agree: hibernate.
		fakeClock.SetTime(time.Date(2026, 6, 22, 20, 5, 0, 0, time.UTC))

		By("Creating plan with force-action=hibernate pre-set")
		plan, _ = testutil.NewHibernatePlanBuilder("force-hib-error-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithAnnotation(wellknown.AnnotationForceAction, wellknown.ForceActionHibernate).
			WithTarget(hibernatorv1alpha1.Target{
				Name:         "database",
				Type:         "rds",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "force-action-aws"},
			}).
			Build()
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Waiting for hibernation job and simulating failure")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)
		hibernationJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "database")
		testutil.SimulateJobFailure(ctx, k8sClient, hibernationJob, fakeClock.Now())

		By("Verifying plan enters PhaseError (force-action does not block error detection)")
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			return plan.Status.ErrorMessage
		}, testutil.DefaultTimeout, testutil.DefaultInterval).ShouldNot(BeEmpty(),
			"plan must surface an ErrorMessage when hibernation fails")

		By("Verifying plan auto-retries: recoveryState drives the retry, not forceActionState")
		// RetryCount=0 on first error → immediate auto-retry (no backoff).
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Verifying a new hibernation job was created for the retry attempt")
		retryJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, retryJob, fakeClock.Now())

		By("Verifying plan reaches Hibernated after recovery")
		testutil.EventuallyRestoreDataSaved(ctx, k8sClient, plan, 0)

		By("Asserting force-action=hibernate is a silent no-op once Hibernated (target already reached)")
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernated, 2*time.Second)
	})

	// -----------------------------------------------------------------------
	// Test 5: Spec.Suspend=true beats force-action; force-action fires after resume
	//
	// Validates that:
	//   a) When Spec.Suspend=true and force-action=hibernate are set simultaneously,
	//      suspension takes priority (selectHandler Priority 2 > Priority 3).
	//   b) After un-suspending, force-action fires and the plan hibernates.
	// -----------------------------------------------------------------------
	It("SuspendBeatsForceAction: suspension wins; force-action re-activates after resume", func() {
		// Monday 08:00 UTC — active window.
		fakeClock.SetTime(time.Date(2026, 6, 29, 8, 0, 0, 0, time.UTC))

		By("Creating plan at Active phase in the active window")
		plan, _ = testutil.NewHibernatePlanBuilder("force-hib-suspend-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name:         "service",
				Type:         "rds",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "force-action-aws"},
			}).
			Build()
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Setting BOTH Spec.Suspend=true AND force-action=hibernate at the same time")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		orig := plan.DeepCopy()
		plan.Spec.Suspend = true
		if plan.Annotations == nil {
			plan.Annotations = make(map[string]string)
		}
		plan.Annotations[wellknown.AnnotationForceAction] = wellknown.ForceActionHibernate
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())

		By("Verifying plan transitions to PhaseSuspended, NOT PhaseHibernating (Suspend beats force-action)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseSuspended)

		// Allow a brief window to confirm it never enters Hibernating during the suspension.
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseSuspended, 2*time.Second)

		By("Removing Spec.Suspend=false to resume the plan")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		orig = plan.DeepCopy()
		plan.Spec.Suspend = false
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())

		By("Verifying force-action=hibernate fires immediately after resume: plan transitions to Hibernating")
		// Do NOT assert PhaseActive here. The unsuspend → Active and the force-action → Hibernating
		// transitions may be dispatched in the same reconcile pass (or back-to-back passes that settle
		// before the poll window). Asserting Active first would be a flaky race; assert Hibernating directly.
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		hibernationJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "service")
		testutil.SimulateJobSuccess(ctx, k8sClient, hibernationJob, fakeClock.Now())
		testutil.EventuallyRestoreDataSaved(ctx, k8sClient, plan, 0)
	})

	// -----------------------------------------------------------------------
	// Test 6: Full CI pipeline pattern — force hibernate then force wakeup
	//
	// Validates a complete CI lifecycle:
	//   1. force-action=hibernate: drives hibernation outside the schedule (active window).
	//   2. Override with force-action=wakeup: drives wakeup while still in the off-hours window.
	//   3. Remove annotation: schedule resumes.
	// -----------------------------------------------------------------------
	It("CI pipeline: force hibernate → override with force wakeup → restore schedule", func() {
		// Monday 08:00 UTC — active window; neither operation is schedule-driven.
		fakeClock.SetTime(time.Date(2026, 7, 6, 8, 0, 0, 0, time.UTC))

		By("Creating plan; starts in Active (on-hours)")
		plan, _ = testutil.NewHibernatePlanBuilder("force-ci-pipeline-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name:         "worker",
				Type:         "rds",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "force-action-aws"},
			}).
			Build()
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		// ── Step 1: force hibernate ───────────────────────────────────────
		By("Setting force-action=hibernate (CI pipeline initiates shutdown)")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		orig := plan.DeepCopy()
		if plan.Annotations == nil {
			plan.Annotations = make(map[string]string)
		}
		plan.Annotations[wellknown.AnnotationForceAction] = wellknown.ForceActionHibernate
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())

		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)
		hibernationJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "worker")
		testutil.SimulateJobSuccess(ctx, k8sClient, hibernationJob, fakeClock.Now())
		testutil.EventuallyRestoreDataSaved(ctx, k8sClient, plan, 0)
		Expect(restoreManager.Save(ctx, plan.Namespace, plan.Name, plan.Spec.Targets[0].Name, &restore.Data{
			Target: plan.Spec.Targets[0].Name,
			IsLive: true,
		})).To(Succeed())

		// ── Step 2: force wakeup (CI pipeline teardown complete, deploy starts) ──
		By("Overriding to force-action=wakeup (CI pipeline needs resources back)")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		Expect(plan.Status.Phase).To(Equal(hibernatorv1alpha1.PhaseHibernated))

		orig = plan.DeepCopy()
		plan.Annotations[wellknown.AnnotationForceAction] = wellknown.ForceActionWakeup
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())

		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)
		wakeupJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "worker")
		testutil.SimulateJobSuccess(ctx, k8sClient, wakeupJob, fakeClock.Now())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Asserting plan stays Active: force-action=wakeup from Active is a no-op (loop prevention)")
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive, 3*time.Second)

		// ── Step 3: remove annotation; advance into hibernation window ───
		By("Removing annotation to restore schedule control")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		orig = plan.DeepCopy()
		delete(plan.Annotations, wellknown.AnnotationForceAction)
		Expect(k8sClient.Patch(ctx, plan, client.MergeFrom(orig))).To(Succeed())

		By("Advancing clock into the hibernation window (20:05) and triggering reconcile")
		fakeClock.SetTime(time.Date(2026, 7, 6, 20, 5, 0, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying schedule resumes control: plan enters PhaseHibernating")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)
	})
})
