//go:build kind

package kind

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/ardikabs/hibernator/test/floci/harness"
)

// Shared test environment, provisioned once by TestMain.
var (
	k8sClient client.Client
	k8sSets   *kubernetes.Clientset
)

func TestMain(m *testing.M) {
	if os.Getenv("KIND_UNIT_ONLY") == "1" {
		os.Exit(m.Run())
	}
	if os.Getenv("FLOCI_ENABLED") != "1" {
		log.Print("kind suite requires FLOCI_ENABLED=1 with host Floci on :4566 (see test/floci/compose.yml)")
		os.Exit(2)
	}
	// Silence controller-runtime's unset-logger diagnostics (API warning
	// headers otherwise dump stacks to test output).
	ctrl.SetLogger(logr.Discard())
	tb := mainTB{}
	currentRun = newRunConfig(os.Getpid())

	// Isolated kubeconfig: kind/kubectl/client all use this file, so the
	// user's ~/.kube/config is never touched.
	kubeconfigPath = filepath.Join(tb.TempDir(), "kubeconfig")
	if err := os.WriteFile(kubeconfigPath, []byte{}, 0600); err != nil {
		log.Fatalf("setup: init kubeconfig placeholder: %v", err)
	}

	code := 1
	defer func() {
		if os.Getenv("KEEP_KIND") != "1" && !(code != 0 && os.Getenv("KEEP_KIND_ON_FAILURE") == "1") {
			log.Printf("deleting suite-owned kind cluster %q", currentRun.clusterName)
			cmd := exec.Command(kindBin(tb), "delete", "cluster", "--name", currentRun.clusterName)
			if out, err := cmd.CombinedOutput(); err != nil {
				log.Printf("delete cluster: %v\n%s", err, out)
			}
		} else {
			log.Printf("suite-owned cluster %q left running", currentRun.clusterName)
		}
		if recovered := recover(); recovered != nil {
			log.Printf("kind setup failed: %v", recovered)
			code = 1
		}
		os.Exit(code)
	}()

	ensureCluster(tb)
	kubeconfigFor(tb)
	k8sClient = newK8sClient(tb)
	restCfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		tb.Fatalf("load kubeconfig for clientset: %v", err)
	}
	k8sSets, err = kubernetes.NewForConfig(restCfg)
	if err != nil {
		tb.Fatalf("create clientset: %v", err)
	}
	buildAndLoad(tb)

	kubecfg := kubeconfigPath
	kubectlApply(tb, kubecfg, "config/crd/bases")
	kubectlApply(tb, kubecfg, "config/manager/manager.yaml")
	kubectlApply(tb, kubecfg, "config/rbac/runner_role.yaml")
	// Production-grade RBAC last: the ClusterRole embedded in manager.yaml is
	// stale (missing scheduleexceptions/notifications/pods/...); this copy
	// mirrors the Helm chart and replaces it wholesale by object name.
	kubectlApply(tb, kubecfg, "test/kind/manifests/controller-clusterrole.yaml")
	ensureWebhookCertSecret(tb, k8sClient)
	patchControllerDeployment(tb, k8sClient)
	ensureFlociRoute(tb, k8sClient)
	verifyFlociRoute(tb, k8sClient)
	waitForDeployment(tb, k8sClient, systemNamespace, controllerDeploy, 5*time.Minute)

	code = m.Run()
}

