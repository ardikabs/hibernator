/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package executorparams

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// Result holds the outcome of parameter validation.
type Result struct {
	// Errors are validation failures that should block the request.
	Errors []string

	// Warnings are non-blocking issues the user should be aware of.
	Warnings []string
}

// HasErrors returns true if validation produced errors.
func (r *Result) HasErrors() bool {
	return len(r.Errors) > 0
}

// Merge combines another Result into this one.
func (r *Result) Merge(other *Result) {
	if other == nil {
		return
	}
	r.Errors = append(r.Errors, other.Errors...)
	r.Warnings = append(r.Warnings, other.Warnings...)
}

// AddError appends a validation error.
func (r *Result) AddError(format string, args ...interface{}) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
}

// AddWarning appends a validation warning.
func (r *Result) AddWarning(format string, args ...interface{}) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
}

// ParamValidator defines the function signature for parameter validators.
// It receives the raw JSON parameters and returns validation results.
type ParamValidator func(params []byte) *Result

// registry holds all registered parameter validators.
var registry = make(map[string]validatorEntry)

// validatorEntry holds a validator and its known fields for unknown field detection.
type validatorEntry struct {
	validator   ParamValidator
	knownFields []string
}

// Register adds a parameter validator for an executor type.
func Register(executorType string, knownFields []string, validator ParamValidator) {
	registry[executorType] = validatorEntry{
		validator:   validator,
		knownFields: knownFields,
	}
}

// ValidateParams validates parameters for a given executor type.
// Returns nil if no validator is registered for the type.
func ValidateParams(executorType string, params []byte) *Result {
	entry, ok := registry[executorType]
	if !ok {
		return nil
	}

	result := &Result{}

	// Check for unknown fields if knownFields is defined
	if len(entry.knownFields) > 0 && len(params) > 0 {
		unknownWarnings := checkUnknownFields(params, entry.knownFields, executorType)
		result.Warnings = append(result.Warnings, unknownWarnings...)
	}

	// Run the validator
	if entry.validator != nil {
		validatorResult := entry.validator(params)
		result.Merge(validatorResult)
	}

	return result
}

// IsRegistered returns true if a validator exists for the executor type.
func IsRegistered(executorType string) bool {
	_, ok := registry[executorType]
	return ok
}

