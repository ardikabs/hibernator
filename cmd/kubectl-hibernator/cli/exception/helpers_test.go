/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package exception

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

func TestParseDaysDefaultsToAllDays(t *testing.T) {
	days, err := parseDays("")
	require.NoError(t, err)
	assert.Equal(t, []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"}, days)
}

func TestParseDaysNormalizesAbbreviationsAndFullNames(t *testing.T) {
	days, err := parseDays("mon, TUESDAY,Wed,thursday,FRI,sat,SUNDAY")
	require.NoError(t, err)
	assert.Equal(t, []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"}, days)
}

func TestParseDaysRejectsInvalidAndDuplicateValues(t *testing.T) {
	tests := []string{
		"Monday,Funday",
		"mon,Monday",
		"Monday,,Tuesday",
	}

	for _, value := range tests {
		t.Run(value, func(t *testing.T) {
			_, err := parseDays(value)
			require.Error(t, err)
		})
	}
}

func TestValidateWindowAcceptsDaytimeAndOvernightWindows(t *testing.T) {
	require.NoError(t, validateWindow("00:00", "23:59"))
	require.NoError(t, validateWindow("23:00", "01:00"))
}

func TestValidateWindowRejectsEqualOrNonStrictTimes(t *testing.T) {
	tests := []struct {
		name  string
		start string
		end   string
	}{
		{name: "equal", start: "12:30", end: "12:30"},
		{name: "single digit hour", start: "1:00", end: "02:00"},
		{name: "single digit minute", start: "01:0", end: "02:00"},
		{name: "hour out of range", start: "24:00", end: "02:00"},
		{name: "minute out of range", start: "01:60", end: "02:00"},
		{name: "surrounding whitespace", start: " 01:00", end: "02:00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Error(t, validateWindow(tt.start, tt.end))
		})
	}
}

func TestValidateWindowReportsStartErrorBeforeEndError(t *testing.T) {
	for range 100 {
		err := validateWindow("1:00", "2:00")
		require.EqualError(t, err, `window start "1:00" must use HH:MM`)
	}
}

func TestParseStatesNormalizesCaseInsensitiveValues(t *testing.T) {
	states, err := parseStates("pending,ACTIVE, Expired, detached")
	require.NoError(t, err)
	assert.Equal(t, map[hibernatorv1alpha1.ExceptionState]struct{}{
		hibernatorv1alpha1.ExceptionStatePending:  {},
		hibernatorv1alpha1.ExceptionStateActive:   {},
		hibernatorv1alpha1.ExceptionStateExpired:  {},
		hibernatorv1alpha1.ExceptionStateDetached: {},
	}, states)
}

func TestParseStatesHandlesEmptyAndRejectsInvalidValues(t *testing.T) {
	states, err := parseStates("")
	require.NoError(t, err)
	assert.Empty(t, states)

	_, err = parseStates("Active,unknown")
	require.Error(t, err)
}

func TestParseExceptionTypesNormalizesCaseInsensitiveValues(t *testing.T) {
	types, err := parseExceptionTypes("suspend,EXTEND, Replace")
	require.NoError(t, err)
	assert.Equal(t, map[hibernatorv1alpha1.ExceptionType]struct{}{
		hibernatorv1alpha1.ExceptionSuspend: {},
		hibernatorv1alpha1.ExceptionExtend:  {},
		hibernatorv1alpha1.ExceptionReplace: {},
	}, types)

	empty, err := parseExceptionTypes("")
	require.NoError(t, err)
	assert.Empty(t, empty)

	_, err = parseExceptionTypes("suspend,bogus")
	require.ErrorContains(t, err, "invalid exception type")
}

func TestCompactPrefixRemovesVowelsFromRightBeforeTruncating(t *testing.T) {
	compacted, err := compactPrefix("abcdefghij", 8)
	require.NoError(t, err)
	assert.Equal(t, "abcdfghj", compacted)

	compacted, err = compactPrefix("bcdf-ghjk", 5)
	require.NoError(t, err)
	assert.Equal(t, "bcdf", compacted)
}

func TestCompactPrefixPreservesSingletonTerminalLabel(t *testing.T) {
	compacted, err := compactPrefix("abcdefghij.a", 10)
	require.NoError(t, err)
	assert.Equal(t, "abcdfghj.a", compacted)
}

func TestCompactPrefixPreservesEveryDNSLabel(t *testing.T) {
	prefix := "a." + strings.Repeat("b", 60)
	compacted, err := compactPrefix(prefix, 54)
	require.NoError(t, err)
	assert.Equal(t, "a."+strings.Repeat("b", 52), compacted)
	assert.LessOrEqual(t, len(compacted), 54)
	assert.Empty(t, validation.IsDNS1123Subdomain(compacted))
}

func TestCompactPrefixRemovesEveryEligibleVowelFromTheRight(t *testing.T) {
	prefix := strings.Repeat("ab", 30)
	require.Len(t, prefix, 60)

	compacted, err := compactPrefix(prefix, 54)
	require.NoError(t, err)
	assert.Len(t, compacted, 54)
	assert.Equal(t, strings.Repeat("ab", 24)+"bbbbbb", compacted)
	assert.NotContains(t, compacted[len(compacted)-6:], "a")
	assert.Empty(t, validation.IsDNS1123Subdomain(compacted))
}

func TestCompactPrefixRejectsInvalidDNSPrefixes(t *testing.T) {
	for _, prefix := range []string{"", "UPPERCASE", "has_underscore", "-leading", "trailing-"} {
		t.Run(prefix, func(t *testing.T) {
			_, err := compactPrefix(prefix, 54)
			require.Error(t, err)
		})
	}
}

func TestBuildExceptionNameCompactsLongPrefix(t *testing.T) {
	name, err := buildExceptionName(strings.Repeat("maintenance", 7), func(length int) (string, error) {
		assert.Equal(t, generatedSuffixLength, length)
		return "a1b2c3d4", nil
	})
	require.NoError(t, err)
	assert.Len(t, name, 63)
	assert.True(t, strings.HasSuffix(name, "-a1b2c3d4"))
}

func TestBuildExceptionNameValidatesGeneratedSuffix(t *testing.T) {
	tests := []string{"short", "A1b2c3d4", "a1b2-c3d", "a1b2c3d_"}
	for _, suffix := range tests {
		t.Run(suffix, func(t *testing.T) {
			_, err := buildExceptionName("maintenance", func(int) (string, error) {
				return suffix, nil
			})
			require.Error(t, err)
		})
	}

	expected := errors.New("entropy unavailable")
	_, err := buildExceptionName("maintenance", func(int) (string, error) {
		return "", expected
	})
	require.ErrorIs(t, err, expected)
}

func TestRandomAlphanumericReturnsRequestedLowercaseAlphabet(t *testing.T) {
	value, err := randomAlphanumeric(256)
	require.NoError(t, err)
	assert.Len(t, value, 256)
	assert.Regexp(t, regexp.MustCompile(`^[a-z0-9]+$`), value)
}

func TestSelectPlansRequiresExactlyOneSelectionMode(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(common.Scheme).Build()

	_, _, err := selectPlans(context.Background(), c, "default", "", "")
	require.Error(t, err)

	_, _, err = selectPlans(context.Background(), c, "default", "plan-a", "env=prod")
	require.Error(t, err)
}

func TestSelectPlansGetsExactPlan(t *testing.T) {
	c := newFakeClient(plan("plan-a", "team-b", nil), plan("plan-b", "team-b", nil))

	plans, selector, err := selectPlans(context.Background(), c, "team-b", "plan-b", "")
	require.NoError(t, err)
	require.Len(t, plans, 1)
	assert.Equal(t, "plan-b", plans[0].Name)
	assert.False(t, selector.Matches(labels.Set{"env": "prod"}))
}

func TestSelectPlansSupportsKubernetesSelectorExpressions(t *testing.T) {
	c := newFakeClient(
		plan("api-prod", "team-b", map[string]string{"env": "prod", "tier": "api", "region": "apac"}),
		plan("worker-prod", "team-a", map[string]string{"env": "prod", "tier": "worker", "region": "emea"}),
		plan("deprecated", "team-a", map[string]string{"env": "prod", "tier": "api", "region": "apac", "deprecated": "true"}),
		plan("api-dev", "team-b", map[string]string{"env": "dev", "tier": "api"}),
	)

	tests := []struct {
		name     string
		selector string
		want     []string
	}{
		{name: "equality", selector: "env=prod", want: []string{"team-a/deprecated", "team-a/worker-prod", "team-b/api-prod"}},
		{name: "set", selector: "tier in (api,worker),env=prod", want: []string{"team-a/deprecated", "team-a/worker-prod", "team-b/api-prod"}},
		{name: "existence", selector: "region", want: []string{"team-a/deprecated", "team-a/worker-prod", "team-b/api-prod"}},
		{name: "nonexistence", selector: "!deprecated,env=prod", want: []string{"team-a/worker-prod", "team-b/api-prod"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plans, selector, err := selectPlans(context.Background(), c, "", "", tt.selector)
			require.NoError(t, err)
			assert.Equal(t, tt.want, planKeys(plans))
			assert.NotNil(t, selector)
		})
	}
}

func TestSelectPlansRejectsMalformedAndZeroMatchSelectors(t *testing.T) {
	c := newFakeClient(plan("plan-a", "default", map[string]string{"env": "prod"}))

	_, _, err := selectPlans(context.Background(), c, "default", "", "env in (")
	require.Error(t, err)

	_, _, err = selectPlans(context.Background(), c, "default", "", "env=dev")
	require.Error(t, err)

	_, _, err = selectPlans(context.Background(), c, "default", "missing-plan", "")
	require.Error(t, err)
}

func TestSelectExceptionsCombinesOptionalFiltersAndSorts(t *testing.T) {
	c := newFakeClient(
		exception("exc-c", "team-b", "plan-a", map[string]string{"env": "prod"}),
		exception("exc-b", "team-a", "plan-b", map[string]string{"env": "prod"}),
		exception("exc-a", "team-a", "plan-a", map[string]string{"env": "prod"}),
		exception("exc-dev", "team-a", "plan-a", map[string]string{"env": "dev"}),
	)

	tests := []struct {
		name     string
		selector string
		planName string
		want     []string
	}{
		{name: "no filters", want: []string{"team-a/exc-a", "team-a/exc-b", "team-a/exc-dev", "team-b/exc-c"}},
		{name: "selector", selector: "env=prod", want: []string{"team-a/exc-a", "team-a/exc-b", "team-b/exc-c"}},
		{name: "exact plan", planName: "plan-a", want: []string{"team-a/exc-a", "team-a/exc-dev", "team-b/exc-c"}},
		{name: "combined", selector: "env=prod", planName: "plan-a", want: []string{"team-a/exc-a", "team-b/exc-c"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exceptions, err := selectExceptions(context.Background(), c, "", tt.selector, tt.planName)
			require.NoError(t, err)
			assert.Equal(t, tt.want, exceptionKeys(exceptions))
		})
	}
}

func TestSelectExceptionsRejectsMalformedSelector(t *testing.T) {
	c := newFakeClient()
	_, err := selectExceptions(context.Background(), c, "default", "env in (", "")
	require.Error(t, err)
}

func TestSelectorLabelsCopiesActualRequirementValues(t *testing.T) {
	protectedKey := "hibernator.ardikabs.com/internal-data"
	selector, err := labels.Parse("env=prod,tier in (api,worker),region,!deprecated,version!=old," + wellknown.LabelPlan + "=source-plan," + protectedKey + "=protected")
	require.NoError(t, err)

	got := selectorLabels(map[string]string{
		"env":                                   "prod",
		"tier":                                  "api",
		"region":                                "apac",
		"version":                               "current",
		"unselected":                            "ignored",
		wellknown.LabelPlan:                     "source-plan",
		"hibernator.ardikabs.com/internal-data": "protected",
	}, selector)

	assert.Equal(t, map[string]string{
		"env":     "prod",
		"tier":    "api",
		"region":  "apac",
		"version": "current",
	}, got)
	assert.NotContains(t, got, wellknown.LabelPlan)
	assert.NotContains(t, got, protectedKey)
}

func TestCallerSetsCanonicalPlanLabelAfterSelectorLabelCopy(t *testing.T) {
	selector, err := labels.Parse("env=prod," + wellknown.LabelPlan + "=stale-plan")
	require.NoError(t, err)

	got := selectorLabels(map[string]string{
		"env":               "prod",
		wellknown.LabelPlan: "stale-plan",
	}, selector)
	assert.NotContains(t, got, wellknown.LabelPlan)

	got[wellknown.LabelPlan] = "selected-plan"
	assert.Equal(t, "selected-plan", got[wellknown.LabelPlan])
}

func TestSelectorLabelsDoesNotInventOrCopyContradictoryLabels(t *testing.T) {
	selector, err := labels.Parse("env=prod,!deprecated,missing!=value")
	require.NoError(t, err)

	got := selectorLabels(map[string]string{"env": "dev"}, selector)
	assert.Empty(t, got)

	selector, err = labels.Parse("env=prod,tier=api")
	require.NoError(t, err)
	got = selectorLabels(map[string]string{"env": "prod", "tier": "worker"}, selector)
	assert.Empty(t, got)
}

func newFakeClient(objs ...client.Object) client.Client {
	return fake.NewClientBuilder().WithScheme(common.Scheme).WithObjects(objs...).Build()
}

func plan(name, namespace string, planLabels map[string]string) *hibernatorv1alpha1.HibernatePlan {
	return &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: planLabels},
	}
}

func exception(name, namespace, planName string, exceptionLabels map[string]string) *hibernatorv1alpha1.ScheduleException {
	return &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: exceptionLabels},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			PlanRef: hibernatorv1alpha1.PlanReference{Name: planName},
		},
	}
}

func planKeys(plans []hibernatorv1alpha1.HibernatePlan) []string {
	keys := make([]string, 0, len(plans))
	for _, plan := range plans {
		keys = append(keys, plan.Namespace+"/"+plan.Name)
	}
	return keys
}

func exceptionKeys(exceptions []hibernatorv1alpha1.ScheduleException) []string {
	keys := make([]string, 0, len(exceptions))
	for _, exception := range exceptions {
		keys = append(keys, exception.Namespace+"/"+exception.Name)
	}
	return keys
}
