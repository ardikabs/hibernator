//go:build kind

package suite

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/executor/noop"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// scnAssertRestoreConsumed asserts wakeup consumed restore data per target.
func ScnAssertRestoreConsumed(t *testing.T, c client.Client, ns, planName string, targets []string) {
	t.Helper()
	restoreMgr := restore.NewManager(c, logr.Discard())
	for _, target := range targets {
		require.Eventually(t, func() bool {
			consumed, err := restoreMgr.Load(context.Background(), ns, planName, target)
			return err == nil && consumed != nil && !consumed.IsLive && consumed.CycleID == ""
		}, 2*time.Minute, 2*time.Second, "wakeup must consume restore data for target %s", target)
	}
}

// hibernateStepExpectation declares everything a hibernate step must prove.
// Empty/nil fields are skipped, so each scenario asserts only its contract.
type HibernateStepExpectation struct {
	// States is the exact expected ledger after Hibernated (target -> state).
	States map[string]hibernatorv1alpha1.ExecutionState
	// Jobs lists targets that must have a completed shutdown Job.
	Jobs []string
	// NoJobs lists targets that must have zero shutdown Jobs (disabled).
	NoJobs []string
	// Order lists StartedAt ordering constraints (a started before b).
	Order [][2]string
	// Markers asserts restore marker round-trip (target -> marker).
	Markers map[string]string
	// Messages asserts ledger message substrings (target -> substring).
	Messages map[string]string
}

// wakeupStepExpectation declares everything a wakeup step must prove.
type WakeupStepExpectation struct {
	// States is the exact expected ledger after the leg (usually Active-phase
	// states remain from hibernate; pass nil to skip ledger assertion).
	States map[string]hibernatorv1alpha1.ExecutionState
	// Jobs lists targets that must have a completed wakeup Job.
	Jobs []string
	// NoJobs lists targets that must have zero wakeup Jobs (disabled).
	NoJobs []string
	// Order lists StartedAt ordering constraints (a started before b).
	Order [][2]string
	// Consume lists targets whose restore data must be consumed.
	Consume []string
}

// runHibernateStep patches the hibernate override and proves exp.
func RunHibernateStep(t *testing.T, c client.Client, cs *kubernetes.Clientset, ns string, planKey client.ObjectKey, exp HibernateStepExpectation) {
	t.Helper()
	PatchOverride(t, c, planKey, wellknown.OverridePhaseTargetHibernate)
	PollPhaseAtLeast(t, c, cs, planKey, hibernatorv1alpha1.PhaseHibernating, 3*time.Minute)
	for _, target := range exp.Jobs {
		PollJobComplete(t, c, cs, planKey, hibernatorv1alpha1.OperationHibernate, target, 3*time.Minute)
	}
	PollPhaseAtLeast(t, c, cs, planKey, HibernatedPhase(), 3*time.Minute)
	if len(exp.States) > 0 {
		PollLedgerExact(t, c, cs, planKey, exp.States, 6*time.Minute)
	}
	ledger := ScnLedger(t, c, planKey)
	for target, substr := range exp.Messages {
		require.Contains(t, ledger[target].Message, substr, "ledger message for %s", target)
	}
	for _, pair := range exp.Order {
		ScnAssertStartedBefore(t, ledger, pair[0], pair[1])
	}
	for _, target := range exp.NoJobs {
		ScnAssertNoJobs(t, c, ns, planKey.Name, hibernatorv1alpha1.OperationHibernate, target)
	}
	if len(exp.Markers) > 0 {
		ScnAssertRestoreMarkers(t, c, ns, planKey.Name, exp.Markers)
	}
}

