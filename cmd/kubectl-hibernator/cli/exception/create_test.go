/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package exception

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

func TestExceptionParentRegistersAllSubcommands(t *testing.T) {
	cmd := NewCommand(&common.RootOptions{})
	require.Equal(t, "exception", cmd.Name())

	names := []string{}
	for _, child := range cmd.Commands() {
		names = append(names, child.Name())
	}
	assert.ElementsMatch(t, []string{"create", "list", "describe", "delete"}, names)
}

func TestExceptionParentDescribesSubcommands(t *testing.T) {
	cmd := NewCommand(&common.RootOptions{})
	require.NotEmpty(t, cmd.Long)
	for _, want := range []string{"create", "list", "describe", "delete"} {
		assert.Contains(t, cmd.Long, want)
	}
}

func TestCreateCommandRequiresExactlyOnePrefix(t *testing.T) {
	for _, args := range [][]string{
		createCommandArgs()[1:],
		append(createCommandArgs(), "extra-prefix"),
	} {
		cmd := NewCommand(&common.RootOptions{Namespace: "team-a"})
		cmd.SetArgs(append([]string{"create"}, args...))
		err := cmd.Execute()
		require.ErrorContains(t, err, "1 arg")
	}
}

func TestCreateCommandRequiresContentFlags(t *testing.T) {
	required := []string{"type", "from", "until", "window-start", "window-end"}
	for _, missing := range required {
		t.Run(missing, func(t *testing.T) {
			args := createCommandArgs()
			args = removeFlag(args, "--"+missing)

			cmd := NewCommand(&common.RootOptions{Namespace: "team-a"})
			cmd.SetArgs(append([]string{"create"}, args...))
			err := cmd.Execute()
			require.ErrorContains(t, err, fmt.Sprintf(`required flag(s) "%s" not set`, missing))
		})
	}
}

func TestCreateCommandRequiresExactlyOnePlanSelectionModeBeforeClient(t *testing.T) {
	clientCalls := 0
	restoreClientFactory(t, func(*common.RootOptions) (client.Client, error) {
		clientCalls++
		return nil, errors.New("client must not be created")
	})

	tests := []struct {
		name      string
		selection []string
	}{
		{name: "neither"},
		{name: "both", selection: []string{"--plan", "plan-a", "-l", "env=prod"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewCommand(&common.RootOptions{Namespace: "team-a"})
			args := removeFlag(createCommandArgs(), "--selector")
			args = append(args, tt.selection...)
			cmd.SetArgs(append([]string{"create"}, args...))
			err := cmd.Execute()
			require.ErrorContains(t, err, "exactly one of --plan or --selector")
		})
	}
	assert.Zero(t, clientCalls)
}

func TestRunCreateRejectsInvalidValuesBeforeClient(t *testing.T) {
	clientCalls := 0
	restoreClientFactory(t, func(*common.RootOptions) (client.Client, error) {
		clientCalls++
		return nil, errors.New("client must not be created")
	})

	now := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		mutate  func(*createOptions)
		wantErr string
	}{
		{name: "invalid type", mutate: func(o *createOptions) { o.exceptionType = "Suspend" }, wantErr: "--type must be one of suspend, extend, or replace"},
		{name: "invalid days", mutate: func(o *createOptions) { o.days = "MON,FUNDAY" }, wantErr: "invalid --days"},
		{name: "invalid window", mutate: func(o *createOptions) { o.windowStart = "9:00" }, wantErr: "invalid window"},
		{name: "invalid from", mutate: func(o *createOptions) { o.from = "not-a-time" }, wantErr: "invalid --from"},
		{name: "invalid until", mutate: func(o *createOptions) { o.until = "not-a-time" }, wantErr: "invalid --until"},
		{name: "reversed validity", mutate: func(o *createOptions) { o.from, o.until = "in 2 hours", "in 1 hour" }, wantErr: "--from must be before --until"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := validCreateOptions(now)
			tt.mutate(opts)
			err := runCreate(context.Background(), opts, "maintenance")
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
	assert.Zero(t, clientCalls)
}

func TestRunCreateRejectsMalformedSelectorBeforeClient(t *testing.T) {
	clientCalls := 0
	restoreClientFactory(t, func(*common.RootOptions) (client.Client, error) {
		clientCalls++
		return nil, errors.New("client must not be created")
	})

	opts := validCreateOptions(time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC))
	opts.selector = "env in ("

	err := runCreate(context.Background(), opts, "maintenance")
	require.ErrorContains(t, err, "parse plan selector")
	assert.Zero(t, clientCalls)
}

