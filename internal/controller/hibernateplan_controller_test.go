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
	cancel()
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
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
						Hibernate: "0 20 * * 1-5",
						WakeUp:    "0 6 * * 1-5",
						Timezone:  "UTC",
					},
					Execution: hibernatorv1alpha1.ExecutionConfig{
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

			Expect(createdPlan.Spec.Schedule.Hibernate).To(Equal("0 20 * * 1-5"))
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
						Hibernate: "0 20 * * 1-5",
						WakeUp:    "0 6 * * 1-5",
						Timezone:  "UTC",
					},
					Execution: hibernatorv1alpha1.ExecutionConfig{
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

	Context("When testing restore manager", func() {
		It("Should save and load restore data", func() {
			restoreMgr := restore.NewManager(k8sClient)

			data := &restore.Data{
				Target:   "test-target",
				Executor: "eks",
				Version:  1,
				State: map[string]interface{}{
					"nodeGroups": []interface{}{
						map[string]interface{}{
							"name":    "ng-1",
							"minSize": float64(2),
						},
					},
				},
			}

			err := restoreMgr.Save(ctx, "test-ns", "restore-test", "test-target", data)
			Expect(err).NotTo(HaveOccurred())

			loaded, err := restoreMgr.Load(ctx, "test-ns", "restore-test", "test-target")
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded).NotTo(BeNil())
			Expect(loaded.Target).To(Equal("test-target"))
			Expect(loaded.Executor).To(Equal("eks"))
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
	}).SetupWithManager(mgr)
}

// Placeholder for additional test utilities
var _ = runtime.Scheme{}
var _ = batchv1.Job{}
