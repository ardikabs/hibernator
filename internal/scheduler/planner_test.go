/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package scheduler

import (
	"reflect"
	"testing"
)

func TestPlanSequential(t *testing.T) {
	p := NewPlanner()
	targets := []string{"a", "b", "c"}
	plan := p.PlanSequential(targets)

	if len(plan.Stages) != 3 {
		t.Errorf("expected 3 stages, got %d", len(plan.Stages))
	}

	for i, stage := range plan.Stages {
		if len(stage.Targets) != 1 || stage.Targets[0] != targets[i] {
			t.Errorf("stage %d: expected [%s], got %v", i, targets[i], stage.Targets)
		}
		if stage.MaxConcurrency != 1 {
			t.Errorf("stage %d: expected maxConcurrency=1, got %d", i, stage.MaxConcurrency)
		}
	}
}

func TestPlanParallel(t *testing.T) {
	p := NewPlanner()
	targets := []string{"a", "b", "c", "d"}
	plan := p.PlanParallel(targets, 2)

	if len(plan.Stages) != 1 {
		t.Errorf("expected 1 stage, got %d", len(plan.Stages))
	}
	if !reflect.DeepEqual(plan.Stages[0].Targets, targets) {
		t.Errorf("expected targets %v, got %v", targets, plan.Stages[0].Targets)
	}
	if plan.Stages[0].MaxConcurrency != 2 {
		t.Errorf("expected maxConcurrency=2, got %d", plan.Stages[0].MaxConcurrency)
	}
}

func TestPlanDAG_Simple(t *testing.T) {
	p := NewPlanner()
	// a -> b -> c (a must finish before b, b before c)
	targets := []string{"a", "b", "c"}
	deps := []Dependency{
		{From: "a", To: "b"},
		{From: "b", To: "c"},
	}

	plan, err := p.PlanDAG(targets, deps, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should produce 3 stages: [a], [b], [c]
	if len(plan.Stages) != 3 {
		t.Errorf("expected 3 stages, got %d", len(plan.Stages))
	}
	expected := [][]string{{"a"}, {"b"}, {"c"}}
	for i, stage := range plan.Stages {
		if !reflect.DeepEqual(stage.Targets, expected[i]) {
			t.Errorf("stage %d: expected %v, got %v", i, expected[i], stage.Targets)
		}
	}
}

func TestPlanDAG_Diamond(t *testing.T) {
	p := NewPlanner()
	// Diamond: a -> b, a -> c, b -> d, c -> d
	targets := []string{"a", "b", "c", "d"}
	deps := []Dependency{
		{From: "a", To: "b"},
		{From: "a", To: "c"},
		{From: "b", To: "d"},
		{From: "c", To: "d"},
	}

	plan, err := p.PlanDAG(targets, deps, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should produce 3 stages: [a], [b, c], [d]
	if len(plan.Stages) != 3 {
		t.Errorf("expected 3 stages, got %d", len(plan.Stages))
	}

	if !reflect.DeepEqual(plan.Stages[0].Targets, []string{"a"}) {
		t.Errorf("stage 0: expected [a], got %v", plan.Stages[0].Targets)
	}
	if !reflect.DeepEqual(plan.Stages[1].Targets, []string{"b", "c"}) {
		t.Errorf("stage 1: expected [b, c], got %v", plan.Stages[1].Targets)
	}
	if !reflect.DeepEqual(plan.Stages[2].Targets, []string{"d"}) {
		t.Errorf("stage 2: expected [d], got %v", plan.Stages[2].Targets)
	}
}

func TestPlanDAG_Cycle(t *testing.T) {
	p := NewPlanner()
	targets := []string{"a", "b", "c"}
	deps := []Dependency{
		{From: "a", To: "b"},
		{From: "b", To: "c"},
		{From: "c", To: "a"}, // cycle
	}

	_, err := p.PlanDAG(targets, deps, 0)
	if err == nil {
		t.Error("expected cycle error, got nil")
	}
}

func TestPlanDAG_UnknownTarget(t *testing.T) {
	p := NewPlanner()
	targets := []string{"a", "b"}
	deps := []Dependency{
		{From: "a", To: "x"}, // x doesn't exist
	}

	_, err := p.PlanDAG(targets, deps, 0)
	if err == nil {
		t.Error("expected unknown target error, got nil")
	}
}

func TestPlanStaged(t *testing.T) {
	p := NewPlanner()
	stages := []Stage{
		{Name: "storage", Parallel: true, Targets: []string{"db1", "db2"}},
		{Name: "compute", Parallel: true, MaxConcurrency: 2, Targets: []string{"a", "b", "c"}},
	}

	plan := p.PlanStaged(stages, 5)

	if len(plan.Stages) != 2 {
		t.Errorf("expected 2 stages, got %d", len(plan.Stages))
	}
	if plan.Stages[0].MaxConcurrency != 5 {
		t.Errorf("stage 0: expected maxConcurrency=5, got %d", plan.Stages[0].MaxConcurrency)
	}
	if plan.Stages[1].MaxConcurrency != 2 {
		t.Errorf("stage 1: expected maxConcurrency=2, got %d", plan.Stages[1].MaxConcurrency)
	}
}
