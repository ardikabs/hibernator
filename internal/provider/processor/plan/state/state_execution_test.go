/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

// ---------------------------------------------------------------------------
// State.buildExecutionPlan()
// ---------------------------------------------------------------------------

func TestBuildExecutionPlan_Sequential_OneStagePerTarget(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernating)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db"},
		{Name: "app"},
		{Name: "cache"},
	}
	plan.Spec.Execution.Strategy.Type = hibernatorv1alpha1.StrategySequential

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	execPlan, err := st.buildExecutionPlan(plan, false)
	require.NoError(t, err)
	assert.Len(t, execPlan.Stages, 3, "sequential: one stage per target")
}

func TestBuildExecutionPlan_Parallel_SingleStage(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernating)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db"}, {Name: "app"}, {Name: "cache"},
	}
	plan.Spec.Execution.Strategy.Type = hibernatorv1alpha1.StrategyParallel

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	execPlan, err := st.buildExecutionPlan(plan, false)
	require.NoError(t, err)
	assert.Len(t, execPlan.Stages, 1, "parallel: all targets in a single stage")
}

func TestBuildExecutionPlan_DAG_RespectsOrder(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernating)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db"}, {Name: "app"},
	}
	plan.Spec.Execution.Strategy.Type = hibernatorv1alpha1.StrategyDAG
	// app depends on db; db must go first.
	plan.Spec.Execution.Strategy.Dependencies = []hibernatorv1alpha1.Dependency{
		{From: "db", To: "app"},
	}

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	execPlan, err := st.buildExecutionPlan(plan, false)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(execPlan.Stages), 2, "DAG: db before app → at least 2 stages")
	assert.Contains(t, execPlan.Stages[0].Targets, "db")
}

func TestBuildExecutionPlan_Staged_ReturnsCorrectStages(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernating)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db"}, {Name: "app"},
	}
	plan.Spec.Execution.Strategy.Type = hibernatorv1alpha1.StrategyStaged
	plan.Spec.Execution.Strategy.Stages = []hibernatorv1alpha1.Stage{
		{Name: "infra", Targets: []string{"db"}},
		{Name: "services", Targets: []string{"app"}},
	}

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	execPlan, err := st.buildExecutionPlan(plan, false)
	require.NoError(t, err)
	assert.Len(t, execPlan.Stages, 2, "staged: two stages as defined")
}

func TestBuildExecutionPlan_UnknownStrategy_ReturnsError(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernating)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{{Name: "db"}}
	plan.Spec.Execution.Strategy.Type = hibernatorv1alpha1.ExecutionStrategyType("Magic")

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	_, err := st.buildExecutionPlan(plan, false)
	assert.Error(t, err, "unknown strategy must return error")
}

func TestBuildExecutionPlan_Reverse_Sequential(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseWakingUp)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db"},
		{Name: "app"},
		{Name: "cache"},
	}
	plan.Spec.Execution.Strategy.Type = hibernatorv1alpha1.StrategySequential

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	forward, err := st.buildExecutionPlan(plan, false)
	require.NoError(t, err)
	backward, err := st.buildExecutionPlan(plan, true)
	require.NoError(t, err)

	// Forward: [db] → [app] → [cache]
	// Reverse: [cache] → [app] → [db]
	require.Len(t, forward.Stages, 3)
	require.Len(t, backward.Stages, 3)
	assert.Equal(t, []string{"db"}, forward.Stages[0].Targets)
	assert.Equal(t, []string{"app"}, forward.Stages[1].Targets)
	assert.Equal(t, []string{"cache"}, forward.Stages[2].Targets)

	assert.Equal(t, []string{"cache"}, backward.Stages[0].Targets)
	assert.Equal(t, []string{"app"}, backward.Stages[1].Targets)
	assert.Equal(t, []string{"db"}, backward.Stages[2].Targets)
}

func TestBuildExecutionPlan_Reverse_Parallel(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseWakingUp)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db"},
		{Name: "app"},
		{Name: "cache"},
	}
	plan.Spec.Execution.Strategy.Type = hibernatorv1alpha1.StrategyParallel
	plan.Spec.Execution.Strategy.MaxConcurrency = ptr.To(int32(2))

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	forward, err := st.buildExecutionPlan(plan, false)
	require.NoError(t, err)
	backward, err := st.buildExecutionPlan(plan, true)
	require.NoError(t, err)

	// Forward: single stage [db, app, cache]
	// Reverse: single stage [cache, app, db] (reversed target order, even though it is a single stage, but we preserve the intent of reversing the target order for parallel strategy as well)
	require.Len(t, forward.Stages, 1)
	require.Len(t, backward.Stages, 1)
	assert.Equal(t, []string{"db", "app", "cache"}, forward.Stages[0].Targets)
	assert.Equal(t, []string{"cache", "app", "db"}, backward.Stages[0].Targets)
	assert.Equal(t, forward.Stages[0].MaxConcurrency, backward.Stages[0].MaxConcurrency)
}

