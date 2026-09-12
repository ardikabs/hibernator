//go:build k8senv

package k8senv

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/ardikabs/hibernator/internal/executor"
	kpexec "github.com/ardikabs/hibernator/internal/executor/karpenter"
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/ardikabs/hibernator/test/k8senv/harness"
)

// nodePoolSpec returns a representative NodePool spec. Values are
// strings/maps only: unstructured round-trips JSON numbers as int64, which
// would break require.Equal against int literals.
func nodePoolSpec() map[string]interface{} {
	return map[string]interface{}{
		"template": map[string]interface{}{
			"spec": map[string]interface{}{
				"requirements": []interface{}{
					map[string]interface{}{"key": "karpenter.sh/capacity-type", "operator": "In", "values": []interface{}{"spot"}},
				},
				"nodeClassRef": map[string]interface{}{"group": "karpenter.k8s.aws", "kind": "EC2NodeClass", "name": "default"},
			},
		},
		"limits":     map[string]interface{}{"cpu": "100"},
		"disruption": map[string]interface{}{"consolidationPolicy": "WhenEmpty"},
	}
}

func karpenterSpec(t *testing.T, kubeconfig []byte, params executorparams.KarpenterParameters, collected map[string]json.RawMessage) executor.Spec {
	t.Helper()
	paramsJSON, err := json.Marshal(params)
	require.NoError(t, err)
	return executor.Spec{
		TargetName: "k8senv-karpenter",
		TargetType: "karpenter",
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

func TestKarpenter_FullLifecycle(t *testing.T) {
	ctx := context.Background()
	env := harness.Setup(t)

	spec := nodePoolSpec()
	harness.SeedNodePool(t, ctx, env.Dynamic, "pool-a", map[string]string{"team": "a"}, spec)

	exec := kpexec.New()
	collected := map[string]json.RawMessage{}
	params := executorparams.KarpenterParameters{NodePools: []string{"pool-a"}}
	require.NoError(t, exec.Validate(karpenterSpec(t, env.Kubeconfig, params, collected)))

	shutdownRes, err := exec.Shutdown(ctx, logr.Discard(), karpenterSpec(t, env.Kubeconfig, params, collected))
	require.NoError(t, err)
	require.Equal(t, "scaled down 1 Karpenter NodePool(s)", shutdownRes.Message)
	harness.RequireNodePoolNotFound(t, ctx, env.Dynamic, "pool-a")
	require.Len(t, collected, 1, "restore data must be reported for the deleted NodePool")

	var state kpexec.NodePoolState
	require.NoError(t, json.Unmarshal(collected["pool-a"], &state))
	require.Equal(t, "pool-a", state.Name)
	require.Equal(t, spec, state.Spec, "saved spec must round-trip verbatim for exact recreation")
	require.Equal(t, map[string]string{"team": "a"}, state.Labels)

	wakeupRes, err := exec.WakeUp(ctx, logr.Discard(), karpenterSpec(t, env.Kubeconfig, params, collected), executor.RestoreData{
		Type:   "karpenter",
		Data:   collected,
		IsLive: true,
	})
	require.NoError(t, err)
	require.Equal(t, "restored 1 Karpenter NodePool(s)", wakeupRes.Message)

	restored := harness.GetNodePool(t, ctx, env.Dynamic, "pool-a")
	restoredSpec, found, err := unstructured.NestedMap(restored.Object, "spec")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, spec, restoredSpec)
	require.Equal(t, map[string]string{"team": "a"}, restored.GetLabels())
}

func TestKarpenter_DiscoverAllAndSelector(t *testing.T) {
	ctx := context.Background()
	env := harness.Setup(t)

	harness.SeedNodePool(t, ctx, env.Dynamic, "pool-a", map[string]string{"team": "a"}, nodePoolSpec())
	harness.SeedNodePool(t, ctx, env.Dynamic, "pool-b", map[string]string{"team": "b"}, nodePoolSpec())

	exec := kpexec.New()
	collected := map[string]json.RawMessage{}

	// Empty params discover every NodePool in the cluster.
	all := executorparams.KarpenterParameters{}
	require.NoError(t, exec.Validate(karpenterSpec(t, env.Kubeconfig, all, collected)))
	shutdownRes, err := exec.Shutdown(ctx, logr.Discard(), karpenterSpec(t, env.Kubeconfig, all, collected))
	require.NoError(t, err)
	require.Equal(t, "scaled down 2 Karpenter NodePool(s)", shutdownRes.Message)
	harness.RequireNodePoolNotFound(t, ctx, env.Dynamic, "pool-a")
	harness.RequireNodePoolNotFound(t, ctx, env.Dynamic, "pool-b")

	wakeupRes, err := exec.WakeUp(ctx, logr.Discard(), karpenterSpec(t, env.Kubeconfig, all, collected), executor.RestoreData{
		Type:   "karpenter",
		Data:   collected,
		IsLive: true,
	})
	require.NoError(t, err)
	require.Equal(t, "restored 2 Karpenter NodePool(s)", wakeupRes.Message)
	harness.GetNodePool(t, ctx, env.Dynamic, "pool-a")
	harness.GetNodePool(t, ctx, env.Dynamic, "pool-b")

	// Label selector scopes discovery to the matching NodePool only.
	selected := map[string]json.RawMessage{}
	byTeam := executorparams.KarpenterParameters{
		NodeSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"team": "a"}},
	}
	shutdownRes, err = exec.Shutdown(ctx, logr.Discard(), karpenterSpec(t, env.Kubeconfig, byTeam, selected))
	require.NoError(t, err)
	require.Equal(t, "scaled down 1 Karpenter NodePool(s)", shutdownRes.Message)
	harness.RequireNodePoolNotFound(t, ctx, env.Dynamic, "pool-a")
	harness.GetNodePool(t, ctx, env.Dynamic, "pool-b")

	wakeupRes, err = exec.WakeUp(ctx, logr.Discard(), karpenterSpec(t, env.Kubeconfig, byTeam, selected), executor.RestoreData{
		Type:   "karpenter",
		Data:   selected,
		IsLive: true,
	})
	require.NoError(t, err)
	require.Equal(t, "restored 1 Karpenter NodePool(s)", wakeupRes.Message)
	harness.GetNodePool(t, ctx, env.Dynamic, "pool-a")
}

