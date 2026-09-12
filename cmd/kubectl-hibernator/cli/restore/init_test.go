/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestRunInitCreatesEntry(t *testing.T) {
	c := useFakeClient(t, seedPlan("p"))
	ctx, _ := testCtx()

	opts := &initOptions{root: testRoot(), target: "db", executor: "rds"}
	require.NoError(t, runInit(ctx, opts, "p"))

	var cm corev1.ConfigMap
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "hibernator-restore-p"}, &cm))
	require.Contains(t, cm.Data, "db.json")
}

func TestRunInitRefusesOverwriteWithoutForce(t *testing.T) {
	cm := seedRestoreCM("p", map[string]string{"db.json": `{"target":"db"}`})
	c := useFakeClient(t, seedPlan("p"), cm)
	ctx, _ := testCtx()

	opts := &initOptions{root: testRoot(), target: "db", executor: "rds"}
	require.ErrorContains(t, runInit(ctx, opts, "p"), "already exists")

	opts.force = true
	require.NoError(t, runInit(ctx, opts, "p"))

	var got corev1.ConfigMap
	require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "hibernator-restore-p"}, &got))
	require.Contains(t, string(got.Data["db.json"]), `"executor":"rds"`)
}

func TestRunInitMissingPlan(t *testing.T) {
	useFakeClient(t)
	ctx, _ := testCtx()
	opts := &initOptions{root: testRoot(), target: "db", executor: "rds"}
	require.ErrorContains(t, runInit(ctx, opts, "missing"), "failed to get HibernatePlan")
}

func TestRunInitDryRunCreatesNothing(t *testing.T) {
	c := useFakeClient(t, seedPlan("p"))
	ctx, buf := testCtx()

	opts := &initOptions{root: testRoot(), target: "db", executor: "rds", dryRun: true}
	require.NoError(t, runInit(ctx, opts, "p"))
	require.Contains(t, buf.String(), "DRY-RUN")

	var cm corev1.ConfigMap
	require.True(t, apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "hibernator-restore-p"}, &cm)))
}
