/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package exception

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

const (
	generatedSuffixLength = 8
	maxExceptionPrefixLen = 54
	hibernatorLabelDomain = "hibernator.ardikabs.com"
	alphanumericAlphabet  = "abcdefghijklmnopqrstuvwxyz0123456789"
	// managedByLabelKey is the standard Kubernetes managed-by label, used to
	// record which tool created a ScheduleException. It is a label (not an
	// annotation) so ownership stays visible to label-based tooling.
	managedByLabelKey = "app.kubernetes.io/managed-by"
	// defaultManagedBy is recorded when --managed-by is not supplied.
	defaultManagedBy = "hibernator-cli"
)

var (
	allDays = []string{
		"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN",
	}
	strictTimePattern = regexp.MustCompile(`^[0-9]{2}:[0-9]{2}$`)
	suffixPattern     = regexp.MustCompile(`^[a-z0-9]{8}$`)
)

type suffixGenerator func(int) (string, error)

func parseDays(value string) ([]string, error) {
	if value == "" {
		return append([]string(nil), allDays...), nil
	}

	fullDays := []string{
		"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday",
	}
	aliases := make(map[string]string, len(allDays)*2)
	for i, day := range allDays {
		aliases[strings.ToLower(day)] = day
		aliases[strings.ToLower(fullDays[i])] = day
	}

	days := make([]string, 0, len(allDays))
	seen := make(map[string]struct{}, len(allDays))
	addDay := func(raw, day string) error {
		if _, duplicate := seen[day]; duplicate {
			return fmt.Errorf("duplicate day %q", raw)
		}
		seen[day] = struct{}{}
		days = append(days, day)
		return nil
	}
	for _, raw := range strings.Split(value, ",") {
		token := strings.TrimSpace(raw)
		if !strings.Contains(token, "-") {
			day, ok := aliases[strings.ToLower(token)]
			if !ok {
				return nil, fmt.Errorf("invalid day %q", raw)
			}
			if err := addDay(raw, day); err != nil {
				return nil, err
			}
			continue
		}
		expanded, err := expandDayRange(token, aliases)
		if err != nil {
			return nil, err
		}
		for _, day := range expanded {
			if err := addDay(raw, day); err != nil {
				return nil, err
			}
		}
	}

	return days, nil
}

// expandDayRange expands a "START-END" weekday range into canonical day
// abbreviations in week order, wrapping past Sunday when the end precedes
// the start (FRI-MON → FRI,SAT,SUN,MON). A range with identical endpoints
// (MON-MON) expands to the full week. Endpoint names accept the same
// case-insensitive abbreviations and full names as single days.
func expandDayRange(token string, aliases map[string]string) ([]string, error) {
	if strings.Count(token, "-") != 1 {
		return nil, fmt.Errorf("invalid day range %q", token)
	}
	bounds := strings.SplitN(token, "-", 2)
	start, ok := aliases[strings.ToLower(strings.TrimSpace(bounds[0]))]
	if !ok {
		return nil, fmt.Errorf("invalid day %q", strings.TrimSpace(bounds[0]))
	}
	end, ok := aliases[strings.ToLower(strings.TrimSpace(bounds[1]))]
	if !ok {
		return nil, fmt.Errorf("invalid day %q", strings.TrimSpace(bounds[1]))
	}
	if start == end {
		return append([]string(nil), allDays...), nil
	}

	startIdx, endIdx := dayIndex(start), dayIndex(end)
	expanded := make([]string, 0, 7)
	for i := startIdx; ; i = (i + 1) % len(allDays) {
		expanded = append(expanded, allDays[i])
		if i == endIdx {
			break
		}
	}
	return expanded, nil
}

func dayIndex(day string) int {
	for i, name := range allDays {
		if name == day {
			return i
		}
	}
	return -1
}

func validateWindow(start, end string) error {
	if !strictTimePattern.MatchString(start) {
		return fmt.Errorf("window start %q must use HH:MM", start)
	}
	if _, err := time.Parse("15:04", start); err != nil {
		return fmt.Errorf("invalid window start %q: %w", start, err)
	}
	if !strictTimePattern.MatchString(end) {
		return fmt.Errorf("window end %q must use HH:MM", end)
	}
	if _, err := time.Parse("15:04", end); err != nil {
		return fmt.Errorf("invalid window end %q: %w", end, err)
	}
	if start == end {
		return fmt.Errorf("window start and end must differ")
	}
	return nil
}

