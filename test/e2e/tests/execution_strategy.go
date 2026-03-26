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
	"k8s.io/utils/ptr"
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
					Type:           hibernatorv1alpha1.StrategyParallel,
					MaxConcurrency: ptr.To[int32](2),
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

		It("MaxConcurrency: should serialise execution when maxConcurrency=1 is set despite Parallel strategy", func() {
			mc := int32(1)
			plan, _ = testutil.NewHibernatePlanBuilder("parallel-maxconc", testNamespace).
				WithSchedule("20:00", "06:00").
				WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
					Type:           hibernatorv1alpha1.StrategyParallel,
					MaxConcurrency: &mc,
				}).
				WithTarget(
					hibernatorv1alpha1.Target{
						Name:         "pm-target-1",
						Type:         "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
					hibernatorv1alpha1.Target{
						Name:         "pm-target-2",
						Type:         "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
					hibernatorv1alpha1.Target{
						Name:         "pm-target-3",
						Type:         "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
				).
				Build()

			Expect(k8sClient.Create(ctx, plan)).To(Succeed())

			By("Advancing time to hibernation window")
			fakeClock.SetTime(time.Date(2026, 3, 9, 20, 1, 10, 0, time.UTC))
			testutil.TriggerReconcile(ctx, k8sClient, plan)
			testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

			By("Verifying exactly 1 Job is created first (maxConcurrency=1 prevents bursting)")
			var firstBatch batchv1.JobList
			Eventually(func() int {
				_ = k8sClient.List(ctx, &firstBatch, client.InNamespace(testNamespace), client.MatchingLabels{
					wellknown.LabelPlan:      plan.Name,
					wellknown.LabelOperation: "shutdown",
				})
				return len(firstBatch.Items)
			}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Equal(1))

			By("Confirming exactly 1 Job remains active (no second Job races ahead)")
			Consistently(func() int {
				var jl batchv1.JobList
				_ = k8sClient.List(ctx, &jl, client.InNamespace(testNamespace), client.MatchingLabels{
					wellknown.LabelPlan:      plan.Name,
					wellknown.LabelOperation: "shutdown",
				})
				active := 0
				for _, j := range jl.Items {
					if _, stale := j.Labels[wellknown.LabelStaleRunnerJob]; !stale {
						active++
					}
				}
				return active
			}, 2*time.Second, 250*time.Millisecond).Should(Equal(1))

			firstJob := firstBatch.Items[0]
			firstTarget := firstJob.Labels[wellknown.LabelTarget]
			testutil.SimulateJobSuccess(ctx, k8sClient, &firstJob, fakeClock.Now())

			By("Verifying a second Job is created for the next target after the first completes")
			Eventually(func() int {
				var jl batchv1.JobList
				_ = k8sClient.List(ctx, &jl, client.InNamespace(testNamespace), client.MatchingLabels{
					wellknown.LabelPlan:      plan.Name,
					wellknown.LabelOperation: "shutdown",
				})
				count := 0
				for _, j := range jl.Items {
					if _, stale := j.Labels[wellknown.LabelStaleRunnerJob]; stale {
						continue
					}
					if j.Labels[wellknown.LabelTarget] != firstTarget {
						count++
					}
				}
				return count
			}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Equal(1))
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
			jobWeb := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "web")
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(jobWeb), jobWeb) // Ensure latest

			By("[Shutdown] Simulating 'web' success, verifying 'app' Job creation")
			testutil.SimulateJobSuccess(ctx, k8sClient, jobWeb, fakeClock.Now())
			jobApp := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "app")
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(jobApp), jobApp) // Ensure latest

			By("[Shutdown] Simulating 'app' success, verifying 'db' Job creation")
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(jobApp), jobApp) // Ensure latest
			testutil.SimulateJobSuccess(ctx, k8sClient, jobApp, fakeClock.Now())
			jobDB := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "db")

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
			jobDBWake := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "db")
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(jobDBWake), jobDBWake) // Ensure latest

			By("[Wakeup] Simulating 'db' success, verifying 'app' Job creation")
			testutil.SimulateJobSuccess(ctx, k8sClient, jobDBWake, fakeClock.Now())
			jobAppWake := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "app")
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(jobAppWake), jobAppWake) // Ensure latest

			By("[Wakeup] Simulating 'app' success, verifying 'web' Job creation")
			testutil.SimulateJobSuccess(ctx, k8sClient, jobAppWake, fakeClock.Now())
			jobWebWake := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "web")
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(jobWebWake), jobWebWake) // Ensure latest

			By("Verifying plan returns to Active phase")
			testutil.SimulateJobSuccess(ctx, k8sClient, jobWebWake, fakeClock.Now())
			testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)
		})

		It("StrictMidChainFailure: should skip all downstream targets and enter PhaseError when a mid-chain node fails", func() {
			// Same topology as the happy path: web -> app -> db.
			// web succeeds, app fails — db must be skipped and the plan must error.
			plan, _ = testutil.NewHibernatePlanBuilder("dag-strict-failure", testNamespace).
				WithSchedule("20:00", "06:00").
				WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
					Type: hibernatorv1alpha1.StrategyDAG,
					Dependencies: []hibernatorv1alpha1.Dependency{
						{From: "web", To: "app"},
						{From: "app", To: "db"},
					},
				}).
				WithBehavior(hibernatorv1alpha1.Behavior{
					Mode:    hibernatorv1alpha1.BehaviorStrict,
					Retries: ptr.To(int32(1)),
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

			By("Advancing time to hibernation window")
			fakeClock.SetTime(time.Date(2026, 3, 16, 20, 1, 10, 0, time.UTC))
			testutil.TriggerReconcile(ctx, k8sClient, plan)
			testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

			// --- First attempt: web succeeds, app fails ---
			By("[Attempt-1] Simulating 'web' success")
			jobWeb1 := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "web")
			testutil.SimulateJobSuccess(ctx, k8sClient, jobWeb1, fakeClock.Now())

			By("[Attempt-1] Simulating 'app' failure — 'db' must not be scheduled")
			jobApp1 := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "app")
			testutil.SimulateJobFailure(ctx, k8sClient, jobApp1, fakeClock.Now())

			By("[Attempt-1] Verifying 'db' Job is not created (Strict mode skips downstream)")
			Consistently(func() int {
				var jl batchv1.JobList
				_ = k8sClient.List(ctx, &jl, client.InNamespace(testNamespace), client.MatchingLabels{
					wellknown.LabelPlan:      plan.Name,
					wellknown.LabelOperation: "shutdown",
					wellknown.LabelTarget:    "db",
				})
				return len(jl.Items)
			}, 2*time.Second, 250*time.Millisecond).Should(Equal(0), "'db' Job must not be created when upstream 'app' fails in Strict mode")

			// The plan auto-retries (Retries=1, first failure triggers one retry).
			By("Waiting for auto-retry (plan re-enters Hibernating)")
			testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

			// --- Retry attempt: web succeeds again, app fails again ---
			By("[Attempt-2] Simulating 'web' success")
			jobWeb2 := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "web")
			testutil.SimulateJobSuccess(ctx, k8sClient, jobWeb2, fakeClock.Now())

			By("[Attempt-2] Simulating 'app' failure again — retries exhausted")
			jobApp2 := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "app")
			testutil.SimulateJobFailure(ctx, k8sClient, jobApp2, fakeClock.Now())

			By("Verifying plan enters PhaseError (retries exhausted, 'db' was never scheduled)")
			testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseError)

			By("Confirming 'db' Job was never created throughout the entire failure cycle")
			var dbJobs batchv1.JobList
			_ = k8sClient.List(ctx, &dbJobs, client.InNamespace(testNamespace), client.MatchingLabels{
				wellknown.LabelPlan:      plan.Name,
				wellknown.LabelOperation: "shutdown",
				wellknown.LabelTarget:    "db",
			})
			Expect(dbJobs.Items).To(BeEmpty(), "'db' must never be scheduled when its dependency 'app' always fails")
		})

		// Multi-branch topology: a→b, a→c, b→d, c→e, d→f
		// PlanDAG produces 4 stages: [a], [b,c], [d,e], [f]
		//
		// Strict test: 'b' fails at stage 1 → AsPlanError → PhaseError
		// BestEffort test: 'b' fails → c succeeds → d,f pruned → e succeeds → PhaseHibernated (partial success)
		It("StrictMultiBranch: should enter PhaseError when one branch fails in a multi-branch DAG", func() {
			plan, _ = testutil.NewHibernatePlanBuilder("dag-strict-multi", testNamespace).
				WithSchedule("20:00", "06:00").
				WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
					Type:           hibernatorv1alpha1.StrategyDAG,
					MaxConcurrency: ptr.To[int32](2),
					Dependencies: []hibernatorv1alpha1.Dependency{
						{From: "a", To: "b"},
						{From: "a", To: "c"},
						{From: "b", To: "d"},
						{From: "c", To: "e"},
						{From: "d", To: "f"},
					},
				}).
				WithBehavior(hibernatorv1alpha1.Behavior{
					Mode:    hibernatorv1alpha1.BehaviorStrict,
					Retries: ptr.To[int32](0),
				}).
				WithTarget(
					hibernatorv1alpha1.Target{
						Name: "a", Type: "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
					hibernatorv1alpha1.Target{
						Name: "b", Type: "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
					hibernatorv1alpha1.Target{
						Name: "c", Type: "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
					hibernatorv1alpha1.Target{
						Name: "d", Type: "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
					hibernatorv1alpha1.Target{
						Name: "e", Type: "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
					hibernatorv1alpha1.Target{
						Name: "f", Type: "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
				).
				Build()

			Expect(k8sClient.Create(ctx, plan)).To(Succeed())

			By("Advancing time to hibernation window")
			fakeClock.SetTime(time.Date(2026, 3, 16, 20, 1, 10, 0, time.UTC))
			testutil.TriggerReconcile(ctx, k8sClient, plan)
			testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

			By("[Stage 0] Simulating 'a' success")
			jobA := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "a")
			testutil.SimulateJobSuccess(ctx, k8sClient, jobA, fakeClock.Now())

			By("[Stage 1] Waiting for 'b' and 'c' Jobs (dispatched in parallel)")
			jobsBC := testutil.EventuallyMultiJobsCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "b", "c")

			By("[Stage 1] Simulating 'b' failure — Strict mode should halt immediately")
			for _, j := range jobsBC {
				switch j.Labels[wellknown.LabelTarget] {
				case "b":
					testutil.SimulateJobFailure(ctx, k8sClient, j, fakeClock.Now())
				case "c":
					testutil.SimulateJobSuccess(ctx, k8sClient, j, fakeClock.Now())
				}
			}

			By("Verifying plan enters PhaseError (Strict: stage 1 failure blocks entire plan)")
			testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseError)

			By("Confirming 'd', 'e', 'f' Jobs were never created")
			for _, target := range []string{"d", "e", "f"} {
				var jl batchv1.JobList
				_ = k8sClient.List(ctx, &jl, client.InNamespace(testNamespace), client.MatchingLabels{
					wellknown.LabelPlan:      plan.Name,
					wellknown.LabelOperation: "shutdown",
					wellknown.LabelTarget:    target,
				})
				Expect(jl.Items).To(BeEmpty(), "Job for target %q must not be created when upstream fails in Strict mode", target)
			}
		})

		It("BestEffortMultiBranch: should prune failed branch and continue independent branches", func() {
			plan, _ = testutil.NewHibernatePlanBuilder("dag-besteffort-multi", testNamespace).
				WithSchedule("20:00", "06:00").
				WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
					Type:           hibernatorv1alpha1.StrategyDAG,
					MaxConcurrency: ptr.To[int32](2),
					Dependencies: []hibernatorv1alpha1.Dependency{
						{From: "a", To: "b"},
						{From: "a", To: "c"},
						{From: "b", To: "d"},
						{From: "c", To: "e"},
						{From: "d", To: "f"},
					},
				}).
				WithBehavior(hibernatorv1alpha1.Behavior{
					Mode:    hibernatorv1alpha1.BehaviorBestEffort,
					Retries: ptr.To[int32](0),
				}).
				WithTarget(
					hibernatorv1alpha1.Target{
						Name: "a", Type: "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
					hibernatorv1alpha1.Target{
						Name: "b", Type: "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
					hibernatorv1alpha1.Target{
						Name: "c", Type: "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
					hibernatorv1alpha1.Target{
						Name: "d", Type: "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
					hibernatorv1alpha1.Target{
						Name: "e", Type: "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
					hibernatorv1alpha1.Target{
						Name: "f", Type: "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
				).
				Build()

			Expect(k8sClient.Create(ctx, plan)).To(Succeed())

			By("Advancing time to hibernation window")
			fakeClock.SetTime(time.Date(2026, 3, 16, 20, 1, 10, 0, time.UTC))
			testutil.TriggerReconcile(ctx, k8sClient, plan)
			testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

			By("[Stage 0] Simulating 'a' success")
			jobA := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "a")
			testutil.SimulateJobSuccess(ctx, k8sClient, jobA, fakeClock.Now())

			By("[Stage 1] Waiting for 'b' and 'c' Jobs")
			jobsBC := testutil.EventuallyMultiJobsCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "b", "c")

			By("[Stage 1] Simulating 'b' failure and 'c' success")
			for _, j := range jobsBC {
				switch j.Labels[wellknown.LabelTarget] {
				case "b":
					testutil.SimulateJobFailure(ctx, k8sClient, j, fakeClock.Now())
				case "c":
					testutil.SimulateJobSuccess(ctx, k8sClient, j, fakeClock.Now())
				}
			}

			By("[Stage 2] 'd' should be pruned (upstream 'b' failed), 'e' should execute (upstream 'c' ok)")
			jobE := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "e")
			testutil.SimulateJobSuccess(ctx, k8sClient, jobE, fakeClock.Now())

			// Verify 'd' was pruned — no Job created, state is Aborted with abort message.
			Consistently(func() int {
				var jl batchv1.JobList
				_ = k8sClient.List(ctx, &jl, client.InNamespace(testNamespace), client.MatchingLabels{
					wellknown.LabelPlan:      plan.Name,
					wellknown.LabelOperation: "shutdown",
					wellknown.LabelTarget:    "d",
				})
				return len(jl.Items)
			}, 2*time.Second, 250*time.Millisecond).Should(Equal(0), "'d' Job must not be created (pruned: upstream 'b' failed)")

			By("[Stage 3] 'f' should be pruned (upstream 'd' was pruned)")
			// 'f' depends on 'd' which was pruned (StateAborted) — cascade prune.
			Consistently(func() int {
				var jl batchv1.JobList
				_ = k8sClient.List(ctx, &jl, client.InNamespace(testNamespace), client.MatchingLabels{
					wellknown.LabelPlan:      plan.Name,
					wellknown.LabelOperation: "shutdown",
					wellknown.LabelTarget:    "f",
				})
				return len(jl.Items)
			}, 2*time.Second, 250*time.Millisecond).Should(Equal(0), "'f' Job must not be created (pruned: upstream 'd' was pruned)")

			By("Verifying execution states: a=Completed, b=Failed, c=Completed, d=Aborted, e=Completed, f=Aborted")
			// Execution indices follow spec target order: a=0, b=1, c=2, d=3, e=4, f=5
			testutil.EventuallyTargetState(ctx, k8sClient, plan, 0, hibernatorv1alpha1.StateCompleted) // a
			testutil.EventuallyTargetState(ctx, k8sClient, plan, 1, hibernatorv1alpha1.StateFailed)    // b (job failure)
			testutil.EventuallyTargetState(ctx, k8sClient, plan, 2, hibernatorv1alpha1.StateCompleted) // c
			testutil.EventuallyTargetState(ctx, k8sClient, plan, 3, hibernatorv1alpha1.StateAborted)   // d (aborted: upstream b failed)
			testutil.EventuallyTargetState(ctx, k8sClient, plan, 4, hibernatorv1alpha1.StateCompleted) // e
			testutil.EventuallyTargetState(ctx, k8sClient, plan, 5, hibernatorv1alpha1.StateAborted)   // f (aborted: upstream d aborted)

			// Voluntary trigger to reflect the state changes in the plan status after job simulations
			testutil.TriggerReconcile(ctx, k8sClient, plan)

			By("Verifying plan reaches PhaseHibernated (BestEffort: partial success)")
			testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernated)

			By("Verifying aborted targets have 'Aborted' message")
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			Expect(plan.Status.Executions[3].Message).To(ContainSubstring("Aborted"), "target 'd' should have abort message")
			Expect(plan.Status.Executions[5].Message).To(ContainSubstring("Aborted"), "target 'f' should have abort message")
		})
	})

	Describe("Staged Strategy", func() {
		It("Should execute targets in stages", func() {
			plan, _ = testutil.NewHibernatePlanBuilder("staged-strategy", testNamespace).
				WithSchedule("20:00", "06:00").
				WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
					Type:           hibernatorv1alpha1.StrategyStaged,
					MaxConcurrency: ptr.To[int32](2),
					Stages: []hibernatorv1alpha1.Stage{
						{
							Name:    "stage-1",
							Targets: []string{"s-target-1"},
						},
						{
							Name:     "stage-2",
							Parallel: true,
							Targets:  []string{"s-target-2", "s-target-3"},
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
			jobFirstStage := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "s-target-1")

			By("Simulating success for stage-1, verifying stage-2 Jobs creation")
			testutil.SimulateJobSuccess(ctx, k8sClient, jobFirstStage, fakeClock.Now())
			jobsSecondStage := testutil.EventuallyMultiJobsCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "s-target-2", "s-target-3")

			By("Simulating success for stage-2")
			testutil.SimulateJobSuccess(ctx, k8sClient, jobsSecondStage[0], fakeClock.Now())
			testutil.SimulateJobSuccess(ctx, k8sClient, jobsSecondStage[1], fakeClock.Now())

			By("Verifying plan reaches Hibernated phase")
			testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernated)
		})

		It("WakeupOrdering: should execute wakeup stages in reverse order (last stage first)", func() {
			// Stages on shutdown: stage-1 (s-target-1) then stage-2 (s-target-2 + s-target-3).
			// Expected wakeup order (reversed): stage-2 (s-target-2 + s-target-3) then stage-1 (s-target-1).
			plan, _ = testutil.NewHibernatePlanBuilder("staged-wakeup", testNamespace).
				WithSchedule("20:00", "06:00").
				WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
					Type:           hibernatorv1alpha1.StrategyStaged,
					MaxConcurrency: ptr.To[int32](2),
					Stages: []hibernatorv1alpha1.Stage{
						{Name: "stage-1", Parallel: true, Targets: []string{"sw-target-1"}},
						{Name: "stage-2", Parallel: true, Targets: []string{"sw-target-2", "sw-target-3"}},
					},
				}).
				WithTarget(
					hibernatorv1alpha1.Target{
						Name: "sw-target-1", Type: "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
					hibernatorv1alpha1.Target{
						Name: "sw-target-2", Type: "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
					hibernatorv1alpha1.Target{
						Name: "sw-target-3", Type: "noop",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "strategy-aws"},
					},
				).
				Build()

			Expect(k8sClient.Create(ctx, plan)).To(Succeed())

			// --- SHUTDOWN ---
			By("[Shutdown] Advancing time to hibernation window")
			fakeClock.SetTime(time.Date(2026, 3, 23, 20, 1, 10, 0, time.UTC))
			testutil.TriggerReconcile(ctx, k8sClient, plan)
			testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

			By("[Shutdown] Completing stage-1 (sw-target-1)")
			jobStage1 := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "sw-target-1")
			testutil.SimulateJobSuccess(ctx, k8sClient, jobStage1, fakeClock.Now())

			By("[Shutdown] Completing stage-2 (sw-target-2 and sw-target-3 in parallel)")
			jobsStage2 := testutil.EventuallyMultiJobsCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "sw-target-2", "sw-target-3")
			testutil.SimulateJobSuccess(ctx, k8sClient, jobsStage2[0], fakeClock.Now())
			testutil.SimulateJobSuccess(ctx, k8sClient, jobsStage2[1], fakeClock.Now())

			By("[Shutdown] Waiting for all targets to reach Hibernated with restore data")
			testutil.EventuallyRestoreDataSaved(ctx, k8sClient, plan, 0)
			testutil.EventuallyRestoreDataSaved(ctx, k8sClient, plan, 1)
			testutil.EventuallyRestoreDataSaved(ctx, k8sClient, plan, 2)

			// Inject restore data so the wakeup phase can proceed.
			for _, target := range plan.Spec.Targets {
				Expect(restoreManager.Save(ctx, plan.Namespace, plan.Name, target.Name, &restore.Data{
					Target: target.Name,
				})).To(Succeed())
			}

			// --- WAKEUP ---
			By("[Wakeup] Advancing clock to wakeup window")
			fakeClock.SetTime(time.Date(2026, 3, 24, 6, 1, 10, 0, time.UTC))
			testutil.TriggerReconcile(ctx, k8sClient, plan)
			testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)

			By("[Wakeup] Verifying stage-2 Jobs are created first (reverse order: sw-target-2 + sw-target-3)")
			wakeStage2Jobs := testutil.EventuallyMultiJobsCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "sw-target-2", "sw-target-3")

			By("[Wakeup] Confirming sw-target-1 Job is NOT yet created while stage-2 is in progress")
			Consistently(func() int {
				var jl batchv1.JobList
				_ = k8sClient.List(ctx, &jl, client.InNamespace(testNamespace), client.MatchingLabels{
					wellknown.LabelPlan:      plan.Name,
					wellknown.LabelOperation: hibernatorv1alpha1.OperationWakeUp,
					wellknown.LabelTarget:    "sw-target-1",
				})
				return len(jl.Items)
			}, 2*time.Second, 250*time.Millisecond).Should(Equal(0), "stage-1 wakeup Job must not appear before stage-2 completes")

			By("[Wakeup] Completing stage-2 wakeup Jobs")
			testutil.SimulateJobSuccess(ctx, k8sClient, wakeStage2Jobs[0], fakeClock.Now())
			testutil.SimulateJobSuccess(ctx, k8sClient, wakeStage2Jobs[1], fakeClock.Now())

			By("[Wakeup] Verifying stage-1 Job is now created (sw-target-1)")
			wakeStage1Job := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "sw-target-1")
			testutil.SimulateJobSuccess(ctx, k8sClient, wakeStage1Job, fakeClock.Now())

			By("[Wakeup] Verifying plan returns to Active phase")
			testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)
		})
	})
})
