//go:build kind

// Package suite holds the kind e2e support infrastructure: cluster setup,
// fixtures, polling helpers, and assertions. Test entry points live in the
// parent kind package and drive this suite; only Test functions remain there.
package suite

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

const (
	kindNodeImage = "kindest/node:v1.34.0"

	// SystemNamespace hosts the controller and shared test services.
	SystemNamespace = "hibernator-system"
	// ControllerDeploy is the controller Deployment name.
	ControllerDeploy  = "hibernator-controller"
	runnerSA          = "hibernator-runner"
	webhookSecretName = "hibernator-webhook-certs"
	webhookCertDir    = "/tmp/k8s-webhook-server/serving-certs"
)

// RunConfig identifies one isolated suite run: cluster, namespace, and images.
type RunConfig struct {
	RunID           string
	ClusterName     string
	Namespace       string
	ControllerImage string
	RunnerImage     string
}

func NewRunConfig(pid int) RunConfig {
	runID := fmt.Sprintf("%d-%d", pid, time.Now().UnixNano())
	return RunConfig{
		RunID:           runID,
		ClusterName:     "hibernator-" + runID,
		Namespace:       "hibernator-e2e-" + runID,
		ControllerImage: "hibernator-controller:" + runID,
		RunnerImage:     "hibernator-runner:" + runID,
	}
}

// CurrentRun is assigned once by TestMain before any helper runs.
var CurrentRun RunConfig

// K8sClient and K8sSets are provisioned once by TestMain in the parent kind
// package. Declared here (not in a _test.go file) so helpers can use them;
// TestMain assigns them, tests read them via these names.
var (
	K8sClient client.Client
	K8sSets   *kubernetes.Clientset
)

// TB is the subset of testing.T used by setup helpers so TestMain can drive
// them without a *testing.T (via MainTB). *testing.T satisfies it as-is.
type TB interface {
	Helper()
	Fatalf(format string, args ...any)
	Logf(format string, args ...any)
	TempDir() string
}

// MainTB adapts setup helpers to TestMain (fatal errors exit, temp dirs leak
// intentionally for post-mortem debugging).
type MainTB struct{}

func (MainTB) Helper() {}
func (MainTB) Fatalf(format string, args ...any) {
	panic(fmt.Sprintf("setup: "+format, args...))
}
func (MainTB) Logf(format string, args ...any) { log.Printf("setup: "+format, args...) }
func (MainTB) TempDir() string {
	dir, err := os.MkdirTemp("", "kind-e2e-")
	if err != nil {
		log.Fatalf("setup: mktemp: %v", err)
	}
	return dir
}

// runCLI runs a binary and returns trimmed stdout. Errors include stderr.
func runCLI(t TB, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run %s %s: %v\nstderr: %s", name, strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

// repoRoot walks up from the test working directory until go.mod is found.
// (go test runs with cwd set to the package source directory.)
func repoRoot(t TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repo root (go.mod) not found")
		}
		dir = parent
	}
}

func KindBin(t TB) string {
	t.Helper()
	p := filepath.Join(repoRoot(t), "bin", "kind")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("bin/kind missing (run `make kind-tool`): %v", err)
	}
	return p
}

// EnsureCluster creates the kind cluster if absent (idempotent).
func EnsureCluster(t TB) {
	t.Helper()
	out := runCLI(t, KindBin(t), "get", "clusters")
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == CurrentRun.ClusterName {
			t.Logf("kind cluster %q already exists", CurrentRun.ClusterName)
			return
		}
	}
	t.Logf("creating kind cluster %q (node image %s, may take minutes on first pull)", CurrentRun.ClusterName, kindNodeImage)
	runCLI(t, KindBin(t), "create", "cluster",
		"--name", CurrentRun.ClusterName,
		"--image", kindNodeImage,
		"--kubeconfig", KubeconfigPath,
		"--wait", "3m",
	)
}

// KubeconfigPath is set by TestMain before any helper runs: an isolated
// kubeconfig file so kind/kubectl never touch the user's ~/.kube/config.
var KubeconfigPath string

// KubeconfigFor writes the kind kubeconfig to KubeconfigPath.
func KubeconfigFor(t TB) {
	t.Helper()
	out := runCLI(t, KindBin(t), "get", "kubeconfig", "--name", CurrentRun.ClusterName)
	if err := os.WriteFile(KubeconfigPath, []byte(out), 0600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
}

// NewK8sClient builds a controller-runtime client for the kind cluster.
func NewK8sClient(t TB) client.Client {
	t.Helper()
	restCfg, err := clientcmd.BuildConfigFromFlags("", KubeconfigPath)
	if err != nil {
		t.Fatalf("load kubeconfig: %v", err)
	}
	// clientgoscheme.Scheme is shared; AddToScheme is idempotent for our types
	// because setup runs once per TestMain.
	if err := hibernatorv1alpha1.AddToScheme(clientgoscheme.Scheme); err != nil {
		t.Fatalf("add hibernator scheme: %v", err)
	}
	c, err := client.New(restCfg, client.Options{Scheme: clientgoscheme.Scheme})
	if err != nil {
		t.Fatalf("create k8s client: %v", err)
	}
	return c
}

// BuildAndLoad builds controller/runner images locally and loads them into kind.
func BuildAndLoad(t TB) {
	t.Helper()
	root := repoRoot(t)
	dockerBuild := func(dockerfile, target, tag string, buildArgs ...string) {
		args := []string{"build", "-f", dockerfile}
		if target != "" {
			args = append(args, "--target", target)
		}
		args = append(args, "-t", tag)
		args = append(args, buildArgs...)
		args = append(args, ".")
		cmd := exec.Command("docker", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("docker build --target %s: %v\n%s", target, err, out)
		}
	}
	dockerBuild("Dockerfile", "controller", CurrentRun.ControllerImage)
	dockerBuild("Dockerfile", "runner", CurrentRun.RunnerImage)

	runCLI(t, KindBin(t), "load", "docker-image",
		CurrentRun.ControllerImage, CurrentRun.RunnerImage,
		"--name", CurrentRun.ClusterName,
	)
}

// KubectlApply applies a manifest path to the kind cluster (never touches current-context).
func KubectlApply(t TB, kubeconfig, path string) {
	t.Helper()
	// Resolve repo-relative paths against the repo root.
	if !filepath.IsAbs(path) {
		path = filepath.Join(repoRoot(t), path)
	}
	runCLI(t, "kubectl", "--kubeconfig", kubeconfig, "apply", "-f", path)
}

// EnsureNamespace creates ns if absent.
func EnsureNamespace(t TB, c client.Client, ns string) {
	t.Helper()
	obj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}
	if err := c.Create(context.Background(), obj); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace %s: %v", ns, err)
	}
}

