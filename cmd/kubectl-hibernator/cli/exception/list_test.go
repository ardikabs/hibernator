/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package exception

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
)

func TestListCommandIsRegisteredWithAlias(t *testing.T) {
	cmd := NewCommand(&common.RootOptions{})
	found := map[string]bool{}
	for _, child := range cmd.Commands() {
		found[child.Name()] = true
		for _, alias := range child.Aliases {
			found[alias] = true
		}
	}
	assert.True(t, found["list"])
	assert.True(t, found["ls"])
}

func TestRunListDefaultsToResolvedNamespace(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		exceptionWithPlan("exc-a", "team-a", "plan-a", nil),
		exceptionWithPlan("exc-b", "team-b", "plan-a", nil),
	).Build()
	useCreateClient(t, c)

	opts := &listOptions{root: &common.RootOptions{Namespace: "team-a"}}
	ctx, buf := createTestContext()
	require.NoError(t, runList(ctx, opts))
	out := buf.String()
	assert.Contains(t, out, "exc-a")
	assert.NotContains(t, out, "exc-b")
}

func TestRunListAllNamespacesIncludesNamespaceColumn(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		exceptionWithPlan("exc-a", "team-a", "plan-a", nil),
		exceptionWithPlan("exc-b", "team-b", "plan-a", nil),
	).Build()
	useCreateClient(t, c)

	opts := &listOptions{root: &common.RootOptions{Namespace: "team-a"}, allNamespaces: true}
	ctx, buf := createTestContext()
	require.NoError(t, runList(ctx, opts))
	out := buf.String()
	assert.Contains(t, out, "NAMESPACE")
	assert.Contains(t, out, "exc-a")
	assert.Contains(t, out, "exc-b")
}

func TestRunListFiltersBySelectorExpression(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		exceptionWithPlan("exc-api", "team-a", "plan-a", map[string]string{"env": "prod", "tier": "api"}),
		exceptionWithPlan("exc-worker", "team-a", "plan-b", map[string]string{"env": "prod", "tier": "worker"}),
		exceptionWithPlan("exc-dev", "team-a", "plan-c", map[string]string{"env": "dev", "tier": "api"}),
	).Build()
	useCreateClient(t, c)

	opts := &listOptions{root: &common.RootOptions{Namespace: "team-a"}, selector: "tier in (api,worker),env=prod"}
	ctx, buf := createTestContext()
	require.NoError(t, runList(ctx, opts))
	out := buf.String()
	assert.Contains(t, out, "exc-api")
	assert.Contains(t, out, "exc-worker")
	assert.NotContains(t, out, "exc-dev")
}

func TestRunListFiltersByPlanAndCombinesWithSelector(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		exceptionWithPlan("exc-a", "team-a", "plan-a", map[string]string{"env": "prod"}),
		exceptionWithPlan("exc-b", "team-a", "plan-b", map[string]string{"env": "prod"}),
		exceptionWithPlan("exc-c", "team-a", "plan-a", map[string]string{"env": "dev"}),
	).Build()
	useCreateClient(t, c)

	opts := &listOptions{root: &common.RootOptions{Namespace: "team-a"}, selector: "env=prod", plan: "plan-a"}
	ctx, buf := createTestContext()
	require.NoError(t, runList(ctx, opts))
	out := buf.String()
	assert.Contains(t, out, "exc-a")
	assert.NotContains(t, out, "exc-b")
	assert.NotContains(t, out, "exc-c")
}

func TestRunListFiltersByType(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		typeException("exc-suspend", "team-a", "plan-a", hibernatorv1alpha1.ExceptionSuspend, nil),
		typeException("exc-extend", "team-a", "plan-b", hibernatorv1alpha1.ExceptionExtend, nil),
		typeException("exc-replace", "team-a", "plan-c", hibernatorv1alpha1.ExceptionReplace, nil),
	).Build()
	useCreateClient(t, c)

	opts := &listOptions{root: &common.RootOptions{Namespace: "team-a"}, types: "suspend,EXTEND"}
	ctx, buf := createTestContext()
	require.NoError(t, runList(ctx, opts))
	out := buf.String()
	assert.Contains(t, out, "exc-suspend")
	assert.Contains(t, out, "exc-extend")
	assert.NotContains(t, out, "exc-replace")
}

