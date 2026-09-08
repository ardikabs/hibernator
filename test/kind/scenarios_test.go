//go:build kind

package kind

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/require"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/ardikabs/hibernator/test/kind/suite"
)

// TestScenarios exercises every website/docs/scenarios setup against the
// real controller and real runner pods, with all targets mocked by the noop
// executor. Each scenario's plan and exceptions live in
// suite/testdata/scenarios/<name>.yaml and are applied verbatim (namespace,
// schedule default, and validity anchored by the loader); the Go test only
// drives the flow and asserts expectations through the stable step interface
// in suite/steps.go. Cloud behavior is intentionally out of scope here —
// it is proven in test/awsenv (simulation) and test/e2e. Override
// annotations drive every cycle so no test waits on wall-clock windows;
// schedule timing itself stays covered by TestNoopScheduleCycle.
func TestScenarios(t *testing.T) {
	ctx := context.Background()
	c := suite.K8sClient
	require.NotNil(t, c, "TestMain must provision the cluster client")

	ns := suite.CurrentRun.Namespace + "-scn"
	suite.EnsureNamespace(suite.MainTB{}, c, ns)
	t.Cleanup(func() {
		_ = c.Delete(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	})
	suite.EnsureRunnerRBAC(suite.MainTB{}, c, ns)
	connector := suite.ScnConnector(t, c, ns)
	_ = connector

	t.Run("nightly-full-shutdown", func(t *testing.T) {
		b := suite.LoadScenario(t, ns, "nightly-full-shutdown")
		key := b.ApplyPlan(t, c)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseActive, 2*time.Minute)
		suite.RunHibernateStep(t, c, suite.K8sSets, ns, key, suite.HibernateStepExpectation{
			States: map[string]hibernatorv1alpha1.ExecutionState{
				"nodegroups": hibernatorv1alpha1.StateCompleted,
				"database":   hibernatorv1alpha1.StateCompleted,
				"bastion":    hibernatorv1alpha1.StateCompleted,
			},
			Jobs: []string{"nodegroups", "database", "bastion"},
			Markers: map[string]string{
				"nodegroups": "nightly-nodegroups",
				"database":   "nightly-database",
				"bastion":    "nightly-bastion",
			},
		})
		suite.RunWakeupStep(t, c, suite.K8sSets, ns, key, suite.WakeupStepExpectation{
			Jobs:    []string{"nodegroups", "database", "bastion"},
			Consume: []string{"nodegroups", "database", "bastion"},
		})
	})

	t.Run("weekend-hibernation", func(t *testing.T) {
		// Exact Option A shape from the doc: weekdays listed, so Friday
		// 20:00 hibernates and Monday 06:00 wakes with no weekend cycling.
		// (Days [FRI..MON] would add a Monday 20:00 hibernation with no
		// Tuesday wakeup by the documented per-day semantics.)
		b := suite.LoadScenario(t, ns, "weekend-hibernation")
		key := b.ApplyPlan(t, c)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseActive, 2*time.Minute)
		suite.RunHibernateStep(t, c, suite.K8sSets, ns, key, suite.HibernateStepExpectation{
			States: map[string]hibernatorv1alpha1.ExecutionState{
				"nodegroups": hibernatorv1alpha1.StateCompleted,
				"database":   hibernatorv1alpha1.StateCompleted,
			},
			Jobs: []string{"nodegroups", "database"},
			Markers: map[string]string{
				"nodegroups": "weekend-nodegroups",
				"database":   "weekend-database",
			},
		})
		suite.RunWakeupStep(t, c, suite.K8sSets, ns, key, suite.WakeupStepExpectation{
			Jobs:    []string{"nodegroups", "database"},
			Consume: []string{"nodegroups", "database"},
		})
	})

	t.Run("partial-hibernation", func(t *testing.T) {
		// Plumbing only: selector granularity is executor-side logic proven in
		// test/awsenv and unit tests. Here we prove a multi-target plan with
		// mixed criticality executes and restores end to end.
		b := suite.LoadScenario(t, ns, "partial-hibernation-critical-services")
		key := b.ApplyPlan(t, c)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseActive, 2*time.Minute)
		suite.RunHibernateStep(t, c, suite.K8sSets, ns, key, suite.HibernateStepExpectation{
			States: map[string]hibernatorv1alpha1.ExecutionState{
				"critical-api":  hibernatorv1alpha1.StateCompleted,
				"batch-workers": hibernatorv1alpha1.StateCompleted,
				"dev-sandbox":   hibernatorv1alpha1.StateCompleted,
			},
			Jobs: []string{"critical-api", "batch-workers", "dev-sandbox"},
			Markers: map[string]string{
				"critical-api":  "partial-critical-api",
				"batch-workers": "partial-batch-workers",
				"dev-sandbox":   "partial-dev-sandbox",
			},
		})
		suite.RunWakeupStep(t, c, suite.K8sSets, ns, key, suite.WakeupStepExpectation{
			Jobs:    []string{"critical-api", "batch-workers", "dev-sandbox"},
			Consume: []string{"critical-api", "batch-workers", "dev-sandbox"},
		})
	})

	t.Run("partial-failure-besteffort", func(t *testing.T) {
		b := suite.LoadScenario(t, ns, "partial-failure-besteffort")
		key := b.ApplyPlan(t, c)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseActive, 2*time.Minute)
		suite.PatchOverride(t, c, key, wellknown.OverridePhaseTargetHibernate)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseHibernating, 3*time.Minute)
		suite.PollJobComplete(t, c, suite.K8sSets, key, hibernatorv1alpha1.OperationHibernate, "ns-shop", 3*time.Minute)
		suite.PollJobComplete(t, c, suite.K8sSets, key, hibernatorv1alpha1.OperationHibernate, "ns-reports", 3*time.Minute)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, suite.HibernatedPhase(), 3*time.Minute)
		ledger := suite.ScnLedger(t, c, key)
		require.Equal(t, hibernatorv1alpha1.StateCompleted, ledger["ns-shop"].State)
		require.Equal(t, hibernatorv1alpha1.StateFailed, ledger["ns-pricing"].State)
		require.Equal(t, hibernatorv1alpha1.StateCompleted, ledger["ns-reports"].State)
		require.Contains(t, ledger["ns-pricing"].Message, "simulated stuck PDB")
		suite.RunWakeupStep(t, c, suite.K8sSets, ns, key, suite.WakeupStepExpectation{
			Jobs:    []string{"ns-shop", "ns-pricing", "ns-reports"},
			Consume: []string{"ns-shop", "ns-reports"},
		})
	})

	t.Run("suspend-scheduled-shutdown", func(t *testing.T) {
		b := suite.LoadScenario(t, ns, "suspend-scheduled-shutdown")
		key := b.ApplyPlan(t, c)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseActive, 2*time.Minute)
		// Exceptions only transition while their plan exists (the lifecycle
		// processor works off plan contexts): the carve-out applies only
		// after the plan is up, then the live window is patched in.
		b.ApplyException(t, c, suite.K8sSets, "suspend-now")
		now := time.Now().UTC()
		suite.ScnPatchSchedule(t, c, key, hibernatorv1alpha1.Schedule{
			Timezone: "UTC",
			OffHours: []hibernatorv1alpha1.OffHourWindow{
				{Start: now.Add(-10 * time.Minute).Format("15:04"), End: now.Add(50 * time.Minute).Format("15:04"), DaysOfWeek: []string{suite.DayName(now)}},
			},
		})
		require.Never(t, func() bool {
			plan := &hibernatorv1alpha1.HibernatePlan{}
			if err := c.Get(ctx, key, plan); err != nil {
				return false
			}
			return plan.Status.Phase == hibernatorv1alpha1.PhaseHibernating ||
				plan.Status.Phase == hibernatorv1alpha1.PhaseHibernated
		}, 45*time.Second, 5*time.Second, "suspended plan must stay Active")
		// Lift the carve-out: the still-active window must now hibernate.
		suite.ScnDeleteException(t, c, ns, "suspend-now")
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseHibernating, 3*time.Minute)
		suite.PollJobComplete(t, c, suite.K8sSets, key, hibernatorv1alpha1.OperationHibernate, "nodegroups", 3*time.Minute)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, suite.HibernatedPhase(), 3*time.Minute)
		suite.ScnAssertRestoreMarkers(t, c, ns, "suspend-plan", map[string]string{"nodegroups": "suspend-nodegroups"})
	})

	t.Run("rds-selective-hibernation", func(t *testing.T) {
		// Plumbing only: tagSelector expression matching is RDS-executor logic
		// proven in test/awsenv and unit tests. Here the selective shape
		// executes and restores end to end.
		b := suite.LoadScenario(t, ns, "rds-selective-hibernation")
		key := b.ApplyPlan(t, c)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseActive, 2*time.Minute)
		suite.RunHibernateStep(t, c, suite.K8sSets, ns, key, suite.HibernateStepExpectation{
			States: map[string]hibernatorv1alpha1.ExecutionState{
				"selective-databases": hibernatorv1alpha1.StateCompleted,
			},
			Jobs:    []string{"selective-databases"},
			Markers: map[string]string{"selective-databases": "selective-databases"},
		})
		suite.RunWakeupStep(t, c, suite.K8sSets, ns, key, suite.WakeupStepExpectation{
			Jobs:    []string{"selective-databases"},
			Consume: []string{"selective-databases"},
		})
	})

	t.Run("dependency-ordered-shutdown", func(t *testing.T) {
		b := suite.LoadScenario(t, ns, "dependency-ordered-shutdown")
		key := b.ApplyPlan(t, c)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseActive, 2*time.Minute)
		suite.PatchOverride(t, c, key, wellknown.OverridePhaseTargetHibernate)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseHibernating, 3*time.Minute)
		for _, target := range []string{"app-workloads", "eks-nodegroups", "worker-instances", "database"} {
			suite.PollJobComplete(t, c, suite.K8sSets, key, hibernatorv1alpha1.OperationHibernate, target, 3*time.Minute)
		}
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, suite.HibernatedPhase(), 3*time.Minute)
		ledger := suite.ScnLedger(t, c, key)
		suite.ScnAssertStartedBefore(t, ledger, "app-workloads", "eks-nodegroups")
		suite.ScnAssertStartedBefore(t, ledger, "app-workloads", "worker-instances")
		suite.ScnAssertStartedBefore(t, ledger, "eks-nodegroups", "database")
		suite.ScnAssertStartedBefore(t, ledger, "worker-instances", "database")
		suite.PatchOverride(t, c, key, wellknown.OverridePhaseTargetWakeup)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseWakingUp, 3*time.Minute)
		for _, target := range []string{"app-workloads", "eks-nodegroups", "worker-instances", "database"} {
			suite.PollJobComplete(t, c, suite.K8sSets, key, hibernatorv1alpha1.OperationWakeUp, target, 3*time.Minute)
		}
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseActive, 3*time.Minute)
		ledger = suite.ScnLedger(t, c, key)
		suite.ScnAssertStartedBefore(t, ledger, "database", "eks-nodegroups")
		suite.ScnAssertStartedBefore(t, ledger, "database", "worker-instances")
		suite.ScnAssertStartedBefore(t, ledger, "eks-nodegroups", "app-workloads")
		suite.ScnAssertStartedBefore(t, ledger, "worker-instances", "app-workloads")
	})

	t.Run("staged-hibernation-criticality", func(t *testing.T) {
		b := suite.LoadScenario(t, ns, "staged-hibernation-criticality")
		key := b.ApplyPlan(t, c)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseActive, 2*time.Minute)
		suite.PatchOverride(t, c, key, wellknown.OverridePhaseTargetHibernate)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseHibernating, 3*time.Minute)
		for _, target := range []string{"dev-a", "dev-b", "batch", "shared"} {
			suite.PollJobComplete(t, c, suite.K8sSets, key, hibernatorv1alpha1.OperationHibernate, target, 3*time.Minute)
		}
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, suite.HibernatedPhase(), 3*time.Minute)
		ledger := suite.ScnLedger(t, c, key)
		for _, target := range []string{"dev-a", "dev-b", "batch", "shared"} {
			require.Equal(t, hibernatorv1alpha1.StateCompleted, ledger[target].State)
		}
		suite.ScnAssertStartedBefore(t, ledger, "dev-a", "batch")
		suite.ScnAssertStartedBefore(t, ledger, "dev-b", "batch")
		suite.ScnAssertStartedBefore(t, ledger, "batch", "shared")
		suite.RunWakeupStep(t, c, suite.K8sSets, ns, key, suite.WakeupStepExpectation{
			Jobs:    []string{"dev-a", "dev-b", "batch", "shared"},
			Consume: []string{"dev-a", "dev-b", "batch", "shared"},
		})
	})

	t.Run("event-mode-overrides", func(t *testing.T) {
		b := suite.LoadScenario(t, ns, "event-mode-overrides")
		key := b.ApplyPlan(t, c)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseActive, 2*time.Minute)
		b.ApplyException(t, c, suite.K8sSets, "event-mode")
		suite.PatchOverride(t, c, key, wellknown.OverridePhaseTargetHibernate)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseHibernating, 3*time.Minute)
		suite.PollJobComplete(t, c, suite.K8sSets, key, hibernatorv1alpha1.OperationHibernate, "frontend", 3*time.Minute)
		suite.PollJobComplete(t, c, suite.K8sSets, key, hibernatorv1alpha1.OperationHibernate, "api", 3*time.Minute)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, suite.HibernatedPhase(), 3*time.Minute)
		ledger := suite.ScnLedger(t, c, key)
		require.Equal(t, hibernatorv1alpha1.StateCompleted, ledger["frontend"].State)
		require.Equal(t, hibernatorv1alpha1.StateCompleted, ledger["api"].State)
		// Disabled targets are seeded skipped at transition and get no Job.
		require.Equal(t, hibernatorv1alpha1.StateSkipped, ledger["batch"].State)
		suite.ScnAssertNoJobs(t, c, ns, "event-plan", hibernatorv1alpha1.OperationHibernate, "batch")
		suite.ScnAssertRestoreMarkers(t, c, ns, "event-plan", map[string]string{
			"frontend": "eventmode-frontend",
			"api":      "eventmode-api-override",
		})
		suite.RunWakeupStep(t, c, suite.K8sSets, ns, key, suite.WakeupStepExpectation{
			Jobs:    []string{"frontend", "api"},
			Consume: []string{"frontend", "api"},
		})
		suite.ScnDeleteException(t, c, ns, "event-mode")
	})

	t.Run("weekend-subset-wakeup", func(t *testing.T) {
		b := suite.LoadScenario(t, ns, "weekend-subset-wakeup")
		key := b.ApplyPlan(t, c)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseActive, 2*time.Minute)
		suite.PatchOverride(t, c, key, wellknown.OverridePhaseTargetHibernate)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseHibernating, 3*time.Minute)
		for _, target := range []string{"db-1", "db-2", "db-3"} {
			suite.PollJobComplete(t, c, suite.K8sSets, key, hibernatorv1alpha1.OperationHibernate, target, 3*time.Minute)
		}
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, suite.HibernatedPhase(), 3*time.Minute)
		// Carve-out suspends db-2/db-3: only db-1 wakes until the exception lifts.
		b.ApplyException(t, c, suite.K8sSets, "subset-suspend")
		suite.PatchOverride(t, c, key, wellknown.OverridePhaseTargetWakeup)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseWakingUp, 3*time.Minute)
		suite.PollJobComplete(t, c, suite.K8sSets, key, hibernatorv1alpha1.OperationWakeUp, "db-1", 3*time.Minute)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseActive, 3*time.Minute)
		suite.ScnAssertNoJobs(t, c, ns, "subset", hibernatorv1alpha1.OperationWakeUp, "db-2")
		suite.ScnAssertNoJobs(t, c, ns, "subset", hibernatorv1alpha1.OperationWakeUp, "db-3")
		ledger := suite.ScnLedger(t, c, key)
		require.Equal(t, hibernatorv1alpha1.StateCompleted, ledger["db-1"].State)
		require.Equal(t, hibernatorv1alpha1.StateSkipped, ledger["db-2"].State)
		require.Equal(t, hibernatorv1alpha1.StateSkipped, ledger["db-3"].State)
		suite.ScnAssertRestoreMarkers(t, c, ns, "subset", map[string]string{"db-1": "subset-db-1"})
		suite.ScnDeleteException(t, c, ns, "subset-suspend")
	})

	t.Run("holiday-schedule-replacement", func(t *testing.T) {
		b := suite.LoadScenario(t, ns, "holiday-schedule-replacement")
		key := b.ApplyPlan(t, c)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseActive, 2*time.Minute)
		b.ApplyException(t, c, suite.K8sSets, "holiday")
		// The replacement strategy executes the full cycle; markers prove both
		// targets ran under the replaced execution.
		suite.PatchOverride(t, c, key, wellknown.OverridePhaseTargetHibernate)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseHibernating, 3*time.Minute)
		suite.PollJobComplete(t, c, suite.K8sSets, key, hibernatorv1alpha1.OperationHibernate, "workloads", 3*time.Minute)
		suite.PollJobComplete(t, c, suite.K8sSets, key, hibernatorv1alpha1.OperationHibernate, "database", 3*time.Minute)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, suite.HibernatedPhase(), 3*time.Minute)
		suite.ScnAssertRestoreMarkers(t, c, ns, "holiday-plan", map[string]string{
			"workloads": "holiday-workloads",
			"database":  "holiday-database",
		})
		suite.PatchOverride(t, c, key, wellknown.OverridePhaseTargetWakeup)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseWakingUp, 3*time.Minute)
		suite.PollJobComplete(t, c, suite.K8sSets, key, hibernatorv1alpha1.OperationWakeUp, "workloads", 3*time.Minute)
		suite.PollJobComplete(t, c, suite.K8sSets, key, hibernatorv1alpha1.OperationWakeUp, "database", 3*time.Minute)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseActive, 3*time.Minute)
		suite.ScnAssertRestoreConsumed(t, c, ns, "holiday-plan", []string{"workloads", "database"})
		suite.ScnDeleteException(t, c, ns, "holiday")
	})

	t.Run("dry-run-rehearsal", func(t *testing.T) {
		b := suite.LoadScenario(t, ns, "dry-run-rehearsal")
		key := b.ApplyPlan(t, c)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseActive, 2*time.Minute)
		suite.PatchOverride(t, c, key, wellknown.OverridePhaseTargetHibernate)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseHibernating, 3*time.Minute)
		suite.PollJobComplete(t, c, suite.K8sSets, key, hibernatorv1alpha1.OperationHibernate, "apps", 3*time.Minute)
		suite.PollLedgerState(t, c, suite.K8sSets, key, "nodes", hibernatorv1alpha1.StateFailed, 6*time.Minute)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, suite.HibernatedPhase(), 3*time.Minute)
		ledger := suite.ScnLedger(t, c, key)
		require.Equal(t, hibernatorv1alpha1.StateCompleted, ledger["apps"].State)
		require.Equal(t, hibernatorv1alpha1.StateFailed, ledger["nodes"].State)
		require.Contains(t, ledger["nodes"].Message, "simulated node drain timeout")
		require.Equal(t, hibernatorv1alpha1.StateAborted, ledger["database"].State)
		suite.ScnAssertRestoreMarkers(t, c, ns, "rehearsal", map[string]string{"apps": "dryrun-apps"})
		// The aborted database has no restore data to consume, so consumption
		// is asserted for apps only while both wakeup Jobs are still awaited.
		suite.PatchOverride(t, c, key, wellknown.OverridePhaseTargetWakeup)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseWakingUp, 3*time.Minute)
		suite.PollJobComplete(t, c, suite.K8sSets, key, hibernatorv1alpha1.OperationWakeUp, "apps", 3*time.Minute)
		suite.PollJobComplete(t, c, suite.K8sSets, key, hibernatorv1alpha1.OperationWakeUp, "database", 3*time.Minute)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseActive, 3*time.Minute)
		suite.ScnAssertRestoreMarkers(t, c, ns, "rehearsal", map[string]string{"apps": "dryrun-apps"})
		suite.ScnAssertRestoreConsumed(t, c, ns, "rehearsal", []string{"apps"})
	})

	t.Run("production-grade-setup", func(t *testing.T) {
		// Plumbing only: cross-account IRSA and Slack sinks have no meaning in
		// kind. What transfers is the production shape — DAG + Strict +
		// referenced replace exception — executing cleanly end to end.
		b := suite.LoadScenario(t, ns, "production-grade-setup")
		key := b.ApplyPlan(t, c)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseActive, 2*time.Minute)
		b.ApplyException(t, c, suite.K8sSets, "prod-change-freeze")
		suite.PatchOverride(t, c, key, wellknown.OverridePhaseTargetHibernate)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseHibernating, 3*time.Minute)
		suite.PollJobComplete(t, c, suite.K8sSets, key, hibernatorv1alpha1.OperationHibernate, "workloads", 3*time.Minute)
		suite.PollJobComplete(t, c, suite.K8sSets, key, hibernatorv1alpha1.OperationHibernate, "nodegroups", 3*time.Minute)
		suite.PollJobComplete(t, c, suite.K8sSets, key, hibernatorv1alpha1.OperationHibernate, "database", 3*time.Minute)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, suite.HibernatedPhase(), 3*time.Minute)
		ledger := suite.ScnLedger(t, c, key)
		suite.ScnAssertStartedBefore(t, ledger, "workloads", "nodegroups")
		suite.ScnAssertStartedBefore(t, ledger, "nodegroups", "database")
		suite.ScnAssertRestoreMarkers(t, c, ns, "prod-grade", map[string]string{
			"workloads":  "prodgrade-workloads",
			"nodegroups": "prodgrade-nodegroups",
			"database":   "prodgrade-database",
		})
		suite.PatchOverride(t, c, key, wellknown.OverridePhaseTargetWakeup)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseWakingUp, 3*time.Minute)
		suite.PollJobComplete(t, c, suite.K8sSets, key, hibernatorv1alpha1.OperationWakeUp, "workloads", 3*time.Minute)
		suite.PollJobComplete(t, c, suite.K8sSets, key, hibernatorv1alpha1.OperationWakeUp, "nodegroups", 3*time.Minute)
		suite.PollJobComplete(t, c, suite.K8sSets, key, hibernatorv1alpha1.OperationWakeUp, "database", 3*time.Minute)
		suite.PollPhaseAtLeast(t, c, suite.K8sSets, key, hibernatorv1alpha1.PhaseActive, 3*time.Minute)
		suite.ScnAssertRestoreConsumed(t, c, ns, "prod-grade", []string{"workloads", "nodegroups", "database"})
		suite.ScnDeleteException(t, c, ns, "prod-change-freeze")
	})
}

