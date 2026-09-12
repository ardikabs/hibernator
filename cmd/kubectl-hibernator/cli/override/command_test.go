/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package override

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

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

func seedActivePlan() *hibernatorv1alpha1.HibernatePlan {
	return &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "test-ns"},
		Status:     hibernatorv1alpha1.HibernatePlanStatus{Phase: hibernatorv1alpha1.PhaseActive},
	}
}

func TestRunOverrideValidation(t *testing.T) {
	ctx, _ := testCtx()
	root := &common.RootOptions{Namespace: "test-ns"}

	opts := &overrideOptions{root: root, disable: true, to: "hibernate"}
	require.ErrorContains(t, runOverride(ctx, opts, "p"), "mutually exclusive")

	opts = &overrideOptions{root: root, disable: true, seconds: 60}
	require.ErrorContains(t, runOverride(ctx, opts, "p"), "--disable takes no deadline flags")

	opts = &overrideOptions{root: root}
	require.ErrorContains(t, runOverride(ctx, opts, "p"), "--to is required")

	opts = &overrideOptions{root: root, to: "sideways"}
	require.ErrorContains(t, runOverride(ctx, opts, "p"), "invalid --to")

	opts = &overrideOptions{root: root, to: "hibernate", seconds: 10, until: "in 1 hour"}
	require.ErrorContains(t, runOverride(ctx, opts, "p"), "only one of --seconds or --until")
}

func TestRunOverrideDryRunChangesNothing(t *testing.T) {
	c := useFakeClient(t, seedActivePlan())
	ctx, buf := testCtx()

	opts := &overrideOptions{root: &common.RootOptions{Namespace: "test-ns"}, to: "hibernate", dryRun: true}
	require.NoError(t, runOverride(ctx, opts, "p"))
	require.Contains(t, buf.String(), "DRY-RUN")

	var got hibernatorv1alpha1.HibernatePlan
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "p"}, &got))
	require.Empty(t, got.Annotations)
}

func TestRunOverrideActivatesOnActivePlan(t *testing.T) {
	c := useFakeClient(t, seedActivePlan())
	ctx, _ := testCtx()

	opts := &overrideOptions{root: &common.RootOptions{Namespace: "test-ns"}, to: "hibernate", seconds: 600}
	require.NoError(t, runOverride(ctx, opts, "p"))

	var got hibernatorv1alpha1.HibernatePlan
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "p"}, &got))
	require.Equal(t, "true", got.Annotations[wellknown.AnnotationOverrideAction])
	require.Equal(t, "hibernate", got.Annotations[wellknown.AnnotationOverridePhaseTarget])
	require.NotEmpty(t, got.Annotations[wellknown.AnnotationOverrideUntil])
}

func TestRunOverrideRejectsUnstablePhase(t *testing.T) {
	plan := seedActivePlan()
	plan.Status.Phase = hibernatorv1alpha1.PhaseHibernating
	useFakeClient(t, plan)
	ctx, _ := testCtx()

	opts := &overrideOptions{root: &common.RootOptions{Namespace: "test-ns"}, to: "wakeup"}
	require.ErrorContains(t, runOverride(ctx, opts, "p"), "only applies to Active or Hibernated")
}

func TestRunOverrideEmptyPhaseMessage(t *testing.T) {
	plan := seedActivePlan()
	plan.Status.Phase = ""
	useFakeClient(t, plan)
	ctx, _ := testCtx()

	opts := &overrideOptions{root: &common.RootOptions{Namespace: "test-ns"}, to: "wakeup"}
	require.ErrorContains(t, runOverride(ctx, opts, "p"), "no recorded phase yet")
}

func TestRunOverrideDeactivates(t *testing.T) {
	plan := seedActivePlan()
	plan.Annotations = map[string]string{
		wellknown.AnnotationOverrideAction:      "true",
		wellknown.AnnotationOverridePhaseTarget: "hibernate",
		wellknown.AnnotationOverrideUntil:       "2030-01-01T00:00:00Z",
	}
	c := useFakeClient(t, plan)
	ctx, _ := testCtx()

	opts := &overrideOptions{root: &common.RootOptions{Namespace: "test-ns"}, disable: true}
	require.NoError(t, runOverride(ctx, opts, "p"))

	var got hibernatorv1alpha1.HibernatePlan
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "p"}, &got))
	require.NotContains(t, got.Annotations, wellknown.AnnotationOverrideAction)
	require.NotContains(t, got.Annotations, wellknown.AnnotationOverridePhaseTarget)
	require.NotContains(t, got.Annotations, wellknown.AnnotationOverrideUntil)
}
