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
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
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
		testutil.EnsureDeleted(ctx, k8sClient, exception)
		testutil.EnsureDeleted(ctx, k8sClient, plan)
		testutil.EnsureDeleted(ctx, k8sClient, cloudProvider)
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

	It("LeadTime: should prevent hibernation during the lead-time buffer before the suspension window opens", func() {
		// Schedule: 20:00-06:00 (off-hours every night).
		// ExceptionSuspend: covers 20:00-06:00 with a 1-hour lead time.
		// The lead-time buffer means that from 19:00 onward the exception starts
		// "protecting" the plan — even though the base schedule says hibernate at 20:00,
		// the system must NOT start hibernation between 19:00 and 20:00 because we are
		// inside the lead-time window immediately preceding the suspension period.
		//
		// Timeline:
		//   08:00 → plan starts Active (on-hours, exception is Active with lead time)
		//   19:01 → inside lead-time buffer (1 h before 20:00 suspension) — must stay Active
		//   21:00 → inside suspension window — must stay Active (suspend exception wins)
		baseTime := time.Date(2026, 5, 11, 8, 0, 0, 0, time.UTC) // Monday 08:00
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with 20:00-06:00 hibernation window")
		plan, _ = testutil.NewHibernatePlanBuilder("exc-leadtime-test", testNamespace).
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

		By("Creating ExceptionSuspend covering 20:00-06:00 with a 1-hour lead time")
		// ValidFrom is already in the past so the exception is active immediately.
		// LeadTime=1h means the suspension buffer starts at 19:00, 1 h before the
		// first window opens at 20:00.
		exception = testutil.NewScheduleExceptionBuilder("exc-leadtime", testNamespace, plan.Name).
			WithType(hibernatorv1alpha1.ExceptionSuspend).
			WithValidity(baseTime.Add(-1*time.Hour), baseTime.Add(48*time.Hour)).
			WithLeadTime("1h").
			WithWindows(hibernatorv1alpha1.OffHourWindow{
				Start:      "20:00",
				End:        "06:00",
				DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
			}).
			Build()

		Expect(k8sClient.Create(ctx, exception)).To(Succeed())
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateActive)

		By("Advancing clock to Monday 19:01 — inside the 1-hour lead-time buffer before the 20:00 window")
		// Base schedule says: not in hibernation window yet (20:00 hasn't fired).
		// But the lead-time buffer (19:00-20:00) must prevent any new hibernation from starting.
		fakeClock.SetTime(time.Date(2026, 5, 11, 19, 1, 0, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan remains Active at 19:01 (lead-time buffer prevents hibernation start)")
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive, 4*time.Second)

		By("Advancing clock into the suspension window proper (Monday 21:00)")
		// Now both the base schedule (ShouldHibernate=true) AND the suspension window
		// are active. The Suspend exception must still win — plan stays Active.
		fakeClock.SetTime(time.Date(2026, 5, 11, 21, 1, 0, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan remains Active at 21:00 (suspend exception prevents hibernation)")
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive, 4*time.Second)
	})

	It("ScheduleExceptionLifecycle_Complete: should transition through Pending → Active → Expired → Detached states", func() {
		// This test validates the full exception state machine lifecycle without checking
		// execution side effects (plan phase transitions are covered by GoldenPath E2E tests).
		// Focuses exclusively on ScheduleException state transitions and timestamp recording:
		// 1. Exception created with ValidFrom in future → Pending state
		// 2. Clock advances past ValidFrom → Active state
		// 3. Clock advances past ValidUntil → Expired state
		// 4. Plan deletion (exception without ownerref) → Detached state
		baseTime := time.Date(2026, 5, 5, 8, 0, 0, 0, time.UTC) // Monday 08:00 UTC
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan as reference context")
		plan, _ = testutil.NewHibernatePlanBuilder("exc-lifecycle-complete", testNamespace).
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

		By("Creating ScheduleException with ValidFrom 1 hour in future (09:00 UTC)")
		validFromTime := baseTime.Add(1 * time.Hour)  // 09:00 UTC
		validUntilTime := baseTime.Add(3 * time.Hour) // 11:00 UTC
		exception = testutil.NewScheduleExceptionBuilder("exc-lifecycle", testNamespace, plan.Name).
			WithType(hibernatorv1alpha1.ExceptionExtend).
			WithValidity(validFromTime, validUntilTime).
			WithWindows(hibernatorv1alpha1.OffHourWindow{
				Start:      "09:00",
				End:        "18:00",
				DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
			}).
			Build()

		Expect(k8sClient.Create(ctx, exception)).To(Succeed())

		By("Validating exception reaches Pending state (clock before ValidFrom)")
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStatePending)
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(exception), exception)).To(Succeed())
		Expect(exception.Status.State).To(Equal(hibernatorv1alpha1.ExceptionStatePending))
		Expect(exception.Status.AppliedAt).To(BeNil(), "Pending state should not have AppliedAt")

		By("Verifying exception does not appear in plan.Status.ExceptionReferences (Pending state)")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		// Should not have any active exception references while in Pending state
		pendingRefs := lo.Filter(plan.Status.ExceptionReferences, func(ref hibernatorv1alpha1.ExceptionReference, _ int) bool {
			return ref.Name == exception.Name && ref.State == hibernatorv1alpha1.ExceptionStateActive
		})
		Expect(pendingRefs).To(HaveLen(0), "Pending exception should not appear as active in plan references")

		By("Advancing clock to 09:05 (past ValidFrom) and triggering exception reconcile")
		fakeClock.SetTime(baseTime.Add(65 * time.Minute))
		testutil.TriggerReconcile(ctx, k8sClient, exception)

		By("Validating exception transitions to Active state with AppliedAt timestamp")
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateActive)
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(exception), exception)).To(Succeed())
		Expect(exception.Status.State).To(Equal(hibernatorv1alpha1.ExceptionStateActive))
		Expect(exception.Status.AppliedAt).NotTo(BeNil(), "Active state must set AppliedAt timestamp")

		By("Verifying exception appears in plan.Status.ExceptionReferences with Active state")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
			activeRefs := lo.Filter(plan.Status.ExceptionReferences, func(ref hibernatorv1alpha1.ExceptionReference, _ int) bool {
				return ref.Name == exception.Name && ref.State == hibernatorv1alpha1.ExceptionStateActive
			})
			g.Expect(activeRefs).To(HaveLen(1), "Expected 1 active exception reference")
		}).WithTimeout(10 * time.Second).Should(Succeed())

		By("Advancing clock to 11:05 (past ValidUntil) and triggering exception reconcile")
		fakeClock.SetTime(baseTime.Add(185 * time.Minute))
		testutil.TriggerReconcile(ctx, k8sClient, exception)

		By("Validating exception transitions to Expired state with ExpiredAt timestamp")
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateExpired)
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(exception), exception)).To(Succeed())
		Expect(exception.Status.State).To(Equal(hibernatorv1alpha1.ExceptionStateExpired))
		Expect(exception.Status.ExpiredAt).NotTo(BeNil(), "Expired state must set ExpiredAt timestamp")

		By("Verifying exception no longer appears as active in plan.Status.ExceptionReferences (Expired state)")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan)).To(Succeed())
		expiredRefs := lo.Filter(plan.Status.ExceptionReferences, func(ref hibernatorv1alpha1.ExceptionReference, _ int) bool {
			return ref.Name == exception.Name && ref.State == hibernatorv1alpha1.ExceptionStateActive
		})
		Expect(expiredRefs).To(HaveLen(0), "Expired exception should not appear as active in plan references")

		By("Deleting plan (exception has no ownerref, should transition to Detached)")
		Expect(k8sClient.Delete(ctx, plan)).To(Succeed())

		By("Validating exception transitions to Detached state with DetachedAt timestamp")
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateDetached)
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(exception), exception)).To(Succeed())
		Expect(exception.Status.State).To(Equal(hibernatorv1alpha1.ExceptionStateDetached))
		Expect(exception.Status.DetachedAt).NotTo(BeNil(), "Detached state must set DetachedAt timestamp")

		By("Confirming exception persists after plan deletion (not garbage collected)")
		key := client.ObjectKeyFromObject(exception)
		exception = &hibernatorv1alpha1.ScheduleException{}
		Expect(k8sClient.Get(ctx, key, exception)).To(Succeed())
		Expect(exception.Status.State).To(Equal(hibernatorv1alpha1.ExceptionStateDetached))
	})

	It("ScheduleExceptionLifecycle_PendingActiveExpiredDetached: detailed state transitions with status validation", func() {
		// Comprehensive test validating state machine progression with explicit status assertions.
		// Tests that messages and timestamps are correctly updated at each transition.
		baseTime := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC) // Monday 10:00 UTC
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan")
		plan, _ = testutil.NewHibernatePlanBuilder("exc-states-detailed", testNamespace).
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

		By("Creating ScheduleException with future ValidFrom")
		validFromTime := baseTime.Add(2 * time.Hour)  // 12:00 UTC
		validUntilTime := baseTime.Add(4 * time.Hour) // 14:00 UTC
		exception = testutil.NewScheduleExceptionBuilder("exc-states-test", testNamespace, plan.Name).
			WithType(hibernatorv1alpha1.ExceptionSuspend).
			WithValidity(validFromTime, validUntilTime).
			WithWindows(hibernatorv1alpha1.OffHourWindow{
				Start:      "20:00",
				End:        "06:00",
				DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
			}).
			Build()

		Expect(k8sClient.Create(ctx, exception)).To(Succeed())

		// State 1: Pending
		By("Validating exception is in Pending state (ValidFrom in future)")
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStatePending)

		By("Reading exception status and checking Pending message")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(exception), exception)).To(Succeed())
		Expect(exception.Status.State).To(Equal(hibernatorv1alpha1.ExceptionStatePending))
		Expect(exception.Status.Message).NotTo(BeEmpty(), "Pending state should have a status message")

		// Transition to Active
		By("Advancing clock past ValidFrom (12:05)")
		fakeClock.SetTime(baseTime.Add(125 * time.Minute))
		testutil.TriggerReconcile(ctx, k8sClient, exception)

		// State 2: Active
		By("Validating exception transitions to Active state")
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateActive)

		By("Reading exception status and checking Active message")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(exception), exception)).To(Succeed())
		Expect(exception.Status.State).To(Equal(hibernatorv1alpha1.ExceptionStateActive))
		Expect(exception.Status.Message).NotTo(BeEmpty(), "Active state should have a status message")
		Expect(exception.Status.AppliedAt).NotTo(BeNil(), "Active state should record AppliedAt timestamp")

		// Transition to Expired
		By("Advancing clock past ValidUntil (14:05)")
		fakeClock.SetTime(baseTime.Add(245 * time.Minute))
		testutil.TriggerReconcile(ctx, k8sClient, exception)

		// State 3: Expired
		By("Validating exception transitions to Expired state")
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateExpired)

		By("Reading exception status and checking Expired message")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(exception), exception)).To(Succeed())
		Expect(exception.Status.State).To(Equal(hibernatorv1alpha1.ExceptionStateExpired))
		Expect(exception.Status.Message).NotTo(BeEmpty(), "Expired state should have a status message")
		Expect(exception.Status.ExpiredAt).NotTo(BeNil(), "Expired state should record ExpiredAt timestamp")

		// Transition to Detached (plan deletion)
		By("Deleting the HibernatePlan (exception has no ownerref, should go to Detached)")
		Expect(k8sClient.Delete(ctx, plan)).To(Succeed())

		// State 4: Detached
		By("Validating exception transitions to Detached state")
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateDetached)

		By("Reading exception status and checking Detached message")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(exception), exception)).To(Succeed())
		Expect(exception.Status.State).To(Equal(hibernatorv1alpha1.ExceptionStateDetached))
		Expect(exception.Status.Message).NotTo(BeEmpty(), "Detached state should have a status message")
		Expect(exception.Status.DetachedAt).NotTo(BeNil(), "Detached state should record DetachedAt timestamp")

		By("Confirming exception still exists after plan deletion")
		retrieved := &hibernatorv1alpha1.ScheduleException{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(exception), retrieved)).To(Succeed())
		Expect(retrieved.Status.State).To(Equal(hibernatorv1alpha1.ExceptionStateDetached))
	})

	It("ScheduleExceptionDetached_WithOwnerRef: cascade deletion when plan deletes exception with ownerref", func() {
		// When an exception is created WITH an ownerref to a plan, deleting the plan
		// should cascade-delete the exception (not transition it to Detached).
		// This validates that plan-managed exceptions are properly GC'd while
		// user-created exceptions are preserved.
		baseTime := time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC) // Monday 10:00 UTC
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan")
		plan, _ = testutil.NewHibernatePlanBuilder("exc-ownerref-test", testNamespace).
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

		By("Creating ScheduleException WITH ownerref to plan (managed exception)")
		exception = testutil.NewScheduleExceptionBuilder("exc-managed", testNamespace, plan.Name).
			WithType(hibernatorv1alpha1.ExceptionExtend).
			WithValidity(baseTime.Add(-1*time.Hour), baseTime.Add(2*time.Hour)).
			WithWindows(hibernatorv1alpha1.OffHourWindow{
				Start:      "09:00",
				End:        "18:00",
				DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
			}).
			Build()

		// Add ownerref pointing to the plan
		exception.OwnerReferences = []metav1.OwnerReference{
			{
				APIVersion: hibernatorv1alpha1.GroupVersion.String(),
				Kind:       "HibernatePlan",
				Name:       plan.Name,
				UID:        plan.UID,
				Controller: lo.ToPtr(true),
			},
		}

		Expect(k8sClient.Create(ctx, exception)).To(Succeed())
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateActive)

		By("Deleting the HibernatePlan (exception has ownerref, should be cascade-deleted)")
		Expect(k8sClient.Delete(ctx, plan)).To(Succeed())

		By("Validating exception is cascade-deleted (not transitioned to Detached)")
		testutil.EnsureDeleted(ctx, k8sClient, exception)

		By("Confirming exception is gone from API server")
		retrieved := &hibernatorv1alpha1.ScheduleException{}
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(exception), retrieved)
		Expect(err).To(HaveOccurred())
		Expect(errors.IsNotFound(err)).To(BeTrue(), "Exception should be gone (cascade deleted)")
	})

	It("ScheduleExceptionDetached_NoOwnerRef: plan deletion transitions user-created exception to Detached", func() {
		// When an exception is created WITHOUT ownerref (user-managed), deleting the plan
		// should transition it to Detached (not delete it).
		// This validates that orphaned exceptions are preserved with a clear state.
		baseTime := time.Date(2026, 5, 26, 14, 0, 0, 0, time.UTC) // Monday 14:00 UTC
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan")
		plan, _ = testutil.NewHibernatePlanBuilder("exc-detached-test", testNamespace).
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

		By("Creating ScheduleException WITHOUT ownerref (user-created exception)")
		exception = testutil.NewScheduleExceptionBuilder("exc-unmanaged", testNamespace, plan.Name).
			WithType(hibernatorv1alpha1.ExceptionReplace).
			WithValidity(baseTime.Add(-1*time.Hour), baseTime.Add(48*time.Hour)).
			WithWindows(hibernatorv1alpha1.OffHourWindow{
				Start:      "06:00",
				End:        "22:00",
				DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
			}).
			Build()

		// Explicitly ensure NO ownerref (user-created)
		exception.OwnerReferences = nil

		Expect(k8sClient.Create(ctx, exception)).To(Succeed())
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateActive)

		By("Deleting the HibernatePlan (exception has no ownerref, should transition to Detached)")
		Expect(k8sClient.Delete(ctx, plan)).To(Succeed())

		By("Validating exception transitions to Detached state (not deleted)")
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateDetached)

		By("Confirming exception still exists in Detached state")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(exception), exception)).To(Succeed())
		Expect(exception.Status.State).To(Equal(hibernatorv1alpha1.ExceptionStateDetached))
		Expect(exception.Status.Message).To(ContainSubstring("plan"), "Detached message should reference the plan")
		Expect(controllerutil.ContainsFinalizer(exception, wellknown.ExceptionFinalizerName)).To(BeFalse(), "Detached exception should not have finalizer")
	})
})
