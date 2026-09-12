/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package retry

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

func TestRunRetrySetsAnnotationOnErrorPlan(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "test-ns"},
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			Phase:      hibernatorv1alpha1.PhaseError,
			RetryCount: 2,
		},
	}
	c := useFakeClient(t, plan)
	ctx, buf := testCtx()

	require.NoError(t, runRetry(ctx, &retryOptions{root: &common.RootOptions{Namespace: "test-ns"}}, "p"))

	var got hibernatorv1alpha1.HibernatePlan
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "p"}, &got))
	require.Equal(t, "true", got.Annotations[wellknown.AnnotationRetryNow])
	require.Contains(t, buf.String(), "Previous retries: 2")
}

func TestRunRetryRejectsNonErrorPhase(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "test-ns"},
		Status:     hibernatorv1alpha1.HibernatePlanStatus{Phase: hibernatorv1alpha1.PhaseActive},
	}
	useFakeClient(t, plan)
	ctx, _ := testCtx()

	err := runRetry(ctx, &retryOptions{root: &common.RootOptions{Namespace: "test-ns"}}, "p")
	require.ErrorContains(t, err, "not Error")
	require.ErrorContains(t, err, "restart")
}

func TestRetryPhaseErrorHandlesEmptyPhase(t *testing.T) {
	require.Contains(t, retryPhaseError("p", "").Error(), "no recorded phase yet")
	require.Contains(t, retryPhaseError("p", hibernatorv1alpha1.PhaseActive).Error(), `"Active"`)
}

func TestRunRetryDryRunChangesNothing(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "test-ns"},
		Status:     hibernatorv1alpha1.HibernatePlanStatus{Phase: hibernatorv1alpha1.PhaseError},
	}
	c := useFakeClient(t, plan)
	ctx, buf := testCtx()

	require.NoError(t, runRetry(ctx, &retryOptions{root: &common.RootOptions{Namespace: "test-ns"}, dryRun: true}, "p"))
	require.Contains(t, buf.String(), "DRY-RUN")

	var got hibernatorv1alpha1.HibernatePlan
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "p"}, &got))
	require.Empty(t, got.Annotations)
}