// runWakeupStep patches the wakeup override and proves exp.
func RunWakeupStep(t *testing.T, c client.Client, cs *kubernetes.Clientset, ns string, planKey client.ObjectKey, exp WakeupStepExpectation) {
	t.Helper()
	PatchOverride(t, c, planKey, wellknown.OverridePhaseTargetWakeup)
	PollPhaseAtLeast(t, c, cs, planKey, hibernatorv1alpha1.PhaseWakingUp, 3*time.Minute)
	for _, target := range exp.Jobs {
		PollJobComplete(t, c, cs, planKey, hibernatorv1alpha1.OperationWakeUp, target, 3*time.Minute)
	}
	PollPhaseAtLeast(t, c, cs, planKey, hibernatorv1alpha1.PhaseActive, 3*time.Minute)
	if len(exp.States) > 0 {
		PollLedgerExact(t, c, cs, planKey, exp.States, 3*time.Minute)
	}
	ledger := ScnLedger(t, c, planKey)
	for _, pair := range exp.Order {
		ScnAssertStartedBefore(t, ledger, pair[0], pair[1])
	}
	for _, target := range exp.NoJobs {
		ScnAssertNoJobs(t, c, ns, planKey.Name, hibernatorv1alpha1.OperationWakeUp, target)
	}
	if len(exp.Consume) > 0 {
		ScnAssertRestoreConsumed(t, c, ns, planKey.Name, exp.Consume)
	}
}

// scnConnector creates the shared dummy connector. The noop executor
// validates that a connector exists but never calls it.
func ScnConnector(t *testing.T, c client.Client, ns string) string {
	t.Helper()
	ctx := context.Background()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "scn-creds", Namespace: ns},
		StringData: map[string]string{
			"AWS_ACCESS_KEY_ID":     "test",
			"AWS_SECRET_ACCESS_KEY": "test",
		},
	}
	require.NoError(t, c.Create(ctx, secret))
	t.Cleanup(func() { _ = c.Delete(context.Background(), secret) })
	provider := &hibernatorv1alpha1.CloudProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "scn-aws", Namespace: ns},
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
	return provider.Name
}

func ScnDeleteException(t *testing.T, c client.Client, ns, name string) {
	t.Helper()
	exc := &hibernatorv1alpha1.ScheduleException{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}
	require.NoError(t, c.Delete(context.Background(), exc))
}

// scnPatchSchedule replaces a plan's schedule, triggering a reconcile.
func ScnPatchSchedule(t *testing.T, c client.Client, key client.ObjectKey, schedule hibernatorv1alpha1.Schedule) {
	t.Helper()
	plan := &hibernatorv1alpha1.HibernatePlan{}
	require.NoError(t, c.Get(context.Background(), key, plan))
	base := plan.DeepCopy()
	plan.Spec.Schedule = schedule
	require.NoError(t, c.Patch(context.Background(), plan, client.MergeFrom(base)))
}

// scnLedger returns the current per-target execution states.
func ScnLedger(t *testing.T, c client.Client, key client.ObjectKey) map[string]hibernatorv1alpha1.ExecutionStatus {
	t.Helper()
	plan := &hibernatorv1alpha1.HibernatePlan{}
	require.NoError(t, c.Get(context.Background(), key, plan))
	out := make(map[string]hibernatorv1alpha1.ExecutionStatus, len(plan.Status.Executions))
	for _, exec := range plan.Status.Executions {
		out[exec.Target] = exec
	}
	return out
}

// pollLedgerState waits until a target reaches the wanted ledger state.
func PollLedgerState(t *testing.T, c client.Client, cs *kubernetes.Clientset, key client.ObjectKey, target string, want hibernatorv1alpha1.ExecutionState, timeout time.Duration) {
	t.Helper()
	err := wait.PollUntilContextTimeout(context.Background(), 5*time.Second, timeout, true,
		func(ctx context.Context) (bool, error) {
			plan := &hibernatorv1alpha1.HibernatePlan{}
			if err := c.Get(ctx, key, plan); err != nil {
				return false, nil
			}
			for _, exec := range plan.Status.Executions {
				if exec.Target == target {
					return exec.State == want, nil
				}
			}
			return false, nil
		})
	if err != nil {
		dumpDebug(t, c, cs, key.Namespace, key.Name)
		t.Fatalf("target %s did not reach ledger state %s within %s: %v", target, want, timeout, err)
	}
}

