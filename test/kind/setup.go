//go:build kind

package kind

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

const (
	kindNodeImage     = "kindest/node:v1.34.0"
	systemNamespace   = "hibernator-system"
	controllerDeploy  = "hibernator-controller"
	runnerSA          = "hibernator-runner"
	webhookSecretName = "hibernator-webhook-certs"
	webhookCertDir    = "/tmp/k8s-webhook-server/serving-certs"
	flociServiceName  = "floci"
	flociPort         = 4566
)

type runConfig struct {
	runID           string
	clusterName     string
	namespace       string
	controllerImage string
	runnerImage     string
	runnerTestImage string
}

func newRunConfig(pid int) runConfig {
	runID := fmt.Sprintf("%d-%d", pid, time.Now().UnixNano())
	return runConfig{
		runID:           runID,
		clusterName:     "hibernator-" + runID,
		namespace:       "hibernator-e2e-" + runID,
		controllerImage: "hibernator-controller:" + runID,
		runnerImage:     "hibernator-runner:" + runID,
		runnerTestImage: "hibernator-runner-test:" + runID,
	}
}

var currentRun runConfig

// TB is the subset of testing.T used by setup helpers so TestMain can drive
// them without a *testing.T (via mainTB). *testing.T satisfies it as-is.
type TB interface {
	Helper()
	Fatalf(format string, args ...any)
	Logf(format string, args ...any)
	TempDir() string
}

// mainTB adapts setup helpers to TestMain (fatal errors exit, temp dirs leak
// intentionally for post-mortem debugging).
type mainTB struct{}

func (mainTB) Helper() {}
func (mainTB) Fatalf(format string, args ...any) {
	panic(fmt.Sprintf("setup: "+format, args...))
}
func (mainTB) Logf(format string, args ...any) { log.Printf("setup: "+format, args...) }
func (mainTB) TempDir() string {
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

func kindBin(t TB) string {
	t.Helper()
	p := filepath.Join(repoRoot(t), "bin", "kind")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("bin/kind missing (run `make kind-tool`): %v", err)
	}
	return p
}

// ensureCluster creates the kind cluster if absent (idempotent).
func ensureCluster(t TB) {
	t.Helper()
	out := runCLI(t, kindBin(t), "get", "clusters")
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == currentRun.clusterName {
			t.Logf("kind cluster %q already exists", currentRun.clusterName)
			return
		}
	}
	t.Logf("creating kind cluster %q (node image %s, may take minutes on first pull)", currentRun.clusterName, kindNodeImage)
	runCLI(t, kindBin(t), "create", "cluster",
		"--name", currentRun.clusterName,
		"--image", kindNodeImage,
		"--kubeconfig", kubeconfigPath,
		"--wait", "3m",
	)
}

// kubeconfigPath is set by TestMain before any helper runs: an isolated
// kubeconfig file so kind/kubectl never touch the user's ~/.kube/config.
var kubeconfigPath string

// kubeconfigFor writes the kind kubeconfig to kubeconfigPath.
func kubeconfigFor(t TB) {
	t.Helper()
	out := runCLI(t, kindBin(t), "get", "kubeconfig", "--name", currentRun.clusterName)
	if err := os.WriteFile(kubeconfigPath, []byte(out), 0600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
}

// newK8sClient builds a controller-runtime client for the kind cluster.
func newK8sClient(t TB) client.Client {
	t.Helper()
	restCfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
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

// buildAndLoad builds controller/runner images locally and loads them into kind.
func buildAndLoad(t TB) {
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
	dockerBuild("Dockerfile", "controller", currentRun.controllerImage)
	dockerBuild("Dockerfile", "runner", currentRun.runnerImage)
	dockerBuild("test/kind/Dockerfile.runner-test", "", currentRun.runnerTestImage, "--build-arg", "BASE="+currentRun.runnerImage)

	runCLI(t, kindBin(t), "load", "docker-image",
		currentRun.controllerImage, currentRun.runnerImage, currentRun.runnerTestImage,
		"--name", currentRun.clusterName,
	)
}

// kubectlApply applies a manifest path to the kind cluster (never touches current-context).
func kubectlApply(t TB, kubeconfig, path string) {
	t.Helper()
	// Resolve repo-relative paths against the repo root.
	if !filepath.IsAbs(path) {
		path = filepath.Join(repoRoot(t), path)
	}
	runCLI(t, "kubectl", "--kubeconfig", kubeconfig, "apply", "-f", path)
}

// ensureNamespace creates ns if absent.
func ensureNamespace(t TB, c client.Client, ns string) {
	t.Helper()
	obj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}
	if err := c.Create(context.Background(), obj); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace %s: %v", ns, err)
	}
}

