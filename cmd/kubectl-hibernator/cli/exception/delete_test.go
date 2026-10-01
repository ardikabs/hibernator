/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package exception

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
)

func TestDeleteCommandIsRegistered(t *testing.T) {
	cmd := NewCommand(&common.RootOptions{})
	found := false
	for _, child := range cmd.Commands() {
		if child.Name() == "delete" {
			found = true
		}
	}
	assert.True(t, found)
}

func TestRunDeleteExactNames(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		exceptionWithPlan("exc-a", "team-a", "plan-a", nil),
		exceptionWithPlan("exc-b", "team-a", "plan-b", nil),
	).Build()
	useCreateClient(t, c)

	opts := &deleteOptions{root: &common.RootOptions{Namespace: "team-a"}, in: strings.NewReader("y\n")}
	ctx, buf := createTestContext()
	require.NoError(t, runDelete(ctx, opts, []string{"exc-a", "exc-b"}))
	out := buf.String()
	assert.Contains(t, out, "exc-a")
	assert.Contains(t, out, "exc-b")
	assert.Contains(t, out, "SUCCESS")

	for _, name := range []string{"exc-a", "exc-b"} {
		got := &hibernatorv1alpha1.ScheduleException{}
		require.Error(t, c.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: name}, got))
	}
}

func TestRunDeleteSelectorMode(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		exceptionWithPlan("exc-prod", "team-a", "plan-a", map[string]string{"env": "prod"}),
		exceptionWithPlan("exc-dev", "team-a", "plan-a", map[string]string{"env": "dev"}),
	).Build()
	useCreateClient(t, c)

	opts := &deleteOptions{root: &common.RootOptions{Namespace: "team-a"}, selector: "env=prod", yes: true}
	ctx, buf := createTestContext()
	require.NoError(t, runDelete(ctx, opts, nil))
	assert.Contains(t, buf.String(), "exc-prod")
	assert.NotContains(t, buf.String(), "exc-dev")

	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "exc-dev"}, &hibernatorv1alpha1.ScheduleException{}))
	require.Error(t, c.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "exc-prod"}, &hibernatorv1alpha1.ScheduleException{}))
}

func TestRunDeletePlanMode(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		exceptionWithPlan("exc-a", "team-a", "plan-a", nil),
		exceptionWithPlan("exc-b", "team-a", "plan-b", nil),
	).Build()
	useCreateClient(t, c)

	opts := &deleteOptions{root: &common.RootOptions{Namespace: "team-a"}, plan: "plan-a", yes: true}
	ctx, buf := createTestContext()
	require.NoError(t, runDelete(ctx, opts, nil))
	assert.Contains(t, buf.String(), "exc-a")
	assert.NotContains(t, buf.String(), "exc-b")
}

func TestRunDeleteCombinesSelectorAndPlan(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		exceptionWithPlan("exc-a", "team-a", "plan-a", map[string]string{"env": "prod"}),
		exceptionWithPlan("exc-b", "team-a", "plan-b", map[string]string{"env": "prod"}),
		exceptionWithPlan("exc-c", "team-a", "plan-a", map[string]string{"env": "dev"}),
	).Build()
	useCreateClient(t, c)

	opts := &deleteOptions{root: &common.RootOptions{Namespace: "team-a"}, selector: "env=prod", plan: "plan-a", yes: true}
	ctx, buf := createTestContext()
	require.NoError(t, runDelete(ctx, opts, nil))
	out := buf.String()
	assert.Contains(t, out, "exc-a")
	assert.NotContains(t, out, "exc-b")
	assert.NotContains(t, out, "exc-c")
}

func TestRunDeleteRejectsNamesMixedWithFilters(t *testing.T) {
	calls := 0
	restoreClientFactory(t, func(*common.RootOptions) (client.Client, error) {
		calls++
		return nil, errors.New("client must not be created")
	})

	for _, mutate := range []func(*deleteOptions){
		func(o *deleteOptions) { o.selector = "env=prod" },
		func(o *deleteOptions) { o.plan = "plan-a" },
	} {
		opts := &deleteOptions{root: &common.RootOptions{Namespace: "team-a"}, in: strings.NewReader("y\n")}
		mutate(opts)
		err := runDelete(context.Background(), opts, []string{"exc-a"})
		require.ErrorContains(t, err, "cannot mix")
	}
	assert.Zero(t, calls)
}

