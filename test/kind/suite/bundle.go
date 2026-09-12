//go:build kind

// Package suite holds the kind e2e support infrastructure: scenario loading,
// polling helpers, and assertions. Test entry points live in the parent kind
// package and drive this suite; only Test functions remain there.
package suite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// Scenario file conventions (test/kind/suite/testdata/scenarios/<name>.yaml):
//   - One or more YAML documents: exactly one HibernatePlan plus zero or
//     more ScheduleExceptions, mirroring website/docs/scenarios.
//   - The plan's `schedule` may be omitted, in which case the loader injects
//     an inactive window so the test stays override-driven and fast.
//   - An exception's `validFrom`/`validUntil` may be omitted, in which case
//     the loader anchors them to now-5m/now+55m so the exception is live.
//   - Namespaces are always overridden to the test namespace; object,
//     target, and marker names are used verbatim.
//   - Noop `marker` values must be unique per target and at most 64 chars
//     (the noop executor limit); the loader unit test enforces this.
type Bundle struct {
	Plan       *hibernatorv1alpha1.HibernatePlan
	Exceptions []*hibernatorv1alpha1.ScheduleException
}

// ScenarioDir returns the absolute path of the scenario fixture directory.
// Fixtures live next to this source file and resolve via runtime.Caller,
// so callers work regardless of the test binary's working directory.
func ScenarioDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "resolve loader source path")
	return filepath.Join(filepath.Dir(thisFile), "testdata", "scenarios")
}

// LoadScenario parses a scenario file without touching the cluster.
func LoadScenario(t *testing.T, ns, name string) Bundle {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(ScenarioDir(t), name+".yaml"))
	require.NoError(t, err, "read scenario file %s", name)

	var bundle Bundle
	dec := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(raw), 4096)
	for {
		var doc map[string]interface{}
		err := dec.Decode(&doc)
		if err == io.EOF {
			break
		}
		require.NoError(t, err, "decode scenario file %s", name)
		if len(doc) == 0 {
			continue
		}
		kind, _ := doc["kind"].(string)
		docJSON, err := json.Marshal(doc)
		require.NoError(t, err)
		switch kind {
		case "HibernatePlan":
			var plan hibernatorv1alpha1.HibernatePlan
			require.NoError(t, json.Unmarshal(docJSON, &plan), "unmarshal HibernatePlan in %s", name)
			require.Nil(t, bundle.Plan, "scenario %s must declare exactly one HibernatePlan", name)
			plan.Namespace = ns
			if len(plan.Spec.Schedule.OffHours) == 0 {
				window := HibernationWindowAt(time.Now(), time.Hour, 15*time.Minute)
				plan.Spec.Schedule = hibernatorv1alpha1.Schedule{
					Timezone: "UTC",
					OffHours: []hibernatorv1alpha1.OffHourWindow{
						{Start: window.Start, End: window.End, DaysOfWeek: window.Days},
					},
				}
			}
			bundle.Plan = &plan
		case "ScheduleException":
			var exc hibernatorv1alpha1.ScheduleException
			require.NoError(t, json.Unmarshal(docJSON, &exc), "unmarshal ScheduleException in %s", name)
			exc.Namespace = ns
			now := time.Now().UTC()
			if exc.Spec.ValidFrom.IsZero() {
				exc.Spec.ValidFrom.Time = now.Add(-5 * time.Minute)
			}
			if exc.Spec.ValidUntil.IsZero() {
				exc.Spec.ValidUntil.Time = now.Add(55 * time.Minute)
			}
			bundle.Exceptions = append(bundle.Exceptions, &exc)
		default:
			t.Fatalf("scenario %s: unsupported document kind %q", name, kind)
		}
	}
	require.NotNil(t, bundle.Plan, "scenario %s must declare a HibernatePlan", name)
	return bundle
}

