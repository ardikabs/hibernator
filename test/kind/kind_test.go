//go:build kind

package kind

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/ardikabs/hibernator/test/kind/suite"
)

func TestMain(m *testing.M) {
	if os.Getenv("KIND_UNIT_ONLY") == "1" {
		os.Exit(m.Run())
	}
	// Silence controller-runtime's unset-logger diagnostics (API warning
	// headers otherwise dump stacks to test output).
	ctrl.SetLogger(logr.Discard())
	tb := suite.MainTB{}
	suite.CurrentRun = suite.NewRunConfig(os.Getpid())

	// Isolated kubeconfig: kind/kubectl/client all use this file, so the
	// user's ~/.kube/config is never touched.
	suite.KubeconfigPath = filepath.Join(tb.TempDir(), "kubeconfig")
	if err := os.WriteFile(suite.KubeconfigPath, []byte{}, 0600); err != nil {
		log.Fatalf("setup: init kubeconfig placeholder: %v", err)
	}

	code := 1
	defer func() {
		if os.Getenv("KEEP_KIND") != "1" && !(code != 0 && os.Getenv("KEEP_KIND_ON_FAILURE") == "1") {
			log.Printf("deleting suite-owned kind cluster %q", suite.CurrentRun.ClusterName)
			cmd := exec.Command(suite.KindBin(tb), "delete", "cluster", "--name", suite.CurrentRun.ClusterName)
			if out, err := cmd.CombinedOutput(); err != nil {
				log.Printf("delete cluster: %v\n%s", err, out)
			}
		} else {
			log.Printf("suite-owned cluster %q left running", suite.CurrentRun.ClusterName)
		}
		if recovered := recover(); recovered != nil {
			log.Printf("kind setup failed: %v", recovered)
			code = 1
		}
		os.Exit(code)
	}()

	suite.EnsureCluster(tb)
	suite.KubeconfigFor(tb)
	suite.K8sClient = suite.NewK8sClient(tb)
	restCfg, err := clientcmd.BuildConfigFromFlags("", suite.KubeconfigPath)
	if err != nil {
		tb.Fatalf("load kubeconfig for clientset: %v", err)
	}
	suite.K8sSets, err = kubernetes.NewForConfig(restCfg)
	if err != nil {
		tb.Fatalf("create clientset: %v", err)
	}
	suite.BuildAndLoad(tb)

	kubecfg := suite.KubeconfigPath
	suite.KubectlApply(tb, kubecfg, "config/crd/bases")
	suite.KubectlApply(tb, kubecfg, "config/manager/manager.yaml")
	suite.KubectlApply(tb, kubecfg, "config/rbac/runner_role.yaml")
	// Production-grade RBAC last: the ClusterRole embedded in manager.yaml is
	// stale (missing scheduleexceptions/notifications/pods/...); this copy
	// mirrors the Helm chart and replaces it wholesale by object name.
	suite.KubectlApply(tb, kubecfg, "test/kind/manifests/controller-clusterrole.yaml")
	suite.EnsureWebhookCertSecret(tb, suite.K8sClient)
	suite.PatchControllerDeployment(tb, suite.K8sClient)
	suite.WaitForDeployment(tb, suite.K8sClient, suite.SystemNamespace, suite.ControllerDeploy, 5*time.Minute)

	code = m.Run()
}

