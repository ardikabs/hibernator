/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// stateMergeContext holds the context for state merging decisions.
type stateMergeContext struct {
	key         string
	newValue    any
	existing    *Data
	isSameCycle bool
	now         metav1.Time
	log         logr.Logger
}

// stateMergeStrategy defines the interface for state merging strategies.
type stateMergeStrategy interface {
	// shouldUseNewValue returns true if the new value should be used,
	// false if existing state should be preserved.
	shouldUseNewValue(ctx *stateMergeContext) bool
	// prepareStatus creates the ResourceStatus for this resource.
	prepareStatus(ctx *stateMergeContext) ResourceStatus
}

// differentCycleStrategy: always use new value when cycle differs.
type differentCycleStrategy struct{}

func (s *differentCycleStrategy) shouldUseNewValue(ctx *stateMergeContext) bool {
	return true
}

func (s *differentCycleStrategy) prepareStatus(ctx *stateMergeContext) ResourceStatus {
	return ResourceStatus{LastReportedAt: &ctx.now}
}

// firstReportStrategy: use new value when resource never reported in this cycle.
type firstReportStrategy struct{}

func (s *firstReportStrategy) shouldUseNewValue(ctx *stateMergeContext) bool {
	return true
}

func (s *firstReportStrategy) prepareStatus(ctx *stateMergeContext) ResourceStatus {
	return ResourceStatus{LastReportedAt: &ctx.now}
}

// demandedStateStrategy: use new value when it represents demanded state.
type demandedStateStrategy struct{}

func (s *demandedStateStrategy) shouldUseNewValue(ctx *stateMergeContext) bool {
	return true
}

func (s *demandedStateStrategy) prepareStatus(ctx *stateMergeContext) ResourceStatus {
	return ResourceStatus{LastReportedAt: &ctx.now}
}

// preserveStateStrategy: preserve existing state (restart scenario).
type preserveStateStrategy struct{}

func (s *preserveStateStrategy) shouldUseNewValue(ctx *stateMergeContext) bool {
	return false
}

func (s *preserveStateStrategy) prepareStatus(ctx *stateMergeContext) ResourceStatus {
	status := ResourceStatus{LastReportedAt: &ctx.now}
	if ctx.existing.Status != nil {
		status.StaleCount = ctx.existing.Status[ctx.key].StaleCount
	}
	return status
}

// strategySelector determines which strategy to use for a given resource.
type strategySelector struct{}

func (s *strategySelector) selectStrategy(ctx *stateMergeContext) stateMergeStrategy {
	// Rule 1: Different cycle - always use new value
	if !ctx.isSameCycle {
		return &differentCycleStrategy{}
	}

	// Same cycle: check if previously reported
	wasPreviouslyReported := ctx.existing.Status != nil &&
		ctx.existing.Status[ctx.key].LastReportedAt != nil

	if !wasPreviouslyReported {
		// Rule 2: Never reported before - use new value
		return &firstReportStrategy{}
	}

	// Was reported before - check demanded state
	if stateMap, ok := ctx.newValue.(map[string]any); ok && isDemandedState(stateMap) {
		// Rule 3: Demanded state - replace
		return &demandedStateStrategy{}
	}

	// Rule 4: Same cycle, reported before, not demanded - preserve existing
	return &preserveStateStrategy{}
}