func TestEC2FullChain(t *testing.T) {
	ctx := context.Background()
	hc := harness.Setup(t) // host-side Floci client; skips unless FLOCI_ENABLED=1
	c := k8sClient
	require.NotNil(t, c, "TestMain must provision the cluster client")

	ns := currentRun.namespace
	ensureNamespace(mainTB{}, c, ns)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}
		_ = c.Delete(ctx, namespace)
		_ = wait.PollUntilContextTimeout(ctx, time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
			err := c.Get(ctx, client.ObjectKey{Name: ns}, &corev1.Namespace{})
			return apierrors.IsNotFound(err), client.IgnoreNotFound(err)
		})
	})
	ensureRunnerRBAC(mainTB{}, c, ns)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "kind-floci-creds", Namespace: ns},
		StringData: map[string]string{
			"AWS_ACCESS_KEY_ID":     "test",
			"AWS_SECRET_ACCESS_KEY": "test",
		},
	}
	require.NoError(t, c.Create(ctx, secret))
	t.Cleanup(func() { _ = c.Delete(context.Background(), secret) })

	tags := map[string]string{"floci-e2e": harness.Nonce(), "Role": "bastion"}
	ids := harness.SeedEC2(t, ctx, hc, "kind-fullchain", tags, 2)
	require.Len(t, ids, 2)
	seeded := map[string]bool{ids[0]: true, ids[1]: true}

	provider := &hibernatorv1alpha1.CloudProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "kind-aws", Namespace: ns},
		Spec: hibernatorv1alpha1.CloudProviderSpec{
			Type: hibernatorv1alpha1.CloudProviderAWS,
			AWS: &hibernatorv1alpha1.AWSConfig{
				AccountId: "000000000000",
				Region:    "us-east-1",
				Auth: hibernatorv1alpha1.AWSAuth{
					Static: &hibernatorv1alpha1.StaticAuth{
						SecretRef: hibernatorv1alpha1.SecretReference{Name: secret.Name},
					},
				},
			},
		},
	}
	require.NoError(t, c.Create(ctx, provider))
	t.Cleanup(func() { _ = c.Delete(context.Background(), provider) })

	// Keep the schedule inactive during the smoke test. Manual override annotations
	// still traverse the production controller, Job, runner, restore, and AWS paths
	// without a 15-minute wall-clock wait. The nightly schedule test is opt-in.
	window := hibernationWindowAt(time.Now(), time.Hour, 15*time.Minute)
	t.Logf("inactive schedule window %s-%s UTC days=%v", window.start, window.end, window.days)
	paramsRaw, err := json.Marshal(executorparams.EC2Parameters{
		Selector:        executorparams.EC2Selector{Tags: tags},
		AwaitCompletion: executorparams.AwaitCompletion{Enabled: true, Timeout: "3m"},
	})
	require.NoError(t, err)

	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "kind-smoke", Namespace: ns},
		Spec: hibernatorv1alpha1.HibernatePlanSpec{
			Schedule: hibernatorv1alpha1.Schedule{
				Timezone: "UTC",
				OffHours: []hibernatorv1alpha1.OffHourWindow{
					{Start: window.start, End: window.end, DaysOfWeek: window.days},
				},
			},
			Execution: hibernatorv1alpha1.Execution{
				Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategyParallel},
			},
			Behavior: hibernatorv1alpha1.Behavior{Mode: hibernatorv1alpha1.BehaviorStrict},
			Targets: []hibernatorv1alpha1.Target{
				{
					Name: "bastion",
					Type: "ec2",
					ConnectorRef: hibernatorv1alpha1.ConnectorRef{
						Kind: "CloudProvider",
						Name: provider.Name,
					},
					Parameters: &hibernatorv1alpha1.Parameters{Raw: paramsRaw},
				},
			},
		},
	}
	require.NoError(t, c.Create(ctx, plan))
	t.Cleanup(func() { _ = c.Delete(context.Background(), plan) })
	planKey := client.ObjectKeyFromObject(plan)
	pollPhaseAtLeast(t, c, planKey, hibernatorv1alpha1.PhaseActive, 2*time.Minute)
	patchOverride(t, c, planKey, wellknown.OverridePhaseTargetHibernate)

	// Shutdown leg: real controller -> real runner pod -> real Floci StopInstances.
	pollPhaseAtLeast(t, c, planKey, hibernatorv1alpha1.PhaseHibernating, 3*time.Minute)
	hibernateJob := pollJobComplete(t, c, planKey, hibernatorv1alpha1.OperationHibernate, "bastion", 8*time.Minute)
	t.Logf("hibernate job %s complete", hibernateJob.Name)
	pollPhaseAtLeast(t, c, planKey, hibernatedPhase(), 3*time.Minute)

	// Restore data written by the runner pod (not simulated): must hold both
	// seeded instances with wasRunning=true.
	restoreMgr := restore.NewManager(c, logr.Discard())
	var rd *restore.Data
	require.Eventually(t, func() bool {
		d, err := restoreMgr.Load(ctx, ns, plan.Name, "bastion")
		if err != nil || d == nil || len(d.State) != 2 {
			return false
		}
		rd = d
		return true
	}, 2*time.Minute, 5*time.Second, "restore ConfigMap must hold 2 entries")
	require.Equal(t, "bastion", rd.Target)
	require.Equal(t, "ec2", rd.Executor)
	require.True(t, rd.IsLive)
	require.NotEmpty(t, rd.CycleID)
	require.False(t, rd.CreatedAt.IsZero())
	require.NotNil(t, rd.CapturedAt)
	restoreCM := &corev1.ConfigMap{}
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: ns, Name: restore.GetRestoreConfigMap(plan.Name)}, restoreCM))
	require.Equal(t, plan.Name, restoreCM.Labels[wellknown.LabelPlan])
	for id, raw := range rd.State {
		require.True(t, seeded[id], "restore key %s must be a seeded instance", id)
		stateMap, ok := raw.(map[string]any)
		require.True(t, ok, "restore entry %s must be a state map", id)
		require.Equal(t, true, stateMap["wasRunning"], "instance %s must record wasRunning=true", id)
	}

	// Independent AWS-side check via the host client.
	harness.WaitForInstanceState(t, ctx, hc.EC2, ids, ec2types.InstanceStateNameStopped, 3*time.Minute)

	// Wakeup leg: a controller override dispatches a distinct real runner pod,
	// which reads the restore ConfigMap and calls Floci StartInstances.
	patchOverride(t, c, planKey, wellknown.OverridePhaseTargetWakeup)
	pollPhaseAtLeast(t, c, planKey, hibernatorv1alpha1.PhaseWakingUp, 3*time.Minute)
	wakeupJob := pollJobComplete(t, c, planKey, hibernatorv1alpha1.OperationWakeUp, "bastion", 8*time.Minute)
	t.Logf("wakeup job %s complete", wakeupJob.Name)
	pollPhaseAtLeast(t, c, planKey, hibernatorv1alpha1.PhaseActive, 3*time.Minute)
	harness.WaitForInstanceState(t, ctx, hc.EC2, ids, ec2types.InstanceStateNameRunning, 3*time.Minute)
	require.Eventually(t, func() bool {
		consumed, err := restoreMgr.Load(ctx, ns, plan.Name, "bastion")
		return err == nil && consumed != nil && !consumed.IsLive && consumed.CycleID == ""
	}, 2*time.Minute, 2*time.Second, "wakeup must consume and unlock restore data")
}

