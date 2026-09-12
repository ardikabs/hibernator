/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package resume

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

func TestRunResumeClearsSuspendAndBothAnnotations(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "p",
			Namespace: "test-ns",
			Annotations: map[string]string{
				wellknown.AnnotationSuspendUntil:  "2026-01-01T00:00:00Z",
				wellknown.AnnotationSuspendReason: "deploy",
			},
		},
		Spec: hibernatorv1alpha1.HibernatePlanSpec{Suspend: true},
	}
	c := useFakeClient(t, plan)
	ctx, _ := testCtx()

	require.NoError(t, runResume(ctx, &resumeOptions{root: &common.RootOptions{Namespace: "test-ns"}}, "p"))

	var got hibernatorv1alpha1.HibernatePlan
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "p"}, &got))
	require.False(t, got.Spec.Suspend)
	// Regression: resume must remove the reason alongside the deadline.
	require.NotContains(t, got.Annotations, wellknown.AnnotationSuspendUntil)
	require.NotContains(t, got.Annotations, wellknown.AnnotationSuspendReason)
}

func TestRunResumeNotSuspendedIsNoop(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "test-ns"},
	}
	useFakeClient(t, plan)
	ctx, buf := testCtx()

	require.NoError(t, runResume(ctx, &resumeOptions{root: &common.RootOptions{Namespace: "test-ns"}}, "p"))
	require.Contains(t, buf.String(), "not suspended")
}

func TestRunResumeDryRunChangesNothing(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "p",
			Namespace: "test-ns",
			Annotations: map[string]string{
				wellknown.AnnotationSuspendUntil: "2030-01-01T00:00:00Z",
			},
		},
		Spec: hibernatorv1alpha1.HibernatePlanSpec{Suspend: true},
	}
	c := useFakeClient(t, plan)
	ctx, buf := testCtx()

	require.NoError(t, runResume(ctx, &resumeOptions{root: &common.RootOptions{Namespace: "test-ns"}, dryRun: true}, "p"))
	require.Contains(t, buf.String(), "DRY-RUN")

	var got hibernatorv1alpha1.HibernatePlan
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "p"}, &got))
	require.True(t, got.Spec.Suspend)
	require.Contains(t, got.Annotations, wellknown.AnnotationSuspendUntil)
}

func TestRunResumeMissingPlan(t *testing.T) {
	useFakeClient(t)
	ctx, _ := testCtx()
	require.ErrorContains(t, runResume(ctx, &resumeOptions{root: &common.RootOptions{Namespace: "test-ns"}}, "missing"), "failed to get HibernatePlan")
}
