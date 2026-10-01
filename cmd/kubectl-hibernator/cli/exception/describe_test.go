/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package exception

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
)

func TestDescribeCommandIsRegistered(t *testing.T) {
	cmd := NewCommand(&common.RootOptions{})
	found := false
	for _, child := range cmd.Commands() {
		if child.Name() == "describe" {
			found = true
		}
	}
	assert.True(t, found)
}

func TestRunDescribeZeroArgsListsAllInScope(t *testing.T) {
	now := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		statusException("exc-a", "team-a", "plan-a", hibernatorv1alpha1.ExceptionStateActive, now.Add(-time.Hour), now.Add(time.Hour)),
		statusException("exc-b", "team-b", "plan-a", hibernatorv1alpha1.ExceptionStateActive, now.Add(-time.Hour), now.Add(time.Hour)),
		planWithPhase("plan-a", "team-a", "Hibernated"),
	).Build()
	useCreateClient(t, c)

	opts := &describeOptions{root: &common.RootOptions{Namespace: "team-a"}, now: func() time.Time { return now }}
	ctx, buf := createTestContext()
	require.NoError(t, runDescribe(ctx, opts, nil))
	out := buf.String()
	assert.Contains(t, out, "exc-a")
	assert.NotContains(t, out, "exc-b")
}

func TestRunDescribeExactNamesRejectFilters(t *testing.T) {
	calls := 0
	restoreClientFactory(t, func(*common.RootOptions) (client.Client, error) {
		calls++
		return nil, errors.New("client must not be created")
	})
	now := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)

	for _, mutate := range []func(*describeOptions){
		func(o *describeOptions) { o.selector = "env=prod" },
		func(o *describeOptions) { o.plan = "plan-a" },
		func(o *describeOptions) { o.states = "Active" },
	} {
		opts := &describeOptions{root: &common.RootOptions{Namespace: "team-a"}, now: func() time.Time { return now }}
		mutate(opts)
		err := runDescribe(context.Background(), opts, []string{"exc-a"})
		require.ErrorContains(t, err, "cannot mix")
	}
	assert.Zero(t, calls)
}

func TestRunDescribeNamesAcrossAllNamespaces(t *testing.T) {
	now := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		statusException("shared", "team-a", "plan-a", hibernatorv1alpha1.ExceptionStateActive, now.Add(-time.Hour), now.Add(time.Hour)),
		statusException("shared", "team-b", "plan-b", hibernatorv1alpha1.ExceptionStateActive, now.Add(-time.Hour), now.Add(time.Hour)),
		planWithPhase("plan-a", "team-a", "Active"),
		planWithPhase("plan-b", "team-b", "Active"),
	).Build()
	useCreateClient(t, c)

	opts := &describeOptions{root: &common.RootOptions{Namespace: "team-a"}, allNamespaces: true, now: func() time.Time { return now }}
	ctx, buf := createTestContext()
	require.NoError(t, runDescribe(ctx, opts, []string{"shared"}))
	out := buf.String()
	assert.Contains(t, out, "team-a")
	assert.Contains(t, out, "team-b")

	missingOpts := &describeOptions{root: &common.RootOptions{Namespace: "team-a"}, now: func() time.Time { return now }}
	_, buf2 := createTestContext()
	_ = buf2
	err := runDescribe(ctx, missingOpts, []string{"does-not-exist"})
	require.Error(t, err)
}

func TestRunDescribeStateFilteringIsCaseInsensitiveOr(t *testing.T) {
	now := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		statusException("exc-active", "team-a", "plan-a", hibernatorv1alpha1.ExceptionStateActive, now.Add(-time.Hour), now.Add(time.Hour)),
		statusException("exc-pending", "team-a", "plan-a", hibernatorv1alpha1.ExceptionStatePending, now.Add(time.Hour), now.Add(2*time.Hour)),
		statusException("exc-expired", "team-a", "plan-a", hibernatorv1alpha1.ExceptionStateExpired, now.Add(-2*time.Hour), now.Add(-time.Hour)),
	).Build()
	useCreateClient(t, c)

	opts := &describeOptions{root: &common.RootOptions{Namespace: "team-a"}, states: "active,PENDING", now: func() time.Time { return now }}
	ctx, buf := createTestContext()
	require.NoError(t, runDescribe(ctx, opts, nil))
	out := buf.String()
	assert.Contains(t, out, "exc-active")
	assert.Contains(t, out, "exc-pending")
	assert.NotContains(t, out, "exc-expired")
}

