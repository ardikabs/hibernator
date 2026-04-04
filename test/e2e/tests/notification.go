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
	"sigs.k8s.io/controller-runtime/pkg/client"
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
		Expect(startHibernate.Payload.Plan.Name).To(Equal(plan.Name))
		Expect(startHibernate.Payload.Plan.Namespace).To(Equal(testNamespace))

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

	It("WebhookRejects_DuplicateSinkNames: should reject notification with duplicate sink names", func() {
		By("Attempting to create HibernateNotification with duplicate sink names")
		dup := &hibernatorv1alpha1.HibernateNotification{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "notif-dup-sink",
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.HibernateNotificationSpec{
				Selector: metav1.LabelSelector{
					MatchLabels: map[string]string{"app": "test"},
				},
				OnEvents: []hibernatorv1alpha1.NotificationEvent{hibernatorv1alpha1.EventStart},
				Sinks: []hibernatorv1alpha1.NotificationSink{
					{
						Name:      "my-sink",
						Type:      hibernatorv1alpha1.SinkSlack,
						SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s1"},
					},
					{
						Name:      "my-sink",
						Type:      hibernatorv1alpha1.SinkTelegram,
						SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s2"},
					},
				},
			},
		}

		err := k8sClient.Create(ctx, dup)
		Expect(err).To(HaveOccurred(), "Webhook must reject HibernateNotification with duplicate sink names")
		Expect(errors.IsInvalid(err) || errors.IsForbidden(err)).To(BeTrue(),
			"Expected 422 Invalid or 403 Forbidden, got: %v", err)
	})

	It("WebhookRejects_MultipleDuplicateSinkNames: should reject notification with multiple duplicate sink names", func() {
		By("Attempting to create HibernateNotification with multiple duplicate sink names")
		dup := &hibernatorv1alpha1.HibernateNotification{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "notif-multi-dup-sink",
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.HibernateNotificationSpec{
				Selector: metav1.LabelSelector{
					MatchLabels: map[string]string{"app": "test"},
				},
				OnEvents: []hibernatorv1alpha1.NotificationEvent{hibernatorv1alpha1.EventStart},
				Sinks: []hibernatorv1alpha1.NotificationSink{
					{
						Name:      "dup",
						Type:      hibernatorv1alpha1.SinkSlack,
						SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s1"},
					},
					{
						Name:      "unique",
						Type:      hibernatorv1alpha1.SinkTelegram,
						SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s2"},
					},
					{
						Name:      "dup",
						Type:      hibernatorv1alpha1.SinkWebhook,
						SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s3"},
					},
					{
						Name:      "dup",
						Type:      hibernatorv1alpha1.SinkSlack,
						SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s4"},
					},
				},
			},
		}

		err := k8sClient.Create(ctx, dup)
		Expect(err).To(HaveOccurred(), "Webhook must reject HibernateNotification with multiple duplicate sink names")
		Expect(errors.IsInvalid(err) || errors.IsForbidden(err)).To(BeTrue(),
			"Expected 422 Invalid or 403 Forbidden, got: %v", err)
	})

	It("WebhookAccepts_UniqueSinkNames: should accept notification with unique sink names", func() {
		By("Creating HibernateNotification with unique sink names")
		notif := &hibernatorv1alpha1.HibernateNotification{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "notif-unique-sinks",
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.HibernateNotificationSpec{
				Selector: metav1.LabelSelector{
					MatchLabels: map[string]string{"app": "test"},
				},
				OnEvents: []hibernatorv1alpha1.NotificationEvent{hibernatorv1alpha1.EventStart},
				Sinks: []hibernatorv1alpha1.NotificationSink{
					{
						Name:      "slack-prod",
						Type:      hibernatorv1alpha1.SinkSlack,
						SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s1"},
					},
					{
						Name:      "telegram-prod",
						Type:      hibernatorv1alpha1.SinkTelegram,
						SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s2"},
					},
					{
						Name:      "webhook-prod",
						Type:      hibernatorv1alpha1.SinkWebhook,
						SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s3"},
					},
				},
			},
		}

		Expect(k8sClient.Create(ctx, notif)).To(Succeed(),
			"Webhook should accept HibernateNotification with unique sink names")

		By("Cleaning up accepted notification")
		testutil.EnsureDeleted(ctx, k8sClient, notif)
	})

	It("WebhookRejects_UpdateIntroducesDuplicateSinkNames: should reject update that introduces duplicate sink names", func() {
		By("Creating a valid HibernateNotification with unique sink names")
		notif := &hibernatorv1alpha1.HibernateNotification{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "notif-update-dup",
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.HibernateNotificationSpec{
				Selector: metav1.LabelSelector{
					MatchLabels: map[string]string{"app": "test"},
				},
				OnEvents: []hibernatorv1alpha1.NotificationEvent{hibernatorv1alpha1.EventStart},
				Sinks: []hibernatorv1alpha1.NotificationSink{
					{
						Name:      "sink-a",
						Type:      hibernatorv1alpha1.SinkSlack,
						SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s1"},
					},
					{
						Name:      "sink-b",
						Type:      hibernatorv1alpha1.SinkTelegram,
						SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s2"},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, notif)).To(Succeed())

		By("Updating the notification to introduce duplicate sink names")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(notif), notif)).To(Succeed())
		notif.Spec.Sinks = []hibernatorv1alpha1.NotificationSink{
			{
				Name:      "sink-a",
				Type:      hibernatorv1alpha1.SinkSlack,
				SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s1"},
			},
			{
				Name:      "sink-a",
				Type:      hibernatorv1alpha1.SinkTelegram,
				SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s2"},
			},
		}

		err := k8sClient.Update(ctx, notif)
		Expect(err).To(HaveOccurred(), "Webhook must reject update that introduces duplicate sink names")
		Expect(errors.IsInvalid(err) || errors.IsForbidden(err)).To(BeTrue(),
			"Expected 422 Invalid or 403 Forbidden, got: %v", err)

		By("Cleaning up notification")
		testutil.EnsureDeleted(ctx, k8sClient, notif)
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

	It("NotificationLifecycle: should populate watchedPlans, sinkStatuses, and delivery timestamps after successful notification", func() {
		const planLabel = "notif-status-test"

		By("Creating HibernateNotification subscribed to Start events")
		notification = &hibernatorv1alpha1.HibernateNotification{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "notif-status",
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.HibernateNotificationSpec{
				Selector: metav1.LabelSelector{
					MatchLabels: map[string]string{"test-scenario": planLabel},
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
		Expect(k8sClient.Create(ctx, notification)).To(Succeed())

		// Start in on-hours so the plan initializes to Active.
		fakeClock.SetTime(time.Date(2026, 5, 4, 8, 0, 0, 0, time.UTC))

		By("Creating HibernatePlan with labels matching the notification selector")
		plan, _ = testutil.NewHibernatePlanBuilder("notif-status", testNamespace).
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

		By("Verifying watchedPlans is populated with the matching plan")
		Eventually(func(g Gomega) {
			fresh := &hibernatorv1alpha1.HibernateNotification{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), fresh)).To(Succeed())
			g.Expect(fresh.Status.WatchedPlans).To(ContainElement(
				hibernatorv1alpha1.PlanReference{Name: plan.Name},
			))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())

		// ------------------------------------------------------------------ Trigger hibernation
		By("Advancing clock to off-hours to trigger hibernation")
		fakeClock.SetTime(time.Date(2026, 5, 4, 20, 1, 10, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan transitions to Hibernating")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Waiting for at least one notification to be sent")
		Eventually(fakeNotifSink.Len, testutil.DefaultTimeout, testutil.DefaultInterval).
			Should(BeNumerically(">=", 1))

		By("Verifying notification status has sinkStatuses and lastDeliveryTime populated")
		Eventually(func(g Gomega) {
			fresh := &hibernatorv1alpha1.HibernateNotification{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), fresh)).To(Succeed())

			// SinkStatuses history should have at least one entry.
			g.Expect(fresh.Status.SinkStatuses).NotTo(BeEmpty(), "sinkStatuses should have delivery history")

			// The newest entry should be a success for our fake-webhook sink.
			newest := fresh.Status.SinkStatuses[0]
			g.Expect(newest.Name).To(Equal("fake-webhook"))
			g.Expect(newest.Success).To(BeTrue())
			g.Expect(newest.TransitionTimestamp.IsZero()).To(BeFalse())
			g.Expect(newest.Message).NotTo(BeEmpty())

			// LastDeliveryTime should be set after a successful delivery.
			g.Expect(fresh.Status.LastDeliveryTime).NotTo(BeNil(), "lastDeliveryTime should be set after successful delivery")
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())
	})

	It("NotificationLifecycleOnUpdate: should track multiple plans in watchedPlans when selector matches more than one plan", func() {
		const planLabel = "notif-multi-plan-test"

		var plan2 *hibernatorv1alpha1.HibernatePlan

		By("Creating HibernateNotification subscribed to Start events")
		notification = &hibernatorv1alpha1.HibernateNotification{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "notif-multi-plan",
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.HibernateNotificationSpec{
				Selector: metav1.LabelSelector{
					MatchLabels: map[string]string{"test-scenario": planLabel},
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
		Expect(k8sClient.Create(ctx, notification)).To(Succeed())

		fakeClock.SetTime(time.Date(2026, 5, 11, 8, 0, 0, 0, time.UTC))

		By("Creating first HibernatePlan with matching labels")
		plan, _ = testutil.NewHibernatePlanBuilder("notif-mp-plan-a", testNamespace).
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

		By("Creating second HibernatePlan with matching labels")
		plan2, _ = testutil.NewHibernatePlanBuilder("notif-mp-plan-b", testNamespace).
			WithLabels(map[string]string{"test-scenario": planLabel}).
			WithSchedule("20:00", "06:00", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN").
			WithExecutionStrategy(hibernatorv1alpha1.ExecutionStrategy{
				Type: hibernatorv1alpha1.StrategySequential,
			}).
			WithTarget(hibernatorv1alpha1.Target{
				Name: "cache",
				Type: "noop",
				ConnectorRef: hibernatorv1alpha1.ConnectorRef{
					Kind: "CloudProvider",
					Name: "notif-aws",
				},
			}).
			Build()
		Expect(k8sClient.Create(ctx, plan2)).To(Succeed())

		By("Verifying both plans initialize to Active")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseActive)
		testutil.EventuallyPhase(ctx, k8sClient, plan2, hibernatorv1alpha1.PhaseActive)

		By("Verifying watchedPlans contains both plans (sorted by name)")
		Eventually(func(g Gomega) {
			fresh := &hibernatorv1alpha1.HibernateNotification{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), fresh)).To(Succeed())
			g.Expect(fresh.Status.WatchedPlans).To(HaveLen(2))
			g.Expect(fresh.Status.WatchedPlans[0].Name).To(HavePrefix("notif-mp-plan-a"))
			g.Expect(fresh.Status.WatchedPlans[1].Name).To(HavePrefix("notif-mp-plan-b"))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())

		// Cleanup plan2 explicitly since AfterEach only cleans `plan`.
		defer testutil.EnsureDeleted(ctx, k8sClient, plan2)
	})

	It("NotificationLifecycleOnProgress: should accumulate sinkStatuses history across multiple delivery cycles", func() {
		const planLabel = "notif-history-test"

		By("Creating HibernateNotification subscribed to Start and Success events")
		notification = &hibernatorv1alpha1.HibernateNotification{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "notif-history",
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
						Name:      "fake-webhook",
						Type:      hibernatorv1alpha1.NotificationSinkType(fakenotif.SinkType),
						SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: sinkSecret.Name, Key: ptr.To("config")},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, notification)).To(Succeed())

		fakeClock.SetTime(time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC))

		By("Creating HibernatePlan with matching labels")
		plan, _ = testutil.NewHibernatePlanBuilder("notif-history", testNamespace).
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

		// ------------------------------------------------------------------ First cycle: hibernate
		By("Advancing clock to off-hours to trigger hibernation")
		fakeClock.SetTime(time.Date(2026, 5, 18, 20, 1, 10, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan transitions to Hibernating")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)

		By("Waiting for Start notification")
		Eventually(fakeNotifSink.Len, testutil.DefaultTimeout, testutil.DefaultInterval).
			Should(BeNumerically(">=", 1))

		By("Simulating hibernation Job success")
		hibernationJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name,
			hibernatorv1alpha1.OperationHibernate, "app")
		testutil.SimulateJobSuccess(ctx, k8sClient, hibernationJob, fakeClock.Now())

		By("Verifying plan transitions to Hibernated")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernated)

		By("Waiting for Success notification")
		Eventually(fakeNotifSink.Len, testutil.DefaultTimeout, testutil.DefaultInterval).
			Should(BeNumerically(">=", 2))

		By("Verifying sinkStatuses history has multiple entries in newest-first order")
		Eventually(func(g Gomega) {
			fresh := &hibernatorv1alpha1.HibernateNotification{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(notification), fresh)).To(Succeed())

			// At least 2 entries (Start + Success) delivered.
			g.Expect(len(fresh.Status.SinkStatuses)).To(BeNumerically(">=", 2),
				"sinkStatuses should have at least 2 entries after Start+Success delivery")

			// History is newest-first: the second delivery (Success) should appear before the first (Start).
			g.Expect(fresh.Status.SinkStatuses[0].TransitionTimestamp.Time).To(
				BeTemporally(">=", fresh.Status.SinkStatuses[1].TransitionTimestamp.Time),
				"sinkStatuses should be ordered newest-first",
			)

			// All entries should be successes since the fake sink never errors.
			for _, ss := range fresh.Status.SinkStatuses {
				g.Expect(ss.Success).To(BeTrue())
				g.Expect(ss.Name).To(Equal("fake-webhook"))
			}

			// History should not exceed the cap.
			g.Expect(len(fresh.Status.SinkStatuses)).To(BeNumerically("<=", hibernatorv1alpha1.MaxSinkStatusHistory))
		}, testutil.DefaultTimeout, testutil.DefaultInterval).Should(Succeed())
	})

	It("ExecutionProgress: should fire ExecutionProgress notifications on target state transitions during hibernation", func() {
		const planLabel = "notif-exec-progress-test"

		By("Creating HibernateNotification subscribed to ExecutionProgress events")
		notification = &hibernatorv1alpha1.HibernateNotification{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "notif-exec-progress",
				Namespace: testNamespace,
			},
			Spec: hibernatorv1alpha1.HibernateNotificationSpec{
				Selector: metav1.LabelSelector{
					MatchLabels: map[string]string{"test-scenario": planLabel},
				},
				OnEvents: []hibernatorv1alpha1.NotificationEvent{
					hibernatorv1alpha1.EventExecutionProgress,
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

		// Start in on-hours so the plan initialises to Active first.
		fakeClock.SetTime(time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC))

		By("Creating HibernatePlan with labels matching the notification selector")
		plan, _ = testutil.NewHibernatePlanBuilder("notif-exec-progress", testNamespace).
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

		// ------------------------------------------------------------------ Trigger hibernation
		By("Advancing clock to off-hours to trigger hibernation")
		fakeClock.SetTime(time.Date(2026, 6, 1, 20, 1, 10, 0, time.UTC))
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Verifying plan transitions to Hibernating")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernating)
		hibernatingJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name, hibernatorv1alpha1.OperationHibernate, "database")
		testutil.SimulateJobRunning(ctx, k8sClient, hibernatingJob, fakeClock.Now())
		testutil.TriggerReconcile(ctx, k8sClient, plan)

		By("Waiting for ExecutionProgress notification for target state transition")
		Eventually(func() bool {
			for _, r := range fakeNotifSink.Records() {
				if r.Payload.Event == "ExecutionProgress" &&
					r.Payload.TargetExecution != nil &&
					r.Payload.TargetExecution.Name == "database" {
					return true
				}
			}
			return false
		}, testutil.DefaultTimeout, testutil.DefaultInterval).
			Should(BeTrue(), "expected at least one ExecutionProgress notification with TargetExecution for 'database'")

		By("Verifying ExecutionProgress payload carries correct TargetExecution fields")
		var progressRecords []fakenotif.Record
		for _, r := range fakeNotifSink.Records() {
			if r.Payload.Event == "ExecutionProgress" && r.Payload.TargetExecution != nil {
				progressRecords = append(progressRecords, r)
			}
		}
		Expect(progressRecords).NotTo(BeEmpty())

		firstProgress := progressRecords[0]
		Expect(firstProgress.Payload.TargetExecution.Name).To(Equal("database"))
		Expect(firstProgress.Payload.TargetExecution.Executor).To(Equal("noop"))
		Expect(firstProgress.Payload.Phase).To(Equal("Hibernating"))
		Expect(firstProgress.Payload.Operation).To(Equal("shutdown"))
		Expect(firstProgress.Payload.Plan.Name).To(Equal(plan.Name))
		Expect(firstProgress.Payload.Plan.Namespace).To(Equal(testNamespace))

		// ------------------------------------------------------------------ Simulate Job success
		preSuccessCount := len(progressRecords)

		By("Simulating hibernation Job success")
		hibernationJob := testutil.EventuallyJobCreated(ctx, k8sClient, testNamespace, plan.Name,
			hibernatorv1alpha1.OperationHibernate, "database")
		testutil.SimulateJobSuccess(ctx, k8sClient, hibernationJob, fakeClock.Now())

		By("Verifying plan transitions to Hibernated")
		testutil.EventuallyPhase(ctx, k8sClient, plan, hibernatorv1alpha1.PhaseHibernated)

		By("Waiting for additional ExecutionProgress notification after Job completion (Running → Completed)")
		Eventually(func() int {
			count := 0
			for _, r := range fakeNotifSink.Records() {
				if r.Payload.Event == "ExecutionProgress" && r.Payload.TargetExecution != nil {
					count++
				}
			}
			return count
		}, testutil.DefaultTimeout, testutil.DefaultInterval).
			Should(BeNumerically(">", preSuccessCount),
				"expected additional ExecutionProgress notification after target completed")

		By("Verifying the completed target notification has Completed state")
		Eventually(func() bool {
			for _, r := range fakeNotifSink.Records() {
				if r.Payload.Event == "ExecutionProgress" &&
					r.Payload.TargetExecution != nil &&
					r.Payload.TargetExecution.Name == "database" &&
					r.Payload.TargetExecution.State == "Completed" {
					return true
				}
			}
			return false
		}, testutil.DefaultTimeout, testutil.DefaultInterval).
			Should(BeTrue(), "expected ExecutionProgress notification with state=Completed for 'database'")
	})
})
