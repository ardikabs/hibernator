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

// multiWindowTarget returns a noop target backed by a shared CloudProvider.
func multiWindowTarget(name, cloudProviderName string) hibernatorv1alpha1.Target {
	return hibernatorv1alpha1.Target{
		Name: name,
		Type: "noop",
		ConnectorRef: hibernatorv1alpha1.ConnectorRef{
			Kind: "CloudProvider",
			Name: cloudProviderName,
		},
	}
}

var _ = Describe("MultiWindow Schedule E2E", func() {
	const cpName = "multiwin-aws"

	var (
		plan          *hibernatorv1alpha1.HibernatePlan
		cloudProvider *hibernatorv1alpha1.CloudProvider
		exception     *hibernatorv1alpha1.ScheduleException
	)

	BeforeEach(func() {
		exception = nil // reset so AfterEach skips deletion when not created

		By("Creating mock CloudProvider")
		cloudProvider = &hibernatorv1alpha1.CloudProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cpName,
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

	// ──────────────────────────────────────────────────────────────────────────
	// Base schedule: two daytime windows  09:00–12:00  and  14:00–17:00 Mon–Fri
	// ──────────────────────────────────────────────────────────────────────────

	It("MultiWindow_Base_FirstWindowTriggersHibernation: clock inside first window causes hibernation", func() {
		// Monday 08:00 UTC — before either window opens.
		baseTime := time.Date(2026, 4, 6, 8, 0, 0, 0, time.UTC) // Monday
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with two daytime windows (09:00-12:00 and 14:00-17:00 Mon–Fri)")
		plan, _ = testutil.NewHibernatePlanBuilder("mw-first-win", testNamespace).
			WithSchedule("09:00", "12:00", "MON", "TUE", "WED", "THU", "FRI").
			WithOffHours(
				hibernatorv1alpha1.OffHourWindow{
					Start:      "09:00",
					End:        "12:00",
					DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
				},
				hibernatorv1alpha1.OffHourWindow{
					Start:      "14:00",
					End:        "17:00",
					DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
				},
			).
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(multiWindowTarget("database", cpName)).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Verifying plan initializes to Active at 08:00 (before any window)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Advancing clock to 10:00 — inside first window (09:00-12:00)")
		fakeClock.SetTime(time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan transitions to Hibernating (first window matched)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)
	})

	It("MultiWindow_Base_SecondWindowTriggersHibernation: clock inside second window causes hibernation", func() {
		// Monday 08:00 UTC — before either window opens.
		baseTime := time.Date(2026, 4, 13, 8, 0, 0, 0, time.UTC) // Monday
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with two daytime windows (09:00-12:00 and 14:00-17:00 Mon–Fri)")
		plan, _ = testutil.NewHibernatePlanBuilder("mw-second-win", testNamespace).
			WithSchedule("09:00", "12:00", "MON", "TUE", "WED", "THU", "FRI").
			WithOffHours(
				hibernatorv1alpha1.OffHourWindow{
					Start:      "09:00",
					End:        "12:00",
					DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
				},
				hibernatorv1alpha1.OffHourWindow{
					Start:      "14:00",
					End:        "17:00",
					DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
				},
			).
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(multiWindowTarget("database", cpName)).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Verifying plan initializes to Active at 08:00 (before any window)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Advancing clock to 15:00 — inside second window (14:00-17:00)")
		fakeClock.SetTime(time.Date(2026, 4, 13, 15, 0, 0, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan transitions to Hibernating (second window matched)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)
	})

	It("MultiWindow_Base_GapBetweenWindowsRemainsActive: clock in gap between windows does not hibernate", func() {
		// Monday 13:00 UTC — gap between 12:00 and 14:00 (neither window is active).
		baseTime := time.Date(2026, 4, 20, 13, 0, 0, 0, time.UTC) // Monday
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with two daytime windows (09:00-12:00 and 14:00-17:00 Mon–Fri)")
		plan, _ = testutil.NewHibernatePlanBuilder("mw-gap", testNamespace).
			WithSchedule("09:00", "12:00", "MON", "TUE", "WED", "THU", "FRI").
			WithOffHours(
				hibernatorv1alpha1.OffHourWindow{
					Start:      "09:00",
					End:        "12:00",
					DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
				},
				hibernatorv1alpha1.OffHourWindow{
					Start:      "14:00",
					End:        "17:00",
					DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
				},
			).
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(multiWindowTarget("database", cpName)).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Verifying plan initializes at Active (13:00 is in gap between both windows)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Verifying plan consistently remains Active — neither window should fire during the gap")
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive, 8*time.Second)
	})

	// ──────────────────────────────────────────────────────────────────────────
	// Exception: ExceptionExtend with multiple windows added on top of base plan
	// ──────────────────────────────────────────────────────────────────────────

	It("MultiWindow_Exception_ExtendWithMultipleWindows: exception with two windows each trigger hibernation", func() {
		// Monday 08:00 UTC — base schedule only hibernates 20:00-06:00 (nightly).
		baseTime := time.Date(2026, 4, 27, 8, 0, 0, 0, time.UTC) // Monday
		fakeClock.SetTime(baseTime)

		By("Creating HibernatePlan with nightly base window (20:00-06:00 Mon–Fri)")
		plan, _ = testutil.NewHibernatePlanBuilder("mw-exc-extend", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(multiWindowTarget("database", cpName)).
			Build()

		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Verifying plan initializes to Active at 08:00 (on-hours, outside nightly window)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Creating ExceptionExtend that adds two daytime hibernation windows (09:00-12:00 and 14:00-17:00)")
		exception = testutil.NewScheduleExceptionBuilder("mw-exc-extend", testNamespace, plan.Name).
			WithType(hibernatorv1alpha1.ExceptionExtend).
			WithValidity(baseTime.Add(-1*time.Hour), baseTime.Add(48*time.Hour)).
			WithWindows(
				hibernatorv1alpha1.OffHourWindow{
					Start:      "09:00",
					End:        "12:00",
					DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
				},
				hibernatorv1alpha1.OffHourWindow{
					Start:      "14:00",
					End:        "17:00",
					DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
				},
			).
			Build()

		Expect(k8sClient.Create(ctx, exception)).To(Succeed())

		By("Waiting for ScheduleException to become Active")
		testutil.EventuallyExceptionState(ctx, k8sClient, exception, hibernatorv1alpha1.ExceptionStateActive)

		By("Advancing clock to 10:00 — inside first exception window (09:00-12:00)")
		fakeClock.SetTime(time.Date(2026, 4, 27, 10, 1, 0, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)
		testutil.TriggerReconcile(ctx, k8sClient, exception)

		By("Verifying plan transitions to Hibernating (first exception window matched)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)
	})
})