func parseStates(value string) (map[hibernatorv1alpha1.ExceptionState]struct{}, error) {
	states := make(map[hibernatorv1alpha1.ExceptionState]struct{})
	if value == "" {
		return states, nil
	}

	valid := map[string]hibernatorv1alpha1.ExceptionState{
		"pending":  hibernatorv1alpha1.ExceptionStatePending,
		"active":   hibernatorv1alpha1.ExceptionStateActive,
		"expired":  hibernatorv1alpha1.ExceptionStateExpired,
		"detached": hibernatorv1alpha1.ExceptionStateDetached,
	}
	for _, raw := range strings.Split(value, ",") {
		state, ok := valid[strings.ToLower(strings.TrimSpace(raw))]
		if !ok {
			return nil, fmt.Errorf("invalid exception state %q", raw)
		}
		states[state] = struct{}{}
	}

	return states, nil
}

func parseExceptionTypes(value string) (map[hibernatorv1alpha1.ExceptionType]struct{}, error) {
	types := make(map[hibernatorv1alpha1.ExceptionType]struct{})
	if value == "" {
		return types, nil
	}

	valid := map[string]hibernatorv1alpha1.ExceptionType{
		"suspend": hibernatorv1alpha1.ExceptionSuspend,
		"extend":  hibernatorv1alpha1.ExceptionExtend,
		"replace": hibernatorv1alpha1.ExceptionReplace,
	}
	for _, raw := range strings.Split(value, ",") {
		typ, ok := valid[strings.ToLower(strings.TrimSpace(raw))]
		if !ok {
			return nil, fmt.Errorf("invalid exception type %q", raw)
		}
		types[typ] = struct{}{}
	}

	return types, nil
}

func compactPrefix(prefix string, max int) (string, error) {
	if max < 1 {
		return "", fmt.Errorf("maximum prefix length must be positive")
	}
	if problems := validation.IsDNS1123Subdomain(prefix); len(problems) > 0 {
		return "", fmt.Errorf("invalid exception prefix %q: %s", prefix, strings.Join(problems, "; "))
	}

	for i := len(prefix) - 1; i >= 0 && len(prefix) > max; i-- {
		if !strings.ContainsRune("aeiou", rune(prefix[i])) {
			continue
		}

		labelStart := strings.LastIndex(prefix[:i], ".") + 1
		labelEnd := len(prefix)
		if nextDot := strings.Index(prefix[i:], "."); nextDot >= 0 {
			labelEnd = i + nextDot
		}
		label := prefix[labelStart:labelEnd]
		if len(label) == 1 {
			continue
		}
		candidateLabel := label[:i-labelStart] + label[i-labelStart+1:]
		if len(validation.IsDNS1123Label(candidateLabel)) > 0 {
			continue
		}
		prefix = prefix[:i] + prefix[i+1:]
	}
	if len(prefix) > max {
		prefix = prefix[:max]
	}
	prefix = strings.TrimRight(prefix, "-.")
	if prefix == "" || len(validation.IsDNS1123Subdomain(prefix)) > 0 {
		return "", fmt.Errorf("prefix cannot be compacted to a valid non-empty DNS subdomain")
	}

	return prefix, nil
}

func buildExceptionName(prefix string, generate suffixGenerator) (string, error) {
	if generate == nil {
		return "", fmt.Errorf("suffix generator is required")
	}
	base, err := compactPrefix(prefix, maxExceptionPrefixLen)
	if err != nil {
		return "", err
	}
	suffix, err := generate(generatedSuffixLength)
	if err != nil {
		return "", fmt.Errorf("generate exception name suffix: %w", err)
	}
	if !suffixPattern.MatchString(suffix) {
		return "", fmt.Errorf("generated suffix must contain exactly %d lowercase alphanumeric characters", generatedSuffixLength)
	}

	name := base + "-" + suffix
	if problems := validation.IsDNS1123Subdomain(name); len(problems) > 0 {
		return "", fmt.Errorf("invalid generated exception name %q: %s", name, strings.Join(problems, "; "))
	}
	return name, nil
}