// TestEC2ScheduleCycle is the nightly scheduler integration check. The default
// PR smoke above uses manual overrides to avoid a fixed wall-clock wait while
// still exercising the complete controller/runner/AWS chain.
func TestEC2ScheduleCycle(t *testing.T) {
	if os.Getenv("RUN_KIND_SCHEDULE") != "1" {
		t.Skip("set RUN_KIND_SCHEDULE=1 for the real wall-clock schedule cycle")
	}

	ctx := context.Background()
	hc := harness.Setup(t)
	ns := currentRun.namespace + "-schedule"
	ensureNamespace(mainTB{}, k8sClient, ns)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}
		if err := k8sClient.Delete(cleanupCtx, namespace); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("cleanup schedule namespace: %v", err)
			return
		}
		if err := wait.PollUntilContextTimeout(cleanupCtx, time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
			err := k8sClient.Get(ctx, client.ObjectKey{Name: ns}, &corev1.Namespace{})
			return apierrors.IsNotFound(err), client.IgnoreNotFound(err)
		}); err != nil {
			t.Errorf("schedule namespace %s not deleted: %v", ns, err)
		}
	})
	ensureRunnerRBAC(mainTB{}, k8sClient, ns)

	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "aws-creds", Namespace: ns}, StringData: map[string]string{
		"AWS_ACCESS_KEY_ID": "test", "AWS_SECRET_ACCESS_KEY": "test",
	}}
	require.NoError(t, k8sClient.Create(ctx, secret))
	provider := &hibernatorv1alpha1.CloudProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "aws", Namespace: ns},
		Spec: hibernatorv1alpha1.CloudProviderSpec{Type: hibernatorv1alpha1.CloudProviderAWS, AWS: &hibernatorv1alpha1.AWSConfig{
			AccountId: "000000000000", Region: "us-east-1", Auth: hibernatorv1alpha1.AWSAuth{Static: &hibernatorv1alpha1.StaticAuth{
				SecretRef: hibernatorv1alpha1.SecretReference{Name: secret.Name},
			}},
		}},
	}
	require.NoError(t, k8sClient.Create(ctx, provider))
	tags := map[string]string{"floci-e2e": harness.Nonce(), "scenario": "schedule"}
	ids := harness.SeedEC2(t, ctx, hc, "kind-schedule", tags, 1)
	params, err := json.Marshal(executorparams.EC2Parameters{Selector: executorparams.EC2Selector{Tags: tags}, AwaitCompletion: executorparams.AwaitCompletion{Enabled: true, Timeout: "3m"}})
	require.NoError(t, err)
	window := hibernationWindowAt(time.Now(), 3*time.Minute, 8*time.Minute)
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "schedule-cycle", Namespace: ns},
		Spec: hibernatorv1alpha1.HibernatePlanSpec{
			Schedule:  hibernatorv1alpha1.Schedule{Timezone: "UTC", OffHours: []hibernatorv1alpha1.OffHourWindow{{Start: window.start, End: window.end, DaysOfWeek: window.days}}},
			Execution: hibernatorv1alpha1.Execution{Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategyParallel}},
			Behavior:  hibernatorv1alpha1.Behavior{Mode: hibernatorv1alpha1.BehaviorStrict},
			Targets:   []hibernatorv1alpha1.Target{{Name: "ec2", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: provider.Name}, Parameters: &hibernatorv1alpha1.Parameters{Raw: params}}},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, plan))
	key := client.ObjectKeyFromObject(plan)
	pollPhaseAtLeast(t, k8sClient, key, hibernatorv1alpha1.PhaseHibernating, time.Until(window.startAt)+5*time.Minute)
	pollJobComplete(t, k8sClient, key, hibernatorv1alpha1.OperationHibernate, "ec2", 8*time.Minute)
	pollPhaseAtLeast(t, k8sClient, key, hibernatorv1alpha1.PhaseHibernated, 3*time.Minute)
	harness.WaitForInstanceState(t, ctx, hc.EC2, ids, ec2types.InstanceStateNameStopped, 3*time.Minute)
	pollPhaseAtLeast(t, k8sClient, key, hibernatorv1alpha1.PhaseWakingUp, time.Until(window.endAt)+5*time.Minute)
	pollJobComplete(t, k8sClient, key, hibernatorv1alpha1.OperationWakeUp, "ec2", 8*time.Minute)
	pollPhaseAtLeast(t, k8sClient, key, hibernatorv1alpha1.PhaseActive, 3*time.Minute)
	harness.WaitForInstanceState(t, ctx, hc.EC2, ids, ec2types.InstanceStateNameRunning, 3*time.Minute)
}

