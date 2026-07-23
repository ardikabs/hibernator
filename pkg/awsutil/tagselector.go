/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package awsutil

// TagSelector defines how to select AWS resources by tags.
type TagSelector struct {
	// MatchTags is a map of {key,value} pairs. A single {key,value} is equivalent to
	// a MatchExpression with operator "In" and a single value.
	// Empty value means match any value for that key (Exists semantics).
	MatchTags map[string]string `json:"matchTags,omitempty"`

	// MatchExpressions is a list of tag selector requirements. The requirements are ANDed.
	MatchExpressions []TagSelectorRequirement `json:"matchExpressions,omitempty"`
}

// TagSelectorRequirement is a selector that contains values, a key, and an operator that
// relates the key and values.
//
// Operator semantics:
//   - `In`, `NotIn`, `Exists`, `DoesNotExist` behave like Kubernetes LabelSelector matchExpressions:
//     In/NotIn use exact string equality; `Exists`/`DoesNotExist` test key presence/absence.
//   - `Matches` and `NotMatches` use [`path.Match`](https://pkg.go.dev/path#Match) glob pattern matching instead of exact equality.
//     Supported syntax: `*` (any sequence), `?` (any single character), `[abc]` (character class),
//     `[a-z]` (range), and negation with `^` or `!` inside brackets.
//     Example: operator `Matches`, values: `["prod-*", "v1.?"]` matches `"prod-api"` and `"v1.2"`
//     but not `"staging-api"` or `"v1.23"`.
//
// For Matches and NotMatches, the Values array must contain at least one valid glob pattern.
type TagSelectorRequirement struct {
	// Key is the tag key that the selector applies to.
	Key string `json:"key"`

	// Operator represents a key's relationship to a set of values.
	// Valid operators are `In`, `NotIn`, `Exists`, `DoesNotExist`, `Matches`, and `NotMatches`.
	Operator string `json:"operator"`

	// Values is an array of string values. If the operator is `In`, `NotIn`, `Matches`, or `NotMatches`,
	// the values array must be non-empty. If the operator is `Exists` or `DoesNotExist`,
	// the values array must be empty.
	Values []string `json:"values,omitempty"`
}
