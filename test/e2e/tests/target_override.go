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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/ardikabs/hibernator/test/e2e/testutil"
)

var _ = Describe("TargetOverride E2E", func() {
	var (
		plan          *hibernatorv1alpha1.HibernatePlan
		cloudProvider *hibernatorv1alpha1.CloudProvider
		exception     *hibernatorv1alpha1.ScheduleException
	)

	BeforeEach(func() {
		By("Creating mock CloudProvider")
		cloudProvider = &hibernatorv1alpha1.CloudProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "override-aws",
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
		testutil.EnsureDeleted(ctx, k8sClient, exception)
		testutil.EnsureDeleted(ctx, k8sClient, plan)
		testutil.EnsureDeleted(ctx, k8sClient, cloudProvider)
	})

	It("SuspendDisabled: should wake only enabled targets and seed skipped executions", func() {
		baseTime := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC) // Monday
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with two noop targets")
		plan, _ = testutil.NewHibernatePlanBuilder("override-suspend-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(
				hibernatorv1alpha1.Target{
					Name: "db-0",
					Type: "noop",
					ConnectorRef: hibernatorv1alpha1.ConnectorRef{
						Kind: "CloudProvider",
						Name: "override-aws",
					},
				},
				hibernatorv1alpha1.Target{
					Name: "db-1",
					Type: "noop",
					ConnectorRef: hibernatorv1alpha1.ConnectorRef{
						Kind: "CloudProvider",
						Name: "override-aws",
					},
				},
			).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Hibernating both targets (Monday night)")
		fakeClock.SetTime(time.Date(2026, 3, 2, 21, 0, 0, 0, time.UTC))
		testutil.SimulateHibernation(ctx, k8sClient, plan, restoreManager, fakeClock.Now(), "db-0", "db-1")

		By("Creating suspend exception disabling db-1")
		exception = testutil.NewScheduleExceptionBuilder("override-suspend", testNamespace, plan.Name).
			WithType(hibernatorv1alpha1.ExceptionSuspend).
			WithValidity(baseTime.Add(-1*time.Hour), baseTime.Add(48*time.Hour)).
			WithWindows(hibernatorv1alpha1.OffHourWindow{
				Start:      "00:00",
				End:        "23:59",
				DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
			}).
			WithTargetOverrides(hibernatorv1alpha1.TargetOverride{
				TargetName: "db-1",
				Disabled:   true,
			}).
			Build()

		Expect(k8sClient.Create(ctx, exception)).To(Succeed())
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateActive)

		By("Triggering wakeup while suspend is active")
		testutil.TriggerReconcile(ctx, k8sClient, plan)
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)

		By("Verifying db-1 is seeded skipped and stays listed")
		Eventually(func() bool {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			if len(plan.Status.Executions) != 2 {
				return false
			}
			for _, e := range plan.Status.Executions {
				if e.Target == "db-1" {
					return e.State == hibernatorv1alpha1.StateSkipped &&
						strings.Contains(e.Message, "Skipped: disabled by exception")
				}
			}
			return false
		}).
			WithTimeout(testutil.DefaultTimeout).
			WithPolling(testutil.DefaultInterval).
			Should(BeTrueBecause("db-1 should be seeded skipped"))

		By("Verifying a wakeup Job is created for db-0 only")
		job := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "db-0")
		Expect(job).NotTo(BeNil())

		By("Completing the db-0 wakeup Job")
		testutil.SimulateJobSuccess(ctx, k8sClient, job, fakeClock.Now())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Confirming no wakeup Job was ever created for db-1")
		var jl batchv1.JobList
		_ = k8sClient.List(ctx, &jl, client.InNamespace(testNamespace), client.MatchingLabels{
			wellknown.LabelPlan:      plan.Name,
			wellknown.LabelOperation: string(hibernatorv1alpha1.OperationWakeUp),
			wellknown.LabelTarget:    "db-1",
		})
		Expect(jl.Items).To(BeEmpty(), "no wakeup Job must be created for disabled target db-1")
	})

	It("SuspendDisabledOnDAGUpstream: should treat disabled as succeeded and complete", func() {
		baseTime := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC) // Monday
		fakeClock.SetTime(baseTime)

		By("Creating DAG HibernatePlan a->b with noop targets")
		plan, _ = testutil.NewHibernatePlanBuilder("override-dag-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategyDAG,
				Dependencies: []hibernatorv1alpha1.Dependency{
					{From: "a", To: "b"},
				},
			}).
			WithTarget(
				hibernatorv1alpha1.Target{
					Name: "a",
					Type: "noop",
					ConnectorRef: hibernatorv1alpha1.ConnectorRef{
						Kind: "CloudProvider",
						Name: "override-aws",
					},
				},
				hibernatorv1alpha1.Target{
					Name: "b",
					Type: "noop",
					ConnectorRef: hibernatorv1alpha1.ConnectorRef{
						Kind: "CloudProvider",
						Name: "override-aws",
					},
				},
			).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Hibernating both targets (Monday night)")
		fakeClock.SetTime(time.Date(2026, 3, 2, 21, 0, 0, 0, time.UTC))
		testutil.SimulateHibernation(ctx, k8sClient, plan, restoreManager, fakeClock.Now(), "a", "b")

		By("Creating suspend exception disabling upstream target a")
		exception = testutil.NewScheduleExceptionBuilder("override-dag", testNamespace, plan.Name).
			WithType(hibernatorv1alpha1.ExceptionSuspend).
			WithValidity(baseTime.Add(-1*time.Hour), baseTime.Add(48*time.Hour)).
			WithWindows(hibernatorv1alpha1.OffHourWindow{
				Start:      "00:00",
				End:        "23:59",
				DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
			}).
			WithTargetOverrides(hibernatorv1alpha1.TargetOverride{
				TargetName: "a",
				Disabled:   true,
			}).
			Build()

		Expect(k8sClient.Create(ctx, exception)).To(Succeed())
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateActive)

		By("Triggering wakeup while suspend is active")
		testutil.TriggerReconcile(ctx, k8sClient, plan)
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)

		By("Completing the downstream wakeup Job (upstream seeded succeeded)")
		job := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "b")
		testutil.SimulateJobSuccess(ctx, k8sClient, job, fakeClock.Now())

		By("Verifying plan reaches Active (never PhaseError)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)
	})

	It("SuspendParamsOnStagedNoDisabled: should override specified params, leave others as-is", func() {
		baseTime := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC) // Monday
		fakeClock.SetTime(baseTime)

		By("Creating Staged HibernatePlan with base params on all targets")
		plan, _ = testutil.NewHibernatePlanBuilder("override-staged-params", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategyStaged,
				Stages: []hibernatorv1alpha1.Stage{
					{Name: "stage-a", Parallel: true, Targets: []string{"t0", "t1"}},
					{Name: "stage-b", Parallel: false, Targets: []string{"t2"}},
				},
			}).
			WithTarget(
				stagedNoopTarget("t0", `{"env":"base"}`),
				stagedNoopTarget("t1", `{"env":"base"}`),
				stagedNoopTarget("t2", `{"env":"base"}`),
			).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Hibernating all targets (Monday night)")
		fakeClock.SetTime(time.Date(2026, 3, 2, 21, 0, 0, 0, time.UTC))
		testutil.SimulateHibernation(ctx, k8sClient, plan, restoreManager, fakeClock.Now(), "t0", "t1", "t2")

		By("Creating suspend exception overriding params on t2 only")
		exception = testutil.NewScheduleExceptionBuilder("override-staged-p", testNamespace, plan.Name).
			WithType(hibernatorv1alpha1.ExceptionSuspend).
			WithValidity(baseTime.Add(-1*time.Hour), baseTime.Add(48*time.Hour)).
			WithWindows(hibernatorv1alpha1.OffHourWindow{
				Start:      "00:00",
				End:        "23:59",
				DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
			}).
			WithTargetOverrides(hibernatorv1alpha1.TargetOverride{
				TargetName: "t2",
				Parameters: &hibernatorv1alpha1.Parameters{Raw: []byte(`{"env":"weekend"}`)},
			}).
			Build()

		Expect(k8sClient.Create(ctx, exception)).To(Succeed())
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateActive)

		By("Triggering wakeup while suspend is active (wakeup runs stages reversed: t2 first)")
		testutil.TriggerReconcile(ctx, k8sClient, plan)
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)

		By("Verifying overridden params on the first-stage Job")
		jobT2 := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "t2")
		assertJobParams([]*batchv1.Job{jobT2}, "t2", `{"env":"weekend"}`)
		testutil.SimulateJobSuccess(ctx, k8sClient, jobT2, fakeClock.Now())

		By("Verifying as-is params on the second-stage Jobs")
		jobs := testutil.EventuallyMultiJobsCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "t0", "t1")
		Expect(jobs).To(HaveLen(2))
		assertJobParams(jobs, "t0", `{"env":"base"}`)
		assertJobParams(jobs, "t1", `{"env":"base"}`)

		By("Completing all wakeup Jobs")
		for _, j := range jobs {
			testutil.SimulateJobSuccess(ctx, k8sClient, j, fakeClock.Now())
		}
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)
	})

	It("SuspendDisabledOnStaged: should skip disabled in stage and keep others as-is", func() {
		baseTime := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC) // Monday
		fakeClock.SetTime(baseTime)

		By("Creating Staged HibernatePlan with base params on all targets")
		plan, _ = testutil.NewHibernatePlanBuilder("override-staged-dis", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategyStaged,
				Stages: []hibernatorv1alpha1.Stage{
					{Name: "stage-a", Parallel: true, Targets: []string{"t0", "t1"}},
					{Name: "stage-b", Parallel: false, Targets: []string{"t2"}},
				},
			}).
			WithTarget(
				stagedNoopTarget("t0", `{"env":"base"}`),
				stagedNoopTarget("t1", `{"env":"base"}`),
				stagedNoopTarget("t2", `{"env":"base"}`),
			).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Hibernating all targets (Monday night)")
		fakeClock.SetTime(time.Date(2026, 3, 2, 21, 0, 0, 0, time.UTC))
		testutil.SimulateHibernation(ctx, k8sClient, plan, restoreManager, fakeClock.Now(), "t0", "t1", "t2")

		By("Creating suspend exception disabling t1 and overriding params on t2")
		exception = testutil.NewScheduleExceptionBuilder("override-staged-d", testNamespace, plan.Name).
			WithType(hibernatorv1alpha1.ExceptionSuspend).
			WithValidity(baseTime.Add(-1*time.Hour), baseTime.Add(48*time.Hour)).
			WithWindows(hibernatorv1alpha1.OffHourWindow{
				Start:      "00:00",
				End:        "23:59",
				DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
			}).
			WithTargetOverrides(
				hibernatorv1alpha1.TargetOverride{TargetName: "t1", Disabled: true},
				hibernatorv1alpha1.TargetOverride{
					TargetName: "t2",
					Parameters: &hibernatorv1alpha1.Parameters{Raw: []byte(`{"env":"weekend"}`)},
				},
			).
			Build()

		Expect(k8sClient.Create(ctx, exception)).To(Succeed())
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateActive)

		By("Triggering wakeup while suspend is active (wakeup runs stages reversed: t2 first)")
		testutil.TriggerReconcile(ctx, k8sClient, plan)
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)

		By("Verifying overridden params on the first-stage Job")
		jobT2 := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "t2")
		assertJobParams([]*batchv1.Job{jobT2}, "t2", `{"env":"weekend"}`)
		testutil.SimulateJobSuccess(ctx, k8sClient, jobT2, fakeClock.Now())

		By("Verifying t1 seeded skipped while t0 runs with as-is params")
		jobT0 := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "t0")
		assertJobParams([]*batchv1.Job{jobT0}, "t0", `{"env":"base"}`)
		testutil.SimulateJobSuccess(ctx, k8sClient, jobT0, fakeClock.Now())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Confirming no wakeup Job was ever created for t1")
		var jl batchv1.JobList
		_ = k8sClient.List(ctx, &jl, client.InNamespace(testNamespace), client.MatchingLabels{
			wellknown.LabelPlan:      plan.Name,
			wellknown.LabelOperation: string(hibernatorv1alpha1.OperationWakeUp),
			wellknown.LabelTarget:    "t1",
		})
		Expect(jl.Items).To(BeEmpty(), "no wakeup Job must be created for disabled target t1")
	})

	It("ExtendDisabledAndParams: should skip disabled target and apply params override on extend-triggered hibernate", func() {
		baseTime := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC) // Monday
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with base params on two targets")
		plan, _ = testutil.NewHibernatePlanBuilder("override-extend-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(
				stagedNoopTarget("db-0", `{"env":"base"}`),
				stagedNoopTarget("db-1", `{"env":"base"}`),
			).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Creating extend exception with daytime window plus target overrides")
		exception = testutil.NewScheduleExceptionBuilder("override-extend", testNamespace, plan.Name).
			WithType(hibernatorv1alpha1.ExceptionExtend).
			WithValidity(baseTime.Add(-1*time.Hour), baseTime.Add(48*time.Hour)).
			WithWindows(hibernatorv1alpha1.OffHourWindow{
				Start:      "09:00",
				End:        "18:00",
				DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
			}).
			WithTargetOverrides(
				hibernatorv1alpha1.TargetOverride{TargetName: "db-1", Disabled: true},
				hibernatorv1alpha1.TargetOverride{
					TargetName: "db-0",
					Parameters: &hibernatorv1alpha1.Parameters{Raw: []byte(`{"env":"event"}`)},
				},
			).
			Build()

		Expect(k8sClient.Create(ctx, exception)).To(Succeed())
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateActive)

		By("Advancing clock into extend window to trigger hibernation")
		fakeClock.SetTime(time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)
		testutil.TriggerReconcile(ctx, k8sClient, exception)
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Verifying db-1 is seeded skipped and stays listed")
		Eventually(func() bool {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			if len(plan.Status.Executions) != 2 {
				return false
			}
			for _, e := range plan.Status.Executions {
				if e.Target == "db-1" {
					return e.State == hibernatorv1alpha1.StateSkipped &&
						strings.Contains(e.Message, "Skipped: disabled by exception")
				}
			}
			return false
		}).
			WithTimeout(testutil.DefaultTimeout).
			WithPolling(testutil.DefaultInterval).
			Should(BeTrueBecause("db-1 should be seeded skipped"))

		By("Verifying hibernate Job for db-0 carries overridden params")
		job := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "db-0")
		Expect(job).NotTo(BeNil())
		assertJobParams([]*batchv1.Job{job}, "db-0", `{"env":"event"}`)

		By("Completing hibernate and verifying Hibernated")
		testutil.SimulateJobSuccess(ctx, k8sClient, job, fakeClock.Now())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernated)

		By("Confirming no hibernate Job was ever created for db-1")
		var jl batchv1.JobList
		_ = k8sClient.List(ctx, &jl, client.InNamespace(testNamespace), client.MatchingLabels{
			wellknown.LabelPlan:      plan.Name,
			wellknown.LabelOperation: string(hibernatorv1alpha1.OperationHibernate),
			wellknown.LabelTarget:    "db-1",
		})
		Expect(jl.Items).To(BeEmpty(), "no hibernate Job must be created for disabled target db-1")
	})

	It("ReplaceDisabledAndParams: should skip disabled target and apply params override on replace-triggered hibernate", func() {
		baseTime := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC) // Monday
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with base params on two targets")
		plan, _ = testutil.NewHibernatePlanBuilder("override-replace-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(
				stagedNoopTarget("db-0", `{"env":"base"}`),
				stagedNoopTarget("db-1", `{"env":"base"}`),
			).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Creating replace exception with replacement window plus target overrides")
		exception = testutil.NewScheduleExceptionBuilder("override-replace", testNamespace, plan.Name).
			WithType(hibernatorv1alpha1.ExceptionReplace).
			WithValidity(baseTime.Add(-1*time.Hour), baseTime.Add(48*time.Hour)).
			WithWindows(hibernatorv1alpha1.OffHourWindow{
				Start:      "06:00",
				End:        "22:00",
				DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
			}).
			WithTargetOverrides(
				hibernatorv1alpha1.TargetOverride{TargetName: "db-1", Disabled: true},
				hibernatorv1alpha1.TargetOverride{
					TargetName: "db-0",
					Parameters: &hibernatorv1alpha1.Parameters{Raw: []byte(`{"env":"event"}`)},
				},
			).
			Build()

		Expect(k8sClient.Create(ctx, exception)).To(Succeed())
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateActive)

		By("Advancing clock into replacement window to trigger hibernation")
		fakeClock.SetTime(time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)
		testutil.TriggerReconcile(ctx, k8sClient, exception)
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Verifying db-1 is seeded skipped and stays listed")
		Eventually(func() bool {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)
			if len(plan.Status.Executions) != 2 {
				return false
			}
			for _, e := range plan.Status.Executions {
				if e.Target == "db-1" {
					return e.State == hibernatorv1alpha1.StateSkipped &&
						strings.Contains(e.Message, "Skipped: disabled by exception")
				}
			}
			return false
		}).
			WithTimeout(testutil.DefaultTimeout).
			WithPolling(testutil.DefaultInterval).
			Should(BeTrueBecause("db-1 should be seeded skipped"))

		By("Verifying hibernate Job for db-0 carries overridden params")
		job := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "db-0")
		Expect(job).NotTo(BeNil())
		assertJobParams([]*batchv1.Job{job}, "db-0", `{"env":"event"}`)

		By("Completing hibernate and verifying Hibernated")
		testutil.SimulateJobSuccess(ctx, k8sClient, job, fakeClock.Now())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernated)

		By("Confirming no hibernate Job was ever created for db-1")
		var jl batchv1.JobList
		_ = k8sClient.List(ctx, &jl, client.InNamespace(testNamespace), client.MatchingLabels{
			wellknown.LabelPlan:      plan.Name,
			wellknown.LabelOperation: string(hibernatorv1alpha1.OperationHibernate),
			wellknown.LabelTarget:    "db-1",
		})
		Expect(jl.Items).To(BeEmpty(), "no hibernate Job must be created for disabled target db-1")
	})
})

// stagedNoopTarget builds a noop target with explicit raw params for params assertions.
func stagedNoopTarget(name, paramsJSON string) hibernatorv1alpha1.Target {
	return hibernatorv1alpha1.Target{
		Name: name,
		Type: "noop",
		ConnectorRef: hibernatorv1alpha1.ConnectorRef{
			Kind: "CloudProvider",
			Name: "override-aws",
		},
		Parameters: &hibernatorv1alpha1.Parameters{Raw: []byte(paramsJSON)},
	}
}

// assertJobParams finds the runner Job for target and asserts its
// HIBERNATOR_TARGET_PARAMS env equals wantJSON.
func assertJobParams(jobs []*batchv1.Job, target, wantJSON string) {
	for _, j := range jobs {
		if j.Labels[wellknown.LabelTarget] != target {
			continue
		}
		for _, c := range j.Spec.Template.Spec.Containers {
			for _, e := range c.Env {
				if e.Name == "HIBERNATOR_TARGET_PARAMS" {
					Expect(e.Value).To(Equal(wantJSON), "params for target %q", target)
					return
				}
			}
		}
		Fail("HIBERNATOR_TARGET_PARAMS env not found for target " + target)
	}
	Fail("no Job found for target " + target)
}