// hibernatedPhase indirection keeps phase references greppable in one place.
func hibernatedPhase() hibernatorv1alpha1.PlanPhase {
	return hibernatorv1alpha1.PhaseHibernated
}

// hibernationWindow returns start/end "15:04" strings and day names covering
// [now+lead, now+lead+length], including both endpoint days for midnight wrap.
type scheduleWindow struct {
	start   string
	end     string
	days    []string
	startAt time.Time
	endAt   time.Time
}

func hibernationWindowAt(now time.Time, lead, length time.Duration) scheduleWindow {
	s := now.UTC().Add(lead).Truncate(time.Minute)
	if !s.After(now.UTC().Add(lead)) {
		s = s.Add(time.Minute)
	}
	e := s.Add(length)
	day := func(t time.Time) string { return strings.ToUpper(t.Format("Mon")) }
	days := []string{day(s)}
	if d := day(e); d != days[0] {
		days = append(days, d)
	}
	return scheduleWindow{
		start: s.Format("15:04"), end: e.Format("15:04"), days: days,
		startAt: s, endAt: e,
	}
}

// pollPhaseAtLeast waits for a durable lifecycle milestone. Transient phases
// accept their immediate successful successor so a fast runner cannot make
// the test miss a correct transition between polls.
func pollPhaseAtLeast(t *testing.T, c client.Client, key client.ObjectKey, want hibernatorv1alpha1.PlanPhase, timeout time.Duration) {
	t.Helper()
	var lastPhase hibernatorv1alpha1.PlanPhase
	var lastErr error
	err := wait.PollUntilContextTimeout(context.Background(), 5*time.Second, timeout, true,
		func(ctx context.Context) (bool, error) {
			plan := &hibernatorv1alpha1.HibernatePlan{}
			if err := c.Get(ctx, key, plan); err != nil {
				lastErr = err
				if apierrors.IsForbidden(err) || apierrors.IsInvalid(err) {
					return false, err
				}
				return false, nil
			}
			lastErr = nil
			lastPhase = plan.Status.Phase
			switch want {
			case hibernatorv1alpha1.PhaseHibernating:
				return lastPhase == want || lastPhase == hibernatorv1alpha1.PhaseHibernated, nil
			case hibernatorv1alpha1.PhaseWakingUp:
				return lastPhase == want || lastPhase == hibernatorv1alpha1.PhaseActive, nil
			default:
				return lastPhase == want, nil
			}
		})
	if err != nil {
		dumpDebug(t, c, key.Namespace, key.Name)
		t.Fatalf("plan %s did not reach phase %s within %s (last phase: %s, last API error: %v): %v", key.Name, want, timeout, lastPhase, lastErr, err)
	}
	t.Logf("plan %s reached milestone %s (observed %s)", key.Name, want, lastPhase)
}

