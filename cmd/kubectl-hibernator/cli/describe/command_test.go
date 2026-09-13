/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package describe

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

func TestRunDescribe(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "test-ns"},
	}
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(plan).Build()
	prev := common.ClientFactory
	common.ClientFactory = func(*common.RootOptions) (client.Client, error) { return c, nil }
	t.Cleanup(func() { common.ClientFactory = prev })

	var buf bytes.Buffer
	ctx := output.WithFormatter(context.Background(), output.NewFormatter(&buf, &buf))
	ctx = output.WithWriter(ctx, &buf)

	require.NoError(t, runDescribe(ctx, &describeOptions{root: &common.RootOptions{Namespace: "test-ns"}}, "p"))
	require.Contains(t, buf.String(), "p")

	require.ErrorContains(t, runDescribe(ctx, &describeOptions{root: &common.RootOptions{Namespace: "test-ns"}}, "missing"), "failed to get HibernatePlan")
}
