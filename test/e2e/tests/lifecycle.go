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
		plan          *hibernatorv1alpha1.HibernatePlan
		cloudProvider *hibernatorv1alpha1.CloudProvider
	)

	BeforeEach(func() {
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

		// Capture restore ConfigMaps before deleting plan
		var cmsToCleanup []corev1.ConfigMap
		if plan != nil {
			for _, exec := range plan.Status.Executions {
				if exec.RestoreConfigMapRef != "" {
					cmKey, _ := k8sutil.ObjectKeyFromString(exec.RestoreConfigMapRef)
					cmsToCleanup = append(cmsToCleanup, corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{Name: cmKey.Name, Namespace: cmKey.Namespace},
					})
				}
			}
		}

		testutil.EnsureDeleted(ctx, k8sClient, plan)
		testutil.EnsureDeleted(ctx, k8sClient, cloudProvider)

		for i := range cmsToCleanup {
			testutil.EnsureDeleted(ctx, k8sClient, &cmsToCleanup[i])
		}
	})

	It("GoldenPath: should successfully execute the full cycle Active -> Hibernated -> WakingUp -> Active", func() {
		// 1. Setup: Monday 08:00 UTC (On-Hours)
		baseTime := time.Date(2026, 2, 9, 8, 0, 0, 0, time.UTC)
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with 20:00-06:00 hibernation window. Clock set to Monday 08:00")
		plan, _ = testutil.NewHibernatePlanBuilder("lifecycle-test", testNamespace).
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
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)
		Expect(plan.Status.Executions).To(BeEmpty())

		// 2. Transition to Hibernation
		By("Advancing time to hibernation window (20:01:11)")
		fakeClock.SetTime(time.Date(2026, 2, 9, 20, 1, 11, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan transitions to Hibernating")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		Expect(plan.Status.Executions).To(HaveLen(1))
		Expect(plan.Status.Executions[0].Target).To(Equal("database"))
		Expect(plan.Status.Executions[0].State).To(Equal(hibernatorv1alpha1.StatePending))

		By("Verifying runner Job creation and simulating success")
		hibernationJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "shutdown", "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, hibernationJob, fakeClock.Now())

		By("Verifying plan transitions to Hibernated and saves restore data")
		testutil.EventuallyRestoreDataSaved(ctx, k8sClient, plan, 0)

		// Verify ConfigMap exists and can be retrieved via manager
		cmKey, _ := k8sutil.ObjectKeyFromString(plan.Status.Executions[0].RestoreConfigMapRef)
		var restoreCM corev1.ConfigMap
		Expect(k8sClient.Get(ctx, cmKey, &restoreCM)).To(Succeed())

		// Manually inject some restore data to simulate real-world usage if needed
		Expect(restoreManager.Save(ctx, plan.Namespace, plan.Name, plan.Spec.Targets[0].Name, &restore.Data{
			Target: plan.Spec.Targets[0].Name,
		})).To(Succeed())

		// 3. Transition to Wakeup
		By("Advancing time to wakeup window (Tuesday 06:01:10)")
		fakeClock.SetTime(time.Date(2026, 2, 10, 6, 1, 10, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan transitions to WakingUp")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)

		By("Verifying wakeup Job creation and simulating success")
		wakeupJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "wakeup", "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, wakeupJob, fakeClock.Now())

		By("Verifying plan returns to Active phase")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)
	})

	It("MidOffHoursCreation: should start in Hibernating directly when plan is created inside an active off-hours window", func() {
		// Clock is already inside the hibernation window when the plan is created.
		// The controller should never surface an Active phase — the first stable phase must be Hibernating.
		baseTime := time.Date(2026, 3, 2, 20, 1, 10, 0, time.UTC) // Monday 20:01 — off-hours
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan while clock is already inside the 20:00-06:00 off-hours window")
		plan, _ = testutil.NewHibernatePlanBuilder("mid-offhours-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
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

		By("Verifying plan transitions directly to Hibernating (Active phase is skipped)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Verifying executions were initialised immediately (plan was not idling in Active)")
		Expect(plan.Status.Executions).NotTo(BeEmpty())
	})

	It("TimezoneAware: should evaluate off-hours relative to the plan timezone, not UTC", func() {
		// Asia/Jakarta is UTC+7.
		// At 13:01 UTC on a Monday, it is 20:01 WIB — inside the 20:00-06:00 off-hours window.
		// A UTC-based plan at the same moment would still be on-hours (13:01 < 20:00).
		baseTime := time.Date(2026, 3, 9, 13, 1, 10, 0, time.UTC) // 13:01 UTC = 20:01 WIB
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with Asia/Jakarta timezone (UTC+7)")
		plan, _ = testutil.NewHibernatePlanBuilder("tz-aware-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithTimezone("Asia/Jakarta").
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

		By("Verifying plan enters Hibernating at 13:01 UTC (= 20:01 WIB, off-hours for Jakarta timezone)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Advancing clock to 23:01 UTC (= 06:01 WIB next day — wakeup window for Jakarta)")
		fakeClock.SetTime(time.Date(2026, 3, 9, 23, 1, 10, 0, time.UTC))
		hibernationJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "shutdown", "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, hibernationJob, fakeClock.Now())
		testutil.EventuallyRestoreDataSaved(ctx, k8sClient, plan, 0)
		Expect(restoreManager.Save(ctx, plan.Namespace, plan.Name, "database", &restore.Data{
			Target: "database",
		})).To(Succeed())

		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan wakes up at 23:01 UTC (= 06:01 WIB — on-hours in Jakarta timezone)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)
	})

	It("MultiCycleHibernation: should cleanly complete a second hibernation cycle after returning to Active", func() {
		// Validates that restore data and execution state from a prior cycle do not interfere
		// with a subsequent hibernation-wakeup cycle.
		baseTime := time.Date(2026, 3, 16, 8, 0, 0, 0, time.UTC) // Monday 08:00 — on-hours
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan")
		plan, _ = testutil.NewHibernatePlanBuilder("multi-cycle-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
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
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		// --- First cycle ---
		By("[Cycle-1] Triggering hibernation window (Monday 20:01)")
		fakeClock.SetTime(time.Date(2026, 3, 16, 20, 1, 10, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		shutdownJob1 := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "shutdown", "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, shutdownJob1, fakeClock.Now())
		testutil.EventuallyRestoreDataSaved(ctx, k8sClient, plan, 0)
		Expect(restoreManager.Save(ctx, plan.Namespace, plan.Name, "database", &restore.Data{
			Target: "database",
		})).To(Succeed())

		By("[Cycle-1] Triggering wakeup (Tuesday 06:01)")
		fakeClock.SetTime(time.Date(2026, 3, 17, 6, 1, 10, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)

		wakeupJob1 := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "wakeup", "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, wakeupJob1, fakeClock.Now())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		// --- Second cycle ---
		By("[Cycle-2] Triggering hibernation again the following evening (Tuesday 20:01)")
		fakeClock.SetTime(time.Date(2026, 3, 17, 20, 1, 10, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("[Cycle-2] Verifying a fresh shutdown Job is created without conflicts from the prior cycle")
		shutdownJob2 := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "shutdown", "database")
		Expect(shutdownJob2.Name).NotTo(Equal(shutdownJob1.Name), "second cycle must create a distinct Job")
		testutil.SimulateJobSuccess(ctx, k8sClient, shutdownJob2, fakeClock.Now())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernated)
	})

	It("IdempotentReconcile: should not create duplicate Jobs when reconcile is triggered multiple times while Hibernating", func() {
		baseTime := time.Date(2026, 3, 23, 20, 1, 10, 0, time.UTC) // Monday 20:01 — off-hours
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan inside the hibernation window")
		plan, _ = testutil.NewHibernatePlanBuilder("idempotent-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
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
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Triggering multiple rapid reconciles while a shutdown Job is in flight")
		for range 5 {
			testutil.TriggerReconcile(ctx, k8sClient, plan)
		}

		By("Verifying only one active (non-stale) shutdown Job exists for the target")
		Consistently(func() int {
			var jl batchv1.JobList
			_ = k8sClient.List(ctx, &jl, client.InNamespace(testNamespace), client.MatchingLabels{
				wellknown.LabelPlan:      plan.Name,
				wellknown.LabelOperation: "shutdown",
				wellknown.LabelTarget:    "database",
			})
			active := 0
			for _, j := range jl.Items {
				if _, stale := j.Labels[wellknown.LabelStaleRunnerJob]; !stale {
					active++
				}
			}
			return active
		}, 4*time.Second, 500*time.Millisecond).Should(Equal(1), "only one active Job should exist despite multiple reconcile triggers")
	})
})