// TestScenarioFilesAreValid parses every scenario input without a cluster and
// enforces the file conventions so a malformed fixture fails fast, long
// before an expensive kind run.
func TestScenarioFilesAreValid(t *testing.T) {
	entries, err := os.ReadDir(suite.ScenarioDir(t))
	require.NoError(t, err)
	require.Len(t, entries, 13, "one input file per docs scenario")

	seenPlans := map[string]bool{}
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".yaml")
		b := suite.LoadScenario(t, "test-ns", name)

		require.False(t, seenPlans[b.Plan.Name], "duplicate plan name %s", b.Plan.Name)
		seenPlans[b.Plan.Name] = true
		require.Equal(t, "test-ns", b.Plan.Namespace)
		require.NotEmpty(t, b.Plan.Spec.Schedule.OffHours, "plan %s needs a schedule", b.Plan.Name)
		require.NotEmpty(t, b.Plan.Spec.Targets, "plan %s needs targets", b.Plan.Name)

		targets := map[string]bool{}
		markers := map[string]bool{}
		for _, target := range b.Plan.Spec.Targets {
			require.NotEmpty(t, target.Name)
			require.False(t, targets[target.Name], "duplicate target %s", target.Name)
			targets[target.Name] = true
			require.Equal(t, "noop", target.Type)
			require.Equal(t, "scn-aws", target.ConnectorRef.Name)
			require.NotNil(t, target.Parameters, "target %s needs parameters", target.Name)
			var params executorparams.NoOpParameters
			require.NoError(t, json.Unmarshal(target.Parameters.Raw, &params), "target %s parameters must parse", target.Name)
			require.NotEmpty(t, params.Marker, "target %s needs a deterministic marker", target.Name)
			require.LessOrEqual(t, len(params.Marker), 64, "target %s marker exceeds noop limit", target.Name)
			require.False(t, markers[params.Marker], "duplicate marker %s", params.Marker)
			markers[params.Marker] = true
		}

		for _, exc := range b.Exceptions {
			require.Equal(t, "test-ns", exc.Namespace)
			require.Equal(t, b.Plan.Name, exc.Spec.PlanRef.Name)
			require.NotEmpty(t, exc.Spec.Windows)
			for _, override := range exc.Spec.TargetOverrides {
				require.True(t, targets[override.TargetName], "override references unknown target %s", override.TargetName)
				if override.Parameters != nil {
					var params executorparams.NoOpParameters
					require.NoError(t, json.Unmarshal(override.Parameters.Raw, &params))
					require.LessOrEqual(t, len(params.Marker), 64, "override marker exceeds noop limit")
				}
			}
		}

		for target, deps := range suite.ScenarioDependencies(b) {
			require.True(t, targets[target], "dependency references unknown target %s", target)
			for _, dep := range deps {
				require.True(t, targets[dep], "dependency references unknown target %s", dep)
			}
		}
		for target, stage := range suite.ScenarioStageMembership(b) {
			require.True(t, targets[target], "stage %s references unknown target %s", stage, target)
			_ = stage
		}
	}
}
