/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package scheduler

import "time"

// evaluationStep is a single step in the evaluation pipeline.
// Each stage receives the result from the previous stage and returns a
// (possibly transformed) result. The first stage's input is always nil.
type evaluationStep func(prev *EvaluationResult) (*EvaluationResult, error)

// evaluateWhen returns a no-op passthrough when active is false, otherwise
// returns fn unchanged. This lets callers declare conditional stages inline
// without breaking the pipeline's shape:
//
//	evaluateWhen(ext != nil, func(r *EvaluationResult) (*EvaluationResult, error) {
//	    return e.applyExtend(r, ext.Windows, timezone)
//	})
func evaluateWhen(active bool, fn evaluationStep) evaluationStep {
	if active {
		return fn
	}
	return func(prev *EvaluationResult) (*EvaluationResult, error) {
		return prev, nil
	}
}

// runEvaluationPipeline chains all stages sequentially, threading the result
// of each stage into the next. The first stage receives nil as its input
// (it is responsible for seeding the initial result); subsequent stages
// receive the output of the preceding one. An error from any stage
// short-circuits the pipeline and is returned immediately.
func runEvaluationPipeline(stages ...evaluationStep) (*EvaluationResult, error) {
	var result *EvaluationResult
	for _, stage := range stages {
		var err error
		result, err = stage(result)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

// mergeByType collects all exceptions of the given type and merges them into a
// single logical exception. Windows are concatenated, validity is expanded to
// the widest bounds, and LeadTime uses the maximum. Returns nil if no exception
// of this type exists.
func mergeByType(exceptions []*Exception, t ExceptionType) *Exception {
	var matches []*Exception
	for _, exc := range exceptions {
		if exc.Type == t {
			matches = append(matches, exc)
		}
	}

	if len(matches) == 0 {
		return nil
	}
	if len(matches) == 1 {
		return matches[0]
	}

	merged := &Exception{
		Type:       t,
		ValidFrom:  matches[0].ValidFrom,
		ValidUntil: matches[0].ValidUntil,
	}
	for _, m := range matches {
		if m.ValidFrom.Before(merged.ValidFrom) {
			merged.ValidFrom = m.ValidFrom
		}
		if m.ValidUntil.After(merged.ValidUntil) {
			merged.ValidUntil = m.ValidUntil
		}
		merged.Windows = append(merged.Windows, m.Windows...)
		if m.LeadTime > merged.LeadTime {
			merged.LeadTime = m.LeadTime
		}
	}

	return merged
}

// earlierTime returns the earlier of two times, handling zero values.
// If one time is zero, the non-zero time is returned.
// If both are zero, zero time is returned.
func earlierTime(a, b time.Time) time.Time {
	if a.IsZero() {
		return b
	}
	if b.IsZero() {
		return a
	}
	if a.Before(b) {
		return a
	}
	return b
}
