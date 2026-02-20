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

var _ = Describe("Execution Strategy E2E", func() {
	var (
		plan          *hibernatorv1alpha1.HibernatePlan
		cloudProvider *hibernatorv1alpha1.CloudProvider
	)

	BeforeEach(func() {
		By("Creating mock CloudProvider")
		cloudProvider = &hibernatorv1alpha1.CloudProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "strategy-aws",
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

	Describe("Sequential Strategy", func() {
		It("Should execute targets one-by-one in order", func() {
			plan, _ = testutil.NewHibernatePlanBuilder("seq-strategy", testNamespace).
				WithSchedule("20:00", "06:00").
				WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
					Type: hibernatorv1alpha1.StrategySequential,
				}).
				WithTarget(
					hibernatorv1alpha1.Target{
						Name:         "target-1",
						Type:         "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
					hibernatorv1alpha1.Target{
						Name:         "target-2",
						Type:         "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
				).
				Build()

			Expect(k8sClient.Create(ctx, plan)).To(Succeed())

			By("Advancing time to hibernation window")
			fakeClock.SetTime(time.Date(2026, 2, 9, 20, 1, 10, 0, time.UTC))
			testutil.TriggerReconcile(ctx, k8sClient, plan)
			testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

			By("Verifying target-1 Job is created and target-2 is Pending")
			var jobs batchv1.JobList
			Eventually(func() int {
				_ = k8sClient.List(ctx, &jobs, client.InNamespace(testNamespace), client.MatchingLabels{
					wellknown.LabelPlan: plan.Name,
				})
				return len(jobs.Items)
			}, 10*time.Second, time.Second).Should(Equal(1))
			Expect(jobs.Items[0].Labels[wellknown.LabelTarget]).To(Equal("target-1"))

			By("Simulating success for target-1")
			testutil.SimulateJobSuccess(ctx, k8sClient, &jobs.Items[0], fakeClock.Now())

			By("Verifying target-2 Job is created")
			Eventually(func() int {
				_ = k8sClient.List(ctx, &jobs, client.InNamespace(testNamespace), client.MatchingLabels{
					wellknown.LabelPlan: plan.Name,
				})
				return len(jobs.Items)
			}, 10*time.Second, time.Second).Should(Equal(2))

			// Verify target names in jobs
			targets := make(map[string]bool)
			for _, j := range jobs.Items {
				targets[j.Labels[wellknown.LabelTarget]] = true
			}
			Expect(targets).To(HaveKey("target-1"))
			Expect(targets).To(HaveKey("target-2"))
		})
	})

	Describe("Parallel Strategy", func() {
		It("Should execute all targets simultaneously", func() {
			plan, _ = testutil.NewHibernatePlanBuilder("parallel-strategy", testNamespace).
				WithSchedule("20:00", "06:00").
				WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
					Type: hibernatorv1alpha1.StrategyParallel,
				}).
				WithTarget(
					hibernatorv1alpha1.Target{
						Name:         "p-target-1",
						Type:         "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
					hibernatorv1alpha1.Target{
						Name:         "p-target-2",
						Type:         "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
				).
				Build()

			Expect(k8sClient.Create(ctx, plan)).To(Succeed())

			By("Advancing time to hibernation window")
			fakeClock.SetTime(time.Date(2026, 2, 9, 20, 1, 10, 0, time.UTC))
			testutil.TriggerReconcile(ctx, k8sClient, plan)
			testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

			By("Verifying both Jobs are created at once")
			var jobs batchv1.JobList
			Eventually(func() int {
				_ = k8sClient.List(ctx, &jobs, client.InNamespace(testNamespace), client.MatchingLabels{
					wellknown.LabelPlan: plan.Name,
				})
				return len(jobs.Items)
			}, 10*time.Second, time.Second).Should(Equal(2))
		})
	})

	Describe("DAG Strategy", func() {
		It("Should execute targets according to dependencies (Shutting down: Web -> App -> DB, Waking up: DB -> App -> Web)", func() {
			// Web depends on App, App depends on DB
			plan, _ = testutil.NewHibernatePlanBuilder("dag-strategy", testNamespace).
				WithSchedule("20:00", "06:00").
				WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
					Type: hibernatorv1alpha1.StrategyDAG,
					Dependencies: []hibernatorv1alpha1.Dependency{
						{From: "web", To: "app"},
						{From: "app", To: "db"},
					},
				}).
				WithTarget(
					hibernatorv1alpha1.Target{
						Name: "web", Type: "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
					hibernatorv1alpha1.Target{
						Name: "app", Type: "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
					hibernatorv1alpha1.Target{
						Name: "db", Type: "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
				).
				Build()

			Expect(k8sClient.Create(ctx, plan)).To(Succeed())

			// --- HIBERNATION (Shutdown) ---
			By("[Shutdown] Advancing time to hibernation window")
			fakeClock.SetTime(time.Date(2026, 2, 9, 20, 1, 10, 0, time.UTC))
			testutil.TriggerReconcile(ctx, k8sClient, plan)
			testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

			By("[Shutdown] Verifying only 'web' Job is created first (top of DAG)")
			jobWeb := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "shutdown", "web")
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(jobWeb), jobWeb) // Ensure latest

			By("[Shutdown] Simulating 'web' success, verifying 'app' Job creation")
			testutil.SimulateJobSuccess(ctx, k8sClient, jobWeb, fakeClock.Now())
			jobApp := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "shutdown", "app")
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(jobApp), jobApp) // Ensure latest

			By("[Shutdown] Simulating 'app' success, verifying 'db' Job creation")
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(jobApp), jobApp) // Ensure latest
			testutil.SimulateJobSuccess(ctx, k8sClient, jobApp, fakeClock.Now())
			jobDB := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "shutdown", "db")

			By("[Shutdown] Triggering 'db' success")
			testutil.SimulateJobSuccess(ctx, k8sClient, jobDB, fakeClock.Now())

			// Complete hibernation
			testutil.EventuallyRestoreDataSaved(ctx, k8sClient, plan, 0) // web
			testutil.EventuallyRestoreDataSaved(ctx, k8sClient, plan, 1) // app
			testutil.EventuallyRestoreDataSaved(ctx, k8sClient, plan, 2) // db

			// Verify ConfigMap exists and can be retrieved via manager
			cmKey, _ := k8sutil.ObjectKeyFromString(plan.Status.Executions[0].RestoreConfigMapRef)
			var restoreCM corev1.ConfigMap
			Expect(k8sClient.Get(ctx, cmKey, &restoreCM)).To(Succeed())

			// Manually inject some restore data to simulate real-world usage if needed
			Expect(restoreManager.Save(ctx, plan.Namespace, plan.Name, plan.Spec.Targets[0].Name, &restore.Data{
				Target: plan.Spec.Targets[0].Name,
			})).To(Succeed())

			// --- WAKEUP (Restore) ---
			By("[Wakeup] Advancing time to wakeup window")
			fakeClock.SetTime(time.Date(2026, 2, 10, 6, 1, 10, 0, time.UTC))
			testutil.TriggerReconcile(ctx, k8sClient, plan)
			testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)

			By("[Wakeup] Verifying 'db' Job is created first (reverse order)")
			jobDBWake := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "wakeup", "db")
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(jobDBWake), jobDBWake) // Ensure latest

			By("[Wakeup] Simulating 'db' success, verifying 'app' Job creation")
			testutil.SimulateJobSuccess(ctx, k8sClient, jobDBWake, fakeClock.Now())
			jobAppWake := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "wakeup", "app")
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(jobAppWake), jobAppWake) // Ensure latest

			By("[Wakeup] Simulating 'app' success, verifying 'web' Job creation")
			testutil.SimulateJobSuccess(ctx, k8sClient, jobAppWake, fakeClock.Now())
			jobWebWake := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "wakeup", "web")
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(jobWebWake), jobWebWake) // Ensure latest

			By("Verifying plan returns to Active phase")
			testutil.SimulateJobSuccess(ctx, k8sClient, jobWebWake, fakeClock.Now())
			testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)
		})
	})

	Describe("Staged Strategy", func() {
		It("Should execute targets in stages", func() {
			plan, _ = testutil.NewHibernatePlanBuilder("staged-strategy", testNamespace).
				WithSchedule("20:00", "06:00").
				WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
					Type: hibernatorv1alpha1.StrategyStaged,
					Stages: []hibernatorv1alpha1.Stage{
						{
							Name:    "stage-1",
							Targets: []string{"s-target-1"},
						},
						{
							Name:    "stage-2",
							Targets: []string{"s-target-2", "s-target-3"},
						},
					},
				}).
				WithTarget(
					hibernatorv1alpha1.Target{
						Name: "s-target-1", Type: "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
					hibernatorv1alpha1.Target{
						Name: "s-target-2", Type: "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
					hibernatorv1alpha1.Target{
						Name: "s-target-3", Type: "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
				).
				Build()

			Expect(k8sClient.Create(ctx, plan)).To(Succeed())

			By("Advancing time to hibernation window")
			fakeClock.SetTime(time.Date(2026, 2, 9, 20, 1, 10, 0, time.UTC))
			testutil.TriggerReconcile(ctx, k8sClient, plan)
			testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

			By("Verifying stage-1 Job is created")
			jobFirstStage := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "shutdown", "s-target-1")

			By("Simulating success for stage-1, verifying stage-2 Jobs creation")
			testutil.SimulateJobSuccess(ctx, k8sClient, jobFirstStage, fakeClock.Now())
			jobsSecondStage := testutil.EventuallyMultiJobsCreated(ctx, k8sClient, testNamespace, plan.Name, "shutdown", "s-target-2", "s-target-3")

			By("Simulating success for stage-2")
			testutil.SimulateJobSuccess(ctx, k8sClient, jobsSecondStage[0], fakeClock.Now())
			testutil.SimulateJobSuccess(ctx, k8sClient, jobsSecondStage[1], fakeClock.Now())

			By("Verifying plan reaches Hibernated phase")
			testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernated)
		})
	})
})
