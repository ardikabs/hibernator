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

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/test/e2e/testutil"
)

var _ = Describe("ScheduleException E2E", func() {
	var (
		plan          *hibernatorv1alpha1.HibernatePlan
		cloudProvider *hibernatorv1alpha1.CloudProvider
		exception     *hibernatorv1alpha1.ScheduleException
	)

	BeforeEach(func() {
		By("Creating mock CloudProvider")
		cloudProvider = &hibernatorv1alpha1.CloudProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "exception-aws",
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
		testutil.EnsureDeleted(ctx, k8sClient, exception)
	})

	It("ExceptionSuspend: should prevent hibernation during off-hours carve-out window", func() {
		// 1. Setup: Monday 08:00 UTC (on-hours)
		baseTime := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC) // Monday
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with 20:00-06:00 hibernation window on all days")
		plan, _ = testutil.NewHibernatePlanBuilder("exc-suspend-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "database",
				Type: "rds",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "exception-aws",
				},
			}).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Verifying plan initializes to Active phase")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Creating ExceptionSuspend covering the hibernation window (prevents shutdown Mon night)")
		// ValidFrom is in the past so it activates immediately; ValidUntil is far in the future.
		exception = testutil.NewScheduleExceptionBuilder("exc-suspend", testNamespace, plan.Name).
			WithType(hibernatorv1alpha1.ExceptionSuspend).
			WithValidity(baseTime.Add(-1*time.Hour), baseTime.Add(48*time.Hour)).
			WithWindows(hibernatorv1alpha1.OffHourWindow{
				Start:      "20:00",
				End:        "06:00",
				DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
			}).
			Build()

		Expect(k8sClient.Create(ctx, exception)).To(Succeed())

		By("Waiting for ScheduleException to become Active")
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateActive)

		By("Advancing clock to Monday 21:00 (inside base hibernation window)")
		fakeClock.SetTime(time.Date(2026, 3, 2, 21, 0, 0, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan remains Active (suspend exception prevents hibernation)")
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive, 5*time.Second)
	})

	It("ExceptionExtend: should trigger hibernation during on-hours extension window", func() {
		// 1. Setup: Monday 08:00 UTC (on-hours)
		baseTime := time.Date(2026, 3, 9, 8, 0, 0, 0, time.UTC) // Monday
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with 20:00-06:00 hibernation window (evenings only)")
		plan, _ = testutil.NewHibernatePlanBuilder("exc-extend-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "database",
				Type: "rds",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "exception-aws",
				},
			}).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Verifying plan initializes to Active at 08:00 (on-hours)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Creating ExceptionExtend that adds a daytime hibernation window (09:00-18:00)")
		// ValidFrom is in the past so it activates immediately.
		exception = testutil.NewScheduleExceptionBuilder("exc-extend", testNamespace, plan.Name).
			WithType(hibernatorv1alpha1.ExceptionExtend).
			WithValidity(baseTime.Add(-1*time.Hour), baseTime.Add(48*time.Hour)).
			WithWindows(hibernatorv1alpha1.OffHourWindow{
				Start:      "09:00",
				End:        "18:00",
				DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
			}).
			Build()

		Expect(k8sClient.Create(ctx, exception)).To(Succeed())

		By("Waiting for ScheduleException to become Active")
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateActive)

		By("Advancing clock to Monday 10:00 (inside extend window, outside base schedule)")
		fakeClock.SetTime(time.Date(2026, 3, 9, 10, 1, 0, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)
		testutil.TriggerReconcile(ctx, k8sClient, exception)

		By("Verifying plan transitions to Hibernating (extend exception adds daytime hibernation)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)
	})

	It("ExceptionReplace: should hibernate based on replacement schedule, ignoring base schedule", func() {
		// Setup: Monday 08:00 UTC (on-hours) — base schedule says no hibernation
		baseTime := time.Date(2026, 3, 16, 8, 0, 0, 0, time.UTC) // Monday
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with 20:00-06:00 hibernation window (evenings only)")
		plan, _ = testutil.NewHibernatePlanBuilder("exc-replace-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "database",
				Type: "rds",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "exception-aws",
				},
			}).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Verifying plan initializes to Active at 08:00 (on-hours)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Creating ExceptionReplace with a replacement schedule covering 06:00-22:00 (business hours = hibernate)")
		exception = testutil.NewScheduleExceptionBuilder("exc-replace", testNamespace, plan.Name).
			WithType(hibernatorv1alpha1.ExceptionReplace).
			WithValidity(baseTime.Add(-1*time.Hour), baseTime.Add(48*time.Hour)).
			WithWindows(hibernatorv1alpha1.OffHourWindow{
				Start:      "06:00",
				End:        "22:00",
				DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
			}).
			Build()

		Expect(k8sClient.Create(ctx, exception)).To(Succeed())

		By("Waiting for ScheduleException to become Active")
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateActive)

		By("Advancing clock to Monday 10:00 (inside replacement hibernation window)")
		fakeClock.SetTime(time.Date(2026, 3, 16, 10, 1, 0, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)
		testutil.TriggerReconcile(ctx, k8sClient, exception)

		By("Verifying plan transitions to Hibernating (replacement schedule takes over)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)
	})

	It("ExceptionExpiry: should resume normal schedule when exception expires and wakeup conditions met", func() {
		// 1. Setup: Monday 08:00 UTC (on-hours)
		baseTime := time.Date(2026, 3, 23, 8, 0, 0, 0, time.UTC) // Monday
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with 20:00-06:00 hibernation window")
		plan, _ = testutil.NewHibernatePlanBuilder("exc-expiry-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "database",
				Type: "rds",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "exception-aws",
				},
			}).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Creating ExceptionSuspend that expires at 21:00 (1 hour into off-hours)")
		// The exception window overlaps off-hours but expires at 21:00.
		// After expiry, normal schedule resumes and hibernation should trigger.
		expiryTime := time.Date(2026, 3, 23, 21, 0, 0, 0, time.UTC)
		exception = testutil.NewScheduleExceptionBuilder("exc-expiring", testNamespace, plan.Name).
			WithType(hibernatorv1alpha1.ExceptionSuspend).
			WithValidity(baseTime.Add(-1*time.Hour), expiryTime).
			WithWindows(hibernatorv1alpha1.OffHourWindow{
				Start:      "20:00",
				End:        "06:00",
				DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
			}).
			Build()

		Expect(k8sClient.Create(ctx, exception)).To(Succeed())
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateActive)

		By("Advancing clock to Monday 20:30 — exception still active, plan should stay Active")
		fakeClock.SetTime(time.Date(2026, 3, 23, 20, 30, 0, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive, 3*time.Second)

		By("Advancing clock past 21:00 — exception expired, base schedule resumes")
		fakeClock.SetTime(time.Date(2026, 3, 23, 21, 5, 0, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, exception)

		By("Waiting for exception to become Expired")
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateExpired)

		By("Triggering plan reconcile — schedule should now see no active exception")
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan now transitions to Hibernating (base schedule takes effect)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)
	})

	It("ExceptionFutureValidFrom: should start in Pending state and only become Active after ValidFrom passes", func() {
		// Validates the Pending → Active transition of a ScheduleException whose ValidFrom
		// is set in the future. Until the clock advances past ValidFrom, the exception must
		// have no effect on the plan's schedule evaluation.
		baseTime := time.Date(2026, 4, 6, 9, 0, 0, 0, time.UTC) // Monday 09:00 — on-hours
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with 20:00-06:00 hibernation window")
		plan, _ = testutil.NewHibernatePlanBuilder("exc-future-validfrom", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "database",
				Type: "rds",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "exception-aws",
				},
			}).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		// ValidFrom is 1 hour from now; ValidUntil is far in the future.
		// The exception extends hibernation to cover 09:00-18:00 daytime windows.
		validFrom := baseTime.Add(1 * time.Hour) // 10:00 UTC
		exception = testutil.NewScheduleExceptionBuilder("exc-future-from", testNamespace, plan.Name).
			WithType(hibernatorv1alpha1.ExceptionExtend).
			WithValidity(validFrom, baseTime.Add(48*time.Hour)).
			WithWindows(hibernatorv1alpha1.OffHourWindow{
				Start:      "09:00",
				End:        "18:00",
				DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
			}).
			Build()

		Expect(k8sClient.Create(ctx, exception)).To(Succeed())

		By("Verifying exception starts in Pending state (ValidFrom is still in the future)")
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStatePending)

		By("Confirming plan remains Active at 09:30 — the Pending exception has no effect yet")
		fakeClock.SetTime(time.Date(2026, 4, 6, 9, 30, 0, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive, 3*time.Second)

		By("Advancing clock past ValidFrom (10:05 UTC) and triggering exception reconcile")
		fakeClock.SetTime(time.Date(2026, 4, 6, 10, 5, 0, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, exception)

		By("Verifying exception transitions to Active state")
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateActive)

		By("Triggering plan reconcile — now-active exception should extend hibernation into daytime")
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan transitions to Hibernating (10:05 UTC is inside the 09:00-18:00 extend window)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)
	})

	It("ConflictingExceptions: Suspend takes priority over Extend when both cover the same time window", func() {
		// Two exceptions with overlapping coverage:
		//   - ExceptionExtend:  09:00-18:00 (adds daytime hibernation)
		//   - ExceptionSuspend: 09:00-18:00 (prevents any hibernation in that window)
		// At 11:00 (inside both windows) the Suspend exception must win: plan stays Active.
		//
		// This validates that Suspend takes safety-first priority over Extend when they conflict.
		var exceptionSuspend *hibernatorv1alpha1.ScheduleException

		baseTime := time.Date(2026, 4, 13, 8, 0, 0, 0, time.UTC) // Monday 08:00 — on-hours
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with 20:00-06:00 base hibernation window")
		plan, _ = testutil.NewHibernatePlanBuilder("exc-conflict-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "database",
				Type: "rds",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "exception-aws",
				},
			}).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Creating ExceptionExtend covering 09:00-18:00 (would trigger hibernation during business hours)")
		// exception is already declared in the outer test scope; reuse it for the Extend.
		exception = testutil.NewScheduleExceptionBuilder("exc-conflict-extend", testNamespace, plan.Name).
			WithType(hibernatorv1alpha1.ExceptionExtend).
			WithValidity(baseTime.Add(-1*time.Hour), baseTime.Add(48*time.Hour)).
			WithWindows(hibernatorv1alpha1.OffHourWindow{
				Start:      "09:00",
				End:        "18:00",
				DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
			}).
			Build()
		Expect(k8sClient.Create(ctx, exception)).To(Succeed())
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateActive)
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive, 1*time.Second)

		By("Creating ExceptionSuspend also covering 09:00-18:00 (conflicts with Extend — should win)")
		exceptionSuspend = testutil.NewScheduleExceptionBuilder("exc-conflict-suspend", testNamespace, plan.Name).
			WithType(hibernatorv1alpha1.ExceptionSuspend).
			WithValidity(baseTime.Add(-1*time.Hour), baseTime.Add(48*time.Hour)).
			WithWindows(hibernatorv1alpha1.OffHourWindow{
				Start:      "09:00",
				End:        "18:00",
				DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
			}).
			Build()
		Expect(k8sClient.Create(ctx, exceptionSuspend)).To(Succeed())
		testutil.EventuallyExceptionState(ctx, k8sClient, exceptionSuspend, hibernatorv1alpha1.ExceptionStateActive)

		By("Advancing clock to Monday 11:00 (inside both the Extend and Suspend windows)")
		fakeClock.SetTime(time.Date(2026, 4, 13, 11, 1, 0, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan remains Active — Suspend takes priority over Extend in the overlapping window")
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive, 5*time.Second)
	})
})