func randomAlphanumeric(length int) (string, error) {
	if length < 0 {
		return "", fmt.Errorf("length must not be negative")
	}

	result := make([]byte, length)
	limit := big.NewInt(int64(len(alphanumericAlphabet)))
	for i := range result {
		index, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", fmt.Errorf("generate random character: %w", err)
		}
		result[i] = alphanumericAlphabet[index.Int64()]
	}
	return string(result), nil
}

func selectPlans(
	ctx context.Context,
	c client.Client,
	namespace, planName, selectorText string,
) ([]hibernatorv1alpha1.HibernatePlan, labels.Selector, error) {
	if (planName == "") == (selectorText == "") {
		return nil, nil, fmt.Errorf("exactly one of plan name or selector is required")
	}

	if planName != "" {
		plan := &hibernatorv1alpha1.HibernatePlan{}
		if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: planName}, plan); err != nil {
			return nil, nil, fmt.Errorf("get HibernatePlan %s/%s: %w", namespace, planName, err)
		}
		return []hibernatorv1alpha1.HibernatePlan{*plan}, labels.Nothing(), nil
	}

	selector, err := labels.Parse(selectorText)
	if err != nil {
		return nil, nil, fmt.Errorf("parse plan selector %q: %w", selectorText, err)
	}
	plans := &hibernatorv1alpha1.HibernatePlanList{}
	if err := c.List(ctx, plans,
		client.InNamespace(namespace),
		client.MatchingLabelsSelector{Selector: selector},
	); err != nil {
		return nil, nil, fmt.Errorf("list HibernatePlans: %w", err)
	}
	if len(plans.Items) == 0 {
		return nil, nil, fmt.Errorf("no HibernatePlans match selector %q", selectorText)
	}
	sort.Slice(plans.Items, func(i, j int) bool {
		if plans.Items[i].Namespace == plans.Items[j].Namespace {
			return plans.Items[i].Name < plans.Items[j].Name
		}
		return plans.Items[i].Namespace < plans.Items[j].Namespace
	})
	return plans.Items, selector, nil
}

func selectExceptions(
	ctx context.Context,
	c client.Client,
	namespace, selectorText, planName string,
) ([]hibernatorv1alpha1.ScheduleException, error) {
	selector := labels.Everything()
	if selectorText != "" {
		parsed, err := labels.Parse(selectorText)
		if err != nil {
			return nil, fmt.Errorf("parse exception selector %q: %w", selectorText, err)
		}
		selector = parsed
	}

	list := &hibernatorv1alpha1.ScheduleExceptionList{}
	if err := c.List(ctx, list,
		client.InNamespace(namespace),
		client.MatchingLabelsSelector{Selector: selector},
	); err != nil {
		return nil, fmt.Errorf("list ScheduleExceptions: %w", err)
	}

	exceptions := make([]hibernatorv1alpha1.ScheduleException, 0, len(list.Items))
	for _, exception := range list.Items {
		if planName == "" || exception.Spec.PlanRef.Name == planName {
			exceptions = append(exceptions, exception)
		}
	}
	sort.Slice(exceptions, func(i, j int) bool {
		if exceptions[i].Namespace == exceptions[j].Namespace {
			return exceptions[i].Name < exceptions[j].Name
		}
		return exceptions[i].Namespace < exceptions[j].Namespace
	})
	return exceptions, nil
}

func selectorLabels(planLabels map[string]string, selector labels.Selector) map[string]string {
	result := make(map[string]string)
	if selector == nil || !selector.Matches(labels.Set(planLabels)) {
		return result
	}
	requirements, selectable := selector.Requirements()
	if !selectable {
		return result
	}

	for _, requirement := range requirements {
		key := requirement.Key()
		if domain, _, qualified := strings.Cut(key, "/"); qualified && domain == hibernatorLabelDomain {
			continue
		}
		value, exists := planLabels[key]
		if !exists || !requirement.Matches(labels.Set{key: value}) {
			continue
		}
		result[key] = value
	}
	return result
}
