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
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/test/e2e/helper/fakenotif"
	"github.com/ardikabs/hibernator/test/e2e/testutil"
)

var _ = Describe("Notification E2E", func() {
	var (
		plan          *hibernatorv1alpha1.HibernatePlan
		cloudProvider *hibernatorv1alpha1.CloudProvider
		notification  *hibernatorv1alpha1.HibernateNotification
		sinkSecret    *corev1.Secret
	)

	BeforeEach(func() {
		// Reset the shared fake sink before each test to prevent cross-test contamination.
		fakeNotifSink.Reset()

		By("Creating mock CloudProvider")
		cloudProvider = &hibernatorv1alpha1.CloudProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "notif-aws",
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

		By("Creating sink Secret (required by the dispatcher for secret resolution)")
		sinkSecret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "fake-notif-config",
				Namespace: testNamespace,
			},
			// The fake sink ignores config content, but the dispatcher performs a Secret
			// lookup before calling Send, so the Secret must exist with a "config" key.
			Data: map[string][]byte{
				"config": []byte(`{}`),
			},
		}
		if err := k8sClient.Create(ctx, sinkSecret); err != nil && !errors.IsAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}
	})

	AfterEach(func() {
		By("Cleaning up notification test resources")
		testutil.EnsureDeleted(ctx, k8sClient, plan)
		testutil.EnsureDeleted(ctx, k8sClient, notification)
		testutil.EnsureDeleted(ctx, k8sClient, cloudProvider)
		testutil.EnsureDeleted(ctx, k8sClient, sinkSecret)
		fakeNotifSink.Reset()
	})

	It("StartThenSuccess: should fire EventStart and EventSuccess for a full hibernate-wakeup cycle", func() {
		// The notification selector matches only plans with this label.
		const planLabel = "notif-start-success-test"

		By("Creating HibernateNotification subscribed to Start and Success events")
		notification = &hibernatorv1alpha1.HibernateNotification{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "notif-start-success",
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.HibernateNotificationSpec{
				Selector: metav1.LabelSelector{
					MatchLabels: map[string]string{"test-scenario": planLabel},
				},
				OnEvents: []hibernatorv1alpha1.NotificationEvent{
					hibernatorv1alpha1.EventStart,
					hibernatorv1alpha1.EventSuccess,
				},
				Sinks: []hibernatorv1alpha1.NotificationSink{
					{
						// SinkType matches what fakeNotifSink.Type() returns ("webhook"),
						// so the dispatcher routes requests to the in-memory fake sink.
						Name:      "fake-webhook",
						Type:      hibernatorv1alpha1.NotificationSinkType(fakenotif.SinkType),
						SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: sinkSecret.Name, Key: ptr.To("config")},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, notification)).To(Succeed())

		// Start in on-hours so the plan initialises to Active first.
		fakeClock.SetTime(time.Date(2026, 4, 6, 8, 0, 0, 0, time.UTC))

		By("Creating HibernatePlan with labels matching the notification selector")
		plan, _ = testutil.NewHibernatePlanBuilder("notif-start-success", testNamespace).
			WithLabels(map[string]string{"test-scenario": planLabel}).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "database",
				Type: "noop",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "notif-aws",
				},
			}).
			Build()
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Verifying plan initializes to Active phase")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		// ------------------------------------------------------------------ Hibernate
		By("Advancing clock to off-hours (Monday 20:01 UTC)")
		fakeClock.SetTime(time.Date(2026, 4, 6, 20, 1, 10, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan transitions to Hibernating")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Verifying EventStart notification was sent for the hibernate operation")
		Eventually(fakeNotifSink.Len, testutil.DefaultTimeout, testutil.DefaultInterval).
			Should(BeNumerically(">=", 1),
				"expected at least 1 EventStart notification after Hibernating transition")
		records := fakeNotifSink.Records()
		startHibernate := records[0]
		Expect(startHibernate.Payload.Event).To(Equal("Start"))
		Expect(startHibernate.Payload.Phase).To(Equal("Hibernating"))
		Expect(startHibernate.Payload.Operation).To(Equal("shutdown"))
		Expect(startHibernate.Payload.ID.Name).To(Equal(plan.Name))
		Expect(startHibernate.Payload.ID.Namespace).To(Equal(testNamespace))

		By("Simulating hibernation Job success")
		hibernationJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name,
			hibernatorv1alpha1.OperationHibernate, "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, hibernationJob, fakeClock.Now())

		By("Verifying plan transitions to Hibernated")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernated)

		By("Verifying EventSuccess notification was sent after hibernation completed")
		Eventually(fakeNotifSink.Len, testutil.DefaultTimeout, testutil.DefaultInterval).
			Should(BeNumerically(">=", 2),
				"expected EventSuccess notification after Hibernated transition")
		records = fakeNotifSink.Records()
		successHibernate := records[1]
		Expect(successHibernate.Payload.Event).To(Equal("Success"))
		Expect(successHibernate.Payload.Phase).To(Equal("Hibernated"))
		Expect(successHibernate.Payload.Operation).To(Equal("shutdown"))

		// Inject restore data so the wakeup transition is allowed.
		Expect(restoreManager.Save(ctx, plan.Namespace, plan.Name, plan.Spec.Targets[0].Name,
			&restore.Data{Target: plan.Spec.Targets[0].Name})).To(Succeed())

		// ------------------------------------------------------------------ Wakeup
		By("Advancing clock to on-hours (Tuesday 07:00 UTC)")
		fakeClock.SetTime(time.Date(2026, 4, 7, 7, 0, 0, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan transitions to WakingUp")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseWakingUp)

		By("Verifying EventStart notification was sent for the wakeup operation")
		Eventually(fakeNotifSink.Len, testutil.DefaultTimeout, testutil.DefaultInterval).
			Should(BeNumerically(">=", 3),
				"expected EventStart notification after WakingUp transition")
		records = fakeNotifSink.Records()
		startWakeup := records[2]
		Expect(startWakeup.Payload.Event).To(Equal("Start"))
		Expect(startWakeup.Payload.Phase).To(Equal("WakingUp"))
		Expect(startWakeup.Payload.Operation).To(Equal("wakeup"))

		By("Simulating wakeup Job success")
		wakeupJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name,
			hibernatorv1alpha1.OperationWakeUp, "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, wakeupJob, fakeClock.Now())

		By("Verifying plan returns to Active phase")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		By("Verifying EventSuccess notification was sent after wakeup completed")
		Eventually(fakeNotifSink.Len, testutil.DefaultTimeout, testutil.DefaultInterval).
			Should(BeNumerically(">=", 4),
				"expected EventSuccess notification after Active transition")
		records = fakeNotifSink.Records()
		successWakeup := records[3]
		Expect(successWakeup.Payload.Event).To(Equal("Success"))
		Expect(successWakeup.Payload.Phase).To(Equal("Active"))
		Expect(successWakeup.Payload.Operation).To(Equal("wakeup"))
	})

	It("PhaseChange: should fire EventPhaseChange on every phase transition", func() {
		const planLabel = "notif-phasechange-test"

		By("Creating HibernateNotification subscribed to PhaseChange events only")
		notification = &hibernatorv1alpha1.HibernateNotification{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "notif-phasechange",
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.HibernateNotificationSpec{
				Selector: metav1.LabelSelector{
					MatchLabels: map[string]string{"test-scenario": planLabel},
				},
				OnEvents: []hibernatorv1alpha1.NotificationEvent{
					hibernatorv1alpha1.EventPhaseChange,
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
		Expect(k8sClient.Create(ctx, notification)).To(Succeed())

		fakeClock.SetTime(time.Date(2026, 4, 13, 8, 0, 0, 0, time.UTC)) // Monday on-hours

		By("Creating HibernatePlan with labels matching the notification selector")
		plan, _ = testutil.NewHibernatePlanBuilder("notif-phasechange", testNamespace).
			WithLabels(map[string]string{"test-scenario": planLabel}).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "app",
				Type: "noop",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "notif-aws",
				},
			}).
			Build()
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Verifying plan initializes to Active")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)

		// ------------------------------------------------------------------ Active → Hibernating
		By("Advancing clock to off-hours (Monday 20:01 UTC)")
		fakeClock.SetTime(time.Date(2026, 4, 13, 20, 1, 10, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Waiting for Hibernating phase")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Verifying PhaseChange notification fired for Active→Hibernating transition")
		Eventually(func() bool {
			for _, r := range fakeNotifSink.Records() {
				if r.Payload.Event == "PhaseChange" &&
					r.Payload.Phase == "Hibernating" &&
					r.Payload.PreviousPhase == "Active" {
					return true
				}
			}
			return false
		}, testutil.DefaultTimeout, testutil.DefaultInterval).
			Should(BeTrue(), "expected PhaseChange notification for Active→Hibernating transition")

		// ------------------------------------------------------------------ Hibernating → Hibernated
		By("Simulating hibernation Job success")
		hibernationJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name,
			hibernatorv1alpha1.OperationHibernate, "app")
		testutil.SimulateJobSuccess(ctx, k8sClient, hibernationJob, fakeClock.Now())

		By("Waiting for Hibernated phase")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernated)

		By("Verifying PhaseChange notification fired for Hibernating→Hibernated transition")
		Eventually(func() bool {
			for _, r := range fakeNotifSink.Records() {
				if r.Payload.Event == "PhaseChange" &&
					r.Payload.Phase == "Hibernated" &&
					r.Payload.PreviousPhase == "Hibernating" {
					return true
				}
			}
			return false
		}, testutil.DefaultTimeout, testutil.DefaultInterval).
			Should(BeTrue(), "expected PhaseChange notification for Hibernating→Hibernated transition")
	})

	It("SelectorMismatch: should not fire notifications when plan labels do not match the selector", func() {
		By("Creating HibernateNotification that selects a different label value")
		notification = &hibernatorv1alpha1.HibernateNotification{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "notif-mismatch",
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.HibernateNotificationSpec{
				Selector: metav1.LabelSelector{
					MatchLabels: map[string]string{"test-scenario": "some-other-plan"},
				},
				OnEvents: []hibernatorv1alpha1.NotificationEvent{
					hibernatorv1alpha1.EventStart,
					hibernatorv1alpha1.EventSuccess,
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
		Expect(k8sClient.Create(ctx, notification)).To(Succeed())

		// Start inside off-hours so the plan goes directly to Hibernating.
		fakeClock.SetTime(time.Date(2026, 4, 20, 20, 1, 10, 0, time.UTC)) // Monday 20:01

		By("Creating HibernatePlan with non-matching labels")
		plan, _ = testutil.NewHibernatePlanBuilder("notif-mismatch", testNamespace).
			// Label value intentionally differs from the notification's selector.
			WithLabels(map[string]string{"test-scenario": "notif-mismatch-test"}).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "app",
				Type: "noop",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "notif-aws",
				},
			}).
			Build()
		Expect(k8sClient.Create(ctx, plan)).To(Succeed())

		By("Verifying plan transitions to Hibernating (so execution definitely ran)")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Verifying no notifications were dispatched (selector did not match)")
		Consistently(fakeNotifSink.Len, 2*time.Second, testutil.DefaultInterval).
			Should(BeZero(), "no notifications should be sent when selector does not match the plan")
	})
})