// scnAssertNoJobs fails if any Job exists for the operation/target pair.
func ScnAssertNoJobs(t *testing.T, c client.Client, ns, planName string, op hibernatorv1alpha1.PlanOperation, target string) {
	t.Helper()
	list := &batchv1.JobList{}
	require.NoError(t, c.List(context.Background(), list, client.InNamespace(ns), client.MatchingLabels{
		wellknown.LabelPlan:      planName,
		wellknown.LabelOperation: string(op),
		wellknown.LabelTarget:    target,
	}))
	require.Empty(t, list.Items, "expected no %s jobs for disabled target %s", op, target)
}

// scnAssertRestoreMarkers loads restore state per target and checks the
// deterministic marker round-trip written by the noop executor on shutdown.
func ScnAssertRestoreMarkers(t *testing.T, c client.Client, ns, planName string, want map[string]string) {
	t.Helper()
	ctx := context.Background()
	restoreMgr := restore.NewManager(c, logr.Discard())
	for target, marker := range want {
		var state noop.RestoreState
		require.Eventually(t, func() bool {
			d, err := restoreMgr.Load(ctx, ns, planName, target)
			if err != nil || d == nil || len(d.State) != 1 {
				return false
			}
			for _, raw := range d.State {
				rawJSON, err := json.Marshal(raw)
				if err != nil {
					return false
				}
				if err := json.Unmarshal(rawJSON, &state); err != nil {
					return false
				}
			}
			return state.Marker == marker && state.TargetName == target
		}, 2*time.Minute, 5*time.Second, "restore state for target %s must echo marker %s", target, marker)
	}
}

// scnAssertStartedBefore asserts execution a started before execution b.
func ScnAssertStartedBefore(t *testing.T, ledger map[string]hibernatorv1alpha1.ExecutionStatus, a, b string) {
	t.Helper()
	require.Contains(t, ledger, a)
	require.Contains(t, ledger, b)
	require.NotNil(t, ledger[a].StartedAt, "target %s must have StartedAt", a)
	require.NotNil(t, ledger[b].StartedAt, "target %s must have StartedAt", b)
	require.True(t, ledger[a].StartedAt.Before(ledger[b].StartedAt),
		"target %s (%s) must start before %s (%s)", a, ledger[a].StartedAt, b, ledger[b].StartedAt)
}

// pollExceptionActive waits until the exception lifecycle marks the exception
// Active. Overrides and suppressions key off this status.
func PollExceptionActive(t *testing.T, c client.Client, cs *kubernetes.Clientset, ns, name string, timeout time.Duration) {
	t.Helper()
	err := wait.PollUntilContextTimeout(context.Background(), 2*time.Second, timeout, true,
		func(ctx context.Context) (bool, error) {
			exc := &hibernatorv1alpha1.ScheduleException{}
			if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, exc); err != nil {
				return false, nil
			}
			return exc.Status.State == hibernatorv1alpha1.ExceptionStateActive, nil
		})
	if err != nil {
		dumpDebug(t, c, cs, ns, name)
		t.Fatalf("exception %s did not become Active within %s: %v", name, timeout, err)
	}
}

func DayName(t time.Time) string {
	return strings.ToUpper(t.Format("Mon"))
}

// pollLedgerExact waits until the ledger exactly matches want.
func PollLedgerExact(t *testing.T, c client.Client, cs *kubernetes.Clientset, key client.ObjectKey, want map[string]hibernatorv1alpha1.ExecutionState, timeout time.Duration) {
	t.Helper()
	err := wait.PollUntilContextTimeout(context.Background(), 5*time.Second, timeout, true,
		func(ctx context.Context) (bool, error) {
			plan := &hibernatorv1alpha1.HibernatePlan{}
			if err := c.Get(ctx, key, plan); err != nil {
				return false, nil
			}
			if len(plan.Status.Executions) != len(want) {
				return false, nil
			}
			for _, exec := range plan.Status.Executions {
				if want[exec.Target] != exec.State {
					return false, nil
				}
			}
			return true, nil
		})
	if err != nil {
		dumpDebug(t, c, cs, key.Namespace, key.Name)
		t.Fatalf("ledger did not match expectation within %s: %v", timeout, err)
	}
}
