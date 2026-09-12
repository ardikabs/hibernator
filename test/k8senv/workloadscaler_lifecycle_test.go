//go:build k8senv

package k8senv

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ardikabs/hibernator/internal/executor"
	wsexec "github.com/ardikabs/hibernator/internal/executor/workloadscaler"
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/ardikabs/hibernator/pkg/k8sutil"
	"github.com/ardikabs/hibernator/test/k8senv/harness"
)

func workloadScalerSpec(t *testing.T, kubeconfig []byte, params executorparams.WorkloadScalerParameters, collected map[string]json.RawMessage) executor.Spec {
	t.Helper()
	paramsJSON, err := json.Marshal(params)
	require.NoError(t, err)
	return executor.Spec{
		TargetName: "k8senv-workloadscaler",
		TargetType: "workloadscaler",
		Parameters: paramsJSON,
		ConnectorConfig: executor.ConnectorConfig{
			K8S: harness.K8SConnectorConfig(kubeconfig),
		},
		ReportStateCallback: func(key string, value interface{}) error {
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			collected[key] = raw
			return nil
		},
	}
}

func TestWorkloadScaler_FullLifecycle(t *testing.T) {
	ctx := context.Background()
	env := harness.Setup(t)

	const ns = "ws-lifecycle"
	harness.EnsureNamespace(t, ctx, env.Typed, ns)
	harness.SeedDeployment(t, ctx, env.Typed, ns, "api", 3)

	exec := wsexec.New()
	collected := map[string]json.RawMessage{}
	params := executorparams.WorkloadScalerParameters{
		IncludedGroups: []string{"Deployment"},
		Namespace:      executorparams.NamespaceSelector{Literals: []string{ns}},
	}
	require.NoError(t, exec.Validate(workloadScalerSpec(t, env.Kubeconfig, params, collected)))

	shutdownRes, err := exec.Shutdown(ctx, logr.Discard(), workloadScalerSpec(t, env.Kubeconfig, params, collected))
	require.NoError(t, err)
	require.Equal(t, "scaled 1 workload(s) to zero across 1 namespace(s)", shutdownRes.Message)
	require.Equal(t, int32(0), harness.DeploymentSpecReplicas(t, ctx, env.Typed, ns, "api"))
	require.Len(t, collected, 1, "restore data must be reported for the scaled workload")

	var state wsexec.WorkloadState
	require.NoError(t, json.Unmarshal(collected[ns+"/Deployment/api"], &state))
	require.Equal(t, ns, state.Namespace)
	require.Equal(t, "Deployment", state.Kind)
	require.Equal(t, "api", state.Name)
	require.Equal(t, int32(3), state.Replicas)
	require.True(t, state.WasScaled)

	wakeupRes, err := exec.WakeUp(ctx, logr.Discard(), workloadScalerSpec(t, env.Kubeconfig, params, collected), executor.RestoreData{
		Type:   "workloadscaler",
		Data:   collected,
		IsLive: true,
	})
	require.NoError(t, err)
	require.Equal(t, "restored 1 workload(s)", wakeupRes.Message)
	require.Equal(t, int32(3), harness.DeploymentSpecReplicas(t, ctx, env.Typed, ns, "api"))
}

func TestWorkloadScaler_NamespaceSelectorDiscovery(t *testing.T) {
	ctx := context.Background()
	env := harness.Setup(t)

	harness.EnsureNamespace(t, ctx, env.Typed, "ws-a")
	harness.EnsureNamespace(t, ctx, env.Typed, "ws-b")
	harness.LabelNamespace(t, ctx, env.Typed, "ws-a", "team", "a")
	harness.LabelNamespace(t, ctx, env.Typed, "ws-b", "team", "b")
	harness.SeedDeployment(t, ctx, env.Typed, "ws-a", "api", 2)
	harness.SeedDeployment(t, ctx, env.Typed, "ws-b", "api", 2)

	exec := wsexec.New()
	collected := map[string]json.RawMessage{}
	params := executorparams.WorkloadScalerParameters{
		IncludedGroups: []string{"Deployment"},
		Namespace:      executorparams.NamespaceSelector{Selector: map[string]string{"team": "a"}},
	}
	require.NoError(t, exec.Validate(workloadScalerSpec(t, env.Kubeconfig, params, collected)))

	shutdownRes, err := exec.Shutdown(ctx, logr.Discard(), workloadScalerSpec(t, env.Kubeconfig, params, collected))
	require.NoError(t, err)
	require.Equal(t, "scaled 1 workload(s) to zero across 1 namespace(s)", shutdownRes.Message)
	require.Equal(t, int32(0), harness.DeploymentSpecReplicas(t, ctx, env.Typed, "ws-a", "api"))
	require.Equal(t, int32(2), harness.DeploymentSpecReplicas(t, ctx, env.Typed, "ws-b", "api"), "unselected namespace must be untouched")
	require.Contains(t, collected, "ws-a/Deployment/api")
	require.NotContains(t, collected, "ws-b/Deployment/api")

	wakeupRes, err := exec.WakeUp(ctx, logr.Discard(), workloadScalerSpec(t, env.Kubeconfig, params, collected), executor.RestoreData{
		Type:   "workloadscaler",
		Data:   collected,
		IsLive: true,
	})
	require.NoError(t, err)
	require.Equal(t, "restored 1 workload(s)", wakeupRes.Message)
	require.Equal(t, int32(2), harness.DeploymentSpecReplicas(t, ctx, env.Typed, "ws-a", "api"))
}