func TestRunDeleteRejectsUnqualifiedCollection(t *testing.T) {
	calls := 0
	restoreClientFactory(t, func(*common.RootOptions) (client.Client, error) {
		calls++
		return nil, errors.New("client must not be created")
	})

	opts := &deleteOptions{root: &common.RootOptions{Namespace: "team-a"}, in: strings.NewReader("y\n")}
	err := runDelete(context.Background(), opts, nil)
	require.ErrorContains(t, err, "at least one")
	assert.Zero(t, calls)
}

func TestRunDeleteZeroMatches(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		exceptionWithPlan("exc-a", "team-a", "plan-a", nil),
	).Build()
	useCreateClient(t, c)

	missingOpts := &deleteOptions{root: &common.RootOptions{Namespace: "team-a"}, in: strings.NewReader("y\n")}
	ctx, _ := createTestContext()
	require.Error(t, runDelete(ctx, missingOpts, []string{"does-not-exist"}))

	emptyOpts := &deleteOptions{root: &common.RootOptions{Namespace: "team-a"}, selector: "env=prod", yes: true}
	ctx2, _ := createTestContext()
	require.ErrorContains(t, runDelete(ctx2, emptyOpts, nil), "no ScheduleExceptions")
}

func TestRunDeleteConfirmationDefaultsNo(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		deleted   bool
		cancelled bool
	}{
		{name: "lower y", input: "y\n", deleted: true},
		{name: "upper Y", input: "Y\n", deleted: true},
		{name: "yes", input: "yes\n", deleted: true},
		{name: "upper YES padded", input: "  YES  \n", deleted: true},
		{name: "empty", input: "\n", cancelled: true},
		{name: "n", input: "n\n", cancelled: true},
		{name: "no", input: "no\n", cancelled: true},
		{name: "unrecognized", input: "maybe\n", cancelled: true},
		{name: "eof", input: "", cancelled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
				exceptionWithPlan("exc-a", "team-a", "plan-a", nil),
			).Build()
			useCreateClient(t, c)

			opts := &deleteOptions{root: &common.RootOptions{Namespace: "team-a"}, in: strings.NewReader(tt.input)}
			ctx, buf := createTestContext()
			require.NoError(t, runDelete(ctx, opts, []string{"exc-a"}))

			got := &hibernatorv1alpha1.ScheduleException{}
			err := c.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "exc-a"}, got)
			if tt.deleted {
				require.Error(t, err)
				assert.Contains(t, buf.String(), "SUCCESS")
			}
			if tt.cancelled {
				require.NoError(t, err)
				assert.Contains(t, buf.String(), "Cancelled")
			}
		})
	}
}

func TestRunDeleteYesBypassesConfirmation(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		exceptionWithPlan("exc-a", "team-a", "plan-a", nil),
	).Build()
	useCreateClient(t, c)

	opts := &deleteOptions{root: &common.RootOptions{Namespace: "team-a"}, yes: true}
	ctx, buf := createTestContext()
	require.NoError(t, runDelete(ctx, opts, []string{"exc-a"}))
	assert.Contains(t, buf.String(), "SUCCESS")
	require.Error(t, c.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "exc-a"}, &hibernatorv1alpha1.ScheduleException{}))
}

func TestRunDeleteRejectsMalformedSelectorBeforeClient(t *testing.T) {
	calls := 0
	restoreClientFactory(t, func(*common.RootOptions) (client.Client, error) {
		calls++
		return nil, errors.New("client must not be created")
	})

	opts := &deleteOptions{root: &common.RootOptions{Namespace: "team-a"}, selector: "env in (", yes: true}
	require.ErrorContains(t, runDelete(context.Background(), opts, nil), "parse exception selector")
	assert.Zero(t, calls)
}

