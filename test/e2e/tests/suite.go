//go:build e2e

package tests

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/notification"
	"github.com/ardikabs/hibernator/internal/provider"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/validationwebhook"
	"github.com/ardikabs/hibernator/test/e2e/helper/fakenotif"
)

var (
	cfg           *rest.Config
	k8sClient     client.Client
	testEnv       *envtest.Environment
	ctx           context.Context
	cancel        context.CancelFunc
	mgr           manager.Manager
	fakeClock     *clocktesting.FakeClock
	fakeNotifSink *fakenotif.Sink
	// hibernateplanReconciler *hibernateplan.Reconciler
	restoreManager *restore.Manager
	testNamespace  = "hibernator-e2e-test"
)

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.Background())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			Paths: []string{filepath.Join("..", "..", "config", "webhook")},
		},
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = hibernatorv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = batchv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// Create test namespace
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: testNamespace},
	}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())

	// Set up manager and controller
	By("setting up manager and controller")
	webhookInstallOptions := &testEnv.WebhookInstallOptions
	mgr, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme:         scheme.Scheme,
		Metrics:        metricsserver.Options{BindAddress: "0"},
		LeaderElection: false,
		Cache: cache.Options{
			SyncPeriod: ptr.To(time.Second),
		},
		WebhookServer: webhook.NewServer(webhook.Options{
			Host:    webhookInstallOptions.LocalServingHost,
			Port:    webhookInstallOptions.LocalServingPort,
			CertDir: webhookInstallOptions.LocalServingCertDir,
		}),
	})
	Expect(err).NotTo(HaveOccurred())

	By("registering validation webhooks")
	err = validationwebhook.SetupWithManager(mgr, ctrl.Log.WithName("webhook"))
	Expect(err).NotTo(HaveOccurred())

	fakeClock = clocktesting.NewFakeClock(time.Now())
	restoreManager = restore.NewManager(mgr.GetClient())
	fakeNotifSink = fakenotif.New()

	err = provider.Setup(mgr, fakeClock, provider.ProviderOptions{
		Logger: ctrl.Log.WithName("e2e-test"),
		// Logger:                 logr.Discard(),
		Workers:                1,
		ScheduleBufferDuration: "1m",
		ControlPlaneEndpoint:   "https://hibernator.example.com",
		RunnerImage:            "ghcr.io/ardikabs/hibernator-runner:test",
		RunnerServiceAccount:   "hibernator-runner-sa",
		NotificationOptions: []notification.Option{
			notification.DisableDefaultSinks(),
			notification.WithSink(fakeNotifSink),
		},
	})

	Expect(err).NotTo(HaveOccurred())

	// Start the manager in a goroutine
	go func() {
		defer GinkgoRecover()
		err = mgr.Start(ctx)
		Expect(err).NotTo(HaveOccurred())
	}()

	// Wait for manager to be ready
	Eventually(mgr.GetCache().WaitForCacheSync(ctx), time.Second*10).Should(BeTrue())
})

// Global BeforeEach resets shared mutable state so that every spec — regardless of
// which Describe block it belongs to — starts with a clean slate. This is critical
// for Ginkgo multi-process parallelism (--procs=N) where spec ordering is
// non-deterministic within each process.
var _ = BeforeEach(func() {
	fakeNotifSink.Reset()
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	if cancel != nil {
		cancel()
	}
	if testEnv != nil {
		err := testEnv.Stop()
		Expect(err).NotTo(HaveOccurred())
	}
})
