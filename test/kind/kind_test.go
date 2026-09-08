//go:build kind

package kind

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
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
// this opt-in test proves schedule-driven transitions with a noop target,
// so no cloud backend is involved.
func TestNoopScheduleCycle(t *testing.T) {
	if os.Getenv("RUN_KIND_SCHEDULE") != "1" {
		t.Skip("set RUN_KIND_SCHEDULE=1 for the real wall-clock schedule cycle")
	}

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

	params, err := json.Marshal(executorparams.NoOpParameters{Marker: "sched-noop"})
	require.NoError(t, err)
	window := suite.HibernationWindowAt(time.Now(), 3*time.Minute, 8*time.Minute)
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "schedule-cycle", Namespace: ns},
		Spec: hibernatorv1alpha1.HibernatePlanSpec{
			Schedule:  hibernatorv1alpha1.Schedule{Timezone: "UTC", OffHours: []hibernatorv1alpha1.OffHourWindow{{Start: window.Start, End: window.End, DaysOfWeek: window.Days}}},
			Execution: hibernatorv1alpha1.Execution{Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategyParallel}},
			Behavior:  hibernatorv1alpha1.Behavior{Mode: hibernatorv1alpha1.BehaviorStrict},
			Targets:   []hibernatorv1alpha1.Target{{Name: "noop", Type: "noop", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: provider.Name}, Parameters: &hibernatorv1alpha1.Parameters{Raw: params}}},
		},
	}
	require.NoError(t, c.Create(ctx, plan))
	t.Cleanup(func() { _ = c.Delete(context.Background(), plan) })
	key := client.ObjectKeyFromObject(plan)
	cs := suite.K8sSets
	suite.PollPhaseAtLeast(t, c, cs, key, hibernatorv1alpha1.PhaseHibernating, time.Until(window.StartAt)+5*time.Minute)
	suite.PollJobComplete(t, c, cs, key, hibernatorv1alpha1.OperationHibernate, "noop", 8*time.Minute)
	suite.PollPhaseAtLeast(t, c, cs, key, hibernatorv1alpha1.PhaseHibernated, 3*time.Minute)
	suite.ScnAssertRestoreMarkers(t, c, ns, plan.Name, map[string]string{"noop": "sched-noop"})
	suite.PollPhaseAtLeast(t, c, cs, key, hibernatorv1alpha1.PhaseWakingUp, time.Until(window.EndAt)+5*time.Minute)
	suite.PollJobComplete(t, c, cs, key, hibernatorv1alpha1.OperationWakeUp, "noop", 8*time.Minute)
	suite.PollPhaseAtLeast(t, c, cs, key, hibernatorv1alpha1.PhaseActive, 3*time.Minute)
	suite.ScnAssertRestoreConsumed(t, c, ns, plan.Name, []string{"noop"})
}
