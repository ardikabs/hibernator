//go:build k8senv

// Package harness owns everything the k8senv executor integration suites
// share: environment gating, envtest lifecycle (real API server + etcd, no
// kubelet or controller-manager), kubeconfig injection for the real executor
// client factories, fixture seeding, and cleanup.
//
// Layer contract (see test/README.md): this suite debugs the Kubernetes API
// executors (Karpenter, WorkloadScaler) step by step against a real API.
// Nothing is mocked at the cluster boundary; AwaitCompletion waits stay
// disabled because no controller-manager exists to converge status.
package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/ardikabs/hibernator/pkg/k8sutil"
)

// NodePoolGVR is the Karpenter NodePool resource served by the test CRD in
// testdata/crds.
var NodePoolGVR = schema.GroupVersionResource{
	Group:    "karpenter.sh",
	Version:  "v1",
	Resource: "nodepools",
}

// DeploymentGVR is the built-in apps/v1 deployments resource.
var DeploymentGVR = schema.GroupVersionResource{
	Group:    "apps",
	Version:  "v1",
	Resource: "deployments",
}

// K8sEnv is a running envtest control plane plus clients and the kubeconfig
// bytes the real executor factories consume via K8SConnectorConfig.
type K8sEnv struct {
	RestConfig *rest.Config
	Typed      kubernetes.Interface
	Dynamic    dynamic.Interface
	Kubeconfig []byte

	env *envtest.Environment
}

// Setup gates on K8SENV_ENABLED=1 (otherwise the test fails) and boots one
// envtest control plane per test. Per-test environments trade boot time for
// isolation: fixtures never leak between tests. It must be called before any
// cluster interaction in every k8senv suite test.
func Setup(t *testing.T) *K8sEnv {
	t.Helper()
	if os.Getenv("K8SENV_ENABLED") != "1" {
		t.Fatal("Kubernetes API integration tests require K8SENV_ENABLED=1 with envtest assets (run `make envtest` first, then `make test-k8senv`)")
	}

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{crdDir(t)},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("start envtest control plane: %v (need KUBEBUILDER_ASSETS from `make envtest`)", err)
	}
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Logf("stop envtest control plane: %v", err)
		}
	})

	typed, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err, "build typed client")
	dyn, err := dynamic.NewForConfig(cfg)
	require.NoError(t, err, "build dynamic client")
	kubeconfig, err := restConfigToKubeconfig(cfg)
	require.NoError(t, err, "render kubeconfig for executor factory")

	return &K8sEnv{RestConfig: cfg, Typed: typed, Dynamic: dyn, Kubeconfig: kubeconfig, env: testEnv}
}

// crdDir resolves the fixture CRD directory relative to this source file so
// the suite works regardless of the test binary's working directory.
func crdDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "resolve harness source path")
	return filepath.Join(filepath.Dir(file), "..", "testdata", "crds")
}

// K8SConnectorConfig builds the executor connector config pointing the real
// client factory at this environment. ClusterName/Region are dummies:
// Validate requires them non-empty, but nothing resolves EKS here.
func K8SConnectorConfig(kubeconfig []byte) *k8sutil.K8SConnectorConfig {
	return &k8sutil.K8SConnectorConfig{
		ClusterName: "k8senv",
		Region:      "test",
		Kubeconfig:  kubeconfig,
	}
}

// restConfigToKubeconfig renders a kubeconfig document from a live rest.Config
// (cert data or cert files, token or token file, CA data or CA file).
func restConfigToKubeconfig(cfg *rest.Config) ([]byte, error) {
	caData := cfg.CAData
	if len(caData) == 0 && cfg.CAFile != "" {
		data, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		caData = data
	}
	certData := cfg.CertData
	if len(certData) == 0 && cfg.CertFile != "" {
		data, err := os.ReadFile(cfg.CertFile)
		if err != nil {
			return nil, fmt.Errorf("read client cert file: %w", err)
		}
		certData = data
	}
	keyData := cfg.KeyData
	if len(keyData) == 0 && cfg.KeyFile != "" {
		data, err := os.ReadFile(cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("read client key file: %w", err)
		}
		keyData = data
	}
	token := cfg.BearerToken
	if token == "" && cfg.BearerTokenFile != "" {
		data, err := os.ReadFile(cfg.BearerTokenFile)
		if err != nil {
			return nil, fmt.Errorf("read bearer token file: %w", err)
		}
		token = string(data)
	}

	apiCfg := clientcmdapi.NewConfig()
	apiCfg.Clusters["k8senv"] = &clientcmdapi.Cluster{
		Server:                   cfg.Host,
		CertificateAuthorityData: caData,
		InsecureSkipTLSVerify:    cfg.Insecure,
	}
	apiCfg.AuthInfos["k8senv"] = &clientcmdapi.AuthInfo{
		ClientCertificateData: certData,
		ClientKeyData:         keyData,
		Token:                 token,
	}
	apiCfg.Contexts["k8senv"] = &clientcmdapi.Context{
		Cluster:  "k8senv",
		AuthInfo: "k8senv",
	}
	apiCfg.CurrentContext = "k8senv"
	return clientcmd.Write(*apiCfg)
}

