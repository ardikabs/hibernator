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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/pkg/k8sutil"
	"github.com/ardikabs/hibernator/test/e2e/testutil"
)

var _ = Describe("Schedule Boundary E2E - 1-Minute Buffer Workaround", func() {
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
		testutil.EnsureDeleted(ctx, k8sClient, plan)
		testutil.EnsureDeleted(ctx, k8sClient, cloudProvider)
	})

	It("Should prevent wakeup operation at 23:59-00:00 boundary for full-day hibernation with 1-minute buffer", func() {
		By("Creating HibernatePlan with full-day hibernation (00:00-23:59)")
		plan, _ = testutil.NewHibernatePlanBuilder("boundary-test", testNamespace).
			WithSchedule("00:00", "23:59", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
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

		err := k8sClient.Create(ctx, plan)
		Expect(err).NotTo(HaveOccurred())

		// Setup: Monday 23:00:00 UTC (at day boundary)
		boundaryTime := time.Date(2026, 2, 9, 23, 0, 0, 0, time.UTC)
		fakeClock.SetTime(boundaryTime)

		By("Triggering initial reconciliation at Monday 23:00:00")
		testutil.ReconcileUntilReady(ctx, k8sClient, plan, 30*time.Second)

		By("Verifying plan is in Hibernating phase at boundary")
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
		restoreCM.Data = map[string]string{"database.json": "{}"}
		Expect(k8sClient.Update(ctx, &restoreCM)).To(Succeed())

		By("Advancing clock to 23:59:15 to ensure no phase transition happening")
		afterBoundaryTime := boundaryTime.Add(59 * time.Minute).Add(15 * time.Second)
		fakeClock.SetTime(afterBoundaryTime)

		By("Triggering reconciliation at Monday 23:59:15")
		testutil.ReconcileUntilReady(ctx, k8sClient, plan, 30*time.Second)

		By("Verifying plan remains in Hibernated phase (no conflicting wakeup operation)")
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernated, 10*time.Second)
	})

	It("Should prevent shutdown operation at 00:00 boundary for minimal wakeup windows with 1-minute buffer", func() {
		// This test validates the inverse scenario: a minimal wakeup window (23:59-00:00)
		// where the 1-minute buffer prevents conflicting shutdown at the boundary

		By("Creating HibernatePlan with minimal wakeup window (23:59-00:00)")
		plan, _ = testutil.NewHibernatePlanBuilder("minimal-window-test", testNamespace).
			WithSchedule("23:59", "00:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithTarget(hibernatorv1alpha1.Target{
				Name: "database",
				Type: "rds",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "global-aws",
				},
			}).
			Build()

		err := k8sClient.Create(ctx, plan)
		Expect(err).NotTo(HaveOccurred())

		// Setup: Monday 23:00:00 UTC (at day boundary for 1-minute wakeup window)
		boundaryTime := time.Date(2026, 2, 9, 23, 0, 0, 0, time.UTC)
		fakeClock.SetTime(boundaryTime)

		By("Triggering reconciliation at Monday 23:00:00")
		// hibernateCron="59 23" fires, system should be in hibernation
		testutil.ReconcileUntilReady(ctx, k8sClient, plan, 30*time.Second)

		By("Verifying plan is in Hibernating phase at 23:00")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Advancing clock to 23:59:10")
		afterBoundaryTime := boundaryTime.Add(59*time.Minute + 10*time.Second)
		fakeClock.SetTime(afterBoundaryTime)

		By("Triggering reconciliation at Monday 23:59:10")
		testutil.ReconcileUntilReady(ctx, k8sClient, plan, 30*time.Second)

		By("Verifying plan remains in Active phase (no back-to-back shutdown-wakingup operation)")
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive, 10*time.Second)
	})
})
