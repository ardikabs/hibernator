/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package preview

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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

const previewPlanYAML = `apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: file-plan
spec:
  schedule:
    timezone: UTC
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
  execution:
    strategy:
      type: Parallel
  behavior:
    mode: Strict
  targets:
    - name: db
      type: noop
      connectorRef:
        kind: CloudProvider
        name: test
`

func TestRunPreviewFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.yaml")
	require.NoError(t, os.WriteFile(path, []byte(previewPlanYAML), 0600))

	ctx, buf := testCtx()
	opts := &previewOptions{root: &common.RootOptions{Namespace: "test-ns"}, file: path, events: 2}
	require.NoError(t, runPreview(ctx, opts, nil))
	require.Contains(t, buf.String(), "file-plan")
}

func TestRunPreviewFromFileRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte("::: not yaml ::: ["), 0600))

	ctx, _ := testCtx()
	opts := &previewOptions{root: &common.RootOptions{Namespace: "test-ns"}, file: path, events: 2}
	require.Error(t, runPreview(ctx, opts, nil))
}

func TestRunPreviewFromCluster(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "test-ns"},
		Spec: hibernatorv1alpha1.HibernatePlanSpec{
			Schedule: hibernatorv1alpha1.Schedule{
				Timezone: "UTC",
				OffHours: []hibernatorv1alpha1.OffHourWindow{
					{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"}},
				},
			},
		},
	}
	useFakeClient(t, plan)
	ctx, buf := testCtx()
	opts := &previewOptions{root: &common.RootOptions{Namespace: "test-ns"}, events: 2}
	require.NoError(t, runPreview(ctx, opts, []string{"p"}))
	require.Contains(t, buf.String(), "p")
}