// TestNoopScheduleCycle is the nightly scheduler integration check. The
// default PR smoke uses manual overrides to avoid a fixed wall-clock wait;
// this opt-in test proves schedule-driven transitions with noop targets,
// so no cloud backend is involved.
//
// The window resolves via resolveScheduleWindow: KIND_SCHEDULE_START/END
// ("15:04" UTC) pin absolute anchors for the nightly run (23:55-00:05 across
// midnight); when unset, a relative window is used for ad-hoc local runs.
//
// Three plans share one namespace to cover the midnight crossing from every
// side: a normal schedule transitioning both ways across midnight, a
// full-day hibernation holding Hibernated across midnight, and a
// full-day-active plan asserting zero Jobs across midnight.
func TestNoopScheduleCycle(t *testing.T) {
	if os.Getenv("RUN_KIND_SCHEDULE") != "1" {
		t.Skip("set RUN_KIND_SCHEDULE=1 for the real wall-clock schedule cycle")
	}

	window := resolveScheduleWindow(t)

	ctx := context.Background()
	c := suite.K8sClient
	require.NotNil(t, c, "TestMain must provision the cluster client")

	ns := suite.CurrentRun.Namespace + "-schedule"
	suite.EnsureNamespace(suite.MainTB{}, c, ns)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}
		if err := c.Delete(cleanupCtx, namespace); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("cleanup schedule namespace: %v", err)
			return
		}
		if err := wait.PollUntilContextTimeout(cleanupCtx, time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
			err := c.Get(ctx, client.ObjectKey{Name: ns}, &corev1.Namespace{})
			return apierrors.IsNotFound(err), client.IgnoreNotFound(err)
		}); err != nil {
			t.Errorf("schedule namespace %s not deleted: %v", ns, err)
		}
	})
	suite.EnsureRunnerRBAC(suite.MainTB{}, c, ns)

	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "noop-creds", Namespace: ns}, StringData: map[string]string{
		"AWS_ACCESS_KEY_ID": "test", "AWS_SECRET_ACCESS_KEY": "test",
	}}
	require.NoError(t, c.Create(ctx, secret))
	t.Cleanup(func() { _ = c.Delete(context.Background(), secret) })
	provider := &hibernatorv1alpha1.CloudProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "noop-provider", Namespace: ns},
		Spec: hibernatorv1alpha1.CloudProviderSpec{Type: hibernatorv1alpha1.CloudProviderAWS, AWS: &hibernatorv1alpha1.AWSConfig{
			AccountId: "000000000000", Region: "us-east-1", Auth: hibernatorv1alpha1.AWSAuth{Static: &hibernatorv1alpha1.StaticAuth{
				SecretRef: hibernatorv1alpha1.SecretReference{Name: secret.Name},
			}},
		}},
	}
	require.NoError(t, c.Create(ctx, provider))
	t.Cleanup(func() { _ = c.Delete(context.Background(), provider) })

	// Full-day hibernation across midnight: already off-hours at creation, so
	// it hibernates immediately and must hold Hibernated through the 23:59
	// end-boundary grace into the next day (see
	// docs/findings/full-day-hibernation-patterns.md).
	createSchedulePlan(t, ctx, c, ns, provider.Name, "fullday-hibernate", "sched-fullday",
		[]hibernatorv1alpha1.OffHourWindow{{
			Start: "00:00", End: "23:59",
			DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
		}})
	fullHibernateKey := client.ObjectKey{Namespace: ns, Name: "fullday-hibernate"}
	cs := suite.K8sSets
	suite.PollPhaseAtLeast(t, c, cs, fullHibernateKey, hibernatorv1alpha1.PhaseHibernated, time.Until(window.StartAt)+5*time.Minute)
	suite.PollJobComplete(t, c, cs, fullHibernateKey, hibernatorv1alpha1.OperationHibernate, "noop", 8*time.Minute)

	// Full-day active: a past same-day window has no edges near midnight, so
	// the plan must stay Active (and dispatch zero Jobs) across the day
	// change. A 1-minute 23:59-00:00 window is deliberately NOT used here:
	// the 1m schedule buffer would force a phantom hibernation inside the
	// 00:00-00:01 end grace.
	today := strings.ToUpper(time.Now().UTC().Format("Mon"))
	createSchedulePlan(t, ctx, c, ns, provider.Name, "fullday-active", "sched-active",
		[]hibernatorv1alpha1.OffHourWindow{{
			Start: "12:00", End: "12:10", DaysOfWeek: []string{today},
		}})
	activeKey := client.ObjectKey{Namespace: ns, Name: "fullday-active"}
	suite.PollPhaseAtLeast(t, c, cs, activeKey, hibernatorv1alpha1.PhaseActive, 2*time.Minute)

	// Normal schedule: transitions both ways on the resolved window.
	createSchedulePlan(t, ctx, c, ns, provider.Name, "schedule-cycle", "sched-noop",
		[]hibernatorv1alpha1.OffHourWindow{{
			Start: window.Start, End: window.End, DaysOfWeek: window.Days,
		}})
	key := client.ObjectKey{Namespace: ns, Name: "schedule-cycle"}
	suite.PollPhaseAtLeast(t, c, cs, key, hibernatorv1alpha1.PhaseHibernating, time.Until(window.StartAt)+5*time.Minute)
	suite.PollJobComplete(t, c, cs, key, hibernatorv1alpha1.OperationHibernate, "noop", 8*time.Minute)
	suite.PollPhaseAtLeast(t, c, cs, key, hibernatorv1alpha1.PhaseHibernated, 3*time.Minute)
	suite.ScnAssertRestoreMarkers(t, c, ns, "schedule-cycle", map[string]string{"noop": "sched-noop"})
	suite.PollPhaseAtLeast(t, c, cs, key, hibernatorv1alpha1.PhaseWakingUp, time.Until(window.EndAt)+5*time.Minute)
	suite.PollJobComplete(t, c, cs, key, hibernatorv1alpha1.OperationWakeUp, "noop", 8*time.Minute)
	suite.PollPhaseAtLeast(t, c, cs, key, hibernatorv1alpha1.PhaseActive, 3*time.Minute)
	suite.ScnAssertRestoreConsumed(t, c, ns, "schedule-cycle", []string{"noop"})

	// Midnight steady-state: neither full-day plan may have flapped. The
	// NoJobs asserts cover the whole run, so any boundary dispatch (e.g. a
	// phantom wakeup of the hibernated plan, or any Job for the active
	// plan) fails here even if phases settled back.
	suite.PollPhaseAtLeast(t, c, cs, fullHibernateKey, hibernatorv1alpha1.PhaseHibernated, 2*time.Minute)
	suite.ScnAssertRestoreMarkers(t, c, ns, "fullday-hibernate", map[string]string{"noop": "sched-fullday"})
	suite.ScnAssertNoJobs(t, c, ns, "fullday-hibernate", hibernatorv1alpha1.OperationWakeUp, "noop")

	suite.PollPhaseAtLeast(t, c, cs, activeKey, hibernatorv1alpha1.PhaseActive, 2*time.Minute)
	suite.ScnAssertNoJobs(t, c, ns, "fullday-active", hibernatorv1alpha1.OperationHibernate, "noop")
	suite.ScnAssertNoJobs(t, c, ns, "fullday-active", hibernatorv1alpha1.OperationWakeUp, "noop")
}

