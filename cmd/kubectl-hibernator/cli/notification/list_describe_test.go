/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

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

func seedNotif(name, ns string, match map[string]string) *hibernatorv1alpha1.HibernateNotification {
	return &hibernatorv1alpha1.HibernateNotification{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: hibernatorv1alpha1.HibernateNotificationSpec{
			Selector: metav1.LabelSelector{MatchLabels: match},
			Sinks: []hibernatorv1alpha1.NotificationSink{
				{Name: "ops", Type: hibernatorv1alpha1.SinkWebhook},
			},
		},
	}
}

func seedPlan(name, ns string, labels map[string]string) *hibernatorv1alpha1.HibernatePlan {
	return &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
	}
}

func TestRunListFiltersByPlan(t *testing.T) {
	useFakeClient(t,
		seedNotif("team-a", "test-ns", map[string]string{"team": "a"}),
		seedNotif("team-b", "test-ns", map[string]string{"team": "b"}),
		seedPlan("plan-a", "test-ns", map[string]string{"team": "a"}),
	)
	ctx, buf := testCtx()

	opts := &listOptions{root: &common.RootOptions{Namespace: "test-ns"}, planName: "plan-a"}
	require.NoError(t, runList(ctx, opts))
	require.Contains(t, buf.String(), "team-a")
	require.NotContains(t, buf.String(), "team-b")
}

func TestRunDescribeReportsPlanMatch(t *testing.T) {
	useFakeClient(t,
		seedNotif("n", "test-ns", map[string]string{"env": "prod"}),
		seedPlan("prod-plan", "test-ns", map[string]string{"env": "prod"}),
		seedPlan("dev-plan", "test-ns", map[string]string{"env": "dev"}),
	)
	ctx, buf := testCtx()
	root := &common.RootOptions{Namespace: "test-ns"}

	require.NoError(t, runDescribe(ctx, &describeOptions{root: root, planName: "prod-plan"}, "n"))
	require.Contains(t, buf.String(), "Plan Match: [OK] prod-plan")

	ctx, buf = testCtx()
	require.NoError(t, runDescribe(ctx, &describeOptions{root: root, planName: "dev-plan"}, "n"))
	require.Contains(t, buf.String(), "Plan Match: [NO] dev-plan")
}