func TestRunCreatePersistsExactPlanWithUTCValidity(t *testing.T) {
	zone := time.FixedZone("WIB", 7*60*60)
	now := time.Date(2026, time.October, 1, 10, 30, 0, 0, zone)
	base := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		plan("payments", "team-a", map[string]string{"env": "prod", "owner": "platform"}),
	).Build()
	c := &capturingCreateClient{Client: base}
	useCreateClient(t, c)

	nowCalls := 0
	opts := validCreateOptions(now)
	opts.selector = ""
	opts.plan = "payments"
	opts.days = "Monday,wed,FRI"
	opts.from = "tomorrow at 8am"
	opts.until = "tomorrow at 10am"
	opts.now = func() time.Time {
		nowCalls++
		return now
	}
	opts.generateSuffix = fixedSuffix("a1b2c3d4")

	ctx, buf := createTestContext()
	require.NoError(t, runCreate(ctx, opts, strings.Repeat("maintenance", 7)))
	assert.Equal(t, 1, nowCalls)

	var exceptions hibernatorv1alpha1.ScheduleExceptionList
	require.NoError(t, base.List(context.Background(), &exceptions))
	require.Len(t, exceptions.Items, 1)
	got := exceptions.Items[0]
	assert.LessOrEqual(t, len(got.Name), 63)
	assert.True(t, strings.HasSuffix(got.Name, "-a1b2c3d4"))
	assert.Equal(t, "team-a", got.Namespace)
	assert.Equal(t, map[string]string{wellknown.LabelPlan: "payments"}, got.Labels)
	assert.Equal(t, hibernatorv1alpha1.PlanReference{Name: "payments"}, got.Spec.PlanRef)
	assert.Equal(t, hibernatorv1alpha1.ExceptionSuspend, got.Spec.Type)
	wantFrom := time.Date(2026, time.October, 2, 1, 0, 0, 0, time.UTC)
	wantUntil := time.Date(2026, time.October, 2, 3, 0, 0, 0, time.UTC)
	assert.True(t, got.Spec.ValidFrom.Time.Equal(wantFrom))
	assert.True(t, got.Spec.ValidUntil.Time.Equal(wantUntil))
	require.NotNil(t, c.created)
	assert.Equal(t, time.UTC, c.created.Spec.ValidFrom.Time.Location())
	assert.Equal(t, time.UTC, c.created.Spec.ValidUntil.Time.Location())
	assert.Equal(t, []hibernatorv1alpha1.OffHourWindow{{
		Start: "22:00", End: "06:00", DaysOfWeek: []string{"MON", "WED", "FRI"},
	}}, got.Spec.Windows)
	assert.Contains(t, buf.String(), "payments")
	assert.Contains(t, buf.String(), "SUCCESS")
}

func TestRunCreatePersistsOneExceptionPerMatchExpressionPlan(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		plan("api-prod", "team-a", map[string]string{
			"env": "prod", "tier": "api", "region": "apac", wellknown.LabelPlan: "stale-api",
		}),
		plan("worker-prod", "team-a", map[string]string{
			"env": "prod", "tier": "worker", "region": "emea", wellknown.LabelPlan: "stale-worker",
		}),
		plan("api-dev", "team-a", map[string]string{"env": "dev", "tier": "api", "region": "apac"}),
	).Build()
	useCreateClient(t, c)

	suffixes := []string{"a1b2c3d4", "e5f6g7h8"}
	opts := validCreateOptions(time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC))
	opts.selector = "env=prod,tier in (api,worker),region"
	opts.generateSuffix = func(int) (string, error) {
		suffix := suffixes[0]
		suffixes = suffixes[1:]
		return suffix, nil
	}

	ctx, _ := createTestContext()
	require.NoError(t, runCreate(ctx, opts, "holiday"))

	var exceptions hibernatorv1alpha1.ScheduleExceptionList
	require.NoError(t, c.List(context.Background(), &exceptions))
	require.Len(t, exceptions.Items, 2)
	sort.Slice(exceptions.Items, func(i, j int) bool {
		return exceptions.Items[i].Spec.PlanRef.Name < exceptions.Items[j].Spec.PlanRef.Name
	})
	assert.Equal(t, "api-prod", exceptions.Items[0].Spec.PlanRef.Name)
	assert.Equal(t, map[string]string{
		"env": "prod", "tier": "api", "region": "apac", wellknown.LabelPlan: "api-prod",
	}, exceptions.Items[0].Labels)
	assert.Equal(t, "worker-prod", exceptions.Items[1].Spec.PlanRef.Name)
	assert.Equal(t, map[string]string{
		"env": "prod", "tier": "worker", "region": "emea", wellknown.LabelPlan: "worker-prod",
	}, exceptions.Items[1].Labels)
	for _, exception := range exceptions.Items {
		assert.Equal(t, "team-a", exception.Namespace)
		assert.LessOrEqual(t, len(exception.Name), 63)
	}
}