func TestBuildExecutionPlan_Reverse_DAG_Diamond(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseWakingUp)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "a"},
		{Name: "b"},
		{Name: "c"},
		{Name: "d"},
		{Name: "e"},
	}
	plan.Spec.Execution.Strategy.Type = hibernatorv1alpha1.StrategyDAG
	// Diamond: a → b, a → c, b → d, c → d
	plan.Spec.Execution.Strategy.Dependencies = []hibernatorv1alpha1.Dependency{
		{From: "a", To: "b"},
		{From: "a", To: "c"},
		{From: "b", To: "d"},
		{From: "c", To: "d"},
		{From: "e", To: "d"},
	}

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	forward, err := st.buildExecutionPlan(plan, false)
	require.NoError(t, err)
	backward, err := st.buildExecutionPlan(plan, true)
	require.NoError(t, err)

	// Forward (shutdown): [a, e] → [b, c] → [d]
	require.Len(t, forward.Stages, 3)
	assert.Equal(t, []string{"a", "e"}, forward.Stages[0].Targets)
	assert.Equal(t, []string{"b", "c"}, forward.Stages[1].Targets)
	assert.Equal(t, []string{"d"}, forward.Stages[2].Targets)

	// Reversed edges: d→b, d→c, d→e, b→a, c→a
	// Reverse (wakeup):  [d] → [b, c, e] → [a]
	// The order might seem a bit differ then Forward, specifically for the 'e' target.
	// In Forward, 'e' can run in stage 1 with 'a' since it has no dependencies.
	// But in Reverse, 'e' depends on 'd', so it technically allowed to run earlier as in stage 2 with 'b' and 'c',
	// not need to be waited until stage 3 with 'a'.
	require.Len(t, backward.Stages, 3)
	assert.Equal(t, []string{"d"}, backward.Stages[0].Targets)
	assert.Equal(t, []string{"b", "c", "e"}, backward.Stages[1].Targets)
	assert.Equal(t, []string{"a"}, backward.Stages[2].Targets)
}

func TestBuildExecutionPlan_Reverse_DAG_DiamondWithSpur(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseWakingUp)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}, {Name: "e"},
	}
	plan.Spec.Execution.Strategy.Type = hibernatorv1alpha1.StrategyDAG
	// Diamond + spur: a→b, a→c, b→d, c→d, e→d
	plan.Spec.Execution.Strategy.Dependencies = []hibernatorv1alpha1.Dependency{
		{From: "a", To: "b"},
		{From: "a", To: "c"},
		{From: "b", To: "d"},
		{From: "c", To: "d"},
		{From: "e", To: "d"},
	}

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	forward, err := st.buildExecutionPlan(plan, false)
	require.NoError(t, err)
	backward, err := st.buildExecutionPlan(plan, true)
	require.NoError(t, err)

	// Forward (shutdown): [a, e] → [b, c] → [d]
	require.Len(t, forward.Stages, 3)
	assert.Equal(t, []string{"a", "e"}, forward.Stages[0].Targets)
	assert.Equal(t, []string{"b", "c"}, forward.Stages[1].Targets)
	assert.Equal(t, []string{"d"}, forward.Stages[2].Targets)

	// Reversed edges: d→b, d→c, d→e, b→a, c→a
	// Reverse (wakeup): [d] → [b, c, e] → [a]
	// Key: 'e' depends only on 'd' in reverse, so it runs in stage 2 with b,c
	// NOT stuck in stage 3 with 'a' as a naive array flip would produce.
	require.Len(t, backward.Stages, 3)
	assert.Equal(t, []string{"d"}, backward.Stages[0].Targets)
	assert.Equal(t, []string{"b", "c", "e"}, backward.Stages[1].Targets)
	assert.Equal(t, []string{"a"}, backward.Stages[2].Targets)
}