// RegisteredTypes returns all registered executor types.
func RegisteredTypes() []string {
	types := make([]string, 0, len(registry))
	for t := range registry {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}

// checkUnknownFields checks for fields in params that are not in knownFields.
func checkUnknownFields(params []byte, knownFields []string, executorType string) []string {
	if len(params) == 0 {
		return nil
	}

	var rawMap map[string]interface{}
	if err := json.Unmarshal(params, &rawMap); err != nil {
		// If we can't parse, let the executor handle it
		return nil
	}

	knownSet := make(map[string]bool, len(knownFields))
	for _, f := range knownFields {
		knownSet[f] = true
	}

	var warnings []string
	for field := range rawMap {
		if !knownSet[field] {
			warnings = append(warnings, fmt.Sprintf(
				"unknown parameter %q for executor type %q (known parameters: %s)",
				field, executorType, strings.Join(knownFields, ", "),
			))
		}
	}

	return warnings
}

// init registers all built-in executor validators.
func init() {
	// EC2 validator
	Register("ec2", []string{"selector", "awaitCompletion"}, validateEC2Params)

	// RDS validator
	Register("rds", []string{"selector", "snapshotBeforeStop", "awaitCompletion"}, validateRDSParams)

	// EKS validator (only handles Managed Node Groups via AWS API)
	Register("eks", []string{"clusterName", "nodeGroups", "awaitCompletion"}, validateEKSParams)

	// Karpenter validator
	Register("karpenter", []string{"nodePools", "awaitCompletion"}, validateKarpenterParams)

	// GKE validator
	Register("gke", []string{"nodePools"}, validateGKEParams)

	// CloudSQL validator
	Register("cloudsql", []string{"instanceName", "project"}, validateCloudSQLParams)

	// WorkloadScaler validator
	Register("workloadscaler", []string{"includedGroups", "namespace", "workloadSelector", "awaitCompletion"}, validateWorkloadScalerParams)
}

// validateEC2Params validates EC2 executor parameters.
func validateEC2Params(params []byte) *Result {
	result := &Result{}

	if len(params) == 0 {
		result.AddError("parameters required: either selector.tags or selector.instanceIds must be specified")
		return result
	}

	var p EC2Parameters
	if err := json.Unmarshal(params, &p); err != nil {
		result.AddError("invalid JSON format: %v", err)
		return result
	}

	// Validate selector - either tags or instanceIds required
	if len(p.Selector.Tags) == 0 && len(p.Selector.InstanceIDs) == 0 {
		result.AddError("either selector.tags or selector.instanceIds must be specified")
	}

	// Validate AwaitCompletion timeout format if waiting is enabled
	if p.AwaitCompletion.Enabled && p.AwaitCompletion.Timeout != "" {
		if err := validateWaitTimeout(p.AwaitCompletion.Timeout); err != nil {
			result.AddError("awaitCompletion.timeout has invalid duration format: %v", err)
		}
	}

	return result
}

// validateRDSParams validates RDS executor parameters.
func validateRDSParams(params []byte) *Result {
	result := &Result{}

	if len(params) == 0 {
		result.AddError("parameters required: selector must specify at least one selection method")
		return result
	}

	var p RDSParameters
	if err := json.Unmarshal(params, &p); err != nil {
		result.AddError("invalid JSON format: %v", err)
		return result
	}

	// Validate selector - at least one selection method required
	hasSelection := len(p.Selector.Tags) > 0 ||
		len(p.Selector.ExcludeTags) > 0 ||
		len(p.Selector.InstanceIds) > 0 ||
		len(p.Selector.ClusterIds) > 0 ||
		p.Selector.IncludeAll

	if !hasSelection {
		result.AddError("selector must specify at least one of: tags, excludeTags, instanceIds, clusterIds, or includeAll")
	}

	// Count selection methods used
	methodCount := 0
	if len(p.Selector.Tags) > 0 || len(p.Selector.ExcludeTags) > 0 {
		methodCount++
	}
	if len(p.Selector.InstanceIds) > 0 || len(p.Selector.ClusterIds) > 0 {
		methodCount++
	}
	if p.Selector.IncludeAll {
		methodCount++
	}

	// Only one selection method allowed
	if methodCount > 1 {
		result.AddError("selector must use only one method: either (tags/excludeTags), (instanceIds/clusterIds), or includeAll")
	}

	// Tags and ExcludeTags are mutually exclusive
	if len(p.Selector.Tags) > 0 && len(p.Selector.ExcludeTags) > 0 {
		result.AddError("selector.tags and selector.excludeTags are mutually exclusive")
	}

	// Validate DiscoverInstances and DiscoverClusters are only used with dynamic discovery
	isDynamicDiscovery := len(p.Selector.Tags) > 0 || len(p.Selector.ExcludeTags) > 0 || p.Selector.IncludeAll
	isIntentBased := len(p.Selector.InstanceIds) > 0 || len(p.Selector.ClusterIds) > 0

	if isIntentBased && (p.Selector.DiscoverInstances || p.Selector.DiscoverClusters) {
		result.AddWarning("discoverInstances and discoverClusters are ignored when using explicit instanceIds or clusterIds")
	}

	// For dynamic discovery, at least one resource type must be explicitly enabled (opt-out)
	if isDynamicDiscovery {
		if !p.Selector.DiscoverInstances && !p.Selector.DiscoverClusters {
			result.AddError("at least one of discoverInstances or discoverClusters must be explicitly set to true for dynamic discovery (tags/excludeTags/includeAll)")
		}
	}

	// Validate AwaitCompletion timeout format if waiting is enabled
	if p.AwaitCompletion.Enabled && p.AwaitCompletion.Timeout != "" {
		if err := validateWaitTimeout(p.AwaitCompletion.Timeout); err != nil {
			result.AddError("awaitCompletion.timeout has invalid duration format: %v", err)
		}
	}

	return result
}

// validateEKSParams validates EKS executor parameters.
// EKS executor handles Managed Node Groups only. Use Karpenter executor for NodePools.
func validateEKSParams(params []byte) *Result {
	result := &Result{}

	if len(params) == 0 {
		result.AddError("parameters required: clusterName must be specified")
		return result
	}

	var p EKSParameters
	if err := json.Unmarshal(params, &p); err != nil {
		result.AddError("invalid JSON format: %v", err)
		return result
	}

	if p.ClusterName == "" {
		result.AddError("clusterName is required")
	}

	// nodeGroups is optional - empty means all node groups in the cluster

	// Validate AwaitCompletion timeout format if waiting is enabled
	if p.AwaitCompletion.Enabled && p.AwaitCompletion.Timeout != "" {
		if err := validateWaitTimeout(p.AwaitCompletion.Timeout); err != nil {
			result.AddError("awaitCompletion.timeout has invalid duration format: %v", err)
		}
	}

	return result
}

// validateKarpenterParams validates Karpenter executor parameters.
func validateKarpenterParams(params []byte) *Result {
	result := &Result{}

	// Empty parameters are valid - means target all NodePools
	if len(params) == 0 {
		return result
	}

	var p KarpenterParameters
	if err := json.Unmarshal(params, &p); err != nil {
		result.AddError("invalid JSON format: %v", err)
		return result
	}

	// Empty nodePools is valid - means target all NodePools in the cluster

	// Validate AwaitCompletion timeout format if waiting is enabled
	if p.AwaitCompletion.Enabled && p.AwaitCompletion.Timeout != "" {
		if err := validateWaitTimeout(p.AwaitCompletion.Timeout); err != nil {
			result.AddError("awaitCompletion.timeout has invalid duration format: %v", err)
		}
	}

	return result
}

// validateGKEParams validates GKE executor parameters.
func validateGKEParams(params []byte) *Result {
	result := &Result{}

	if len(params) == 0 {
		result.AddError("parameters required: nodePools must be specified")
		return result
	}

	var p GKEParameters
	if err := json.Unmarshal(params, &p); err != nil {
		result.AddError("invalid JSON format: %v", err)
		return result
	}

	if len(p.NodePools) == 0 {
		result.AddError("nodePools must be specified and non-empty")
	}

	return result
}

// validateCloudSQLParams validates Cloud SQL executor parameters.
func validateCloudSQLParams(params []byte) *Result {
	result := &Result{}

	if len(params) == 0 {
		result.AddError("parameters required: instanceName and project must be specified")
		return result
	}

	var p CloudSQLParameters
	if err := json.Unmarshal(params, &p); err != nil {
		result.AddError("invalid JSON format: %v", err)
		return result
	}

	if p.InstanceName == "" {
		result.AddError("instanceName must be specified")
	}
	if p.Project == "" {
		result.AddError("project must be specified")
	}

	return result
}

// validateWorkloadScalerParams validates WorkloadScaler executor parameters.
func validateWorkloadScalerParams(params []byte) *Result {
	result := &Result{}

	if len(params) == 0 {
		result.AddError("parameters required: namespace must be specified")
		return result
	}

	var p WorkloadScalerParameters
	if err := json.Unmarshal(params, &p); err != nil {
		result.AddError("invalid JSON format: %v", err)
		return result
	}

	// Validate namespace selector (required)
	if len(p.Namespace.Literals) == 0 && len(p.Namespace.Selector) == 0 {
		result.AddError("namespace must specify either literals or selector")
	}

	// Check mutual exclusivity
	if len(p.Namespace.Literals) > 0 && len(p.Namespace.Selector) > 0 {
		result.AddError("namespace.literals and namespace.selector are mutually exclusive")
	}

	// Validate includedGroups defaults to Deployment if empty (no validation error)
	// Empty workloadSelector is valid (means all workloads in namespace)

	// Validate workloadSelector if present
	if p.WorkloadSelector != nil {
		if err := validateLabelSelector(p.WorkloadSelector); err != nil {
			result.AddError("workloadSelector validation failed: %v", err)
		}
	}

	// Validate AwaitCompletion timeout format if waiting is enabled
	if p.AwaitCompletion.Enabled && p.AwaitCompletion.Timeout != "" {
		if err := validateWaitTimeout(p.AwaitCompletion.Timeout); err != nil {
			result.AddError("awaitCompletion.timeout has invalid duration format: %v", err)
		}
	}

	return result
}

// validateLabelSelector validates a LabelSelector structure using Kubernetes validation.
func validateLabelSelector(ls *metav1.LabelSelector) error {
	if ls == nil {
		return nil
	}

	// Use built-in Kubernetes validation for label selectors
	fldPath := field.NewPath("workloadSelector")
	if errs := validation.ValidateLabelSelector(ls, validation.LabelSelectorValidationOptions{}, fldPath); len(errs) > 0 {
		return fmt.Errorf("%v", errs.ToAggregate())
	}

	return nil
}

// validateWaitTimeout validates the timeout field in AwaitCompletion.
// Empty string is valid (means no timeout). Non-empty must be parseable as duration.
func validateWaitTimeout(timeout string) error {
	if timeout == "" {
		return nil
	}
	_, err := time.ParseDuration(timeout)
	return err
}