func TestWorkloadScaler_ValidateRejectsMissingNamespace(t *testing.T) {
	exec := wsexec.New()
	params, err := json.Marshal(executorparams.WorkloadScalerParameters{})
	require.NoError(t, err)
	err = exec.Validate(executor.Spec{
		TargetType: "workloadscaler",
		Parameters: params,
		ConnectorConfig: executor.ConnectorConfig{
			K8S: &k8sutil.K8SConnectorConfig{ClusterName: "k8senv", Region: "test"},
		},
	})
	require.ErrorContains(t, err, "namespace must specify either literals or selector")
}

func TestWorkloadScaler_NoMatchIsNoop(t *testing.T) {
	ctx := context.Background()
	env := harness.Setup(t)

	exec := wsexec.New()
	collected := map[string]json.RawMessage{}
	// Literals are used verbatim: an empty namespace scales zero workloads
	// without error.
	params := executorparams.WorkloadScalerParameters{
		IncludedGroups: []string{"Deployment"},
		Namespace:      executorparams.NamespaceSelector{Literals: []string{"ws-empty"}},
	}
	harness.EnsureNamespace(t, ctx, env.Typed, "ws-empty")

	shutdownRes, err := exec.Shutdown(ctx, logr.Discard(), workloadScalerSpec(t, env.Kubeconfig, params, collected))
	require.NoError(t, err)
	require.Equal(t, "scaled 0 workload(s) to zero across 1 namespace(s)", shutdownRes.Message)
	require.Empty(t, collected)

	wakeupRes, err := exec.WakeUp(ctx, logr.Discard(), workloadScalerSpec(t, env.Kubeconfig, params, collected), executor.RestoreData{})
	require.NoError(t, err)
	require.Equal(t, "wakeup completed for workloadscaler (no restore data)", wakeupRes.Message)
}

func TestWorkloadScaler_StaleRestoreSkipped(t *testing.T) {
	ctx := context.Background()
	env := harness.Setup(t)

	const ns = "ws-stale"
	harness.EnsureNamespace(t, ctx, env.Typed, ns)
	harness.SeedDeployment(t, ctx, env.Typed, ns, "api", 2)

	exec := wsexec.New()
	collected := map[string]json.RawMessage{}
	params := executorparams.WorkloadScalerParameters{
		IncludedGroups: []string{"Deployment"},
		Namespace:      executorparams.NamespaceSelector{Literals: []string{ns}},
	}
	shutdownRes, err := exec.Shutdown(ctx, logr.Discard(), workloadScalerSpec(t, env.Kubeconfig, params, collected))
	require.NoError(t, err)
	require.Equal(t, "scaled 1 workload(s) to zero across 1 namespace(s)", shutdownRes.Message)

	// The workload disappears before wakeup: restore is a stale skip, not a
	// failure (mirrors mid-cycle deletion in production).
	require.NoError(t, env.Typed.AppsV1().Deployments(ns).Delete(ctx, "api", metav1.DeleteOptions{}))
	wakeupRes, err := exec.WakeUp(ctx, logr.Discard(), workloadScalerSpec(t, env.Kubeconfig, params, collected), executor.RestoreData{
		Type:   "workloadscaler",
		Data:   collected,
		IsLive: true,
	})
	require.NoError(t, err)
	require.Equal(t, "restored 0 workload(s), skipped 1 stale workload(s)", wakeupRes.Message)
}