func TestRunDeleteAttemptsAllAndReportsPartialFailure(t *testing.T) {
	base := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		exceptionWithPlan("exc-a", "team-a", "plan-a", nil),
		exceptionWithPlan("exc-b", "team-a", "plan-b", nil),
		exceptionWithPlan("exc-c", "team-a", "plan-c", nil),
	).Build()
	wrapper := &failingDeleteClient{Client: base, failName: "exc-b", err: errors.New("forbidden")}
	useCreateClient(t, wrapper)

	opts := &deleteOptions{root: &common.RootOptions{Namespace: "team-a"}, yes: true}
	ctx, buf := createTestContext()
	err := runDelete(ctx, opts, []string{"exc-a", "exc-b", "exc-c"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "exc-b")
	assert.Equal(t, []string{"exc-a", "exc-b", "exc-c"}, wrapper.attempted)

	out := buf.String()
	assert.Contains(t, out, "exc-a")
	assert.Contains(t, out, "exc-b")
	assert.Contains(t, out, "exc-c")
	assert.Contains(t, out, "FAILED")
	assert.Contains(t, out, "forbidden")

	// Failed deletes remain; successful deletes remain deleted. Use base to avoid double-counting wrapper attempts.
	require.NoError(t, base.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "exc-b"}, &hibernatorv1alpha1.ScheduleException{}))
	require.Error(t, base.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "exc-a"}, &hibernatorv1alpha1.ScheduleException{}))
	require.Error(t, base.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "exc-c"}, &hibernatorv1alpha1.ScheduleException{}))
}

func TestRunDeleteDryRunDeletesNothingWithoutPrompt(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		exceptionWithPlan("exc-a", "team-a", "plan-a", nil),
		exceptionWithPlan("exc-b", "team-a", "plan-b", nil),
	).Build()
	useCreateClient(t, c)

	// Empty stdin and no --yes: dry-run must not prompt and must not delete.
	opts := &deleteOptions{root: &common.RootOptions{Namespace: "team-a"}, dryRun: true, in: strings.NewReader("")}
	ctx, buf := createTestContext()
	require.NoError(t, runDelete(ctx, opts, []string{"exc-a"}))

	out := buf.String()
	assert.Contains(t, out, "[DRY-RUN]")
	assert.Contains(t, out, "exc-a")
	assert.NotContains(t, out, "exc-b")

	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "exc-a"}, &hibernatorv1alpha1.ScheduleException{}))
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "exc-b"}, &hibernatorv1alpha1.ScheduleException{}))
}

func TestRunDeleteJSONRequiresYesInsteadOfPrompting(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		exceptionWithPlan("exc-a", "team-a", "plan-a", nil),
	).Build()
	useCreateClient(t, c)

	opts := &deleteOptions{root: &common.RootOptions{Namespace: "team-a", JsonOutput: true}, in: strings.NewReader("y\n")}
	ctx, _ := createTestContext()
	err := runDelete(ctx, opts, []string{"exc-a"})
	require.ErrorContains(t, err, "--yes")

	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "exc-a"}, &hibernatorv1alpha1.ScheduleException{}))
}

func TestRunDeleteJSONEmitsParseableDocument(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		exceptionWithPlan("exc-a", "team-a", "plan-a", nil),
	).Build()
	useCreateClient(t, c)

	opts := &deleteOptions{root: &common.RootOptions{Namespace: "team-a", JsonOutput: true}, yes: true}
	ctx, buf := createTestContext()
	require.NoError(t, runDelete(ctx, opts, []string{"exc-a"}))

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got), "delete --json output must be a single parseable JSON document")
	assert.Equal(t, "delete", got["action"])
}

func TestRunDeleteJSONDispatch(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		exceptionWithPlan("exc-a", "team-a", "plan-a", nil),
	).Build()
	useCreateClient(t, c)

	opts := &deleteOptions{root: &common.RootOptions{Namespace: "team-a", JsonOutput: true}, yes: true}
	ctx, buf := createTestContext()
	require.NoError(t, runDelete(ctx, opts, []string{"exc-a"}))
	assert.Contains(t, buf.String(), `"action"`)
	assert.Contains(t, buf.String(), "exc-a")
}

type failingDeleteClient struct {
	client.Client
	failName  string
	err       error
	attempted []string
}

func (c *failingDeleteClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if exc, ok := obj.(*hibernatorv1alpha1.ScheduleException); ok {
		c.attempted = append(c.attempted, exc.Name)
		if exc.Name == c.failName {
			return c.err
		}
	}
	return c.Client.Delete(ctx, obj, opts...)
}
