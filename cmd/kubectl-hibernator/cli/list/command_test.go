/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package list

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

func TestRunListShowsPlans(t *testing.T) {
	mkPlan := func(name, phase string) *hibernatorv1alpha1.HibernatePlan {
		return &hibernatorv1alpha1.HibernatePlan{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns"},
			Status:     hibernatorv1alpha1.HibernatePlanStatus{Phase: hibernatorv1alpha1.PlanPhase(phase)},
		}
	}
	// No schedules: ComputeNextEvent yields no event, output stays
	// deterministic regardless of wall clock.
	useFakeClient(t,
		mkPlan("plan-a", string(hibernatorv1alpha1.PhaseActive)),
		mkPlan("plan-b", string(hibernatorv1alpha1.PhaseHibernated)),
	)
	ctx, buf := testCtx()

	require.NoError(t, runList(ctx, &listOptions{root: &common.RootOptions{Namespace: "test-ns"}}))
	require.Contains(t, buf.String(), "plan-a")
	require.Contains(t, buf.String(), "plan-b")
}

func TestRunListEmpty(t *testing.T) {
	useFakeClient(t)
	ctx, _ := testCtx()
	require.NoError(t, runList(ctx, &listOptions{root: &common.RootOptions{Namespace: "test-ns"}}))
}
