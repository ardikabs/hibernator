/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package printers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestExceptionListConsole(t *testing.T) {
	created := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	first := exceptionForPrinter("z-maintenance", "operations", created)
	second := exceptionForPrinter("a-maintenance", "platform", created.Add(time.Hour))
	out := &ExceptionListOutput{Items: []hibernatorv1alpha1.ScheduleException{first, second}}

	var buf bytes.Buffer
	require.NoError(t, (&ConsolePrinter{}).PrintObj(out, &buf))

	expected := fmt.Sprintf(
		"NAME           PLAN       TYPE     STATE   VALID FROM           VALID UNTIL          AGE\n"+
			"z-maintenance  plan-main  suspend  Active  %s  %s  %s\n"+
			"a-maintenance  plan-main  suspend  Active  %s  %s  %s\n",
		formatLocalTime(first.Spec.ValidFrom.Time),
		formatLocalTime(first.Spec.ValidUntil.Time),
		FormatAge(time.Since(first.CreationTimestamp.Time)),
		formatLocalTime(second.Spec.ValidFrom.Time),
		formatLocalTime(second.Spec.ValidUntil.Time),
		FormatAge(time.Since(second.CreationTimestamp.Time)),
	)
	assert.Equal(t, expected, buf.String())
}

func TestExceptionListConsoleAllNamespaces(t *testing.T) {
	exception := exceptionForPrinter(
		"maintenance",
		"operations",
		time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
	)
	out := &ExceptionListOutput{
		Items:         []hibernatorv1alpha1.ScheduleException{exception},
		AllNamespaces: true,
	}

	var buf bytes.Buffer
	require.NoError(t, (&ConsolePrinter{}).PrintObj(out, &buf))

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, []string{"NAME", "NAMESPACE", "PLAN", "TYPE", "STATE", "VALID", "FROM", "VALID", "UNTIL", "AGE"}, strings.Fields(lines[0]))
	assert.Equal(t, "maintenance", strings.Fields(lines[1])[0])
	assert.Equal(t, "operations", strings.Fields(lines[1])[1])
}

func TestExceptionStatusConsole(t *testing.T) {
	exception := exceptionForPrinter(
		"maintenance",
		"operations",
		time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
	)
	appliedAt := metav1.NewTime(time.Date(2026, time.February, 3, 4, 5, 6, 0, time.FixedZone("UTC+7", 7*60*60)))
	expiredAt := metav1.NewTime(time.Date(2026, time.February, 4, 4, 5, 6, 0, time.UTC))
	detachedAt := metav1.NewTime(time.Date(2026, time.February, 5, 4, 5, 6, 0, time.UTC))
	exception.Status.AppliedAt = &appliedAt
	exception.Status.ExpiredAt = &expiredAt
	exception.Status.DetachedAt = &detachedAt
	exception.Status.Message = "plan was removed"

	missingPlan := exceptionForPrinter("detached", "operations", exception.CreationTimestamp.Time)
	missingPlan.Spec.Windows = nil
	missingPlan.Spec.ValidFrom = metav1.Time{}
	missingPlan.Spec.ValidUntil = metav1.Time{}
	missingPlan.Spec.Type = ""
	missingPlan.Status.State = ""

	out := &ExceptionStatusOutput{Items: []ExceptionStatusItem{
		{Exception: exception, PlanPhase: "Hibernated", Timing: "2h remaining", PlanExists: true},
		{Exception: missingPlan, Timing: "expired", PlanExists: false},
	}}

	var buf bytes.Buffer
	require.NoError(t, (&ConsolePrinter{}).PrintObj(out, &buf))
	got := buf.String()

	for _, want := range []string{
		"Name:        maintenance",
		"Namespace:   operations",
		"Plan:        plan-main",
		"Plan Phase:  Hibernated",
		"Type:        suspend",
		"State:       Active",
		"Valid From:  " + formatLocalTime(exception.Spec.ValidFrom.Time),
		"Valid Until: " + formatLocalTime(exception.Spec.ValidUntil.Time),
		"Timing:      2h remaining",
		"22:00 - 06:00 [MON, TUE]",
		"Applied At:  " + formatLocalTime(appliedAt.Time),
		"Expired At:  " + formatLocalTime(expiredAt.Time),
		"Detached At: " + formatLocalTime(detachedAt.Time),
		"Message:     plan was removed",
		"Name:        detached",
		"Plan Phase:  unavailable",
		"Type:        -",
		"State:       -",
		"Valid From:  -",
		"Valid Until: -",
		"Windows:\n  (none)",
		"Message:     -",
	} {
		assert.Contains(t, got, want)
	}
	assert.Less(t, strings.Index(got, "Name:        maintenance"), strings.Index(got, "Name:        detached"))
}