func TestRunCreateRetriesAlreadyExistingNameWithFreshSuffix(t *testing.T) {
	existing := exception("maintenance-aaaaaaaa", "team-a", "old-plan", nil)
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		plan("plan-a", "team-a", nil), existing,
	).Build()
	useCreateClient(t, c)

	suffixes := []string{"aaaaaaaa", "bbbbbbbb"}
	opts := validCreateOptions(time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC))
	opts.plan, opts.selector = "plan-a", ""
	opts.generateSuffix = func(int) (string, error) {
		suffix := suffixes[0]
		suffixes = suffixes[1:]
		return suffix, nil
	}

	ctx, _ := createTestContext()
	require.NoError(t, runCreate(ctx, opts, "maintenance"))
	assert.Empty(t, suffixes)

	created := &hibernatorv1alpha1.ScheduleException{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "maintenance-bbbbbbbb"}, created))
	assert.Equal(t, "plan-a", created.Spec.PlanRef.Name)
}

func TestRunCreateStopsAfterThreeNameCollisions(t *testing.T) {
	base := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		plan("plan-a", "team-a", nil),
		exception("maintenance-aaaaaaaa", "team-a", "old-plan", nil),
	).Build()
	c := &countingCreateClient{Client: base}
	useCreateClient(t, c)

	opts := validCreateOptions(time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC))
	opts.plan, opts.selector = "plan-a", ""
	opts.generateSuffix = fixedSuffix("aaaaaaaa")
	ctx, buf := createTestContext()

	err := runCreate(ctx, opts, "maintenance")
	require.Error(t, err)
	assert.ErrorContains(t, err, "plan-a")
	assert.ErrorContains(t, err, "--plan plan-a")
	assert.Equal(t, 3, c.createCalls)
	assert.Contains(t, buf.String(), "FAILED")
	assert.Contains(t, buf.String(), "already exists")
}

func TestRunCreateAttemptsAllPlansAndReportsPartialFailure(t *testing.T) {
	base := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		plan("plan-a", "team-a", map[string]string{"env": "prod"}),
		plan("plan-b", "team-a", map[string]string{"env": "prod"}),
		plan("plan-c", "team-a", map[string]string{"env": "prod"}),
	).Build()
	c := &failingCreateClient{Client: base, failPlan: "plan-b", err: errors.New("admission denied")}
	useCreateClient(t, c)

	suffixes := []string{"aaaaaaaa", "bbbbbbbb", "cccccccc"}
	opts := validCreateOptions(time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC))
	opts.generateSuffix = func(int) (string, error) {
		suffix := suffixes[0]
		suffixes = suffixes[1:]
		return suffix, nil
	}

	ctx, buf := createTestContext()
	err := runCreate(ctx, opts, "maintenance")
	require.Error(t, err)
	assert.ErrorContains(t, err, "plan-b")
	assert.ErrorContains(t, err, "--plan plan-b")
	assert.Equal(t, []string{"plan-a", "plan-b", "plan-c"}, c.attemptedPlans)

	var exceptions hibernatorv1alpha1.ScheduleExceptionList
	require.NoError(t, base.List(context.Background(), &exceptions))
	require.Len(t, exceptions.Items, 2)
	assert.ElementsMatch(t, []string{"plan-a", "plan-c"}, []string{
		exceptions.Items[0].Spec.PlanRef.Name,
		exceptions.Items[1].Spec.PlanRef.Name,
	})
	printed := buf.String()
	assert.Contains(t, printed, "plan-a")
	assert.Contains(t, printed, "plan-b")
	assert.Contains(t, printed, "plan-c")
	assert.Contains(t, printed, "FAILED")
	assert.Contains(t, printed, "admission denied")
}

