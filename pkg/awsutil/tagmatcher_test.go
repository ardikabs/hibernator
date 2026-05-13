/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package awsutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func TestMatch_MatchTags(t *testing.T) {
	tests := []struct {
		name         string
		resourceTags map[string]string
		selector     *TagSelector
		want         bool
	}{
		{
			name:         "exact match",
			resourceTags: map[string]string{"env": "prod", "team": "backend"},
			selector:     &TagSelector{MatchTags: map[string]string{"env": "prod"}},
			want:         true,
		},
		{
			name:         "multiple tags all match",
			resourceTags: map[string]string{"env": "prod", "team": "backend"},
			selector:     &TagSelector{MatchTags: map[string]string{"env": "prod", "team": "backend"}},
			want:         true,
		},
		{
			name:         "one tag does not match",
			resourceTags: map[string]string{"env": "prod", "team": "backend"},
			selector:     &TagSelector{MatchTags: map[string]string{"env": "prod", "team": "frontend"}},
			want:         false,
		},
		{
			name:         "key missing",
			resourceTags: map[string]string{"env": "prod"},
			selector:     &TagSelector{MatchTags: map[string]string{"team": "backend"}},
			want:         false,
		},
		{
			name:         "empty value matches any value",
			resourceTags: map[string]string{"env": "prod"},
			selector:     &TagSelector{MatchTags: map[string]string{"env": ""}},
			want:         true,
		},
		{
			name:         "empty value key missing",
			resourceTags: map[string]string{"team": "backend"},
			selector:     &TagSelector{MatchTags: map[string]string{"env": ""}},
			want:         false,
		},
		{
			name:         "nil selector matches everything",
			resourceTags: map[string]string{"env": "prod"},
			selector:     nil,
			want:         true,
		},
		{
			name:         "empty selector matches everything",
			resourceTags: map[string]string{"env": "prod"},
			selector:     &TagSelector{},
			want:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Match(tt.resourceTags, tt.selector)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMatch_Expression_In(t *testing.T) {
	tests := []struct {
		name         string
		resourceTags map[string]string
		expr         TagSelectorRequirement
		want         bool
	}{
		{
			name:         "value in list",
			resourceTags: map[string]string{"env": "staging"},
			expr:         TagSelectorRequirement{Key: "env", Operator: "In", Values: []string{"dev", "staging", "prod"}},
			want:         true,
		},
		{
			name:         "value not in list",
			resourceTags: map[string]string{"env": "qa"},
			expr:         TagSelectorRequirement{Key: "env", Operator: "In", Values: []string{"dev", "staging", "prod"}},
			want:         false,
		},
		{
			name:         "key missing",
			resourceTags: map[string]string{"team": "backend"},
			expr:         TagSelectorRequirement{Key: "env", Operator: "In", Values: []string{"dev", "staging"}},
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selector := &TagSelector{MatchExpressions: []TagSelectorRequirement{tt.expr}}
			got := Match(tt.resourceTags, selector)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMatch_Expression_NotIn(t *testing.T) {
	tests := []struct {
		name         string
		resourceTags map[string]string
		expr         TagSelectorRequirement
		want         bool
	}{
		{
			name:         "value not in list",
			resourceTags: map[string]string{"env": "qa"},
			expr:         TagSelectorRequirement{Key: "env", Operator: "NotIn", Values: []string{"dev", "staging", "prod"}},
			want:         true,
		},
		{
			name:         "value in list",
			resourceTags: map[string]string{"env": "prod"},
			expr:         TagSelectorRequirement{Key: "env", Operator: "NotIn", Values: []string{"dev", "staging", "prod"}},
			want:         false,
		},
		{
			name:         "key missing (treated as not in)",
			resourceTags: map[string]string{"team": "backend"},
			expr:         TagSelectorRequirement{Key: "env", Operator: "NotIn", Values: []string{"dev", "staging"}},
			want:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selector := &TagSelector{MatchExpressions: []TagSelectorRequirement{tt.expr}}
			got := Match(tt.resourceTags, selector)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMatch_Expression_Exists(t *testing.T) {
	tests := []struct {
		name         string
		resourceTags map[string]string
		expr         TagSelectorRequirement
		want         bool
	}{
		{
			name:         "key exists",
			resourceTags: map[string]string{"env": "prod"},
			expr:         TagSelectorRequirement{Key: "env", Operator: "Exists"},
			want:         true,
		},
		{
			name:         "key missing",
			resourceTags: map[string]string{"team": "backend"},
			expr:         TagSelectorRequirement{Key: "env", Operator: "Exists"},
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selector := &TagSelector{MatchExpressions: []TagSelectorRequirement{tt.expr}}
			got := Match(tt.resourceTags, selector)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMatch_Expression_DoesNotExist(t *testing.T) {
	tests := []struct {
		name         string
		resourceTags map[string]string
		expr         TagSelectorRequirement
		want         bool
	}{
		{
			name:         "key missing",
			resourceTags: map[string]string{"team": "backend"},
			expr:         TagSelectorRequirement{Key: "env", Operator: "DoesNotExist"},
			want:         true,
		},
		{
			name:         "key exists",
			resourceTags: map[string]string{"env": "prod"},
			expr:         TagSelectorRequirement{Key: "env", Operator: "DoesNotExist"},
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selector := &TagSelector{MatchExpressions: []TagSelectorRequirement{tt.expr}}
			got := Match(tt.resourceTags, selector)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMatch_Expression_Matches(t *testing.T) {
	tests := []struct {
		name         string
		resourceTags map[string]string
		expr         TagSelectorRequirement
		want         bool
	}{
		{
			name:         "wildcard prefix",
			resourceTags: map[string]string{"name": "app-prod-01"},
			expr:         TagSelectorRequirement{Key: "name", Operator: "Matches", Values: []string{"app-*"}},
			want:         true,
		},
		{
			name:         "wildcard suffix",
			resourceTags: map[string]string{"name": "app-prod-01"},
			expr:         TagSelectorRequirement{Key: "name", Operator: "Matches", Values: []string{"*-01"}},
			want:         true,
		},
		{
			name:         "wildcard middle",
			resourceTags: map[string]string{"name": "app-prod-01"},
			expr:         TagSelectorRequirement{Key: "name", Operator: "Matches", Values: []string{"app-*-01"}},
			want:         true,
		},
		{
			name:         "single char wildcard",
			resourceTags: map[string]string{"name": "app1"},
			expr:         TagSelectorRequirement{Key: "name", Operator: "Matches", Values: []string{"app?"}},
			want:         true,
		},
		{
			name:         "no match",
			resourceTags: map[string]string{"name": "db-prod-01"},
			expr:         TagSelectorRequirement{Key: "name", Operator: "Matches", Values: []string{"app-*"}},
			want:         false,
		},
		{
			name:         "key missing",
			resourceTags: map[string]string{"team": "backend"},
			expr:         TagSelectorRequirement{Key: "name", Operator: "Matches", Values: []string{"app-*"}},
			want:         false,
		},
		{
			name:         "multiple patterns one matches",
			resourceTags: map[string]string{"name": "app-prod-01"},
			expr:         TagSelectorRequirement{Key: "name", Operator: "Matches", Values: []string{"db-*", "app-*", "cache-*"}},
			want:         true,
		},
		{
			name:         "exact match via glob",
			resourceTags: map[string]string{"name": "app"},
			expr:         TagSelectorRequirement{Key: "name", Operator: "Matches", Values: []string{"app"}},
			want:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selector := &TagSelector{MatchExpressions: []TagSelectorRequirement{tt.expr}}
			got := Match(tt.resourceTags, selector)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMatch_Expression_NotMatches(t *testing.T) {
	tests := []struct {
		name         string
		resourceTags map[string]string
		expr         TagSelectorRequirement
		want         bool
	}{
		{
			name:         "does not match pattern",
			resourceTags: map[string]string{"name": "db-prod-01"},
			expr:         TagSelectorRequirement{Key: "name", Operator: "NotMatches", Values: []string{"app-*"}},
			want:         true,
		},
		{
			name:         "matches pattern",
			resourceTags: map[string]string{"name": "app-prod-01"},
			expr:         TagSelectorRequirement{Key: "name", Operator: "NotMatches", Values: []string{"app-*"}},
			want:         false,
		},
		{
			name:         "key missing (treated as not matching)",
			resourceTags: map[string]string{"team": "backend"},
			expr:         TagSelectorRequirement{Key: "name", Operator: "NotMatches", Values: []string{"app-*"}},
			want:         true,
		},
		{
			name:         "none of multiple patterns match",
			resourceTags: map[string]string{"name": "lambda-prod-01"},
			expr:         TagSelectorRequirement{Key: "name", Operator: "NotMatches", Values: []string{"db-*", "app-*"}},
			want:         true,
		},
		{
			name:         "one of multiple patterns matches",
			resourceTags: map[string]string{"name": "app-prod-01"},
			expr:         TagSelectorRequirement{Key: "name", Operator: "NotMatches", Values: []string{"db-*", "app-*"}},
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selector := &TagSelector{MatchExpressions: []TagSelectorRequirement{tt.expr}}
			got := Match(tt.resourceTags, selector)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMatch_Combined(t *testing.T) {
	// MatchTags and MatchExpressions are ANDed
	resourceTags := map[string]string{"env": "prod", "team": "backend", "critical": "true"}

	selector := &TagSelector{
		MatchTags: map[string]string{"env": "prod"},
		MatchExpressions: []TagSelectorRequirement{
			{Key: "team", Operator: "In", Values: []string{"backend", "frontend"}},
			{Key: "critical", Operator: "Exists"},
		},
	}
	assert.True(t, Match(resourceTags, selector))

	// One expression fails
	selector2 := &TagSelector{
		MatchTags: map[string]string{"env": "prod"},
		MatchExpressions: []TagSelectorRequirement{
			{Key: "team", Operator: "In", Values: []string{"backend", "frontend"}},
			{Key: "critical", Operator: "DoesNotExist"}, // fails: critical exists
		},
	}
	assert.False(t, Match(resourceTags, selector2))
}

func TestValidateTagSelector(t *testing.T) {
	tests := []struct {
		name      string
		selector  *TagSelector
		wantError bool
	}{
		{
			name:      "nil selector is valid",
			selector:  nil,
			wantError: false,
		},
		{
			name:      "empty selector is valid",
			selector:  &TagSelector{},
			wantError: false,
		},
		{
			name: "valid In expression",
			selector: &TagSelector{
				MatchExpressions: []TagSelectorRequirement{
					{Key: "env", Operator: "In", Values: []string{"prod"}},
				},
			},
			wantError: false,
		},
		{
			name: "In without values is invalid",
			selector: &TagSelector{
				MatchExpressions: []TagSelectorRequirement{
					{Key: "env", Operator: "In", Values: []string{}},
				},
			},
			wantError: true,
		},
		{
			name: "valid Exists expression",
			selector: &TagSelector{
				MatchExpressions: []TagSelectorRequirement{
					{Key: "env", Operator: "Exists"},
				},
			},
			wantError: false,
		},
		{
			name: "Exists with values is invalid",
			selector: &TagSelector{
				MatchExpressions: []TagSelectorRequirement{
					{Key: "env", Operator: "Exists", Values: []string{"prod"}},
				},
			},
			wantError: true,
		},
		{
			name: "empty key is invalid",
			selector: &TagSelector{
				MatchExpressions: []TagSelectorRequirement{
					{Key: "", Operator: "In", Values: []string{"prod"}},
				},
			},
			wantError: true,
		},
		{
			name: "unknown operator is invalid",
			selector: &TagSelector{
				MatchExpressions: []TagSelectorRequirement{
					{Key: "env", Operator: "Contains", Values: []string{"prod"}},
				},
			},
			wantError: true,
		},
		{
			name: "valid Matches expression",
			selector: &TagSelector{
				MatchExpressions: []TagSelectorRequirement{
					{Key: "name", Operator: "Matches", Values: []string{"app-*"}},
				},
			},
			wantError: false,
		},
		{
			name: "invalid glob pattern",
			selector: &TagSelector{
				MatchExpressions: []TagSelectorRequirement{
					{Key: "name", Operator: "Matches", Values: []string{"app-["}},
				},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateTagSelector(tt.selector, field.NewPath("test"))
			if tt.wantError {
				assert.NotEmpty(t, errs)
			} else {
				assert.Empty(t, errs)
			}
		})
	}
}

func TestToTagSelector(t *testing.T) {
	tests := []struct {
		name string
		tags map[string]string
		want *TagSelector
	}{
		{
			name: "non-empty map",
			tags: map[string]string{"env": "prod", "team": "backend"},
			want: &TagSelector{
				MatchTags: map[string]string{"env": "prod", "team": "backend"},
			},
		},
		{
			name: "empty map returns nil",
			tags: map[string]string{},
			want: nil,
		},
		{
			name: "nil map returns nil",
			tags: nil,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToTagSelector(tt.tags)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMatch_UnknownOperator(t *testing.T) {
	resourceTags := map[string]string{"env": "prod"}
	selector := &TagSelector{
		MatchExpressions: []TagSelectorRequirement{
			{Key: "env", Operator: "Unknown", Values: []string{"prod"}},
		},
	}
	assert.False(t, Match(resourceTags, selector))
}