// EnsureWebhookCertSecret generates the self-signed webhook cert and stores it.
func EnsureWebhookCertSecret(t TB, c client.Client) {
	t.Helper()
	certPEM, keyPEM, err := makeWebhookCert()
	if err != nil {
		t.Fatalf("generate webhook cert: %v", err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: webhookSecretName, Namespace: SystemNamespace},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"tls.crt": certPEM,
			"tls.key": keyPEM,
		},
	}
	existing := &corev1.Secret{}
	err = c.Get(context.Background(), client.ObjectKeyFromObject(secret), existing)
	if apierrors.IsNotFound(err) {
		if err := c.Create(context.Background(), secret); err != nil {
			t.Fatalf("create webhook cert secret: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("get webhook cert secret: %v", err)
	}
	existing.Data = secret.Data
	if err := c.Update(context.Background(), existing); err != nil {
		t.Fatalf("update webhook cert secret: %v", err)
	}
}

// PatchControllerDeployment points the deployed controller at the kind images
// with kind-appropriate args, and mounts the webhook cert secret.
func PatchControllerDeployment(t TB, c client.Client) {
	t.Helper()
	ctx := context.Background()
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: SystemNamespace, Name: ControllerDeploy}, dep); err != nil {
		t.Fatalf("get controller deployment: %v", err)
	}

	podSpec := &dep.Spec.Template.Spec
	var ctr *corev1.Container
	for i := range podSpec.Containers {
		if podSpec.Containers[i].Name == "controller" {
			ctr = &podSpec.Containers[i]
		}
	}
	if ctr == nil {
		t.Fatalf("controller container not found in deployment")
	}
	ctr.Image = CurrentRun.ControllerImage
	ctr.ImagePullPolicy = corev1.PullNever
	ctr.Args = []string{
		"--leader-elect=false",
		"--metrics-bind-address=:8080",
		"--health-probe-bind-address=:8081",
		"--runner-image=" + CurrentRun.RunnerImage,
		"--runner-service-account=" + runnerSA,
		"--control-plane-endpoint=127.0.0.1",
		"--enable-streaming=false",
		"--grpc-server-address=:9444",
		"--websocket-server-address=:8082",
	}

	volName := "webhook-certs"
	hasVol := false
	for _, v := range podSpec.Volumes {
		if v.Name == volName {
			hasVol = true
		}
	}
	if !hasVol {
		podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
			Name: volName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: webhookSecretName},
			},
		})
	}
	hasMount := false
	for _, m := range ctr.VolumeMounts {
		if m.Name == volName {
			hasMount = true
		}
	}
	if !hasMount {
		ctr.VolumeMounts = append(ctr.VolumeMounts, corev1.VolumeMount{
			Name:      volName,
			MountPath: webhookCertDir,
			ReadOnly:  true,
		})
	}

	if err := c.Update(ctx, dep); err != nil {
		t.Fatalf("patch controller deployment: %v", err)
	}
}

// EnsureRunnerRBAC creates the runner ServiceAccount + binding in ns.
// (In prod Helm creates these; the controller never provisions them.)
func EnsureRunnerRBAC(t TB, c client.Client, ns string) {
	t.Helper()
	ctx := context.Background()
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: runnerSA, Namespace: ns}}
	if err := c.Create(ctx, sa); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create runner SA: %v", err)
	}
	binding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: runnerSA, Namespace: ns},
		Subjects: []rbacv1.Subject{
			{Kind: "ServiceAccount", Name: runnerSA, Namespace: ns},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     runnerSA,
		},
	}
	existing := &rbacv1.RoleBinding{}
	err := c.Get(ctx, client.ObjectKeyFromObject(binding), existing)
	if apierrors.IsNotFound(err) {
		if err := c.Create(ctx, binding); err != nil {
			t.Fatalf("create runner binding: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("get runner binding: %v", err)
	}
	existing.Subjects = binding.Subjects
	existing.RoleRef = binding.RoleRef
	if err := c.Update(ctx, existing); err != nil {
		t.Fatalf("reconcile runner binding: %v", err)
	}
}

// WaitForDeployment polls until the deployment is Available.
func WaitForDeployment(t TB, c client.Client, ns, name string, timeout time.Duration) {
	t.Helper()
	ctx := context.Background()
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true,
		func(ctx context.Context) (bool, error) {
			dep := &appsv1.Deployment{}
			if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, dep); err != nil {
				return false, nil
			}
			for _, cond := range dep.Status.Conditions {
				if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
					return true, nil
				}
			}
			return false, nil
		})
	if err != nil {
		t.Fatalf("deployment %s/%s not available within %s: %v", ns, name, timeout, err)
	}
}
