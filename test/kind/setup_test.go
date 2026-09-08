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
)

func TestNewRunConfigGeneratesUniqueOwnedResources(t *testing.T) {
	a := newRunConfig(101)
	b := newRunConfig(102)

	require.NotEqual(t, a.clusterName, b.clusterName)
	require.True(t, strings.HasPrefix(a.clusterName, "hibernator-"))
	require.True(t, strings.HasPrefix(b.clusterName, "hibernator-"))
	require.True(t, strings.HasPrefix(a.namespace, "hibernator-e2e-"))
	require.Contains(t, a.controllerImage, a.runID)
	require.Contains(t, a.runnerTestImage, a.runID)
}

func TestHibernationWindowRoundsStartUp(t *testing.T) {
	now := time.Date(2026, 9, 7, 23, 58, 40, 0, time.UTC)
	window := hibernationWindowAt(now, 3*time.Minute, 15*time.Minute)

	require.Equal(t, "00:02", window.start)
	require.Equal(t, "00:17", window.end)
	require.Equal(t, []string{"TUE"}, window.days)
	require.Equal(t, time.Date(2026, 9, 8, 0, 2, 0, 0, time.UTC), window.startAt)
	require.Equal(t, time.Date(2026, 9, 8, 0, 17, 0, 0, time.UTC), window.endAt)
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

	require.True(t, jobBelongsToCycle(job, uid, "cycle-1"))
	require.False(t, jobBelongsToCycle(job, uid, "cycle-2"))
	require.False(t, jobBelongsToCycle(job, types.UID("other-plan"), "cycle-1"))
}
