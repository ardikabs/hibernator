//go:build e2e

package tests

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/ardikabs/hibernator/test/e2e/testutil"
)

var _ = Describe("Revert operation E2E", func() {
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

		if plan != nil {
			restoreCM := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      restore.GetRestoreConfigMap(plan.Name),
					Namespace: plan.Namespace,
				},
			}
			testutil.EnsureDeleted(ctx, k8sClient, restoreCM)
		}

		testutil.EnsureDeleted(ctx, k8sClient, plan)
		testutil.EnsureDeleted(ctx, k8sClient, cloudProvider)
	})

	// buildRevertPlan creates a HibernatePlan with two noop targets and strict behavior.
	// Strict behavior is required so that a partial hibernation failure lands the plan in PhaseError.
	buildRevertPlan := func(name string) {
		plan, _ = testutil.NewHibernatePlanBuilder(name, testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE").
			WithBehavior(hibernatorv1alpha1.Behavior{
				Mode:    hibernatorv1alpha1.BehaviorStrict,
				Retries: ptr.To(int32(0)),
			}).
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(
				hibernatorv1alpha1.Target{
					Name: "database",
					Type: "noop",
					ConnectorRef: hibernatorv1alpha1.ConnectorRef{
						Kind: "CloudProvider",
						Name: "global-aws",
					},
				},
				hibernatorv1alpha1.Target{
					Name: "cache",
					Type: "noop",
					ConnectorRef: hibernatorv1alpha1.ConnectorRef{
						Kind: "CloudProvider",
						Name: "global-aws",
					},
				},
			).
			Build()
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
	}

	// setRevertAnnotation patches the plan with the revert annotation.
	setRevertAnnotation := func() {
		Eventually(func() error {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan); err != nil {
				return err
			}
			patch := client.MergeFrom(plan.DeepCopy())
			if plan.Annotations == nil {
				plan.Annotations = make(map[string]string)
			}
			plan.Annotations[wellknown.AnnotationRevert] = "true"
			return k8sClient.Patch(ctx, plan, patch)
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())
	}

	It("RevertOnActiveWindow: should revert to Active when the revert annotation is applied during the active window", func() {
		By("Creating plan at Monday 08:00 UTC (active window)")
		fakeClock.SetTime(time.Date(2026, 2, 9, 8, 0, 0, 0, time.UTC))
		buildRevertPlan("revert-active-test")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Advancing time to hibernation window and failing partially")
		fakeClock.SetTime(time.Date(2026, 2, 9, 20, 1, 11, 0, time.UTC))
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		databaseJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, databaseJob, fakeClock.Now())
		Expect(restoreManager.Save(ctx, plan.Namespace, plan.Name, "database", &restore.Data{
			Target: "database",
			IsLive: true,
		})).To(Succeed())

		cacheJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "cache")
		testutil.SimulateJobFailure(ctx, k8sClient, cacheJob, fakeClock.Now())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseError)

		By("Advancing time to active window")
		fakeClock.SetTime(time.Date(2026, 2, 10, 6, 1, 10, 0, time.UTC))
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseError, 2*time.Second)

		By("Applying revert annotation and verifying plan transitions to WakingUp for revert")
		setRevertAnnotation()
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)

		By("Simulating successful wakeup for previous successful targets")
		cacheWakeupJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "cache")
		testutil.SimulateJobSuccess(ctx, k8sClient, cacheWakeupJob, fakeClock.Now())
		databaseWakeupJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, databaseWakeupJob, fakeClock.Now())

		By("Verifying plan returns to Active and revert annotation is consumed")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)
		testutil.EventuallyAnnotationRemoved(ctx, k8sClient, plan, wellknown.AnnotationRevert)
		Expect(plan.Spec.Suspend).To(BeFalse())
	})

	It("RevertOnHibernationWindow: should revert to Suspended when the revert annotation is applied during the hibernation window", func() {
		By("Creating plan at Monday 08:00 UTC (active window)")
		fakeClock.SetTime(time.Date(2026, 2, 9, 8, 0, 0, 0, time.UTC))
		buildRevertPlan("revert-suspend-test")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Advancing time to hibernation window and failing partially")
		fakeClock.SetTime(time.Date(2026, 2, 9, 20, 1, 11, 0, time.UTC))
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		databaseJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, databaseJob, fakeClock.Now())
		Expect(restoreManager.Save(ctx, plan.Namespace, plan.Name, "database", &restore.Data{
			Target: "database",
			IsLive: true,
		})).To(Succeed())

		cacheJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "cache")
		testutil.SimulateJobFailure(ctx, k8sClient, cacheJob, fakeClock.Now())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseError)

		By("Applying revert annotation while still in hibernation window")
		fakeClock.SetTime(time.Date(2026, 2, 9, 22, 0, 0, 0, time.UTC))
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseError, 2*time.Second)
		setRevertAnnotation()
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan transitions to WakingUp for revert")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)

		By("Simulating successful wakeup for both targets")
		cacheWakeupJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "cache")
		testutil.SimulateJobSuccess(ctx, k8sClient, cacheWakeupJob, fakeClock.Now())
		databaseWakeupJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, databaseWakeupJob, fakeClock.Now())

		By("Verifying plan settles in Suspended with revert reason and annotation consumed")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseSuspended)
		testutil.EventuallyAnnotationRemoved(ctx, k8sClient, plan, wellknown.AnnotationRevert)
		Expect(plan.Spec.Suspend).To(BeTrue())
		Expect(plan.Annotations[wellknown.AnnotationSuspendReason]).To(Equal("revert"))
	})

	It("RevertOnNoLiveRestoreData: should no-op revert when no live restore data exists", func() {
		By("Creating plan with a single target at Monday 08:00 UTC")
		fakeClock.SetTime(time.Date(2026, 2, 9, 8, 0, 0, 0, time.UTC))

		plan, _ = testutil.NewHibernatePlanBuilder("revert-noop-test", testNamespace).
			WithSchedule("20:00", "06:00", "MON", "TUE").
			WithBehavior(hibernatorv1alpha1.Behavior{
				Mode:    hibernatorv1alpha1.BehaviorStrict,
				Retries: ptr.To(int32(0)),
			}).
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "database",
				Type: "noop",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "global-aws",
				},
			}).
			Build()
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Advancing time to hibernation window and failing the only target")
		fakeClock.SetTime(time.Date(2026, 2, 9, 20, 1, 11, 0, time.UTC))
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		databaseJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "database")
		testutil.SimulateJobFailure(ctx, k8sClient, databaseJob, fakeClock.Now())
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseError)

		By("Applying revert annotation without any live restore data")
		setRevertAnnotation()
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan stays in Error, annotation is removed, and status explains why")
		testutil.ConsistentllyAtPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseError, testutil.MinConsistentDuration)
		testutil.EventuallyAnnotationRemoved(ctx, k8sClient, plan, wellknown.AnnotationRevert)
		testutil.EventuallyErrorMessageContains(ctx, k8sClient, plan, "no targets have live restore data")
	})

	It("RevertOnWakeupFailure: should return to Error and remove the revert annotation when wakeup fails during revert", func() {
		By("Creating plan at Monday 08:00 UTC (active window)")
		fakeClock.SetTime(time.Date(2026, 2, 9, 8, 0, 0, 0, time.UTC))
		buildRevertPlan("revert-wakeup-fail-test")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Advancing time to hibernation window and failing partially")
		fakeClock.SetTime(time.Date(2026, 2, 9, 20, 1, 11, 0, time.UTC))
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		databaseJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, databaseJob, fakeClock.Now())
		Expect(restoreManager.Save(ctx, plan.Namespace, plan.Name, "database", &restore.Data{
			Target: "database",
			IsLive: true,
		})).To(Succeed())

		cacheJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "cache")
		testutil.SimulateJobFailure(ctx, k8sClient, cacheJob, fakeClock.Now())
		// cache failed hibernation — no live restore data for it

		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseError)

		By("Applying revert annotation")
		setRevertAnnotation()
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan transitions to WakingUp for revert")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)

		By("Simulating wakeup failure for the previously-successful target")
		cacheWakeupJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "cache")
		testutil.SimulateJobSuccess(ctx, k8sClient, cacheWakeupJob, fakeClock.Now())
		databaseWakeupJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationWakeUp, "database")
		testutil.SimulateJobFailure(ctx, k8sClient, databaseWakeupJob, fakeClock.Now())

		By("Verifying plan returns to Error, revert annotation is removed, and OperationTrigger is cleared to prevent retry loop")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseError)
		testutil.EventuallyAnnotationRemoved(ctx, k8sClient, plan, wellknown.AnnotationRevert)
		Expect(plan.Status.ErrorMessage).NotTo(BeEmpty())
		Expect(plan.Status.OperationTrigger).To(BeEmpty(), "OperationTrigger should be cleared after failed revert to prevent retry loop")
	})
})
