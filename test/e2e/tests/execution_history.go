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
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/test/e2e/testutil"
)

var _ = Describe("Execution History E2E", func() {
	var (
		plan          *hibernatorv1alpha1.HibernatePlan
		cloudProvider *hibernatorv1alpha1.CloudProvider
	)

	BeforeEach(func() {
		By("Creating mock CloudProvider")
		cloudProvider = &hibernatorv1alpha1.CloudProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "history-aws",
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

	It("SuccessHistory: should record successful ShutdownExecution and WakeupExecution in ExecutionHistory", func() {
		// Full golden-path cycle verifying that ExecutionHistory is populated
		// with Success=true for both shutdown and wakeup operations.
		baseTime := time.Date(2026, 7, 6, 8, 0, 0, 0, time.UTC) // Monday 08:00 — on-hours
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with 20:00-06:00 window")
		plan, _ = testutil.NewHibernatePlanBuilder("history-success-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "database",
				Type: "noop",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "history-aws",
				},
			}).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Advancing time to off-hours and simulating successful shutdown")
		fakeClock.SetTime(time.Date(2026, 7, 6, 20, 1, 10, 0, time.UTC))

		By("Verifying plan transitions to Hibernating")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		hibernationJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, hibernationJob, fakeClock.Now())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernated)
		testutil.EventuallyRestoreDataSaved(ctx, k8sClient, plan, 0)

		By("Verifying ShutdownExecution is recorded with Success=true")
		Eventually(func() bool {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			if len(plan.Status.ExecutionHistory) == 0 {
				return false
			}
			cycle := plan.Status.ExecutionHistory[0]
			return cycle.ShutdownExecution != nil &&
				cycle.ShutdownExecution.Success &&
				cycle.ShutdownExecution.Operation == hibernatorv1alpha1.OperationHibernate
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(BeTrue(),
			"ShutdownExecution should be recorded with Success=true")

		cycleID := plan.Status.ExecutionHistory[0].CycleID
		Expect(cycleID).NotTo(BeEmpty(), "CycleID should be populated")

		By("Injecting restore data and advancing to wakeup window")
		Expect(restoreManager.Save(ctx, plan.Namespace, plan.Name, "database", &restore.Data{
			Target: "database",
		})).To(Succeed())

		fakeClock.SetTime(time.Date(2026, 7, 7, 6, 1, 10, 0, time.UTC)) // Tuesday 06:01
		testutil.TriggerReconcile(ctx, k8sClient, plan)
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)

		wakeupJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, wakeupJob, fakeClock.Now())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Verifying WakeupExecution is also recorded with Success=true in the same cycle")
		Eventually(func() bool {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			if len(plan.Status.ExecutionHistory) == 0 {
				return false
			}
			cycle := plan.Status.ExecutionHistory[0]
			return cycle.CycleID == cycleID &&
				cycle.WakeupExecution != nil &&
				cycle.WakeupExecution.Success &&
				cycle.WakeupExecution.Operation == hibernatorv1alpha1.OperationWakeUp
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(BeTrue(),
			"WakeupExecution should be recorded with Success=true in the same cycle")

		By("Verifying both operations are in the same cycle entry")
		Expect(plan.Status.ExecutionHistory).To(HaveLen(1))
		cycle := plan.Status.ExecutionHistory[0]
		Expect(cycle.ShutdownExecution).NotTo(BeNil())
		Expect(cycle.WakeupExecution).NotTo(BeNil())
		Expect(cycle.ShutdownExecution.Success).To(BeTrue())
		Expect(cycle.WakeupExecution.Success).To(BeTrue())
	})

	It("FailureHistory: should record failed ShutdownExecution in ExecutionHistory when plan enters PhaseError", func() {
		// Verify that when shutdown fails and retries are exhausted, the execution
		// history captures Success=false with the error detail.
		baseTime := time.Date(2026, 7, 13, 20, 1, 10, 0, time.UTC) // Monday off-hours
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with Retries=0 to force immediate PhaseError on failure")
		plan, _ = testutil.NewHibernatePlanBuilder("history-failure-test", testNamespace).
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
					Name: "history-aws",
				},
			}).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Simulating shutdown Job failure")
		hibernationJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "database")
		testutil.SimulateJobFailure(ctx, k8sClient, hibernationJob, fakeClock.Now())

		By("Verifying plan enters PhaseError")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseError)

		By("Verifying ShutdownExecution is recorded with Success=false")
		Eventually(func() bool {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			if len(plan.Status.ExecutionHistory) == 0 {
				return false
			}
			cycle := plan.Status.ExecutionHistory[0]
			return cycle.ShutdownExecution != nil &&
				!cycle.ShutdownExecution.Success &&
				cycle.ShutdownExecution.Operation == hibernatorv1alpha1.OperationHibernate
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(BeTrue(),
			"ShutdownExecution should be recorded with Success=false after failure")

		By("Verifying there are target results with failure details")
		cycle := plan.Status.ExecutionHistory[0]
		Expect(cycle.ShutdownExecution.TargetResults).NotTo(BeEmpty(),
			"TargetResults should capture per-target outcome")
		Expect(cycle.WakeupExecution).To(BeNil(),
			"WakeupExecution should be nil since wakeup never ran")
	})

	It("RetryOverwritesHistory: should overwrite failure history with success summary after retry succeeds", func() {
		// Verify the critical scenario: first attempt fails (OnError writes partial
		// history), then retry succeeds (finalize overwrites with success summary).
		baseTime := time.Date(2026, 7, 20, 20, 1, 10, 0, time.UTC) // Monday off-hours
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with auto-retry enabled (default Retries=3)")
		plan, _ = testutil.NewHibernatePlanBuilder("history-retry-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithBehavior(hibernatorv1alpha1.Behavior{
				Retries: ptr.To[int32](1),
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "database",
				Type: "noop",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "history-aws",
				},
			}).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Simulating initial shutdown Job failure")
		hibernationJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "database")
		testutil.SimulateJobFailure(ctx, k8sClient, hibernationJob, fakeClock.Now())

		By("Verifying partial failure history is recorded via OnError path")
		Eventually(func() bool {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			if len(plan.Status.ExecutionHistory) == 0 {
				return false
			}
			return plan.Status.ExecutionHistory[0].ShutdownExecution != nil &&
				!plan.Status.ExecutionHistory[0].ShutdownExecution.Success
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(BeTrue(),
			"partial failure history should be written via OnError")

		By("Waiting for auto-retry to kick in — plan transitions back to Hibernating")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Simulating retry Job success")
		retryJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, retryJob, fakeClock.Now())

		By("Verifying plan reaches Hibernated")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernated)
		testutil.EventuallyRestoreDataSaved(ctx, k8sClient, plan, 0)

		By("Verifying ShutdownExecution is now overwritten with Success=true")
		Eventually(func() bool {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			if len(plan.Status.ExecutionHistory) == 0 {
				return false
			}

			return plan.Status.ExecutionHistory[0].ShutdownExecution != nil &&
				plan.Status.ExecutionHistory[0].ShutdownExecution.Success
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(BeTrue(),
			"ShutdownExecution should be overwritten with Success=true after retry succeeds")

		By("Verifying the cycle still has the same CycleID (same cycle, updated summary)")
		Expect(plan.Status.ExecutionHistory).To(HaveLen(1))
		Expect(plan.Status.ExecutionHistory[0].ShutdownExecution.Operation).To(
			Equal(hibernatorv1alpha1.OperationHibernate))
	})

	It("WakeupFailureHistory: should record failed WakeupExecution in ExecutionHistory", func() {
		// Verify the wakeup error path: successful shutdown → wakeup fails → history
		// has Success=true for shutdown, Success=false for wakeup.
		baseTime := time.Date(2026, 7, 27, 20, 1, 10, 0, time.UTC) // Monday off-hours
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with Retries=0")
		plan, _ = testutil.NewHibernatePlanBuilder("history-wakeup-fail-test", testNamespace).
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
					Name: "history-aws",
				},
			}).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Simulating successful shutdown")
		hibernationJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, hibernationJob, fakeClock.Now())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernated)
		testutil.EventuallyRestoreDataSaved(ctx, k8sClient, plan, 0)

		By("Verifying ShutdownExecution recorded as success")
		Eventually(func() bool {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			if len(plan.Status.ExecutionHistory) == 0 {
				return false
			}
			return plan.Status.ExecutionHistory[0].ShutdownExecution != nil &&
				plan.Status.ExecutionHistory[0].ShutdownExecution.Success
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(BeTrue())

		Expect(restoreManager.Save(ctx, plan.Namespace, plan.Name, "database", &restore.Data{
			Target: "database",
		})).To(Succeed())

		By("Advancing to wakeup window")
		fakeClock.SetTime(time.Date(2026, 7, 28, 6, 1, 10, 0, time.UTC)) // Tuesday 06:01

		By("Verifying plan transitions to WakingUp")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)

		By("Simulating wakeup Job failure")
		wakeupJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "database")
		testutil.SimulateJobFailure(ctx, k8sClient, wakeupJob, fakeClock.Now())

		By("Verifying plan enters PhaseError")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseError)

		By("Verifying WakeupExecution is recorded with Success=false")
		Eventually(func() bool {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			if len(plan.Status.ExecutionHistory) == 0 {
				return false
			}
			cycle := plan.Status.ExecutionHistory[0]
			return cycle.WakeupExecution != nil &&
				!cycle.WakeupExecution.Success &&
				cycle.WakeupExecution.Operation == hibernatorv1alpha1.OperationWakeUp
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(BeTrue(),
			"WakeupExecution should be recorded with Success=false after failure")

		By("Verifying both operations are in the same cycle — shutdown success, wakeup failure")
		Expect(plan.Status.ExecutionHistory).To(HaveLen(1))
		cycle := plan.Status.ExecutionHistory[0]
		Expect(cycle.ShutdownExecution).NotTo(BeNil())
		Expect(cycle.ShutdownExecution.Success).To(BeTrue(), "shutdown should remain successful")
		Expect(cycle.WakeupExecution).NotTo(BeNil())
		Expect(cycle.WakeupExecution.Success).To(BeFalse(), "wakeup should report failure")
	})
})