func TestRunCreatePrintsRecreationCommandForFailedPlans(t *testing.T) {
	base := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		plan("plan-a", "team-a", map[string]string{"env": "prod"}),
		plan("plan-b", "team-a", map[string]string{"env": "prod"}),
	).Build()
	c := &failingCreateClient{Client: base, failPlan: "plan-b", err: errors.New("admission denied")}
	useCreateClient(t, c)

	opts := validCreateOptions(time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC))
	opts.days = "MON,TUE"
	opts.generateSuffix = fixedSuffix("a1b2c3d4")

	ctx, buf := createTestContext()
	err := runCreate(ctx, opts, "maintenance")
	require.Error(t, err)

	want := `kubectl hibernator exception create maintenance -n team-a --plan plan-b --type suspend --from "in 1 hour" --until "in 2 hours" --window-start 22:00 --window-end 06:00 --days MON,TUE`
	assert.Contains(t, err.Error(), want)
	assert.Contains(t, buf.String(), want)
}

func TestRunCreateDryRunPrintsActualExceptionsWithoutCreating(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		plan("plan-a", "team-a", map[string]string{"env": "prod"}),
		plan("plan-b", "team-a", map[string]string{"env": "prod"}),
	).Build()
	useCreateClient(t, c)

	opts := validCreateOptions(time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC))
	opts.dryRun = true
	opts.generateSuffix = fixedSuffix("a1b2c3d4")

	ctx, buf := createTestContext()
	require.NoError(t, runCreate(ctx, opts, "maintenance"))

	out := buf.String()
	assert.Contains(t, out, "[DRY-RUN]")
	assert.Contains(t, out, "maintenance-a1b2c3d4")
	assert.Contains(t, out, "plan-a")
	assert.Contains(t, out, "plan-b")

	var exceptions hibernatorv1alpha1.ScheduleExceptionList
	require.NoError(t, c.List(context.Background(), &exceptions))
	assert.Empty(t, exceptions.Items)
}