func TestBuildExecutionPlan_Reverse_DAG_Chain(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseWakingUp)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	}
	plan.Spec.Execution.Strategy.Type = hibernatorv1alpha1.StrategyDAG
	// Chain: a → b → c
	plan.Spec.Execution.Strategy.Dependencies = []hibernatorv1alpha1.Dependency{
		{From: "a", To: "b"},
		{From: "b", To: "c"},
	}

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	forward, err := st.buildExecutionPlan(plan, false)
	require.NoError(t, err)
	backward, err := st.buildExecutionPlan(plan, true)
	require.NoError(t, err)

	// Forward: [a] → [b] → [c]
	require.Len(t, forward.Stages, 3)
	assert.Equal(t, []string{"a"}, forward.Stages[0].Targets)
	assert.Equal(t, []string{"b"}, forward.Stages[1].Targets)
	assert.Equal(t, []string{"c"}, forward.Stages[2].Targets)

	// Reverse: [c] → [b] → [a]
	require.Len(t, backward.Stages, 3)
	assert.Equal(t, []string{"c"}, backward.Stages[0].Targets)
	assert.Equal(t, []string{"b"}, backward.Stages[1].Targets)
	assert.Equal(t, []string{"a"}, backward.Stages[2].Targets)
}

func TestBuildExecutionPlan_Reverse_DAG_MultiBranch(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseWakingUp)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}, {Name: "e"}, {Name: "f"},
	}
	plan.Spec.Execution.Strategy.Type = hibernatorv1alpha1.StrategyDAG
	// Multi-branch: a→b, a→c, b→d, c→e, d→f
	//     a
	//    / \
	//   b   c
	//   |   |
	//   d   e
	//   |
	//   f
	plan.Spec.Execution.Strategy.Dependencies = []hibernatorv1alpha1.Dependency{
		{From: "a", To: "b"},
		{From: "a", To: "c"},
		{From: "b", To: "d"},
		{From: "c", To: "e"},
		{From: "d", To: "f"},
	}

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	forward, err := st.buildExecutionPlan(plan, false)
	require.NoError(t, err)
	backward, err := st.buildExecutionPlan(plan, true)
	require.NoError(t, err)

	// Forward: [a] → [b, c] → [d, e] → [f]
	require.Len(t, forward.Stages, 4)
	assert.Equal(t, []string{"a"}, forward.Stages[0].Targets)
	assert.Equal(t, []string{"b", "c"}, forward.Stages[1].Targets)
	assert.Equal(t, []string{"d", "e"}, forward.Stages[2].Targets)
	assert.Equal(t, []string{"f"}, forward.Stages[3].Targets)

	// Reversed edges: f→d, d→b, e→c, b→a, c→a
	// Reverse: [e, f] → [c, d] → [b] → [a]
	// Key: 'e' and 'f' are leaf nodes in original graph → both become roots in reverse
	require.Len(t, backward.Stages, 4)
	assert.Equal(t, []string{"e", "f"}, backward.Stages[0].Targets)
	assert.Equal(t, []string{"c", "d"}, backward.Stages[1].Targets)
	assert.Equal(t, []string{"b"}, backward.Stages[2].Targets)
	assert.Equal(t, []string{"a"}, backward.Stages[3].Targets)
}

func TestBuildExecutionPlan_Reverse_Staged(t *testing.T) {
	plan := basePlanForState("p", hibernatorv1alpha1.PhaseWakingUp)
	plan.Spec.Targets = []hibernatorv1alpha1.Target{
		{Name: "db"}, {Name: "cache"}, {Name: "app"}, {Name: "web"},
	}
	plan.Spec.Execution.Strategy.Type = hibernatorv1alpha1.StrategyStaged
	plan.Spec.Execution.Strategy.Stages = []hibernatorv1alpha1.Stage{
		{Name: "storage", Parallel: true, Targets: []string{"db", "cache"}},
		{Name: "services", Parallel: true, Targets: []string{"app", "web"}},
	}

	c := newHandlerFakeClient(plan)
	st := newHandlerState(plan, c)

	forward, err := st.buildExecutionPlan(plan, false)
	require.NoError(t, err)
	backward, err := st.buildExecutionPlan(plan, true)
	require.NoError(t, err)

	// Forward: stage[storage: db,cache] → stage[services: app,web]
	require.Len(t, forward.Stages, 2)
	assert.Equal(t, []string{"db", "cache"}, forward.Stages[0].Targets)
	assert.Equal(t, []string{"app", "web"}, forward.Stages[1].Targets)

	// Reverse: stage[services: app,web] → stage[storage: db,cache]
	require.Len(t, backward.Stages, 2)
	assert.Equal(t, []string{"app", "web"}, backward.Stages[0].Targets)
	assert.Equal(t, []string{"db", "cache"}, backward.Stages[1].Targets)
}