// EnsureNamespace creates ns (ignoring AlreadyExists) and registers deletion
// cleanup. Environments are per-test, so fixed names never collide.
func EnsureNamespace(t *testing.T, ctx context.Context, typed kubernetes.Interface, ns string) {
	t.Helper()
	_, err := typed.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace %s: %v", ns, err)
	}
	t.Cleanup(func() {
		_ = typed.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{})
	})
}

// LabelNamespace sets one label on a namespace (namespace discovery by
// selector exercises the typed ListNamespaces path).
func LabelNamespace(t *testing.T, ctx context.Context, typed kubernetes.Interface, ns, key, value string) {
	t.Helper()
	got, err := typed.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	require.NoError(t, err, "get namespace %s", ns)
	if got.Labels == nil {
		got.Labels = map[string]string{}
	}
	got.Labels[key] = value
	_, err = typed.CoreV1().Namespaces().Update(ctx, got, metav1.UpdateOptions{})
	require.NoError(t, err, "label namespace %s", ns)
}
func SeedDeployment(t *testing.T, ctx context.Context, typed kubernetes.Interface, ns, name string, replicas int32) {
	t.Helper()
	_, err := typed.AppsV1().Deployments(ns).Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: map[string]string{"app": name}},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(replicas),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "pause", Image: "registry.k8s.io/pause:3.10"}}},
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err, "seed deployment %s/%s", ns, name)
}

// DeploymentSpecReplicas reads deployment.spec.replicas via the typed client.
// NOTE: this deliberately does NOT read the scale subresource: the API
// server omits `replicas` from scale GET responses when it is 0
// (autoscaling/v1 omitempty), so a scaled-to-zero workload reads back as
// "absent". The Deployment object itself is authoritative.
func DeploymentSpecReplicas(t *testing.T, ctx context.Context, typed kubernetes.Interface, ns, name string) int32 {
	t.Helper()
	dep, err := typed.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err, "get deployment %s/%s", ns, name)
	require.NotNil(t, dep.Spec.Replicas, "deployment.spec.replicas must be set for %s/%s", ns, name)
	return *dep.Spec.Replicas
}

// SeedNodePool creates a Karpenter NodePool with the given labels and spec.
// Spec values must be strings/maps only: unstructured round-trips JSON
// numbers as int64, which would break require.Equal on int literals.
func SeedNodePool(t *testing.T, ctx context.Context, dyn dynamic.Interface, name string, labels map[string]string, spec map[string]interface{}) {
	t.Helper()
	meta := map[string]interface{}{"name": name}
	if len(labels) > 0 {
		strMap := make(map[string]interface{}, len(labels))
		for k, v := range labels {
			strMap[k] = v
		}
		meta["labels"] = strMap
	}
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "karpenter.sh/v1",
		"kind":       "NodePool",
		"metadata":   meta,
		"spec":       spec,
	}}
	_, err := dyn.Resource(NodePoolGVR).Create(ctx, obj, metav1.CreateOptions{})
	require.NoError(t, err, "seed NodePool %s", name)
}

// GetNodePool fetches a NodePool, failing the test on any error.
func GetNodePool(t *testing.T, ctx context.Context, dyn dynamic.Interface, name string) *unstructured.Unstructured {
	t.Helper()
	obj, err := dyn.Resource(NodePoolGVR).Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err, "get NodePool %s", name)
	return obj
}

// RequireNodePoolNotFound fails unless the NodePool is gone (deleted state,
// not an API error).
func RequireNodePoolNotFound(t *testing.T, ctx context.Context, dyn dynamic.Interface, name string) {
	t.Helper()
	_, err := dyn.Resource(NodePoolGVR).Get(ctx, name, metav1.GetOptions{})
	require.Error(t, err, "NodePool %s must be gone", name)
	require.True(t, apierrors.IsNotFound(err), "NodePool %s must be NotFound, got: %v", name, err)
}
