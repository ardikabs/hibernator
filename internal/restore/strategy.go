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
	key            string
	newValue       any
	existing       *Data
	incomingStatus map[string]ResourceStatus // Status from accumulator with LastReportedAt
	isSameCycle    bool
	now            metav1.Time
	log            logr.Logger
}

// stateMergeStrategy defines the interface for state merging strategies.
type stateMergeStrategy interface {
	// shouldUseNewValue returns true if the new value should be used,
	// false if existing state should be preserved.
	shouldUseNewValue(ctx *stateMergeContext) bool

	// setStatus sets the ResourceStatus for this resource.
	setStatus(ctx *stateMergeContext) ResourceStatus
}

// differentCycleStrategy: always use new value when cycle differs.
type differentCycleStrategy struct{}

func (s *differentCycleStrategy) shouldUseNewValue(ctx *stateMergeContext) bool {
	return true
}

func (s *differentCycleStrategy) setStatus(ctx *stateMergeContext) ResourceStatus {
	// Use the incoming LastReportedAt from accumulator (when callback was invoked)
	if incoming, ok := ctx.incomingStatus[ctx.key]; ok && incoming.LastReportedAt != nil {
		return ResourceStatus{LastReportedAt: incoming.LastReportedAt}
	}

	return ResourceStatus{LastReportedAt: &ctx.now}
}

// firstReportStrategy: use new value when resource never reported in this cycle.
type firstReportStrategy struct{}

func (s *firstReportStrategy) shouldUseNewValue(ctx *stateMergeContext) bool {
	return true
}

func (s *firstReportStrategy) setStatus(ctx *stateMergeContext) ResourceStatus {
	// Use the incoming LastReportedAt from accumulator (when callback was invoked)
	if incoming, ok := ctx.incomingStatus[ctx.key]; ok && incoming.LastReportedAt != nil {
		return ResourceStatus{LastReportedAt: incoming.LastReportedAt}
	}

	return ResourceStatus{LastReportedAt: &ctx.now}
}

// demandedStateStrategy: use new value when it represents demanded state.
type demandedStateStrategy struct{}

func (s *demandedStateStrategy) shouldUseNewValue(ctx *stateMergeContext) bool {
	return true
}

func (s *demandedStateStrategy) setStatus(ctx *stateMergeContext) ResourceStatus {
	// Use the incoming LastReportedAt from accumulator (when callback was invoked)
	if incoming, ok := ctx.incomingStatus[ctx.key]; ok && incoming.LastReportedAt != nil {
		return ResourceStatus{LastReportedAt: incoming.LastReportedAt}
	}

	return ResourceStatus{LastReportedAt: &ctx.now}
}

// preserveStateStrategy: preserve existing state (restart scenario).
type preserveStateStrategy struct{}

func (s *preserveStateStrategy) shouldUseNewValue(ctx *stateMergeContext) bool {
	return false
}

func (s *preserveStateStrategy) setStatus(ctx *stateMergeContext) ResourceStatus {
	// Preserve the existing LastReportedAt (from when the resource was first reported in this cycle)
	status := ResourceStatus{}
	if ctx.existing.Status != nil {
		status.StaleCount = ctx.existing.Status[ctx.key].StaleCount
		if ctx.existing.Status[ctx.key].LastReportedAt != nil {
			status.LastReportedAt = ctx.existing.Status[ctx.key].LastReportedAt
		}
	}
	// Fall back to incoming status if no existing status
	if status.LastReportedAt == nil {
		if incoming, ok := ctx.incomingStatus[ctx.key]; ok && incoming.LastReportedAt != nil {
			status.LastReportedAt = incoming.LastReportedAt
		} else {
			status.LastReportedAt = &ctx.now
		}
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