func TestRunDescribeStateComposesWithPlanOrSelector(t *testing.T) {
	now := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	activeProd := statusException("exc-active-prod", "team-a", "plan-a", hibernatorv1alpha1.ExceptionStateActive, now.Add(-time.Hour), now.Add(time.Hour))
	activeProd.Labels = map[string]string{"env": "prod"}
	pendingProd := statusException("exc-pending-prod", "team-a", "plan-a", hibernatorv1alpha1.ExceptionStatePending, now.Add(time.Hour), now.Add(2*time.Hour))
	pendingProd.Labels = map[string]string{"env": "prod"}
	activeDev := statusException("exc-active-dev", "team-a", "plan-b", hibernatorv1alpha1.ExceptionStateActive, now.Add(-time.Hour), now.Add(time.Hour))
	activeDev.Labels = map[string]string{"env": "dev"}

	seed := func(t *testing.T) {
		t.Helper()
		c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(activeProd, pendingProd, activeDev).Build()
		useCreateClient(t, c)
	}

	t.Run("state with plan", func(t *testing.T) {
		seed(t)
		opts := &describeOptions{root: &common.RootOptions{Namespace: "team-a"}, plan: "plan-a", states: "Active", now: func() time.Time { return now }}
		ctx, buf := createTestContext()
		require.NoError(t, runDescribe(ctx, opts, nil))
		out := buf.String()
		assert.Contains(t, out, "exc-active-prod")
		assert.NotContains(t, out, "exc-pending-prod")
		assert.NotContains(t, out, "exc-active-dev")
	})

	t.Run("state with selector", func(t *testing.T) {
		seed(t)
		opts := &describeOptions{root: &common.RootOptions{Namespace: "team-a"}, selector: "env=prod", states: "Pending", now: func() time.Time { return now }}
		ctx, buf := createTestContext()
		require.NoError(t, runDescribe(ctx, opts, nil))
		out := buf.String()
		assert.Contains(t, out, "exc-pending-prod")
		assert.NotContains(t, out, "exc-active-prod")
		assert.NotContains(t, out, "exc-active-dev")
	})
}

func TestRunDescribeCombinesStatePlanAndLabelFilters(t *testing.T) {
	now := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	withLabels := statusException("exc-a", "team-a", "plan-a", hibernatorv1alpha1.ExceptionStateActive, now.Add(-time.Hour), now.Add(time.Hour))
	withLabels.Labels = map[string]string{"env": "prod"}
	otherPlan := statusException("exc-b", "team-a", "plan-b", hibernatorv1alpha1.ExceptionStateActive, now.Add(-time.Hour), now.Add(time.Hour))
	otherPlan.Labels = map[string]string{"env": "prod"}
	otherEnv := statusException("exc-c", "team-a", "plan-a", hibernatorv1alpha1.ExceptionStateActive, now.Add(-time.Hour), now.Add(time.Hour))
	otherEnv.Labels = map[string]string{"env": "dev"}
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(withLabels, otherPlan, otherEnv).Build()
	useCreateClient(t, c)

	opts := &describeOptions{root: &common.RootOptions{Namespace: "team-a"}, selector: "env=prod", plan: "plan-a", states: "Active", now: func() time.Time { return now }}
	ctx, buf := createTestContext()
	require.NoError(t, runDescribe(ctx, opts, nil))
	out := buf.String()
	assert.Contains(t, out, "exc-a")
	assert.NotContains(t, out, "exc-b")
	assert.NotContains(t, out, "exc-c")
}

func TestRunDescribeRejectsMalformedStateBeforeClient(t *testing.T) {
	calls := 0
	restoreClientFactory(t, func(*common.RootOptions) (client.Client, error) {
		calls++
		return nil, errors.New("client must not be created")
	})
	opts := &describeOptions{root: &common.RootOptions{Namespace: "team-a"}, states: "Bogus", now: time.Now}
	err := runDescribe(context.Background(), opts, nil)
	require.ErrorContains(t, err, "invalid exception state")
	assert.Zero(t, calls)
}

func TestRunDescribeDerivesTimingCategories(t *testing.T) {
	now := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		statusException("exc-future", "team-a", "plan-a", hibernatorv1alpha1.ExceptionStatePending, now.Add(time.Hour), now.Add(2*time.Hour)),
		statusException("exc-active", "team-a", "plan-a", hibernatorv1alpha1.ExceptionStateActive, now.Add(-time.Hour), now.Add(time.Hour)),
		statusException("exc-expired", "team-a", "plan-a", hibernatorv1alpha1.ExceptionStateExpired, now.Add(-2*time.Hour), now.Add(-time.Hour)),
	).Build()
	useCreateClient(t, c)

	opts := &describeOptions{root: &common.RootOptions{Namespace: "team-a"}, now: func() time.Time { return now }}
	ctx, buf := createTestContext()
	require.NoError(t, runDescribe(ctx, opts, nil))
	out := buf.String()
	assert.Contains(t, out, "starts in")
	assert.Contains(t, out, "remaining")
	assert.Contains(t, out, "expired")
}

