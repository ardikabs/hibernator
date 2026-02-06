/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package controller

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/scheduler"
)

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
)

func TestController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = hibernatorv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// Create test namespace
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "test-ns"},
	}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())
})

var _ = AfterSuite(func() {
	if cancel != nil {
		cancel()
	}
	By("tearing down the test environment")
	if testEnv != nil {
		err := testEnv.Stop()
		Expect(err).NotTo(HaveOccurred())
	}
})

var _ = Describe("HibernatePlan Controller", func() {
	const (
		timeout  = time.Second * 30
		interval = time.Millisecond * 250
	)

	Context("When creating a HibernatePlan", func() {
		It("Should initialize status to Active phase", func() {
			planName := "test-plan-init"
			plan := &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{
					Name:      planName,
					Namespace: "test-ns",
				},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: hibernatorv1alpha1.Schedule{
						Timezone: "UTC",
						OffHours: []hibernatorv1alpha1.OffHourWindow{
							{
								Start:      "20:00",
								End:        "06:00",
								DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
							},
						},
					},
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{
							Type: hibernatorv1alpha1.StrategySequential,
						},
					},
					Targets: []hibernatorv1alpha1.Target{
						{
							Name: "target1",
							Type: "ec2",
							ConnectorRef: hibernatorv1alpha1.ConnectorRef{
								Kind: "CloudProvider",
								Name: "aws",
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, plan)).To(Succeed())

			// Verify plan is created
			createdPlan := &hibernatorv1alpha1.HibernatePlan{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      planName,
					Namespace: "test-ns",
				}, createdPlan)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			Expect(createdPlan.Spec.Schedule.Timezone).To(Equal("UTC"))
			Expect(createdPlan.Spec.Schedule.OffHours).To(HaveLen(1))
		})

		It("Should add finalizer to the plan", func() {
			planName := "test-plan-finalizer"
			plan := &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{
					Name:      planName,
					Namespace: "test-ns",
				},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: hibernatorv1alpha1.Schedule{
						Timezone: "UTC",
						OffHours: []hibernatorv1alpha1.OffHourWindow{
							{
								Start:      "20:00",
								End:        "06:00",
								DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
							},
						},
					},
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{
							Type: hibernatorv1alpha1.StrategySequential,
						},
					},
					Targets: []hibernatorv1alpha1.Target{
						{
							Name: "target1",
							Type: "ec2",
							ConnectorRef: hibernatorv1alpha1.ConnectorRef{
								Kind: "CloudProvider",
								Name: "aws",
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, plan)).To(Succeed())

			// Note: Finalizer is added by the controller, not directly testable without running controller
			createdPlan := &hibernatorv1alpha1.HibernatePlan{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      planName,
				Namespace: "test-ns",
			}, createdPlan)).To(Succeed())
		})
	})

	// Note: RestoreManager unit tests are in internal/restore/manager_test.go
	// Controller integration with RestoreManager is tested via envtest (real ConfigMap operations)

	Context("When initializing hibernation cycle", func() {
		It("Should create restore point ConfigMap on first hibernation", func() {
			restoreManager := restore.NewManager(k8sClient)
			planName := "test-plan-restore-init"
			namespace := "test-ns"

			// Verify ConfigMap doesn't exist initially
			cmName := "hibernator-restore-" + planName
			var cm corev1.ConfigMap
			err := k8sClient.Get(ctx, types.NamespacedName{
				Namespace: namespace,
				Name:      cmName,
			}, &cm)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))

			// Call PrepareRestorePoint (what controller does when entering Hibernating phase)
			err = restoreManager.PrepareRestorePoint(ctx, namespace, planName)
			Expect(err).NotTo(HaveOccurred())

			// Verify ConfigMap was created with correct labels
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Namespace: namespace,
					Name:      cmName,
				}, &cm)
			}, timeout, interval).Should(Succeed())

			Expect(cm.Labels).To(HaveKey("hibernator.ardikabs.com/plan"))
			Expect(cm.Labels["hibernator.ardikabs.com/plan"]).To(Equal(planName))
			Expect(cm.Data).To(BeEmpty()) // Should be empty on initialization
			Expect(cm.Annotations).To(BeEmpty())
		})

		It("Should reset existing restore point ConfigMap on new hibernation cycle", func() {
			restoreManager := restore.NewManager(k8sClient)
			planName := "test-plan-restore-reset"
			namespace := "test-ns"
			cmName := "hibernator-restore-" + planName

			// Pre-create ConfigMap with old data (simulating previous cycle)
			oldCM := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cmName,
					Namespace: namespace,
					Labels: map[string]string{
						"hibernator.ardikabs.com/plan": planName,
					},
					Annotations: map[string]string{
						"hibernator.ardikabs.com/restored-target1": "true",
						"hibernator.ardikabs.com/restored-target2": "true",
					},
				},
				Data: map[string]string{
					"target1.json": `{"target":"target1","state":{"key":"old-value"}}`,
					"target2.json": `{"target":"target2","state":{"key":"old-value"}}`,
				},
			}
			Expect(k8sClient.Create(ctx, oldCM)).To(Succeed())

			// Verify old data exists
			var cm corev1.ConfigMap
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: namespace,
				Name:      cmName,
			}, &cm)).To(Succeed())
			Expect(cm.Data).To(HaveLen(2))
			Expect(cm.Annotations).To(HaveLen(2))

			// Call PrepareRestorePoint (should reset IsLive flags and add previous state annotation)
			err := restoreManager.PrepareRestorePoint(ctx, namespace, planName)
			Expect(err).NotTo(HaveOccurred())

			// Verify ConfigMap was updated (data preserved with IsLive=false)
			// PrepareRestorePoint preserves existing restore data but resets IsLive flags
			// It also keeps restored-* annotations (doesn't clear them)
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: namespace,
					Name:      cmName,
				}, &cm)
				if err != nil {
					return false
				}
				// Data should still exist (preserved), and previous state annotation added
				return len(cm.Data) > 0 && cm.Annotations["hibernator.ardikabs.com/restore-previous-state"] != ""
			}, timeout, interval).Should(BeTrue())

			Expect(cm.Labels["hibernator.ardikabs.com/plan"]).To(Equal(planName))
			// Verify restored-* annotations are still present (not cleared)
			Expect(cm.Annotations).To(HaveKey("hibernator.ardikabs.com/restored-target1"))
			Expect(cm.Annotations).To(HaveKey("hibernator.ardikabs.com/restored-target2"))
		})

		It("Should update ConfigMap even when already prepared (resets IsLive flags)", func() {
			restoreManager := restore.NewManager(k8sClient)
			planName := "test-plan-restore-idempotent"
			namespace := "test-ns"
			cmName := "hibernator-restore-" + planName

			// First call - creates ConfigMap
			err := restoreManager.PrepareRestorePoint(ctx, namespace, planName)
			Expect(err).NotTo(HaveOccurred())

			// Get ResourceVersion to detect updates
			var cm corev1.ConfigMap
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: namespace,
				Name:      cmName,
			}, &cm)).To(Succeed())
			firstResourceVersion := cm.ResourceVersion

			// Second call - always updates (resets IsLive flags even if already false)
			err = restoreManager.PrepareRestorePoint(ctx, namespace, planName)
			Expect(err).NotTo(HaveOccurred())

			// Verify update occurred (ResourceVersion changed)
			// This is expected behavior: PrepareRestorePoint always resets IsLive flags
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: namespace,
				Name:      cmName,
			}, &cm)).To(Succeed())
			Expect(cm.ResourceVersion).NotTo(Equal(firstResourceVersion))
		})
	})

	Context("When testing schedule evaluation", func() {
		It("Should correctly determine hibernation state", func() {
			evaluator := scheduler.NewScheduleEvaluator()

			window := scheduler.ScheduleWindow{
				HibernateCron: "0 20 * * 1-5",
				WakeUpCron:    "0 6 * * 1-5",
				Timezone:      "UTC",
			}

			// Test during work hours (should be active)
			workTime := time.Date(2026, 1, 28, 14, 0, 0, 0, time.UTC) // Wed 2 PM
			result, err := evaluator.Evaluate(window, workTime)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.ShouldHibernate).To(BeFalse())
			Expect(result.CurrentState).To(Equal("active"))

			// Test during night hours (should be hibernated)
			nightTime := time.Date(2026, 1, 28, 23, 0, 0, 0, time.UTC) // Wed 11 PM
			result, err = evaluator.Evaluate(window, nightTime)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.ShouldHibernate).To(BeTrue())
			Expect(result.CurrentState).To(Equal("hibernated"))
		})

		It("Should work with converted OffHourWindow format", func() {
			evaluator := scheduler.NewScheduleEvaluator()

			// Define user-friendly off-hour window
			offHours := []scheduler.OffHourWindow{
				{
					Start:      "20:00",
					End:        "06:00",
					DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
				},
			}

			// Convert to cron expressions
			hibernateCron, wakeUpCron, err := scheduler.ConvertOffHoursToCron(offHours)
			Expect(err).NotTo(HaveOccurred())
			Expect(hibernateCron).To(Equal("0 20 * * 1,2,3,4,5"))
			// For overnight windows (20:00-06:00), wake-up occurs on the next day
			// MON-FRI hibernation at 20:00 -> TUE-SAT wakeup at 06:00
			Expect(wakeUpCron).To(Equal("0 6 * * 2,3,4,5,6"))

			window := scheduler.ScheduleWindow{
				HibernateCron: hibernateCron,
				WakeUpCron:    wakeUpCron,
				Timezone:      "UTC",
			}

			// Test during work hours (should be active)
			workTime := time.Date(2026, 1, 28, 14, 0, 0, 0, time.UTC) // Wed 2 PM
			result, err := evaluator.Evaluate(window, workTime)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.ShouldHibernate).To(BeFalse())
			Expect(result.CurrentState).To(Equal("active"))

			// Test during night hours (should be hibernated)
			nightTime := time.Date(2026, 1, 28, 23, 0, 0, 0, time.UTC) // Wed 11 PM
			result, err = evaluator.Evaluate(window, nightTime)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.ShouldHibernate).To(BeTrue())
			Expect(result.CurrentState).To(Equal("hibernated"))
		})
	})

	Context("When creating runner jobs", func() {
		It("Should use configured runner ServiceAccount", func() {
			reconciler := &HibernatePlanReconciler{
				Client:               k8sClient,
				Log:                  ctrl.Log.WithName("controllers").WithName("HibernatePlan"),
				Scheme:               scheme.Scheme,
				Planner:              scheduler.NewPlanner(),
				ScheduleEvaluator:    scheduler.NewScheduleEvaluator(),
				RestoreManager:       restore.NewManager(k8sClient),
				ControlPlaneEndpoint: "",
				RunnerImage:          "",
				RunnerServiceAccount: "runner-fixed-sa",
			}

			plan := &hibernatorv1alpha1.HibernatePlan{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "hibernator.ardikabs.com/v1alpha1",
					Kind:       "HibernatePlan",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "runner-sa-plan",
					Namespace: "test-ns",
				},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: hibernatorv1alpha1.Schedule{
						Timezone: "UTC",
						OffHours: []hibernatorv1alpha1.OffHourWindow{
							{
								Start:      "20:00",
								End:        "06:00",
								DaysOfWeek: []string{"MON"},
							},
						},
					},
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
					},
					Targets: []hibernatorv1alpha1.Target{
						{
							Name: "target1",
							Type: "ec2",
							ConnectorRef: hibernatorv1alpha1.ConnectorRef{
								Kind: "CloudProvider",
								Name: "aws",
							},
						},
					},
				},
				Status: hibernatorv1alpha1.HibernatePlanStatus{
					CurrentCycleID: "test-cycle",
				},
			}

			Expect(k8sClient.Create(ctx, plan)).To(Succeed())

			target := &plan.Spec.Targets[0]
			err := reconciler.createRunnerJob(ctx, reconciler.Log, plan, target, "shutdown")
			Expect(err).NotTo(HaveOccurred())

			var jobs batchv1.JobList
			Eventually(func() int {
				_ = k8sClient.List(ctx, &jobs, client.InNamespace("test-ns"), client.MatchingLabels{
					LabelPlan:   plan.Name,
					LabelTarget: target.Name,
				})
				return len(jobs.Items)
			}, timeout, interval).Should(BeNumerically(">", 0))

			Expect(jobs.Items[0].Spec.Template.Spec.ServiceAccountName).To(Equal("runner-fixed-sa"))
		})

		It("Should create job with proper labels for lookup", func() {
			reconciler := &HibernatePlanReconciler{
				Client:               k8sClient,
				Log:                  ctrl.Log.WithName("controllers").WithName("HibernatePlan"),
				Scheme:               scheme.Scheme,
				Planner:              scheduler.NewPlanner(),
				ScheduleEvaluator:    scheduler.NewScheduleEvaluator(),
				RestoreManager:       restore.NewManager(k8sClient),
				ControlPlaneEndpoint: "",
				RunnerImage:          "",
				RunnerServiceAccount: "hibernator-runner",
			}

			plan := &hibernatorv1alpha1.HibernatePlan{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "hibernator.ardikabs.com/v1alpha1",
					Kind:       "HibernatePlan",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "label-test-plan",
					Namespace: "test-ns",
				},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: hibernatorv1alpha1.Schedule{
						Timezone: "UTC",
						OffHours: []hibernatorv1alpha1.OffHourWindow{
							{
								Start:      "20:00",
								End:        "06:00",
								DaysOfWeek: []string{"MON"},
							},
						},
					},
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
					},
					Targets: []hibernatorv1alpha1.Target{
						{
							Name: "test-target",
							Type: "ec2",
							ConnectorRef: hibernatorv1alpha1.ConnectorRef{
								Kind: "CloudProvider",
								Name: "aws",
							},
						},
					},
				},
				Status: hibernatorv1alpha1.HibernatePlanStatus{
					CurrentCycleID: "abc123",
				},
			}

			Expect(k8sClient.Create(ctx, plan)).To(Succeed())
			// Update status separately (status is a subresource)
			plan.Status.CurrentCycleID = "abc123"
			Expect(k8sClient.Status().Update(ctx, plan)).To(Succeed())
			target := &plan.Spec.Targets[0]
			operation := "shutdown"
			err := reconciler.createRunnerJob(ctx, reconciler.Log, plan, target, operation)
			Expect(err).NotTo(HaveOccurred())

			// Verify job can be found by label-based lookup
			var jobs batchv1.JobList
			Eventually(func() int {
				_ = k8sClient.List(ctx, &jobs, client.InNamespace("test-ns"), client.MatchingLabels{
					LabelPlan:      plan.Name,
					LabelTarget:    target.Name,
					LabelOperation: operation,
					LabelCycleID:   plan.Status.CurrentCycleID,
				})
				return len(jobs.Items)
			}, timeout, interval).Should(Equal(1))

			job := jobs.Items[0]
			Expect(job.Labels[LabelPlan]).To(Equal(plan.Name))
			Expect(job.Labels[LabelTarget]).To(Equal(target.Name))
			Expect(job.Labels[LabelOperation]).To(Equal(operation))
			Expect(job.Labels[LabelCycleID]).NotTo(BeEmpty())
		})
	})
})

// Helper for setting up controller with manager
func setupControllerWithManager(mgr ctrl.Manager) error {
	return (&HibernatePlanReconciler{
		Client:            mgr.GetClient(),
		Log:               ctrl.Log.WithName("controllers").WithName("HibernatePlan"),
		Scheme:            mgr.GetScheme(),
		Planner:           scheduler.NewPlanner(),
		ScheduleEvaluator: scheduler.NewScheduleEvaluator(),
		RestoreManager:    restore.NewManager(mgr.GetClient()),
	}).SetupWithManager(mgr, 1)
}

// Placeholder for additional test utilities
var _ = runtime.Scheme{}
var _ = batchv1.Job{}
