//go:build e2e

package tests

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/restore"
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

	It("Should successfully execute the golden path: Active -> Hibernated -> WakingUp -> Active", func() {
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
		By("Advancing time to hibernation window (20:01)")
		fakeClock.SetTime(time.Date(2026, 2, 9, 20, 1, 0, 0, time.UTC))
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
		By("Advancing time to wakeup window (Tuesday 06:01)")
		fakeClock.SetTime(time.Date(2026, 2, 10, 6, 1, 0, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan transitions to WakingUp")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)

		By("Verifying wakeup Job creation and simulating success")
		wakeupJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, "wakeup", "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, wakeupJob, fakeClock.Now())

		By("Verifying plan returns to Active phase")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)
	})
})