func patchOverride(t *testing.T, c client.Client, key client.ObjectKey, target string) {
	t.Helper()
	plan := &hibernatorv1alpha1.HibernatePlan{}
	require.NoError(t, c.Get(context.Background(), key, plan))
	base := plan.DeepCopy()
	if plan.Annotations == nil {
		plan.Annotations = map[string]string{}
	}
	plan.Annotations[wellknown.AnnotationOverrideAction] = "true"
	plan.Annotations[wellknown.AnnotationOverridePhaseTarget] = target
	require.NoError(t, c.Patch(context.Background(), plan, client.MergeFrom(base)))
}

// pollJobComplete waits until the newest runner Job for (operation, target)
// reports JobComplete. A JobFailed condition fails the test with pod logs.
func pollJobComplete(t *testing.T, c client.Client, planKey client.ObjectKey, op hibernatorv1alpha1.PlanOperation, target string, timeout time.Duration) *batchv1.Job {
	t.Helper()
	ctx := context.Background()
	var job *batchv1.Job
	var lastErr error
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true,
		func(ctx context.Context) (bool, error) {
			plan := &hibernatorv1alpha1.HibernatePlan{}
			if err := c.Get(ctx, planKey, plan); err != nil {
				lastErr = err
				return false, nil
			}
			if plan.Status.CurrentCycleID == "" {
				return false, nil
			}
			list := &batchv1.JobList{}
			if err := c.List(ctx, list, client.InNamespace(planKey.Namespace), client.MatchingLabels{
				wellknown.LabelPlan:      planKey.Name,
				wellknown.LabelOperation: string(op),
				wellknown.LabelTarget:    target,
				wellknown.LabelCycleID:   plan.Status.CurrentCycleID,
			}); err != nil {
				lastErr = err
				if apierrors.IsForbidden(err) || apierrors.IsInvalid(err) {
					return false, err
				}
				return false, nil
			}
			lastErr = nil
			matches := make([]batchv1.Job, 0, len(list.Items))
			for i := range list.Items {
				if jobBelongsToCycle(list.Items[i], plan.UID, plan.Status.CurrentCycleID) {
					matches = append(matches, list.Items[i])
				}
			}
			if len(matches) == 0 {
				return false, nil
			}
			newest := &matches[0]
			for i := range matches {
				if matches[i].CreationTimestamp.After(newest.CreationTimestamp.Time) {
					newest = &matches[i]
				}
			}
			job = newest
			for _, cond := range newest.Status.Conditions {
				if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
					return false, fmt.Errorf("runner job %s failed: %s", newest.Name, cond.Message)
				}
				if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
					return true, nil
				}
			}
			return false, nil
		})
	if err != nil {
		dumpDebug(t, c, planKey.Namespace, planKey.Name)
		t.Fatalf("runner job (%s/%s) not complete within %s (last API error: %v): %v", op, target, timeout, lastErr, err)
	}
	require.NotEmpty(t, job.Spec.Template.Spec.Containers)
	require.Equal(t, currentRun.runnerTestImage, job.Spec.Template.Spec.Containers[0].Image, "runner Job must use this run's image")
	return job
}