func TestRunCreateDryRunJSONRendersFullObjects(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		plan("plan-a", "team-a", map[string]string{"env": "prod"}),
	).Build()
	useCreateClient(t, c)

	opts := validCreateOptions(time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC))
	opts.plan, opts.selector = "plan-a", ""
	opts.days = "MON"
	opts.dryRun = true
	opts.root.JsonOutput = true
	opts.generateSuffix = fixedSuffix("a1b2c3d4")

	ctx, buf := createTestContext()
	require.NoError(t, runCreate(ctx, opts, "maintenance"))

	var got struct {
		Items []struct {
			Name      string `json:"name"`
			Plan      string `json:"plan"`
			Type      string `json:"type"`
			ValidFrom string `json:"validFrom"`
			Windows   []struct {
				Start      string   `json:"start"`
				End        string   `json:"end"`
				DaysOfWeek []string `json:"daysOfWeek"`
			} `json:"windows"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got.Items, 1)
	assert.Equal(t, "maintenance-a1b2c3d4", got.Items[0].Name)
	assert.Equal(t, "plan-a", got.Items[0].Plan)
	assert.Equal(t, "suspend", got.Items[0].Type)
	assert.NotEmpty(t, got.Items[0].ValidFrom)
	require.Len(t, got.Items[0].Windows, 1)
	assert.Equal(t, "22:00", got.Items[0].Windows[0].Start)
	assert.Equal(t, []string{"MON"}, got.Items[0].Windows[0].DaysOfWeek)

	var exceptions hibernatorv1alpha1.ScheduleExceptionList
	require.NoError(t, c.List(context.Background(), &exceptions))
	assert.Empty(t, exceptions.Items)
}

func TestRunCreatePersistsLeadTimeForSuspend(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		plan("plan-a", "team-a", nil),
	).Build()
	useCreateClient(t, c)

	opts := validCreateOptions(time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC))
	opts.plan, opts.selector = "plan-a", ""
	opts.leadTime = "1h"
	opts.generateSuffix = fixedSuffix("a1b2c3d4")

	ctx, _ := createTestContext()
	require.NoError(t, runCreate(ctx, opts, "maintenance"))

	got := &hibernatorv1alpha1.ScheduleException{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "maintenance-a1b2c3d4"}, got))
	assert.Equal(t, "1h", got.Spec.LeadTime)
}

func TestRunCreateRejectsLeadTimeForNonSuspend(t *testing.T) {
	clientCalls := 0
	restoreClientFactory(t, func(*common.RootOptions) (client.Client, error) {
		clientCalls++
		return nil, errors.New("client must not be created")
	})

	now := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)

	nonSuspend := validCreateOptions(now)
	nonSuspend.exceptionType = "extend"
	nonSuspend.leadTime = "30m"
	require.ErrorContains(t, runCreate(context.Background(), nonSuspend, "maintenance"), "only valid with --type suspend")

	badDuration := validCreateOptions(now)
	badDuration.leadTime = "soon"
	require.ErrorContains(t, runCreate(context.Background(), badDuration, "maintenance"), "invalid --lead-time")

	assert.Zero(t, clientCalls)
}

func TestCreateOutputFlagRegistered(t *testing.T) {
	cmd := newCreateCommand(&common.RootOptions{})
	flag := cmd.Flags().Lookup("output")
	require.NotNil(t, flag)
	assert.Equal(t, "o", flag.Shorthand)
}

func TestRunCreateDryRunYAMLPrintsManifestsWithoutCreating(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		plan("plan-a", "team-a", map[string]string{"env": "prod"}),
		plan("plan-b", "team-a", map[string]string{"env": "prod"}),
	).Build()
	useCreateClient(t, c)

	opts := validCreateOptions(time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC))
	opts.dryRun = true
	opts.format = "yaml"
	opts.days = "MON"
	opts.generateSuffix = fixedSuffix("a1b2c3d4")

	ctx, buf := createTestContext()
	require.NoError(t, runCreate(ctx, opts, "maintenance"))

	out := buf.String()
	assert.Contains(t, out, "kind: ScheduleException")
	assert.Contains(t, out, "apiVersion: hibernator.ardikabs.com/v1alpha1")
	assert.Contains(t, out, "maintenance-a1b2c3d4")
	assert.Contains(t, out, "plan-a")
	assert.Contains(t, out, "plan-b")
	assert.Contains(t, out, "22:00")

	var exceptions hibernatorv1alpha1.ScheduleExceptionList
	require.NoError(t, c.List(context.Background(), &exceptions))
	assert.Empty(t, exceptions.Items)
}

func TestRunCreateDryRunYAMLIgnoresGlobalJSON(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		plan("plan-a", "team-a", map[string]string{"env": "prod"}),
	).Build()
	useCreateClient(t, c)

	opts := validCreateOptions(time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC))
	opts.plan, opts.selector = "plan-a", ""
	opts.dryRun = true
	opts.format = "yaml"
	opts.root.JsonOutput = true
	opts.generateSuffix = fixedSuffix("a1b2c3d4")

	ctx, buf := createTestContext()
	require.NoError(t, runCreate(ctx, opts, "maintenance"))
	assert.Contains(t, buf.String(), "kind: ScheduleException")
}

func TestRunCreateWarnsAndIgnoresOutputWithoutDryRun(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		plan("plan-a", "team-a", map[string]string{"env": "prod"}),
	).Build()
	useCreateClient(t, c)

	opts := validCreateOptions(time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC))
	opts.plan, opts.selector = "plan-a", ""
	opts.format = "yaml"
	opts.generateSuffix = fixedSuffix("a1b2c3d4")

	ctx, buf := createTestContext()
	require.NoError(t, runCreate(ctx, opts, "maintenance"))

	out := buf.String()
	assert.Contains(t, out, "ignoring")
	assert.Contains(t, out, "SUCCESS")

	got := &hibernatorv1alpha1.ScheduleException{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "maintenance-a1b2c3d4"}, got))
}

func TestRunCreateRejectsUnsupportedOutput(t *testing.T) {
	clientCalls := 0
	restoreClientFactory(t, func(*common.RootOptions) (client.Client, error) {
		clientCalls++
		return nil, errors.New("client must not be created")
	})

	opts := validCreateOptions(time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC))
	opts.dryRun = true
	opts.format = "xml"

	require.ErrorContains(t, runCreate(context.Background(), opts, "maintenance"), `unsupported --output "xml"`)
	assert.Zero(t, clientCalls)
}

func TestCreateProvenanceFlagsRegistered(t *testing.T) {
	cmd := newCreateCommand(&common.RootOptions{})
	purpose := cmd.Flags().Lookup("purpose")
	require.NotNil(t, purpose)
	assert.Equal(t, "", purpose.DefValue)
	managedBy := cmd.Flags().Lookup("managed-by")
	require.NotNil(t, managedBy)
	assert.Equal(t, "hibernator-cli", managedBy.DefValue)
}

func TestRunCreatePersistsPurposeAndManagedBy(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		plan("plan-a", "team-a", nil),
	).Build()
	useCreateClient(t, c)

	opts := validCreateOptions(time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC))
	opts.plan, opts.selector = "plan-a", ""
	opts.purpose = "Ramadan support"
	opts.managedBy = "gitops"
	opts.generateSuffix = fixedSuffix("a1b2c3d4")

	ctx, _ := createTestContext()
	require.NoError(t, runCreate(ctx, opts, "maintenance"))

	got := &hibernatorv1alpha1.ScheduleException{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "maintenance-a1b2c3d4"}, got))
	assert.Equal(t, "Ramadan support", got.Annotations[wellknown.AnnotationPurpose])
	assert.Equal(t, "gitops", got.Labels[managedByLabelKey])
}

func TestRunCreateOmitsEmptyPurposeAndManagedBy(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		plan("plan-a", "team-a", nil),
	).Build()
	useCreateClient(t, c)

	opts := validCreateOptions(time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC))
	opts.plan, opts.selector = "plan-a", ""
	opts.purpose = ""
	opts.managedBy = ""
	opts.generateSuffix = fixedSuffix("a1b2c3d4")

	ctx, _ := createTestContext()
	require.NoError(t, runCreate(ctx, opts, "maintenance"))

	got := &hibernatorv1alpha1.ScheduleException{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "maintenance-a1b2c3d4"}, got))
	assert.NotContains(t, got.Annotations, wellknown.AnnotationPurpose)
	assert.NotContains(t, got.Labels, managedByLabelKey)
	assert.Equal(t, "plan-a", got.Labels[wellknown.LabelPlan])
}

func TestRunCreateManagedByOverwritesPlanCopiedValue(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		plan("plan-a", "team-a", map[string]string{"env": "prod", "app.kubernetes.io/managed-by": "helm"}),
	).Build()
	useCreateClient(t, c)

	opts := validCreateOptions(time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC))
	opts.plan, opts.selector = "", "app.kubernetes.io/managed-by=helm,env=prod"
	opts.managedBy = "hibernator-cli"
	opts.generateSuffix = fixedSuffix("a1b2c3d4")

	ctx, _ := createTestContext()
	require.NoError(t, runCreate(ctx, opts, "maintenance"))

	got := &hibernatorv1alpha1.ScheduleException{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "maintenance-a1b2c3d4"}, got))
	assert.Equal(t, "hibernator-cli", got.Labels[managedByLabelKey])
	assert.Equal(t, "prod", got.Labels["env"])
	assert.Equal(t, "plan-a", got.Labels[wellknown.LabelPlan])
}

func TestRunCreateRejectsInvalidManagedByBeforeClient(t *testing.T) {
	clientCalls := 0
	restoreClientFactory(t, func(*common.RootOptions) (client.Client, error) {
		clientCalls++
		return nil, errors.New("client must not be created")
	})

	opts := validCreateOptions(time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC))
	opts.managedBy = "not a valid label value!"

	require.ErrorContains(t, runCreate(context.Background(), opts, "maintenance"), "invalid --managed-by")
	assert.Zero(t, clientCalls)
}

func TestRunCreateDryRunJSONIncludesAnnotations(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		plan("plan-a", "team-a", nil),
	).Build()
	useCreateClient(t, c)

	opts := validCreateOptions(time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC))
	opts.plan, opts.selector = "plan-a", ""
	opts.purpose = "Ramadan support"
	opts.managedBy = "hibernator-cli"
	opts.dryRun = true
	opts.root.JsonOutput = true
	opts.generateSuffix = fixedSuffix("a1b2c3d4")

	ctx, buf := createTestContext()
	require.NoError(t, runCreate(ctx, opts, "maintenance"))

	var got struct {
		Items []struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got.Items, 1)
	assert.Equal(t, "Ramadan support", got.Items[0].Annotations[wellknown.AnnotationPurpose])
}

func TestRunCreateRecreationCommandIncludesProvenance(t *testing.T) {
	base := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(
		plan("plan-a", "team-a", map[string]string{"env": "prod"}),
	).Build()
	c := &failingCreateClient{Client: base, failPlan: "plan-a", err: errors.New("admission denied")}
	useCreateClient(t, c)

	opts := validCreateOptions(time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC))
	opts.purpose = "Ramadan support"
	opts.managedBy = "gitops"
	opts.generateSuffix = fixedSuffix("a1b2c3d4")

	ctx, _ := createTestContext()
	err := runCreate(ctx, opts, "maintenance")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `--purpose "Ramadan support"`)
	assert.Contains(t, err.Error(), "--managed-by gitops")
}

func TestRunCreateUsesJSONOutputMode(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(plan("plan-a", "team-a", nil)).Build()
	useCreateClient(t, c)

	opts := validCreateOptions(time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC))
	opts.root.JsonOutput = true
	opts.plan, opts.selector = "plan-a", ""
	opts.generateSuffix = fixedSuffix("a1b2c3d4")
	ctx, buf := createTestContext()

	require.NoError(t, runCreate(ctx, opts, "maintenance"))
	assert.JSONEq(t, `{
		"action":"create",
		"items":[{"plan":"plan-a","name":"maintenance-a1b2c3d4","result":"SUCCESS","message":"created"}]
	}`, buf.String())
}

func createCommandArgs() []string {
	return []string{
		"maintenance",
		"--type", "suspend",
		"--from", "in 1 hour",
		"--until", "in 2 hours",
		"--window-start", "22:00",
		"--window-end", "06:00",
		"--selector", "env=prod",
	}
}

func removeFlag(args []string, flag string) []string {
	result := append([]string(nil), args...)
	for i := 0; i < len(result); i++ {
		if result[i] == flag {
			return append(result[:i], result[i+2:]...)
		}
	}
	return result
}

func validCreateOptions(now time.Time) *createOptions {
	return &createOptions{
		root:           &common.RootOptions{Namespace: "team-a"},
		selector:       "env=prod",
		exceptionType:  "suspend",
		from:           "in 1 hour",
		until:          "in 2 hours",
		windowStart:    "22:00",
		windowEnd:      "06:00",
		now:            func() time.Time { return now },
		generateSuffix: fixedSuffix("a1b2c3d4"),
	}
}

func fixedSuffix(value string) suffixGenerator {
	return func(int) (string, error) { return value, nil }
}

func createTestContext() (context.Context, *bytes.Buffer) {
	var buf bytes.Buffer
	ctx := output.WithFormatter(context.Background(), output.NewFormatter(&buf, &buf))
	return output.WithWriter(ctx, &buf), &buf
}

func useCreateClient(t *testing.T, c client.Client) {
	t.Helper()
	restoreClientFactory(t, func(*common.RootOptions) (client.Client, error) { return c, nil })
}

func restoreClientFactory(t *testing.T, factory func(*common.RootOptions) (client.Client, error)) {
	t.Helper()
	previous := common.ClientFactory
	common.ClientFactory = factory
	t.Cleanup(func() { common.ClientFactory = previous })
}

type failingCreateClient struct {
	client.Client
	failPlan       string
	err            error
	attemptedPlans []string
}

func (c *failingCreateClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	exception, ok := obj.(*hibernatorv1alpha1.ScheduleException)
	if !ok {
		return c.Client.Create(ctx, obj, opts...)
	}
	c.attemptedPlans = append(c.attemptedPlans, exception.Spec.PlanRef.Name)
	if exception.Spec.PlanRef.Name == c.failPlan {
		return c.err
	}
	return c.Client.Create(ctx, obj, opts...)
}

var _ client.Client = (*failingCreateClient)(nil)

type capturingCreateClient struct {
	client.Client
	created *hibernatorv1alpha1.ScheduleException
}

func (c *capturingCreateClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if exception, ok := obj.(*hibernatorv1alpha1.ScheduleException); ok {
		c.created = exception.DeepCopy()
	}
	return c.Client.Create(ctx, obj, opts...)
}

var _ client.Client = (*capturingCreateClient)(nil)

type countingCreateClient struct {
	client.Client
	createCalls int
}

func (c *countingCreateClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	c.createCalls++
	return c.Client.Create(ctx, obj, opts...)
}

var _ client.Client = (*countingCreateClient)(nil)