func TestRunDescribeReportsMissingPlanWithoutFailing(t *testing.T) {
	now := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		statusException("exc-missing", "team-a", "gone", hibernatorv1alpha1.ExceptionStateDetached, now.Add(-time.Hour), now.Add(time.Hour)),
		statusException("exc-ok", "team-a", "here", hibernatorv1alpha1.ExceptionStateActive, now.Add(-time.Hour), now.Add(time.Hour)),
		planWithPhase("here", "team-a", "Hibernated"),
	).Build()
	useCreateClient(t, c)

	opts := &describeOptions{root: &common.RootOptions{Namespace: "team-a"}, now: func() time.Time { return now }}
	ctx, buf := createTestContext()
	require.NoError(t, runDescribe(ctx, opts, nil))
	out := buf.String()
	assert.Contains(t, out, "exc-missing")
	assert.Contains(t, out, "exc-ok")
	assert.Contains(t, out, "unavailable")
	assert.Contains(t, out, "Hibernated")
}

func TestRunDescribeResolvesExplicitPlanNamespace(t *testing.T) {
	now := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	exc := statusException("exc-a", "team-a", "plan-a", hibernatorv1alpha1.ExceptionStateActive, now.Add(-time.Hour), now.Add(time.Hour))
	exc.Spec.PlanRef.Namespace = "team-b"
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		exc,
		planWithPhase("plan-a", "team-b", "Active"),
	).Build()
	useCreateClient(t, c)

	opts := &describeOptions{root: &common.RootOptions{Namespace: "team-a"}, now: func() time.Time { return now }}
	ctx, buf := createTestContext()
	require.NoError(t, runDescribe(ctx, opts, nil))
	assert.Contains(t, buf.String(), "Active")
}

func TestRunDescribeCachesPlanLookups(t *testing.T) {
	now := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	base := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		statusException("exc-a", "team-a", "plan-a", hibernatorv1alpha1.ExceptionStateActive, now.Add(-time.Hour), now.Add(time.Hour)),
		statusException("exc-b", "team-a", "plan-a", hibernatorv1alpha1.ExceptionStateActive, now.Add(-time.Hour), now.Add(time.Hour)),
		planWithPhase("plan-a", "team-a", "Active"),
	).Build()
	counter := &countingGetClient{Client: base}
	useCreateClient(t, counter)

	opts := &describeOptions{root: &common.RootOptions{Namespace: "team-a"}, now: func() time.Time { return now }}
	ctx, _ := createTestContext()
	require.NoError(t, runDescribe(ctx, opts, nil))
	assert.LessOrEqual(t, counter.planGets, 1)
}

func TestRunDescribeFailsOnNonNotFoundPlanError(t *testing.T) {
	now := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	base := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		statusException("exc-a", "team-a", "plan-a", hibernatorv1alpha1.ExceptionStateActive, now.Add(-time.Hour), now.Add(time.Hour)),
	).Build()
	useCreateClient(t, &failingPlanGetClient{Client: base, err: errors.New("boom")})

	opts := &describeOptions{root: &common.RootOptions{Namespace: "team-a"}, now: func() time.Time { return now }}
	ctx, _ := createTestContext()
	require.ErrorContains(t, runDescribe(ctx, opts, nil), "boom")
}

func TestRunDescribeJSONDispatch(t *testing.T) {
	now := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		statusException("exc-a", "team-a", "plan-a", hibernatorv1alpha1.ExceptionStateActive, now.Add(-time.Hour), now.Add(time.Hour)),
		planWithPhase("plan-a", "team-a", "Active"),
	).Build()
	useCreateClient(t, c)

	opts := &describeOptions{root: &common.RootOptions{Namespace: "team-a", JsonOutput: true}, now: func() time.Time { return now }}
	ctx, buf := createTestContext()
	require.NoError(t, runDescribe(ctx, opts, nil))
	assert.Contains(t, buf.String(), `"timing"`)
	assert.Contains(t, buf.String(), "exc-a")
}

func statusException(name, namespace, planName string, state hibernatorv1alpha1.ExceptionState, from, until time.Time) *hibernatorv1alpha1.ScheduleException {
	return &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			PlanRef:    hibernatorv1alpha1.PlanReference{Name: planName},
			ValidFrom:  metav1.NewTime(from),
			ValidUntil: metav1.NewTime(until),
			Type:       hibernatorv1alpha1.ExceptionSuspend,
		},
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{State: state},
	}
}

func planWithPhase(name, namespace, phase string) *hibernatorv1alpha1.HibernatePlan {
	return &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Status:     hibernatorv1alpha1.HibernatePlanStatus{Phase: hibernatorv1alpha1.PlanPhase(phase)},
	}
}

type countingGetClient struct {
	client.Client
	planGets int
}

func (c *countingGetClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*hibernatorv1alpha1.HibernatePlan); ok {
		c.planGets++
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

type failingPlanGetClient struct {
	client.Client
	err error
}

func (c *failingPlanGetClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*hibernatorv1alpha1.HibernatePlan); ok {
		return c.err
	}
	return c.Client.Get(ctx, key, obj, opts...)
}
