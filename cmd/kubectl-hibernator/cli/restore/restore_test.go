/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
	"github.com/ardikabs/hibernator/internal/restore"
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

func seedPlan(name string) *hibernatorv1alpha1.HibernatePlan {
	return &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns"},
	}
}

// restoreEntry builds one ConfigMap data value for target with the given
// resource states.
func restoreEntry(t *testing.T, target, executor string, isLive bool, state map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(restore.Data{
		Target:   target,
		Executor: executor,
		Version:  1,
		IsLive:   isLive,
		State:    state,
	})
	require.NoError(t, err)
	return string(raw)
}

func seedRestoreCM(planName string, entries map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      restore.GetRestoreConfigMap(planName),
			Namespace: "test-ns",
		},
		Data: entries,
	}
}
