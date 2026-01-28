/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package scheduler implements execution planning for HibernatePlan.
package scheduler

import (
	"errors"
	"fmt"
	"sort"
)

// ErrCycleDetected is returned when a cycle is found in DAG.
var ErrCycleDetected = errors.New("cycle detected in dependency graph")

// ErrTargetNotFound is returned when a dependency references unknown target.
var ErrTargetNotFound = errors.New("dependency references unknown target")

// Dependency represents a DAG edge.
type Dependency struct {
	From string
	To   string
}

// Stage represents an execution stage with targets.
type Stage struct {
	Name           string
	Parallel       bool
	MaxConcurrency int32
	Targets        []string
}

// ExecutionPlan represents the computed execution order.
type ExecutionPlan struct {
	// Stages in execution order (each stage can be parallel).
	Stages []ExecutionStage
}

// ExecutionStage is a group of targets that can execute together.
type ExecutionStage struct {
	// Targets to execute in this stage.
	Targets []string
	// MaxConcurrency limits parallelism (0 = unlimited).
	MaxConcurrency int32
}

// Planner computes execution plans from strategies.
type Planner struct{}

// NewPlanner creates a new Planner.
func NewPlanner() *Planner {
	return &Planner{}
}

// PlanSequential creates a plan where targets run one at a time in order.
func (p *Planner) PlanSequential(targets []string) ExecutionPlan {
	stages := make([]ExecutionStage, len(targets))
	for i, t := range targets {
		stages[i] = ExecutionStage{
			Targets:        []string{t},
			MaxConcurrency: 1,
		}
	}
	return ExecutionPlan{Stages: stages}
}

// PlanParallel creates a plan where all targets run in parallel with bounded concurrency.
func (p *Planner) PlanParallel(targets []string, maxConcurrency int32) ExecutionPlan {
	if maxConcurrency <= 0 {
		maxConcurrency = int32(len(targets))
	}
	return ExecutionPlan{
		Stages: []ExecutionStage{
			{
				Targets:        targets,
				MaxConcurrency: maxConcurrency,
			},
		},
	}
}

// PlanDAG creates a plan from DAG dependencies using topological sort.
// Returns stages where each stage contains targets with satisfied dependencies.
func (p *Planner) PlanDAG(targets []string, deps []Dependency, maxConcurrency int32) (ExecutionPlan, error) {
	// Build target set
	targetSet := make(map[string]bool)
	for _, t := range targets {
		targetSet[t] = true
	}

	// Validate dependencies
	for _, d := range deps {
		if !targetSet[d.From] {
			return ExecutionPlan{}, fmt.Errorf("%w: %s", ErrTargetNotFound, d.From)
		}
		if !targetSet[d.To] {
			return ExecutionPlan{}, fmt.Errorf("%w: %s", ErrTargetNotFound, d.To)
		}
	}

	// Build adjacency list and in-degree map
	// Note: From -> To means To depends on From (From must complete before To)
	adj := make(map[string][]string)
	inDegree := make(map[string]int)
	for _, t := range targets {
		adj[t] = []string{}
		inDegree[t] = 0
	}
	for _, d := range deps {
		adj[d.From] = append(adj[d.From], d.To)
		inDegree[d.To]++
	}

	// Kahn's algorithm for topological sort with level tracking
	var stages []ExecutionStage
	processed := 0

	for processed < len(targets) {
		// Find all nodes with in-degree 0
		var ready []string
		for t, deg := range inDegree {
			if deg == 0 {
				ready = append(ready, t)
			}
		}

		if len(ready) == 0 {
			return ExecutionPlan{}, ErrCycleDetected
		}

		// Sort for deterministic order
		sort.Strings(ready)

		// Remove processed nodes
		for _, t := range ready {
			delete(inDegree, t)
			for _, neighbor := range adj[t] {
				inDegree[neighbor]--
			}
		}

		mc := maxConcurrency
		if mc <= 0 {
			mc = int32(len(ready))
		}

		stages = append(stages, ExecutionStage{
			Targets:        ready,
			MaxConcurrency: mc,
		})
		processed += len(ready)
	}

	return ExecutionPlan{Stages: stages}, nil
}

// PlanStaged creates a plan from predefined stages.
func (p *Planner) PlanStaged(stages []Stage, globalMaxConcurrency int32) ExecutionPlan {
	result := make([]ExecutionStage, len(stages))
	for i, s := range stages {
		mc := s.MaxConcurrency
		if mc <= 0 {
			if s.Parallel {
				mc = globalMaxConcurrency
				if mc <= 0 {
					mc = int32(len(s.Targets))
				}
			} else {
				mc = 1
			}
		}
		result[i] = ExecutionStage{
			Targets:        s.Targets,
			MaxConcurrency: mc,
		}
	}
	return ExecutionPlan{Stages: result}
}

// ValidateDAG checks if dependencies form a valid DAG.
func (p *Planner) ValidateDAG(targets []string, deps []Dependency) error {
	_, err := p.PlanDAG(targets, deps, 0)
	return err
}