// resolveScheduleWindow returns the hibernation window for the schedule-cycle
// test. KIND_SCHEDULE_START/END ("15:04" UTC) pin absolute anchors for the
// nightly run (e.g. 23:55-00:05 across midnight); when both are unset, a
// relative window is used for ad-hoc local runs. Anchored runs fail fast
// when setup finished too close to the start edge: plans must exist before
// the hibernate edge fires, otherwise the cycle is unobservable and every
// subsequent poll would fail with a misleading timeout.
func resolveScheduleWindow(t *testing.T) suite.ScheduleWindow {
	t.Helper()
	startEnv, endEnv := os.Getenv("KIND_SCHEDULE_START"), os.Getenv("KIND_SCHEDULE_END")
	if startEnv == "" && endEnv == "" {
		return suite.HibernationWindowAt(time.Now(), 3*time.Minute, 8*time.Minute)
	}
	if startEnv == "" || endEnv == "" {
		t.Fatalf("KIND_SCHEDULE_START and KIND_SCHEDULE_END must be set together (got %q/%q)", startEnv, endEnv)
	}
	window, err := suite.FixedScheduleWindow(time.Now(), startEnv, endEnv)
	require.NoError(t, err, "invalid fixed schedule anchors")
	const minSetupLead = 90 * time.Second
	if lead := time.Until(window.StartAt); lead < minSetupLead {
		t.Fatalf("fixed window start %s is %s away (need >=%s): setup overran the nightly budget or cron fired late",
			window.StartAt.Format(time.RFC3339), lead.Round(time.Second), minSetupLead)
	}
	t.Logf("fixed schedule window %s-%s days=%v (start %s)", window.Start, window.End, window.Days, window.StartAt.Format(time.RFC3339))
	return window
}

// createSchedulePlan creates a UTC noop plan with the given off-hours
// windows and registers cleanup. Markers are deterministic per plan so
// restore asserts can tell the plans apart.
func createSchedulePlan(t *testing.T, ctx context.Context, c client.Client, ns, providerName, planName, marker string, windows []hibernatorv1alpha1.OffHourWindow) {
	t.Helper()
	params, err := json.Marshal(executorparams.NoOpParameters{Marker: marker})
	require.NoError(t, err)
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: planName, Namespace: ns},
		Spec: hibernatorv1alpha1.HibernatePlanSpec{
			Schedule:  hibernatorv1alpha1.Schedule{Timezone: "UTC", OffHours: windows},
			Execution: hibernatorv1alpha1.Execution{Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategyParallel}},
			Behavior:  hibernatorv1alpha1.Behavior{Mode: hibernatorv1alpha1.BehaviorStrict},
			Targets:   []hibernatorv1alpha1.Target{{Name: "noop", Type: "noop", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: providerName}, Parameters: &hibernatorv1alpha1.Parameters{Raw: params}}},
		},
	}
	require.NoError(t, c.Create(ctx, plan))
	t.Cleanup(func() { _ = c.Delete(context.Background(), plan) })
}