// ensureWebhookCertSecret generates the self-signed webhook cert and stores it.
func ensureWebhookCertSecret(t TB, c client.Client) {
	t.Helper()
	certPEM, keyPEM, err := makeWebhookCert()
	if err != nil {
		t.Fatalf("generate webhook cert: %v", err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: webhookSecretName, Namespace: systemNamespace},
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

// patchControllerDeployment points the deployed controller at the kind images
// with kind-appropriate args, and mounts the webhook cert secret.
func patchControllerDeployment(t TB, c client.Client) {
	t.Helper()
	ctx := context.Background()
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: systemNamespace, Name: controllerDeploy}, dep); err != nil {
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
	ctr.Image = currentRun.controllerImage
	ctr.ImagePullPolicy = corev1.PullNever
	ctr.Args = []string{
		"--leader-elect=false",
		"--metrics-bind-address=:8080",
		"--health-probe-bind-address=:8081",
		"--runner-image=" + currentRun.runnerTestImage,
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

// ensureRunnerRBAC creates the runner ServiceAccount + binding in ns.
// (In prod Helm creates these; the controller never provisions them.)
func ensureRunnerRBAC(t TB, c client.Client, ns string) {
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

// hostGatewayIP returns the IP pods use to reach the host.
// Override with FLOCI_HOST_IP; otherwise read the kind bridge gateway.
// (docker's go-template printer mishandles the IPv6 entry lacking a Gateway,
// so the JSON output is parsed here instead.)
func hostGatewayIP(t TB) string {
	t.Helper()
	if ip := os.Getenv("FLOCI_HOST_IP"); ip != "" {
		return ip
	}
	out := runCLI(t, "docker", "network", "inspect", "kind")
	var networks []struct {
		IPAM struct {
			Config []struct {
				Gateway string
			}
		}
	}
	if err := json.Unmarshal([]byte(out), &networks); err != nil {
		t.Fatalf("parse docker network inspect: %v", err)
	}
	for _, n := range networks {
		for _, entry := range n.IPAM.Config {
			if entry.Gateway != "" {
				return entry.Gateway
			}
		}
	}
	t.Fatalf("no gateway found on kind network (set FLOCI_HOST_IP to override)")
	return ""
}

// ensureFlociRoute exposes host Floci inside the cluster at floci.hibernator-system:4566
// via a ClusterIP Service with manually managed Endpoints.
func ensureFlociRoute(t TB, c client.Client) {
	t.Helper()
	ctx := context.Background()
	ip := hostGatewayIP(t)
	t.Logf("host Floci gateway IP: %s", ip)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: flociServiceName, Namespace: systemNamespace},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Name: "aws", Port: flociPort}},
		},
	}
	existingSVC := &corev1.Service{}
	err := c.Get(ctx, client.ObjectKeyFromObject(svc), existingSVC)
	switch {
	case apierrors.IsNotFound(err):
		if err := c.Create(ctx, svc); err != nil {
			t.Fatalf("create floci service: %v", err)
		}
	case err != nil:
		t.Fatalf("get floci service: %v", err)
	default:
		existingSVC.Spec.Ports = svc.Spec.Ports
		if err := c.Update(ctx, existingSVC); err != nil {
			t.Fatalf("reconcile floci service: %v", err)
		}
	}

	eps := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Name: flociServiceName, Namespace: systemNamespace},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{{IP: ip}},
				Ports:     []corev1.EndpointPort{{Name: "aws", Port: flociPort}},
			},
		},
	}
	existingEPS := &corev1.Endpoints{}
	err = c.Get(ctx, client.ObjectKeyFromObject(eps), existingEPS)
	switch {
	case apierrors.IsNotFound(err):
		if err := c.Create(ctx, eps); err != nil {
			t.Fatalf("create floci endpoints: %v", err)
		}
	case err != nil:
		t.Fatalf("get floci endpoints: %v", err)
	default:
		existingEPS.Subsets = eps.Subsets
		if err := c.Update(ctx, existingEPS); err != nil {
			t.Fatalf("update floci endpoints: %v", err)
		}
	}
}

// verifyFlociRoute proves the exact in-cluster DNS and signed AWS protocol
// path used by runner pods before a long scenario starts.
func verifyFlociRoute(t TB, c client.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	backoff := int32(0)
	ttl := int32(60)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "floci-sts-probe-" + currentRun.runID, Namespace: systemNamespace},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				RestartPolicy: corev1.RestartPolicyNever,
				Containers: []corev1.Container{{
					Name:            "aws",
					Image:           "amazon/aws-cli:2.27.49",
					ImagePullPolicy: corev1.PullIfNotPresent,
					Args: []string{
						"--endpoint-url", "http://floci.hibernator-system.svc:4566",
						"sts", "get-caller-identity",
					},
					Env: []corev1.EnvVar{
						{Name: "AWS_ACCESS_KEY_ID", Value: "test"},
						{Name: "AWS_SECRET_ACCESS_KEY", Value: "test"},
						{Name: "AWS_DEFAULT_REGION", Value: "us-east-1"},
					},
				}},
			}},
		},
	}
	if err := c.Create(ctx, job); err != nil {
		t.Fatalf("create in-cluster Floci STS probe: %v", err)
	}
	defer func() { _ = c.Delete(context.Background(), job) }()

	var last batchv1.Job
	err := wait.PollUntilContextTimeout(ctx, 2*time.Second, 3*time.Minute, true, func(ctx context.Context) (bool, error) {
		if err := c.Get(ctx, client.ObjectKeyFromObject(job), &last); err != nil {
			return false, err
		}
		for _, cond := range last.Status.Conditions {
			if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
				return false, fmt.Errorf("STS probe failed: %s", cond.Message)
			}
			if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("in-cluster Floci STS probe did not complete: %v", err)
	}
	t.Logf("in-cluster Floci STS probe succeeded")
}

// waitForDeployment polls until the deployment is Available.
func waitForDeployment(t TB, c client.Client, ns, name string, timeout time.Duration) {
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
