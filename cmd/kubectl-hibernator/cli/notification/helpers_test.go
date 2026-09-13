/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSelectorMatchesPlan(t *testing.T) {
	sel := metav1.LabelSelector{MatchLabels: map[string]string{"env": "prod"}}

	require.True(t, selectorMatchesPlan(sel, map[string]string{"env": "prod", "team": "a"}))
	require.False(t, selectorMatchesPlan(sel, map[string]string{"env": "dev"}))
	require.False(t, selectorMatchesPlan(sel, nil))

	// Empty selector matches everything, including unlabeled plans.
	require.True(t, selectorMatchesPlan(metav1.LabelSelector{}, nil))

	// Invalid selector never matches (mirrors controller fail-closed).
	bad := metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
		{Key: "env", Operator: "Bogus", Values: []string{"prod"}},
	}}
	require.False(t, selectorMatchesPlan(bad, map[string]string{"env": "prod"}))
}