// jobBelongsToCycle uses durable ownership evidence. Terminal Jobs are
// deliberately marked stale by the controller after completion, so the stale
// label cannot be used to reject the Job that proves this cycle completed.
func jobBelongsToCycle(job batchv1.Job, planUID types.UID, cycleID string) bool {
	if job.Labels[wellknown.LabelCycleID] != cycleID {
		return false
	}
	for _, owner := range job.OwnerReferences {
		if owner.UID == planUID {
			return true
		}
	}
	return false
}

// dumpDebug logs Jobs and runner pod logs for post-mortem triage.
func dumpDebug(t *testing.T, c client.Client, ns, planName string) {
	t.Helper()
	ctx := context.Background()
	plan := &hibernatorv1alpha1.HibernatePlan{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: planName}, plan); err == nil {
		t.Logf("debug: plan phase=%s operation=%s cycle=%s error=%q executions=%+v", plan.Status.Phase, plan.Status.CurrentOperation, plan.Status.CurrentCycleID, plan.Status.ErrorMessage, plan.Status.Executions)
	} else {
		t.Logf("debug: get plan: %v", err)
	}
	jobs := &batchv1.JobList{}
	if err := c.List(ctx, jobs, client.InNamespace(ns), client.MatchingLabels{wellknown.LabelPlan: planName}); err != nil {
		t.Logf("debug: list jobs: %v", err)
		return
	}
	for _, j := range jobs.Items {
		t.Logf("debug: job %s labels=%v active=%d succeeded=%d failed=%d conditions=%+v", j.Name, j.Labels, j.Status.Active, j.Status.Succeeded, j.Status.Failed, j.Status.Conditions)
	}
	pods := &corev1.PodList{}
	if err := c.List(ctx, pods, client.InNamespace(ns), client.MatchingLabels{wellknown.LabelPlan: planName}); err != nil {
		t.Logf("debug: list pods: %v", err)
		return
	}
	tail := int64(100)
	for _, p := range pods.Items {
		t.Logf("debug: pod %s phase=%s reason=%q message=%q containers=%+v", p.Name, p.Status.Phase, p.Status.Reason, p.Status.Message, p.Status.ContainerStatuses)
		raw, err := k8sSets.CoreV1().Pods(ns).GetLogs(p.Name, &corev1.PodLogOptions{TailLines: &tail}).Do(ctx).Raw()
		if err != nil {
			t.Logf("debug: pod %s logs unavailable: %v", p.Name, err)
			continue
		}
		t.Logf("debug: pod %s (phase %s) last logs:\n%s", p.Name, p.Status.Phase, raw)
	}
	restoreCM := &corev1.ConfigMap{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: restore.GetRestoreConfigMap(planName)}, restoreCM); err == nil {
		t.Logf("debug: restore ConfigMap labels=%v annotations=%v data=%v", restoreCM.Labels, restoreCM.Annotations, restoreCM.Data)
	}
	controllerPods := &corev1.PodList{}
	if err := c.List(ctx, controllerPods, client.InNamespace(systemNamespace), client.MatchingLabels{"app.kubernetes.io/component": "controller"}); err == nil {
		tail := int64(200)
		for _, p := range controllerPods.Items {
			raw, err := k8sSets.CoreV1().Pods(systemNamespace).GetLogs(p.Name, &corev1.PodLogOptions{TailLines: &tail}).Do(ctx).Raw()
			if err == nil {
				t.Logf("debug: controller pod %s logs:\n%s", p.Name, raw)
			}
		}
	}
	service := &corev1.Service{}
	endpoints := &corev1.Endpoints{}
	_ = c.Get(ctx, client.ObjectKey{Namespace: systemNamespace, Name: flociServiceName}, service)
	_ = c.Get(ctx, client.ObjectKey{Namespace: systemNamespace, Name: flociServiceName}, endpoints)
	t.Logf("debug: Floci service clusterIP=%s ports=%v endpoints=%v", service.Spec.ClusterIP, service.Spec.Ports, endpoints.Subsets)
}
