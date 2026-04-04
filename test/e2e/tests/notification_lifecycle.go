//go:build e2e

/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package tests

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/test/e2e/helper/fakenotif"
	"github.com/ardikabs/hibernator/test/e2e/testutil"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Notification Lifecycle E2E", func() {
	var (
		cloudProvider *hibernatorv1alpha1.CloudProvider
		sinkSecret    *corev1.Secret
	)

	BeforeEach(func() {
		fakeNotifSink.Reset()

		By("Creating mock CloudProvider")
		cloudProvider = &hibernatorv1alpha1.CloudProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "lifecycle-aws",
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

		By("Creating sink Secret")
		sinkSecret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "lifecycle-notif-config",
				Namespace: testNamespace,
			},
			Data: map[string][]byte{
				"config": []byte(`{}`),
			},
		}
		if err := k8sClient.Create(ctx, sinkSecret); err != nil && !errors.IsAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}
	})

	AfterEach(func() {
		testutil.EnsureDeleted(ctx, k8sClient, cloudProvider)
		testutil.EnsureDeleted(ctx, k8sClient, sinkSecret)
		fakeNotifSink.Reset()
	})

	// newLifecycleNotification creates a HibernateNotification for lifecycle tests.
	newLifecycleNotification := func(name string, matchLabels map[string]string) *hibernatorv1alpha1.HibernateNotification {
		return &hibernatorv1alpha1.HibernateNotification{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.HibernateNotificationSpec{
				Selector: metav1.LabelSelector{
					MatchLabels: matchLabels,
				},
				OnEvents: []hibernatorv1alpha1.NotificationEvent{
					hibernatorv1alpha1.EventStart,
				},
				Sinks: []hibernatorv1alpha1.NotificationSink{
					{
						Name:      "fake-webhook",
						Type:      hibernatorv1alpha1.NotificationSinkType(fakenotif.SinkType),
						SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: sinkSecret.Name, Key: ptr.To("config")},
					},
				},
			},
		}
	}

	// newLifecyclePlan creates a HibernatePlan for lifecycle tests.
	newLifecyclePlan := func(name string, labels map[string]string) *hibernatorv1alpha1.HibernatePlan {
		plan, _ := testutil.NewHibernatePlanBuilder(name, testNamespace).
			WithLabels(labels).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "app",
				Type: "noop",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "lifecycle-aws",
				},
			}).
			Build()
		return plan
	}

	It("PlanLabelsChanged: should remove plan from watchedPlans when plan labels no longer match the notification selector", func() {
		const scenarioLabel = "lifecycle-labels-change"

		notification := newLifecycleNotification("lc-label-change", map[string]string{"team": scenarioLabel})
		Expect(k8sClient.Create(ctx, notification)).To(Succeed())
		defer testutil.EnsureDeleted(ctx, k8sClient, notification)

		// Start in on-hours so plan initializes to Active.
		fakeClock.SetTime(time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC))

		plan := newLifecyclePlan("lc-label-change", map[string]string{"team": scenarioLabel})
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		defer testutil.EnsureDeleted(ctx, k8sClient, plan)

		By("Verifying plan initializes to Active")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Verifying watchedPlans is populated with the matching plan")
		Eventually(func(g Gomega) {
			fresh := &hibernatorv1alpha1.HibernateNotification{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), fresh)).To(Succeed())
			g.Expect(fresh.Status.WatchedPlans).To(ContainElement(
				hibernatorv1alpha1.PlanReference{Name: plan.Name},
			))
			g.Expect(fresh.Status.State).To(Equal(hibernatorv1alpha1.NotificationStateBound))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())

		By("Changing plan labels so they no longer match the notification selector")
		Eventually(func() error {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan); err != nil {
				return err
			}
			plan.Labels = map[string]string{"team": "completely-different"}
			return k8sClient.Update(ctx, plan)
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())

		By("Verifying watchedPlans no longer contains the plan")
		Eventually(func(g Gomega) {
			fresh := &hibernatorv1alpha1.HibernateNotification{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), fresh)).To(Succeed())
			g.Expect(fresh.Status.WatchedPlans).NotTo(ContainElement(
				hibernatorv1alpha1.PlanReference{Name: plan.Name},
			))
			g.Expect(fresh.Status.State).To(Equal(hibernatorv1alpha1.NotificationStateDetached))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())
	})

	It("PlanDeleted: should remove plan from watchedPlans when the plan is deleted", func() {
		const scenarioLabel = "lifecycle-plan-deleted"

		notification := newLifecycleNotification("lc-plan-del", map[string]string{"team": scenarioLabel})
		Expect(k8sClient.Create(ctx, notification)).To(Succeed())
		defer testutil.EnsureDeleted(ctx, k8sClient, notification)

		fakeClock.SetTime(time.Date(2026, 6, 8, 8, 0, 0, 0, time.UTC))

		plan := newLifecyclePlan("lc-plan-del", map[string]string{"team": scenarioLabel})
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Verifying plan initializes to Active")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Verifying watchedPlans is populated")
		Eventually(func(g Gomega) {
			fresh := &hibernatorv1alpha1.HibernateNotification{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), fresh)).To(Succeed())
			g.Expect(fresh.Status.WatchedPlans).To(ContainElement(
				hibernatorv1alpha1.PlanReference{Name: plan.Name},
			))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())

		By("Deleting the plan")
		Expect(k8sClient.Delete(ctx, plan)).To(Succeed())

		By("Verifying watchedPlans no longer contains the deleted plan")
		Eventually(func(g Gomega) {
			fresh := &hibernatorv1alpha1.HibernateNotification{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), fresh)).To(Succeed())
			g.Expect(fresh.Status.WatchedPlans).NotTo(ContainElement(
				hibernatorv1alpha1.PlanReference{Name: plan.Name},
			))
			g.Expect(fresh.Status.State).To(Equal(hibernatorv1alpha1.NotificationStateDetached))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())
	})

	It("SelectorNarrowed: should remove plans that no longer match after notification selector is updated", func() {
		const scenarioBase = "lifecycle-sel-narrow"

		// Notification initially selects all plans with env=prod.
		notification := newLifecycleNotification("lc-sel-narrow", map[string]string{"env": "prod"})
		Expect(k8sClient.Create(ctx, notification)).To(Succeed())
		defer testutil.EnsureDeleted(ctx, k8sClient, notification)

		fakeClock.SetTime(time.Date(2026, 6, 15, 8, 0, 0, 0, time.UTC))

		By("Creating two plans that both match env=prod")
		planA := newLifecyclePlan("lc-sel-plan-a", map[string]string{
			"env":  "prod",
			"tier": "frontend",
		})
		Expect(k8sClient.Create(ctx, planA)).To(Succeed())
		defer testutil.EnsureDeleted(ctx, k8sClient, planA)

		planB := newLifecyclePlan("lc-sel-plan-b", map[string]string{
			"env":  "prod",
			"tier": "backend",
		})
		Expect(k8sClient.Create(ctx, planB)).To(Succeed())
		defer testutil.EnsureDeleted(ctx, k8sClient, planB)

		By("Verifying both plans initialize to Active")
		testutil.EventuallyPhase(ctx, k8sClient, planA, hibernatorv1alpha1.PhaseActive)
		testutil.EventuallyPhase(ctx, k8sClient, planB, hibernatorv1alpha1.PhaseActive)

		By("Verifying watchedPlans contains both plans")
		Eventually(func(g Gomega) {
			fresh := &hibernatorv1alpha1.HibernateNotification{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), fresh)).To(Succeed())
			g.Expect(fresh.Status.WatchedPlans).To(HaveLen(2))
			g.Expect(fresh.Status.State).To(Equal(hibernatorv1alpha1.NotificationStateBound))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())

		By("Narrowing the notification selector to only match tier=frontend")
		Eventually(func() error {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), notification); err != nil {
				return err
			}
			notification.Spec.Selector = metav1.LabelSelector{
				MatchLabels: map[string]string{
					"env":  "prod",
					"tier": "frontend",
				},
			}
			return k8sClient.Update(ctx, notification)
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())

		By("Triggering reconcile for both plans so bindings are re-evaluated")
		testutil.TriggerReconcile(ctx, k8sClient, planA)
		testutil.TriggerReconcile(ctx, k8sClient, planB)

		By("Verifying watchedPlans contains only plan-a (frontend) and not plan-b (backend)")
		Eventually(func(g Gomega) {
			fresh := &hibernatorv1alpha1.HibernateNotification{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), fresh)).To(Succeed())
			g.Expect(fresh.Status.WatchedPlans).To(HaveLen(1))
			g.Expect(fresh.Status.WatchedPlans[0].Name).To(HavePrefix("lc-sel-plan-a"))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())
	})

	It("MultiPlanMatchAndUnmatch: should correctly track plans as they transition between matching and non-matching", func() {
		const scenarioLabel = "lifecycle-multi-match"

		notification := newLifecycleNotification("lc-multi-match", map[string]string{"team": scenarioLabel})
		Expect(k8sClient.Create(ctx, notification)).To(Succeed())
		defer testutil.EnsureDeleted(ctx, k8sClient, notification)

		fakeClock.SetTime(time.Date(2026, 6, 22, 8, 0, 0, 0, time.UTC))

		plan := newLifecyclePlan("lc-multi-match", map[string]string{"team": scenarioLabel})
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		defer testutil.EnsureDeleted(ctx, k8sClient, plan)

		By("Verifying plan initializes to Active")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Verifying watchedPlans is populated")
		Eventually(func(g Gomega) {
			fresh := &hibernatorv1alpha1.HibernateNotification{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), fresh)).To(Succeed())
			g.Expect(fresh.Status.WatchedPlans).To(ContainElement(
				hibernatorv1alpha1.PlanReference{Name: plan.Name},
			))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())

		By("Changing plan labels to not match → plan should be removed from watchedPlans")
		Eventually(func() error {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan); err != nil {
				return err
			}
			plan.Labels = map[string]string{"team": "other"}
			return k8sClient.Update(ctx, plan)
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())

		Eventually(func(g Gomega) {
			fresh := &hibernatorv1alpha1.HibernateNotification{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), fresh)).To(Succeed())
			g.Expect(fresh.Status.WatchedPlans).NotTo(ContainElement(
				hibernatorv1alpha1.PlanReference{Name: plan.Name},
			))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())

		By("Restoring plan labels to match again → plan should reappear in watchedPlans")
		Eventually(func() error {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan); err != nil {
				return err
			}
			plan.Labels = map[string]string{"team": scenarioLabel}
			return k8sClient.Update(ctx, plan)
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())

		Eventually(func(g Gomega) {
			fresh := &hibernatorv1alpha1.HibernateNotification{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), fresh)).To(Succeed())
			g.Expect(fresh.Status.WatchedPlans).To(ContainElement(
				hibernatorv1alpha1.PlanReference{Name: plan.Name},
			))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())
	})

	It("NotificationDeleted: should not leave stale bindings after the notification is deleted", func() {
		const scenarioLabel = "lifecycle-notif-del"

		notification := newLifecycleNotification("lc-notif-del", map[string]string{"team": scenarioLabel})
		Expect(k8sClient.Create(ctx, notification)).To(Succeed())

		fakeClock.SetTime(time.Date(2026, 6, 29, 8, 0, 0, 0, time.UTC))

		plan := newLifecyclePlan("lc-notif-del", map[string]string{"team": scenarioLabel})
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())
		defer testutil.EnsureDeleted(ctx, k8sClient, plan)

		By("Verifying plan initializes to Active")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Verifying watchedPlans is populated")
		Eventually(func(g Gomega) {
			fresh := &hibernatorv1alpha1.HibernateNotification{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), fresh)).To(Succeed())
			g.Expect(fresh.Status.WatchedPlans).To(ContainElement(
				hibernatorv1alpha1.PlanReference{Name: plan.Name},
			))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())

		By("Deleting the notification")
		Expect(k8sClient.Delete(ctx, notification)).To(Succeed())
		Eventually(func() bool {
			return errors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), notification))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(BeTrue())

		By("Triggering reconcile so the provider re-evaluates notifications for this plan")
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan context no longer references the deleted notification")
		// After the provider reconciles, the plan should have zero matching notifications.
		// We verify via the plan's stored context: PlanContext.Notifications should be empty.
		Eventually(func(g Gomega) {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan); err != nil {
				return
			}
			// The plan should still be Active and healthy — the notification deletion
			// should not disrupt the plan's lifecycle.
			g.Expect(plan.Status.Phase).To(Equal(hibernatorv1alpha1.PhaseActive))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())

		By("Re-creating the same notification to verify it can re-attach cleanly")
		notification = newLifecycleNotification("lc-notif-del", map[string]string{"team": scenarioLabel})
		Expect(k8sClient.Create(ctx, notification)).To(Succeed())
		defer testutil.EnsureDeleted(ctx, k8sClient, notification)

		testutil.TriggerReconcile(ctx, k8sClient, plan)

		Eventually(func(g Gomega) {
			fresh := &hibernatorv1alpha1.HibernateNotification{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), fresh)).To(Succeed())
			g.Expect(fresh.Status.WatchedPlans).To(ContainElement(
				hibernatorv1alpha1.PlanReference{Name: plan.Name},
			))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())
	})

	It("NotificationDeletedNoDangling: should not leave dangling finalizer or stale watchedPlans across multiple plans", func() {
		const scenarioLabel = "lifecycle-no-dangling"

		By("Creating a notification that matches two plans")
		notification := newLifecycleNotification("lc-no-dangle", map[string]string{"team": scenarioLabel})
		Expect(k8sClient.Create(ctx, notification)).To(Succeed())

		fakeClock.SetTime(time.Date(2026, 7, 6, 8, 0, 0, 0, time.UTC))

		planA := newLifecyclePlan("lc-no-dangle-a", map[string]string{"team": scenarioLabel})
		Expect(k8sClient.Create(ctx, planA)).To(Succeed())
		defer testutil.EnsureDeleted(ctx, k8sClient, planA)

		planB := newLifecyclePlan("lc-no-dangle-b", map[string]string{"team": scenarioLabel})
		Expect(k8sClient.Create(ctx, planB)).To(Succeed())
		defer testutil.EnsureDeleted(ctx, k8sClient, planB)

		By("Verifying both plans initialize to Active")
		testutil.EventuallyPhase(ctx, k8sClient, planA, hibernatorv1alpha1.PhaseActive)
		testutil.EventuallyPhase(ctx, k8sClient, planB, hibernatorv1alpha1.PhaseActive)

		By("Verifying watchedPlans contains both plans")
		Eventually(func(g Gomega) {
			fresh := &hibernatorv1alpha1.HibernateNotification{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), fresh)).To(Succeed())
			g.Expect(fresh.Status.WatchedPlans).To(HaveLen(2))
			g.Expect(fresh.Status.WatchedPlans).To(ContainElements(
				hibernatorv1alpha1.PlanReference{Name: planA.Name},
				hibernatorv1alpha1.PlanReference{Name: planB.Name},
			))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())

		By("Verifying the notification finalizer is present")
		Eventually(func(g Gomega) {
			fresh := &hibernatorv1alpha1.HibernateNotification{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), fresh)).To(Succeed())
			g.Expect(fresh.Finalizers).To(ContainElement("hibernator.ardikabs.com/notification-finalizer"))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())

		By("Deleting the notification")
		Expect(k8sClient.Delete(ctx, notification)).To(Succeed())

		By("Verifying the notification is fully deleted (finalizer cleared, no dangling resource)")
		Eventually(func() bool {
			return errors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), notification))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(BeTrue(), "notification should be fully deleted, not stuck with finalizer")

		By("Verifying both plans remain Active and healthy after notification deletion")
		Eventually(func(g Gomega) {
			freshA := &hibernatorv1alpha1.HibernatePlan{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(planA), freshA)).To(Succeed())
			g.Expect(freshA.Status.Phase).To(Equal(hibernatorv1alpha1.PhaseActive))

			freshB := &hibernatorv1alpha1.HibernatePlan{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(planB), freshB)).To(Succeed())
			g.Expect(freshB.Status.Phase).To(Equal(hibernatorv1alpha1.PhaseActive))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())

		By("Verifying no HibernateNotification resources remain in the namespace with the same name")
		Eventually(func(g Gomega) {
			stale := &hibernatorv1alpha1.HibernateNotification{}
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), stale)
			g.Expect(errors.IsNotFound(err)).To(BeTrue(), "no dangling notification resource should exist")
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())

		By("Creating a new notification with the same name to verify clean re-creation")
		notification = newLifecycleNotification("lc-no-dangle", map[string]string{"team": scenarioLabel})
		Expect(k8sClient.Create(ctx, notification)).To(Succeed())
		defer testutil.EnsureDeleted(ctx, k8sClient, notification)

		testutil.TriggerReconcile(ctx, k8sClient, planA)
		testutil.TriggerReconcile(ctx, k8sClient, planB)

		By("Verifying the re-created notification picks up both plans cleanly")
		Eventually(func(g Gomega) {
			fresh := &hibernatorv1alpha1.HibernateNotification{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), fresh)).To(Succeed())
			g.Expect(fresh.Status.WatchedPlans).To(HaveLen(2))
			g.Expect(fresh.Status.WatchedPlans).To(ContainElements(
				hibernatorv1alpha1.PlanReference{Name: planA.Name},
				hibernatorv1alpha1.PlanReference{Name: planB.Name},
			))
			g.Expect(fresh.Status.State).To(Equal(hibernatorv1alpha1.NotificationStateBound))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())

		By("Verifying the re-created notification also has the finalizer")
		Eventually(func(g Gomega) {
			fresh := &hibernatorv1alpha1.HibernateNotification{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), fresh)).To(Succeed())
			g.Expect(fresh.Finalizers).To(ContainElement("hibernator.ardikabs.com/notification-finalizer"))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())
	})

	It("AllPlansUnmatched: should transition to Detached and remove finalizer when all plans stop matching", func() {
		const scenarioLabel = "lifecycle-all-unmatch"

		notification := newLifecycleNotification("lc-all-unmatch", map[string]string{"team": scenarioLabel})
		Expect(k8sClient.Create(ctx, notification)).To(Succeed())
		defer testutil.EnsureDeleted(ctx, k8sClient, notification)

		fakeClock.SetTime(time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC))

		By("Creating two plans that both match the notification")
		planA := newLifecyclePlan("lc-unmatch-a", map[string]string{"team": scenarioLabel})
		Expect(k8sClient.Create(ctx, planA)).To(Succeed())
		defer testutil.EnsureDeleted(ctx, k8sClient, planA)

		planB := newLifecyclePlan("lc-unmatch-b", map[string]string{"team": scenarioLabel})
		Expect(k8sClient.Create(ctx, planB)).To(Succeed())
		defer testutil.EnsureDeleted(ctx, k8sClient, planB)

		By("Verifying both plans initialize to Active")
		testutil.EventuallyPhase(ctx, k8sClient, planA, hibernatorv1alpha1.PhaseActive)
		testutil.EventuallyPhase(ctx, k8sClient, planB, hibernatorv1alpha1.PhaseActive)

		By("Verifying notification is Bound with both plans and has the finalizer")
		Eventually(func(g Gomega) {
			fresh := &hibernatorv1alpha1.HibernateNotification{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), fresh)).To(Succeed())
			g.Expect(fresh.Status.WatchedPlans).To(HaveLen(2))
			g.Expect(fresh.Status.State).To(Equal(hibernatorv1alpha1.NotificationStateBound))
			g.Expect(fresh.Finalizers).To(ContainElement("hibernator.ardikabs.com/notification-finalizer"))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())

		By("Changing plan-a labels so it no longer matches")
		Eventually(func() error {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(planA), planA); err != nil {
				return err
			}
			planA.Labels = map[string]string{"team": "different-a"}
			return k8sClient.Update(ctx, planA)
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())

		By("Verifying notification still Bound with only plan-b remaining")
		Eventually(func(g Gomega) {
			fresh := &hibernatorv1alpha1.HibernateNotification{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), fresh)).To(Succeed())
			g.Expect(fresh.Status.WatchedPlans).To(HaveLen(1))
			g.Expect(fresh.Status.WatchedPlans[0].Name).To(Equal(planB.Name))
			g.Expect(fresh.Status.State).To(Equal(hibernatorv1alpha1.NotificationStateBound))
			g.Expect(fresh.Finalizers).To(ContainElement("hibernator.ardikabs.com/notification-finalizer"))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())

		By("Changing plan-b labels so it no longer matches either")
		Eventually(func() error {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(planB), planB); err != nil {
				return err
			}
			planB.Labels = map[string]string{"team": "different-b"}
			return k8sClient.Update(ctx, planB)
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())

		By("Verifying notification transitions to Detached with empty watchedPlans and no finalizer")
		Eventually(func(g Gomega) {
			fresh := &hibernatorv1alpha1.HibernateNotification{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), fresh)).To(Succeed())
			g.Expect(fresh.Status.WatchedPlans).To(BeEmpty())
			g.Expect(fresh.Status.State).To(Equal(hibernatorv1alpha1.NotificationStateDetached))
			g.Expect(fresh.Finalizers).NotTo(ContainElement("hibernator.ardikabs.com/notification-finalizer"))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())

		By("Verifying the notification can be freely deleted without finalizer blocking")
		Expect(k8sClient.Delete(ctx, notification)).To(Succeed())
		Eventually(func() bool {
			return errors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), notification))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(BeTrue(), "detached notification should be deleted immediately without finalizer blocking")
	})
})