func TestRunListCombinesTypeWithPlanAndSelector(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		typeException("exc-a", "team-a", "plan-a", hibernatorv1alpha1.ExceptionSuspend, map[string]string{"env": "prod"}),
		typeException("exc-b", "team-a", "plan-b", hibernatorv1alpha1.ExceptionSuspend, map[string]string{"env": "prod"}),
		typeException("exc-c", "team-a", "plan-a", hibernatorv1alpha1.ExceptionExtend, map[string]string{"env": "prod"}),
		typeException("exc-d", "team-a", "plan-a", hibernatorv1alpha1.ExceptionSuspend, map[string]string{"env": "dev"}),
	).Build()
	useCreateClient(t, c)

	opts := &listOptions{root: &common.RootOptions{Namespace: "team-a"}, selector: "env=prod", plan: "plan-a", types: "suspend"}
	ctx, buf := createTestContext()
	require.NoError(t, runList(ctx, opts))
	out := buf.String()
	assert.Contains(t, out, "exc-a")
	assert.NotContains(t, out, "exc-b")
	assert.NotContains(t, out, "exc-c")
	assert.NotContains(t, out, "exc-d")
}

func TestRunListRejectsInvalidTypeBeforeClient(t *testing.T) {
	calls := 0
	restoreClientFactory(t, func(*common.RootOptions) (client.Client, error) {
		calls++
		return nil, errors.New("client must not be created")
	})

	opts := &listOptions{root: &common.RootOptions{Namespace: "team-a"}, types: "suspend,bogus"}
	require.ErrorContains(t, runList(context.Background(), opts), "invalid exception type")
	assert.Zero(t, calls)
}

func TestRunListRejectsMalformedSelectorBeforeClient(t *testing.T) {
	calls := 0
	restoreClientFactory(t, func(*common.RootOptions) (client.Client, error) {
		calls++
		return nil, errors.New("client must not be created")
	})

	opts := &listOptions{root: &common.RootOptions{Namespace: "team-a"}, selector: "env in ("}
	err := runList(context.Background(), opts)
	require.ErrorContains(t, err, "parse exception selector")
	assert.Zero(t, calls)
}

func TestRunListSortsByNamespaceThenName(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		exceptionWithPlan("exc-c", "team-b", "plan-a", nil),
		exceptionWithPlan("exc-b", "team-a", "plan-a", nil),
		exceptionWithPlan("exc-a", "team-a", "plan-a", nil),
	).Build()
	useCreateClient(t, c)

	opts := &listOptions{root: &common.RootOptions{}, allNamespaces: true}
	ctx, buf := createTestContext()
	require.NoError(t, runList(ctx, opts))
	out := buf.String()
	assert.Less(t, strings.Index(out, "exc-a"), strings.Index(out, "exc-b"))
	assert.Less(t, strings.Index(out, "exc-b"), strings.Index(out, "exc-c"))
}

func TestRunListEmptyPrintsHeader(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).Build()
	useCreateClient(t, c)

	opts := &listOptions{root: &common.RootOptions{Namespace: "team-a"}}
	ctx, buf := createTestContext()
	require.NoError(t, runList(ctx, opts))
	assert.Contains(t, buf.String(), "NAME")
}

func TestRunListJSONDispatch(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		exceptionWithPlan("exc-a", "team-a", "plan-a", nil),
	).Build()
	useCreateClient(t, c)

	opts := &listOptions{root: &common.RootOptions{Namespace: "team-a", JsonOutput: true}}
	ctx, buf := createTestContext()
	require.NoError(t, runList(ctx, opts))
	assert.Contains(t, buf.String(), `"plan"`)
	assert.Contains(t, buf.String(), "exc-a")
}

func typeException(name, namespace, planName string, typ hibernatorv1alpha1.ExceptionType, lbls map[string]string) *hibernatorv1alpha1.ScheduleException {
	return &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: lbls},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			PlanRef: hibernatorv1alpha1.PlanReference{Name: planName},
			Type:    typ,
		},
	}
}

func exceptionWithPlan(name, namespace, planName string, lbls map[string]string) *hibernatorv1alpha1.ScheduleException {
	return &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: lbls},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			PlanRef: hibernatorv1alpha1.PlanReference{Name: planName},
		},
	}
}