// ScenarioDependencies returns target -> upstream dependencies from a DAG strategy.
func ScenarioDependencies(b Bundle) map[string][]string {
	out := map[string][]string{}
	if b.Plan == nil {
		return out
	}
	for _, dep := range b.Plan.Spec.Execution.Strategy.Dependencies {
		out[dep.To] = append(out[dep.To], dep.From)
	}
	return out
}

// ScenarioStageMembership returns target -> stage name.
func ScenarioStageMembership(b Bundle) map[string]string {
	out := map[string]string{}
	if b.Plan == nil {
		return out
	}
	for _, stage := range b.Plan.Spec.Execution.Strategy.Stages {
		for _, target := range stage.Targets {
			out[target] = stage.Name
		}
	}
	return out
}

// applyPlan creates the plan object with cleanup and returns its key.
func (b Bundle) ApplyPlan(t *testing.T, c client.Client) client.ObjectKey {
	t.Helper()
	require.NoError(t, c.Create(context.Background(), b.Plan))
	t.Cleanup(func() { _ = c.Delete(context.Background(), b.Plan) })
	return client.ObjectKeyFromObject(b.Plan)
}

// applyException creates one exception from the bundle by name, waits until
// the controller marks it Active, and registers cleanup.
func (b Bundle) ApplyException(t *testing.T, c client.Client, cs *kubernetes.Clientset, name string) {
	t.Helper()
	var found *hibernatorv1alpha1.ScheduleException
	for _, exc := range b.Exceptions {
		if exc.Name == name {
			found = exc
		}
	}
	require.NotNil(t, found, "scenario has no exception named %s", name)
	require.NoError(t, c.Create(context.Background(), found))
	t.Cleanup(func() { _ = c.Delete(context.Background(), found) })
	PollExceptionActive(t, c, cs, found.Namespace, name, 2*time.Minute)
}

// hibernatedPhase indirection keeps phase references greppable in one place.
func HibernatedPhase() hibernatorv1alpha1.PlanPhase {
	return hibernatorv1alpha1.PhaseHibernated
}

// hibernationWindow returns start/end "15:04" strings and day names covering
// [now+lead, now+lead+length], including both endpoint days for midnight wrap.
type ScheduleWindow struct {
	Start   string
	End     string
	Days    []string
	StartAt time.Time
	EndAt   time.Time
}

func HibernationWindowAt(now time.Time, lead, length time.Duration) ScheduleWindow {
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
	return ScheduleWindow{
		Start: s.Format("15:04"), End: e.Format("15:04"), Days: days,
		StartAt: s, EndAt: e,
	}
}

// FixedScheduleWindow builds a ScheduleWindow anchored to absolute wall-clock
// times ("15:04", UTC) on the day containing now. An end at or before start
// rolls to the next day (overnight window, e.g. 23:55-00:05 spans midnight);
// both endpoint days are emitted so the scheduler fires each edge exactly
// once. Unlike HibernationWindowAt, the anchors do not drift with setup
// duration, which is what makes fixed nightly windows observable.
func FixedScheduleWindow(now time.Time, startHHMM, endHHMM string) (ScheduleWindow, error) {
	parse := func(label, s string) (int, int, error) {
		parts := strings.Split(s, ":")
		if len(parts) != 2 {
			return 0, 0, fmt.Errorf("invalid %s time %q, expected HH:MM", label, s)
		}
		var hh, mm int
		if _, err := fmt.Sscanf(s, "%d:%d", &hh, &mm); err != nil {
			return 0, 0, fmt.Errorf("invalid %s time %q: %w", label, s, err)
		}
		if hh < 0 || hh > 23 || mm < 0 || mm > 59 {
			return 0, 0, fmt.Errorf("invalid %s time %q, hour 0-23 and minute 0-59 required", label, s)
		}
		return hh, mm, nil
	}

	sh, sm, err := parse("start", startHHMM)
	if err != nil {
		return ScheduleWindow{}, err
	}
	eh, em, err := parse("end", endHHMM)
	if err != nil {
		return ScheduleWindow{}, err
	}
	if sh == eh && sm == em {
		return ScheduleWindow{}, fmt.Errorf("start and end times must be different; start=%s, end=%s", startHHMM, endHHMM)
	}

	now = now.UTC()
	s := time.Date(now.Year(), now.Month(), now.Day(), sh, sm, 0, 0, time.UTC)
	e := time.Date(now.Year(), now.Month(), now.Day(), eh, em, 0, 0, time.UTC)
	if !e.After(s) {
		e = e.Add(24 * time.Hour)
	}
	day := func(t time.Time) string { return strings.ToUpper(t.Format("Mon")) }
	days := []string{day(s)}
	if d := day(e); d != days[0] {
		days = append(days, d)
	}
	return ScheduleWindow{
		Start: s.Format("15:04"), End: e.Format("15:04"), Days: days,
		StartAt: s, EndAt: e,
	}, nil
}

