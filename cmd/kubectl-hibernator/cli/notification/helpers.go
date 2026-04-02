/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// selectorMatchesPlan evaluates whether a notification's label selector matches
// the given plan labels. This mirrors the controller's runtime matching logic.
func selectorMatchesPlan(selector metav1.LabelSelector, planLabels map[string]string) bool {
	sel, err := metav1.LabelSelectorAsSelector(&selector)
	if err != nil {
		return false
	}
	return sel.Matches(labels.Set(planLabels))
}
