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
// The window resolves via resolveScheduleWindow: always relative to the
// moment the test starts (hibernate edge ~3 min out, wakeup edge a fixed
// 5 min later). There are no absolute clock anchors: GitHub `schedule`
// triggers are best-effort and never fire at an exact minute, so pinning
// the suite to a fixed HH:MM (e.g. via KIND_SCHEDULE_START/END) made the
// nightly flaky by design. The cron time itself is arbitrary night time;
// the suite adapts to whenever the job actually starts.
//
// Four plans share one namespace: a normal schedule transitioning both
// ways on the resolved window, a full-day hibernation holding Hibernated,
// a day-aware parking hold proving steady Hibernated on every calendar day
// (see resolveParkingWindow), and a full-day-active plan asserting zero
// Jobs.
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

	// Day-aware parking hold: the suite runs every 6h (`55 */6 * * *`), so
	// it fires on weekday daytimes, weeknights, and weekends alike. A
	// static MON-FRI 20:00-06:00 window is only hibernate-desired at night
	// and on weekends (on a weekday daytime the last 06:00 wakeup beats
	// the previous 20:00 hibernation, so the plan is legitimately Active
	// and a Hibernated assert fails with a misleading "weekend-parking"
	// message). resolveParkingWindow therefore picks the window — and the
	// plan name — from the calendar day and time the test is running on,
	// so the plan is always hibernate-desired at creation and the failure
	// message always names the scenario that actually ran.
	parkingName, parkingMarker, parkingWindows, parkingReason := resolveParkingWindow(t, time.Now())
	t.Logf("parking plan %q reason: %s windows=%v", parkingName, parkingReason, parkingWindows)
	createSchedulePlan(t, ctx, c, ns, provider.Name, parkingName, parkingMarker, parkingWindows)
	parkingKey := client.ObjectKey{Namespace: ns, Name: parkingName}
	suite.PollPhaseAtLeast(t, c, cs, parkingKey, hibernatorv1alpha1.PhaseHibernated, 8*time.Minute)
	suite.PollJobComplete(t, c, cs, parkingKey, hibernatorv1alpha1.OperationHibernate, "noop", 8*time.Minute)

	// Full-day active: a window that ended well before the test has no
	// edges near the resolved hibernation window, so the plan must stay
	// Active (and dispatch zero Jobs) for the whole run. The window is
	// anchored relative to now (2h ago) rather than a fixed clock time so
	// the suite stays agnostic to whenever cron actually fires. A 1-minute
	// 23:59-00:00 window is deliberately NOT used here: the 1m schedule
	// buffer would force a phantom hibernation inside the 00:00-00:01 end
	// grace.
	pastStart := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Minute)
	pastEnd := pastStart.Add(10 * time.Minute)
	dayOf := func(tm time.Time) string { return strings.ToUpper(tm.Format("Mon")) }
	pastDays := []string{dayOf(pastStart)}
	if d := dayOf(pastEnd); d != pastDays[0] {
		pastDays = append(pastDays, d)
	}
	createSchedulePlan(t, ctx, c, ns, provider.Name, "fullday-active", "sched-active",
		[]hibernatorv1alpha1.OffHourWindow{{
			Start: pastStart.Format("15:04"), End: pastEnd.Format("15:04"), DaysOfWeek: pastDays,
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

	// Steady-state: neither full-day plan may have flapped. The
	// NoJobs asserts cover the whole run, so any boundary dispatch (e.g. a
	// phantom wakeup of the hibernated plan, or any Job for the active
	// plan) fails here even if phases settled back.
	suite.PollPhaseAtLeast(t, c, cs, fullHibernateKey, hibernatorv1alpha1.PhaseHibernated, 2*time.Minute)
	suite.ScnAssertRestoreMarkers(t, c, ns, "fullday-hibernate", map[string]string{"noop": "sched-fullday"})
	suite.ScnAssertNoJobs(t, c, ns, "fullday-hibernate", hibernatorv1alpha1.OperationWakeUp, "noop")

	suite.PollPhaseAtLeast(t, c, cs, parkingKey, hibernatorv1alpha1.PhaseHibernated, 2*time.Minute)
	suite.ScnAssertRestoreMarkers(t, c, ns, parkingName, map[string]string{"noop": parkingMarker})
	suite.ScnAssertNoJobs(t, c, ns, parkingName, hibernatorv1alpha1.OperationWakeUp, "noop")

	suite.PollPhaseAtLeast(t, c, cs, activeKey, hibernatorv1alpha1.PhaseActive, 2*time.Minute)
	suite.ScnAssertNoJobs(t, c, ns, "fullday-active", hibernatorv1alpha1.OperationHibernate, "noop")
	suite.ScnAssertNoJobs(t, c, ns, "fullday-active", hibernatorv1alpha1.OperationWakeUp, "noop")
}

// utcDayName returns the UTC three-letter day name (MON..SUN) for tm,
// matching the DaysOfWeek convention used by OffHourWindow.
func utcDayName(tm time.Time) string {
	return strings.ToUpper(tm.UTC().Format("Mon"))
}

// resolveParkingWindow picks the parking-hold plan from the calendar day
// and time the test is running on, so the plan is hibernate-desired at
// creation no matter which of the 6-hourly cron slots fired.
//
//   - Weekend (Sat/Sun): "weekend-parking" with the classic MON-FRI
//     20:00-06:00 UTC window. Parked since Friday 20:00 (no Saturday
//     wakeup edge exists), it proves no-weekend-cycling live.
//   - Weeknight (Mon-Fri 20:00-06:00): "weeknight-parking" with the same
//     window, in-window at creation, proving off-hours hibernation.
//   - Weekday daytime (Mon-Fri 06:00-20:00): "daytime-parking" with a
//     relative hold window (hibernate edge 2h ago, wakeup edge 8h later,
//     days covering both endpoints). The overnight window would
//     legitimately evaluate to Active here, so a static window would fail
//     by design; the relative hold instead proves a steady Hibernated
//     hold through the whole run on every weekday.
//
// Slot-to-branch mapping for the `55 */6 * * *` cadence (nominal 00:55,
// 06:55, 12:55, 18:55 UTC; GitHub fires best-effort so the branching keys
// off the actual clock, not the slot): 00:55 exercises weeknight (or
// weekend), while 06:55/12:55/18:55 exercise daytime-hold (or weekend).
// Weekly that yields ~5 weeknight + ~15 daytime + ~8 weekend proofs, so a
// delayed or skipped slot never loses a scenario. The :55 minute offset
// is deliberate: every window edge in this suite sits on the hour or
// :59 with ~1m grace, and runs last ~15m, so :55 keeps 55m clearance
// from all edges in both directions.
//
// The returned reason is logged by the caller so a failure message always
// carries the scenario that actually ran.
func resolveParkingWindow(t *testing.T, now time.Time) (planName, marker string, windows []hibernatorv1alpha1.OffHourWindow, reason string) {
	t.Helper()
	now = now.UTC()
	overnight := []hibernatorv1alpha1.OffHourWindow{{
		Start: "20:00", End: "06:00",
		DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
	}}
	switch now.Weekday() {
	case time.Saturday, time.Sunday:
		return "weekend-parking", "sched-park-weekend", overnight,
			"weekend run: parked since Friday 20:00 UTC with no Saturday wakeup edge"
	default:
		hm := now.Hour()*60 + now.Minute()
		if hm >= 20*60 || hm < 6*60 {
			return "weeknight-parking", "sched-park-weeknight", overnight,
				"weeknight run: inside the MON-FRI 20:00-06:00 UTC off-hours window"
		}
		// Weekday daytime: relative hold window already in effect.
		start := now.Add(-2 * time.Hour).Truncate(time.Minute)
		end := start.Add(8 * time.Hour)
		days := []string{utcDayName(start)}
		if d := utcDayName(end); d != days[0] {
			days = append(days, d)
		}
		return "daytime-parking", "sched-park-daytime",
			[]hibernatorv1alpha1.OffHourWindow{{
				Start: start.Format("15:04"), End: end.Format("15:04"), DaysOfWeek: days,
			}},
			"weekday daytime run: overnight window would be Active, using relative hold window"
	}
}

// resolveScheduleWindow returns the hibernation window for the schedule-cycle
// test. The window is always relative to now: the hibernate edge lands ~3
// min out (room to create all four plans before it fires, otherwise the
// cycle is unobservable and every subsequent poll would fail with a
// misleading timeout) and the wakeup edge follows a fixed 5 min later. The
// fixed gap is deliberate: the noop executor finishes in seconds, so 5 min
// guarantees hibernate has fully settled into Hibernated before the wakeup
// edge fires, with no race between the two transitions.
//
// Total wall-clock is bounded: ~3 min lead + 5 min hibernated + a few
// minutes of Job/phase asserts, comfortably inside the 30 min job timeout
// no matter when cron actually fires. Legacy KIND_SCHEDULE_START/END
// anchors are deliberately ignored (logged when set so stale workflow env
// is visible): GitHub `schedule` triggers are best-effort and never
// guaranteed to fire at an exact minute, so absolute anchors made the
// nightly flaky by design.
func resolveScheduleWindow(t *testing.T) suite.ScheduleWindow {
	t.Helper()
	if startEnv, endEnv := os.Getenv("KIND_SCHEDULE_START"), os.Getenv("KIND_SCHEDULE_END"); startEnv != "" || endEnv != "" {
		t.Logf("ignoring legacy KIND_SCHEDULE_START/END anchors (%q/%q): window is always relative to now", startEnv, endEnv)
	}
	const lead = 3 * time.Minute
	const length = 5 * time.Minute
	window := suite.HibernationWindowAt(time.Now(), lead, length)
	t.Logf("relative schedule window %s-%s days=%v (hibernate %s, wakeup %s, length %s)",
		window.Start, window.End, window.Days,
		window.StartAt.Format(time.RFC3339), window.EndAt.Format(time.RFC3339), length)
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