func TestExceptionOperationConsole(t *testing.T) {
	out := &ExceptionOperationOutput{
		Action: "delete",
		Items: []ExceptionOperationResult{
			{Plan: "plan-a", Name: "maintenance-a", Result: "deleted", Message: ""},
			{Plan: "plan-b", Name: "maintenance-b", Result: "failed", Message: "forbidden"},
		},
	}

	var buf bytes.Buffer
	require.NoError(t, (&ConsolePrinter{}).PrintObj(out, &buf))
	assert.Equal(t,
		"PLAN    EXCEPTION      RESULT   MESSAGE\n"+
			"plan-a  maintenance-a  deleted  \n"+
			"plan-b  maintenance-b  failed   forbidden\n",
		buf.String(),
	)
}

func TestExceptionListJSON(t *testing.T) {
	created := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.FixedZone("UTC+7", 7*60*60))
	exception := exceptionForPrinter("maintenance", "operations", created)
	out := &ExceptionListOutput{Items: []hibernatorv1alpha1.ScheduleException{exception}}
	converted := (&JSONPrinter{}).exceptionListToJSON(out)
	assert.Equal(t, []OffHourWindowJSON{{
		Start: "22:00", End: "06:00", DaysOfWeek: []string{"MON", "TUE"},
	}}, converted.Items[0].Windows)

	got := printExceptionJSON(t, out)
	item := got["items"].([]any)[0].(map[string]any)

	assert.Equal(t, "maintenance", item["name"])
	assert.Equal(t, "operations", item["namespace"])
	assert.Equal(t, map[string]any{"env": "prod"}, item["labels"])
	assert.Equal(t, "plan-main", item["plan"])
	assert.Equal(t, "suspend", item["type"])
	assert.Equal(t, "Active", item["state"])
	assert.Equal(t, "2026-02-03T04:05:06Z", item["validFrom"])
	assert.Equal(t, "2026-02-04T05:06:07Z", item["validUntil"])
	assert.Equal(t, "2026-01-01T20:04:05Z", item["createdAt"])
	assert.Equal(t, map[string]any{
		"start":      "22:00",
		"end":        "06:00",
		"daysOfWeek": []any{"MON", "TUE"},
	}, item["windows"].([]any)[0])
	assert.NotContains(t, got, "allNamespaces")
}

