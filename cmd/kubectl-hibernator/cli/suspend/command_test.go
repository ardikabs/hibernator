/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package suspend

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

func testRoot() *common.RootOptions {
	return &common.RootOptions{Namespace: "test-ns"}
}

func testCtx() (context.Context, *bytes.Buffer) {
	var buf bytes.Buffer
	ctx := output.WithFormatter(context.Background(), output.NewFormatter(&buf, &buf))
	return output.WithWriter(ctx, &buf), &buf
}

func useFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(objs...).Build()
	prev := common.ClientFactory
	common.ClientFactory = func(*common.RootOptions) (client.Client, error) { return c, nil }
	t.Cleanup(func() { common.ClientFactory = prev })
	return c
}

func seedPlan(name string, phase hibernatorv1alpha1.PlanPhase) *hibernatorv1alpha1.HibernatePlan {
	return &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns"},
		Status:     hibernatorv1alpha1.HibernatePlanStatus{Phase: phase},
	}
}

func getPlan(t *testing.T, ctx context.Context, c client.Client, name string) *hibernatorv1alpha1.HibernatePlan {
	t.Helper()
	var got hibernatorv1alpha1.HibernatePlan
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: name}, &got))
	return &got
}

func TestRunSuspendValidation(t *testing.T) {
	ctx, _ := testCtx()
	opts := &suspendOptions{root: testRoot(), seconds: 10, until: "in 1 hour"}

	require.ErrorContains(t, runSuspend(ctx, opts, "p"), "only one of --seconds or --until")
	opts = &suspendOptions{root: testRoot(), seconds: -5}
	require.ErrorContains(t, runSuspend(ctx, opts, "p"), "--seconds must be positive")
	opts = &suspendOptions{root: testRoot(), until: "not a time at all xyz"}
	require.ErrorContains(t, runSuspend(ctx, opts, "p"), "invalid --until value")
}

func TestRunSuspendDryRunChangesNothing(t *testing.T) {
	c := useFakeClient(t, seedPlan("p", hibernatorv1alpha1.PhaseActive))
	ctx, buf := testCtx()

	opts := &suspendOptions{root: testRoot(), seconds: 3600, reason: "test", dryRun: true}
	require.NoError(t, runSuspend(ctx, opts, "p"))
	require.Contains(t, buf.String(), "DRY-RUN")

	got := getPlan(t, ctx, c, "p")
	require.False(t, got.Spec.Suspend)
	require.Empty(t, got.Annotations)
}

func TestRunSuspendAppliesAnnotations(t *testing.T) {
	c := useFakeClient(t, seedPlan("p", hibernatorv1alpha1.PhaseActive))
	ctx, _ := testCtx()

	opts := &suspendOptions{root: testRoot(), seconds: 3600, reason: "deploy"}
	require.NoError(t, runSuspend(ctx, opts, "p"))

	got := getPlan(t, ctx, c, "p")
	require.True(t, got.Spec.Suspend)
	require.Equal(t, "deploy", got.Annotations[wellknown.AnnotationSuspendReason])
	until, err := time.Parse(time.RFC3339, got.Annotations[wellknown.AnnotationSuspendUntil])
	require.NoError(t, err)
	require.WithinDuration(t, time.Now().Add(time.Hour), until, 5*time.Minute)
}

func TestRunSuspendIndefiniteOmitsDeadline(t *testing.T) {
	c := useFakeClient(t, seedPlan("p", hibernatorv1alpha1.PhaseActive))
	ctx, buf := testCtx()

	opts := &suspendOptions{root: testRoot(), reason: "hold"}
	require.NoError(t, runSuspend(ctx, opts, "p"))

	got := getPlan(t, ctx, c, "p")
	require.True(t, got.Spec.Suspend)
	require.NotContains(t, got.Annotations, wellknown.AnnotationSuspendUntil)
	require.Contains(t, buf.String(), "Indefinitely")
}

func TestRunSuspendMissingPlan(t *testing.T) {
	useFakeClient(t)
	ctx, _ := testCtx()
	require.ErrorContains(t, runSuspend(ctx, &suspendOptions{root: testRoot()}, "missing"), "failed to get HibernatePlan")
}
