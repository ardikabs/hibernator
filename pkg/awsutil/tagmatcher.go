/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package awsutil

import (
	"fmt"
	"path"

	"k8s.io/apimachinery/pkg/util/validation/field"
)

// Match checks if the given resource tags satisfy the selector.
// All requirements in the selector are ANDed together.
func Match(resourceTags map[string]string, selector *TagSelector) bool {
	if selector == nil {
		return true
	}

	// MatchTags are ANDed together
	for key, value := range selector.MatchTags {
		if !matchTag(resourceTags, key, value) {
			return false
		}
	}

	// MatchExpressions are ANDed together
	for _, expr := range selector.MatchExpressions {
		if !matchExpression(resourceTags, expr) {
			return false
		}
	}

	return true
}

// matchTag checks if a resource has the given tag key and (optionally) value.
// An empty value means match any value for that key (Exists semantics).
func matchTag(resourceTags map[string]string, key, value string) bool {
	resourceValue, hasKey := resourceTags[key]
	if !hasKey {
		return false
	}
	if value != "" && resourceValue != value {
		return false
	}
	return true
}

// matchExpression checks a single expression requirement against resource tags.
func matchExpression(resourceTags map[string]string, expr TagSelectorRequirement) bool {
	resourceValue, hasKey := resourceTags[expr.Key]

	switch expr.Operator {
	case "In":
		if !hasKey {
			return false
		}
		for _, v := range expr.Values {
			if resourceValue == v {
				return true
			}
		}
		return false

	case "NotIn":
		if !hasKey {
			return true
		}
		for _, v := range expr.Values {
			if resourceValue == v {
				return false
			}
		}
		return true

	case "Exists":
		return hasKey

	case "DoesNotExist":
		return !hasKey

	case "Matches":
		if !hasKey {
			return false
		}
		for _, pattern := range expr.Values {
			matched, err := path.Match(pattern, resourceValue)
			if err == nil && matched {
				return true
			}
		}
		return false

	case "NotMatches":
		if !hasKey {
			return true
		}
		for _, pattern := range expr.Values {
			matched, err := path.Match(pattern, resourceValue)
			if err == nil && matched {
				return false
			}
		}
		return true

	default:
		// Unknown operator: treat as not matching for safety
		return false
	}
}

// ValidateTagSelector validates a TagSelector structure.
// It mimics the Kubernetes metav1.LabelSelector validation pattern using field.ErrorList.
func ValidateTagSelector(selector *TagSelector, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	if selector == nil {
		return allErrs
	}

	for i, expr := range selector.MatchExpressions {
		allErrs = append(allErrs, validateExpression(expr, fldPath.Child("matchExpressions").Index(i))...)
	}

	return allErrs
}

func validateExpression(expr TagSelectorRequirement, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList

	if expr.Key == "" {
		allErrs = append(allErrs, field.Required(fldPath.Child("key"), ""))
	}

	switch expr.Operator {
	case "In", "NotIn", "Matches", "NotMatches":
		if len(expr.Values) == 0 {
			allErrs = append(allErrs, field.Required(fldPath.Child("values"),
				fmt.Sprintf("operator %q requires at least one value", expr.Operator)))
		}
	case "Exists", "DoesNotExist":
		if len(expr.Values) > 0 {
			allErrs = append(allErrs, field.Forbidden(fldPath.Child("values"),
				fmt.Sprintf("operator %q does not accept values", expr.Operator)))
		}
	default:
		allErrs = append(allErrs, field.NotSupported(fldPath.Child("operator"), expr.Operator,
			[]string{"In", "NotIn", "Exists", "DoesNotExist", "Matches", "NotMatches"}))
	}

	// Validate glob patterns for Matches/NotMatches
	if expr.Operator == "Matches" || expr.Operator == "NotMatches" {
		for i, pattern := range expr.Values {
			if _, err := path.Match(pattern, ""); err != nil {
				allErrs = append(allErrs, field.Invalid(fldPath.Child("values").Index(i), pattern,
					fmt.Sprintf("invalid glob pattern: %v", err)))
			}
		}
	}

	return allErrs
}

// ToTagSelector converts a legacy map[string]string tag filter into a TagSelector.
// This is useful for backward compatibility.
func ToTagSelector(tags map[string]string) *TagSelector {
	if len(tags) == 0 {
		return nil
	}

	selector := &TagSelector{
		MatchTags: make(map[string]string, len(tags)),
	}
	for k, v := range tags {
		selector.MatchTags[k] = v
	}
	return selector
}