func TestExceptionStatusJSON(t *testing.T) {
	exception := exceptionForPrinter(
		"maintenance",
		"operations",
		time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
	)
	appliedAt := metav1.NewTime(time.Date(2026, time.February, 3, 4, 5, 6, 0, time.FixedZone("UTC+7", 7*60*60)))
	expiredAt := metav1.NewTime(time.Date(2026, time.February, 4, 4, 5, 6, 0, time.UTC))
	detachedAt := metav1.NewTime(time.Date(2026, time.February, 5, 4, 5, 6, 0, time.UTC))
	exception.Status.AppliedAt = &appliedAt
	exception.Status.ExpiredAt = &expiredAt
	exception.Status.DetachedAt = &detachedAt
	exception.Status.Message = "plan was removed"
	out := &ExceptionStatusOutput{Items: []ExceptionStatusItem{{
		Exception: exception, PlanPhase: "Hibernated", Timing: "2h remaining", PlanExists: true,
	}}}
	converted := (&JSONPrinter{}).exceptionStatusToJSON(out)
	assert.Equal(t, []OffHourWindowJSON{{
		Start: "22:00", End: "06:00", DaysOfWeek: []string{"MON", "TUE"},
	}}, converted.Items[0].Windows)

	got := printExceptionJSON(t, out)
	item := got["items"].([]any)[0].(map[string]any)

	assert.Equal(t, "Hibernated", item["planPhase"])
	assert.Equal(t, true, item["planExists"])
	assert.Equal(t, "2h remaining", item["timing"])
	assert.Equal(t, "2026-02-02T21:05:06Z", item["appliedAt"])
	assert.Equal(t, "2026-02-04T04:05:06Z", item["expiredAt"])
	assert.Equal(t, "2026-02-05T04:05:06Z", item["detachedAt"])
	assert.Equal(t, "plan was removed", item["message"])
	assert.Equal(t, "22:00", item["windows"].([]any)[0].(map[string]any)["start"])
}

func TestExceptionJSONEmptyWindowsEncodeAsArray(t *testing.T) {
	exception := exceptionForPrinter(
		"maintenance",
		"operations",
		time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
	)
	exception.Spec.Windows = nil

	list := printExceptionJSON(t, &ExceptionListOutput{
		Items: []hibernatorv1alpha1.ScheduleException{exception},
	})
	listItem := list["items"].([]any)[0].(map[string]any)
	assert.Equal(t, []any{}, listItem["windows"])

	status := printExceptionJSON(t, &ExceptionStatusOutput{
		Items: []ExceptionStatusItem{{Exception: exception}},
	})
	statusItem := status["items"].([]any)[0].(map[string]any)
	assert.Equal(t, []any{}, statusItem["windows"])
}

func TestExceptionOperationJSON(t *testing.T) {
	out := &ExceptionOperationOutput{
		Action: "create",
		Items: []ExceptionOperationResult{{
			Plan: "plan-main", Name: "maintenance", Result: "created", Message: "ok",
		}},
	}
	converted := (&JSONPrinter{}).exceptionOperationToJSON(out)
	assert.Equal(t, []ExceptionOperationResultJSON{{
		Plan: "plan-main", Name: "maintenance", Result: "created", Message: "ok",
	}}, converted.Items)

	got := printExceptionJSON(t, out)
	assert.Equal(t, "create", got["action"])
	assert.Equal(t, map[string]any{
		"plan": "plan-main", "name": "maintenance", "result": "created", "message": "ok",
	}, got["items"].([]any)[0])
}

func exceptionForPrinter(name, namespace string, created time.Time) hibernatorv1alpha1.ScheduleException {
	return hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         namespace,
			Labels:            map[string]string{"env": "prod"},
			CreationTimestamp: metav1.NewTime(created),
		},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			PlanRef:    hibernatorv1alpha1.PlanReference{Name: "plan-main"},
			ValidFrom:  metav1.NewTime(time.Date(2026, time.February, 3, 4, 5, 6, 0, time.UTC)),
			ValidUntil: metav1.NewTime(time.Date(2026, time.February, 4, 5, 6, 7, 0, time.UTC)),
			Type:       hibernatorv1alpha1.ExceptionSuspend,
			Windows: []hibernatorv1alpha1.OffHourWindow{{
				Start: "22:00", End: "06:00", DaysOfWeek: []string{"MON", "TUE"},
			}},
		},
		Status: hibernatorv1alpha1.ScheduleExceptionStatus{State: hibernatorv1alpha1.ExceptionStateActive},
	}
}

func printExceptionJSON(t *testing.T, obj any) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (&JSONPrinter{}).PrintObj(obj, &buf))

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	return got
}
