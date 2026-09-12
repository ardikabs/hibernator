//go:build kind

package kind

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/ardikabs/hibernator/test/kind/suite"
)

func TestNewRunConfigGeneratesUniqueOwnedResources(t *testing.T) {
	a := suite.NewRunConfig(101)
	b := suite.NewRunConfig(102)

	require.NotEqual(t, a.ClusterName, b.ClusterName)
	require.True(t, strings.HasPrefix(a.ClusterName, "hibernator-"))
	require.True(t, strings.HasPrefix(b.ClusterName, "hibernator-"))
	require.True(t, strings.HasPrefix(a.Namespace, "hibernator-e2e-"))
	require.Contains(t, a.ControllerImage, a.RunID)
	require.Contains(t, a.RunnerImage, a.RunID)
}

func TestHibernationWindowRoundsStartUp(t *testing.T) {
	now := time.Date(2026, 9, 7, 23, 58, 40, 0, time.UTC)
	window := suite.HibernationWindowAt(now, 3*time.Minute, 15*time.Minute)

	require.Equal(t, "00:02", window.Start)
	require.Equal(t, "00:17", window.End)
	require.Equal(t, []string{"TUE"}, window.Days)
	require.Equal(t, time.Date(2026, 9, 8, 0, 2, 0, 0, time.UTC), window.StartAt)
	require.Equal(t, time.Date(2026, 9, 8, 0, 17, 0, 0, time.UTC), window.EndAt)
}

func TestFixedScheduleWindowSameDay(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) // Monday
	window, err := suite.FixedScheduleWindow(now, "14:00", "14:10")

	require.NoError(t, err)
	require.Equal(t, "14:00", window.Start)
	require.Equal(t, "14:10", window.End)
	require.Equal(t, []string{"MON"}, window.Days)
	require.Equal(t, time.Date(2026, 9, 7, 14, 0, 0, 0, time.UTC), window.StartAt)
	require.Equal(t, time.Date(2026, 9, 7, 14, 10, 0, 0, time.UTC), window.EndAt)
}

func TestFixedScheduleWindowOvernightEmitsBothDays(t *testing.T) {
	now := time.Date(2026, 9, 7, 23, 50, 0, 0, time.UTC) // Monday
	window, err := suite.FixedScheduleWindow(now, "23:55", "00:05")

	require.NoError(t, err)
	require.Equal(t, "23:55", window.Start)
	require.Equal(t, "00:05", window.End)
	require.Equal(t, []string{"MON", "TUE"}, window.Days)
	require.Equal(t, time.Date(2026, 9, 7, 23, 55, 0, 0, time.UTC), window.StartAt)
	require.Equal(t, time.Date(2026, 9, 8, 0, 5, 0, 0, time.UTC), window.EndAt)
}

func TestFixedScheduleWindowRejectsBadAnchors(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)

	for _, tc := range []struct{ start, end string }{
		{"10:00", "10:00"}, // zero-length window
		{"25:00", "10:00"}, // hour out of range
		{"10:00", "10:60"}, // minute out of range
		{"ab:cd", "10:00"}, // not numeric
		{"10:00:00", "11:00"},
		{"", "11:00"},
	} {
		_, err := suite.FixedScheduleWindow(now, tc.start, tc.end)
		require.Error(t, err, "start=%q end=%q must be rejected", tc.start, tc.end)
	}
}

func TestJobBelongsToCycleAcceptsControllerMarkedTerminalJob(t *testing.T) {
	uid := types.UID("plan-uid")
	job := batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Labels: map[string]string{
			wellknown.LabelCycleID:        "cycle-1",
			wellknown.LabelStaleRunnerJob: "true",
		},
		OwnerReferences: []metav1.OwnerReference{{UID: uid}},
	}}

	require.True(t, suite.JobBelongsToCycle(job, uid, "cycle-1"))
	require.False(t, suite.JobBelongsToCycle(job, uid, "cycle-2"))
	require.False(t, suite.JobBelongsToCycle(job, types.UID("other-plan"), "cycle-1"))
}