// pollPhaseAtLeast waits for a durable lifecycle milestone. Transient phases
// accept their immediate successful successor so a fast runner cannot make
// the test miss a correct transition between polls.
func PollPhaseAtLeast(t *testing.T, c client.Client, cs *kubernetes.Clientset, key client.ObjectKey, want hibernatorv1alpha1.PlanPhase, timeout time.Duration) {
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
		dumpDebug(t, c, cs, key.Namespace, key.Name)
		t.Fatalf("plan %s did not reach phase %s within %s (last phase: %s, last API error: %v): %v", key.Name, want, timeout, lastPhase, lastErr, err)
	}
	t.Logf("plan %s reached milestone %s (observed %s)", key.Name, want, lastPhase)
}

func PatchOverride(t *testing.T, c client.Client, key client.ObjectKey, target string) {
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
func PollJobComplete(t *testing.T, c client.Client, cs *kubernetes.Clientset, planKey client.ObjectKey, op hibernatorv1alpha1.PlanOperation, target string, timeout time.Duration) *batchv1.Job {
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
				if JobBelongsToCycle(list.Items[i], plan.UID, plan.Status.CurrentCycleID) {
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
		dumpDebug(t, c, cs, planKey.Namespace, planKey.Name)
		t.Fatalf("runner job (%s/%s) not complete within %s (last API error: %v): %v", op, target, timeout, lastErr, err)
	}
	require.NotEmpty(t, job.Spec.Template.Spec.Containers)
	require.Equal(t, CurrentRun.RunnerImage, job.Spec.Template.Spec.Containers[0].Image, "runner Job must use this run's image")
	return job
}

// jobBelongsToCycle uses durable ownership evidence. Terminal Jobs are
// deliberately marked stale by the controller after completion, so the stale
// label cannot be used to reject the Job that proves this cycle completed.
func JobBelongsToCycle(job batchv1.Job, planUID types.UID, cycleID string) bool {
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
// The clientset arrives as a parameter so this helper never reaches outside
// its own scope for shared state.
func dumpDebug(t *testing.T, c client.Client, cs *kubernetes.Clientset, ns, planName string) {
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
		raw, err := cs.CoreV1().Pods(ns).GetLogs(p.Name, &corev1.PodLogOptions{TailLines: &tail}).Do(ctx).Raw()
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
	if err := c.List(ctx, controllerPods, client.InNamespace(SystemNamespace), client.MatchingLabels{"app.kubernetes.io/component": "controller"}); err == nil {
		tail := int64(200)
		for _, p := range controllerPods.Items {
			raw, err := cs.CoreV1().Pods(SystemNamespace).GetLogs(p.Name, &corev1.PodLogOptions{TailLines: &tail}).Do(ctx).Raw()
			if err == nil {
				t.Logf("debug: controller pod %s logs:\n%s", p.Name, raw)
			}
		}
	}
}