func TestKarpenter_ValidateRejectsMissingConnector(t *testing.T) {
	exec := kpexec.New()
	params, err := json.Marshal(executorparams.KarpenterParameters{})
	require.NoError(t, err)
	err = exec.Validate(executor.Spec{TargetType: "karpenter", Parameters: params})
	require.ErrorContains(t, err, "K8S connector config is required")
}

func TestKarpenter_NoMatchIsNoop(t *testing.T) {
	ctx := context.Background()
	env := harness.Setup(t)

	exec := kpexec.New()
	collected := map[string]json.RawMessage{}

	// Empty discovery on an empty cluster is a no-op, not an error.
	shutdownRes, err := exec.Shutdown(ctx, logr.Discard(), karpenterSpec(t, env.Kubeconfig, executorparams.KarpenterParameters{}, collected))
	require.NoError(t, err)
	require.Equal(t, "shutdown completed for Karpenter (no NodePools found)", shutdownRes.Message)
	require.Empty(t, collected)

	wakeupRes, err := exec.WakeUp(ctx, logr.Discard(), karpenterSpec(t, env.Kubeconfig, executorparams.KarpenterParameters{}, collected), executor.RestoreData{})
	require.NoError(t, err)
	require.Equal(t, "wakeup completed for Karpenter (no restore data)", wakeupRes.Message)

	// An explicit name for an already-deleted pool is a stale skip, so
	// repeated shutdowns are idempotent.
	harness.SeedNodePool(t, ctx, env.Dynamic, "pool-a", nil, nodePoolSpec())
	explicit := executorparams.KarpenterParameters{NodePools: []string{"pool-a"}}
	shutdownRes, err = exec.Shutdown(ctx, logr.Discard(), karpenterSpec(t, env.Kubeconfig, explicit, collected))
	require.NoError(t, err)
	require.Equal(t, "scaled down 1 Karpenter NodePool(s)", shutdownRes.Message)
	shutdownRes, err = exec.Shutdown(ctx, logr.Discard(), karpenterSpec(t, env.Kubeconfig, explicit, collected))
	require.NoError(t, err)
	require.Equal(t, "scaled down 0 Karpenter NodePool(s), skipped 1 stale NodePool(s)", shutdownRes.Message)
}

func TestKarpenter_StaleRestoreSkipped(t *testing.T) {
	ctx := context.Background()
	env := harness.Setup(t)

	// The pool still exists, so recreating it from restore data is a stale
	// skip rather than an overwrite.
	harness.SeedNodePool(t, ctx, env.Dynamic, "pool-a", nil, nodePoolSpec())
	stale, err := json.Marshal(kpexec.NodePoolState{Name: "pool-a", Spec: nodePoolSpec()})
	require.NoError(t, err)

	exec := kpexec.New()
	collected := map[string]json.RawMessage{}
	wakeupRes, err := exec.WakeUp(ctx, logr.Discard(), karpenterSpec(t, env.Kubeconfig, executorparams.KarpenterParameters{}, collected), executor.RestoreData{
		Type:   "karpenter",
		Data:   map[string]json.RawMessage{"pool-a": stale},
		IsLive: true,
	})
	require.NoError(t, err)
	require.Equal(t, "restored 0 Karpenter NodePool(s), skipped 1 stale NodePool(s)", wakeupRes.Message)
	harness.GetNodePool(t, ctx, env.Dynamic, "pool-a")
}
