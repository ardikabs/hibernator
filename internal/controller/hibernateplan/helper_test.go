/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// helpers_unit_test.go covers pure-function helper methods on the Reconciler
// that have no side effects and do not require a k8s API server.

package hibernateplan

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// ---- fixture helpers ----

func planWithTargets(targets ...hibernatorv1alpha1.Target) *hibernatorv1alpha1.HibernatePlan {
	return &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "plan", Namespace: "default"},
		Spec: hibernatorv1alpha1.HibernatePlanSpec{
			Targets: targets,
		},
	}
}

func target(name, ttype string) hibernatorv1alpha1.Target {
	return hibernatorv1alpha1.Target{
		Name: name,
		Type: ttype,
		ConnectorRef: hibernatorv1alpha1.ConnectorRef{
			Kind: "CloudProvider",
			Name: "aws",
		},
	}
}

func nilReconciler() *Reconciler {
	// Minimal reconciler for pure-function helpers that don't use k8s client
	r, _, _ := newHibernatePlanReconciler()
	return r
}

// ---- findTargetType ----

func TestFindTargetType_Found(t *testing.T) {
	r := nilReconciler()
	plan := planWithTargets(target("db", "rds"), target("app", "eks"))

	if got := r.findTargetType(plan, "db"); got != "rds" {
		t.Errorf("findTargetType(db) = %q, want %q", got, "rds")
	}
	if got := r.findTargetType(plan, "app"); got != "eks" {
		t.Errorf("findTargetType(app) = %q, want %q", got, "eks")
	}
}

func TestFindTargetType_NotFound(t *testing.T) {
	r := nilReconciler()
	plan := planWithTargets(target("db", "rds"))

	if got := r.findTargetType(plan, "missing"); got != "" {
		t.Errorf("findTargetType(missing) = %q, want empty string", got)
	}
}

func TestFindTargetType_EmptyTargets(t *testing.T) {
	r := nilReconciler()
	plan := planWithTargets()

	if got := r.findTargetType(plan, "anything"); got != "" {
		t.Errorf("findTargetType on empty plan = %q, want empty string", got)
	}
}

// ---- findExecutionStatus ----

func TestFindExecutionStatus_Found(t *testing.T) {
	r := nilReconciler()
	plan := planWithTargets(target("db", "rds"))
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "db", Executor: "rds", State: hibernatorv1alpha1.StateRunning},
	}

	got := r.findExecutionStatus(plan, "rds", "db")
	if got == nil {
		t.Fatal("expected non-nil execution status")
	}
	if got.State != hibernatorv1alpha1.StateRunning {
		t.Errorf("State = %v, want Running", got.State)
	}
}

func TestFindExecutionStatus_NotFound(t *testing.T) {
	r := nilReconciler()
	plan := planWithTargets(target("db", "rds"))
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "db", Executor: "rds", State: hibernatorv1alpha1.StateCompleted},
	}

	// Wrong target name
	if got := r.findExecutionStatus(plan, "rds", "app"); got != nil {
		t.Errorf("expected nil for missing target, got %v", got)
	}
}

func TestFindExecutionStatus_OldFormat(t *testing.T) {
	r := nilReconciler()
	plan := planWithTargets(target("db", "rds"))
	// Old format is "type/name"
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "rds/db", Executor: "rds", State: hibernatorv1alpha1.StateFailed},
	}

	got := r.findExecutionStatus(plan, "rds", "db")
	if got == nil {
		t.Fatal("expected non-nil execution status for old format target")
	}
	if got.State != hibernatorv1alpha1.StateFailed {
		t.Errorf("State = %v, want Failed", got.State)
	}
}

// ---- findFailedDependencies ----

func TestFindFailedDependencies_NoDependencies(t *testing.T) {
	r := nilReconciler()
	plan := planWithTargets(target("db", "rds"), target("app", "eks"))

	stage := scheduler.ExecutionStage{Targets: []string{"app"}}
	result := r.findFailedDependencies(plan, nil, stage)
	if result != nil {
		t.Errorf("expected nil for no dependencies, got %v", result)
	}
}

func TestFindFailedDependencies_DependencyNotFailed(t *testing.T) {
	r := nilReconciler()
	plan := planWithTargets(target("db", "rds"), target("app", "eks"))
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "db", Executor: "rds", State: hibernatorv1alpha1.StateCompleted},
	}

	deps := []hibernatorv1alpha1.Dependency{{From: "db", To: "app"}}
	stage := scheduler.ExecutionStage{Targets: []string{"app"}}
	result := r.findFailedDependencies(plan, deps, stage)
	if len(result) != 0 {
		t.Errorf("expected no failed dependencies, got %v", result)
	}
}

func TestFindFailedDependencies_DependencyFailed(t *testing.T) {
	r := nilReconciler()
	plan := planWithTargets(target("db", "rds"), target("app", "eks"))
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "db", Executor: "rds", State: hibernatorv1alpha1.StateFailed},
	}

	deps := []hibernatorv1alpha1.Dependency{{From: "db", To: "app"}}
	stage := scheduler.ExecutionStage{Targets: []string{"app"}}
	result := r.findFailedDependencies(plan, deps, stage)
	if len(result) != 1 || result[0] != "db" {
		t.Errorf("expected [db] as failed dependency, got %v", result)
	}
}

func TestFindFailedDependencies_TargetNotInStage(t *testing.T) {
	r := nilReconciler()
	plan := planWithTargets(target("db", "rds"), target("app", "eks"), target("cache", "ec2"))
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "db", Executor: "rds", State: hibernatorv1alpha1.StateFailed},
	}

	// Stage has "cache" which doesn't depend on "db" in our deps list
	deps := []hibernatorv1alpha1.Dependency{{From: "db", To: "app"}}
	stage := scheduler.ExecutionStage{Targets: []string{"cache"}}
	result := r.findFailedDependencies(plan, deps, stage)
	if len(result) != 0 {
		t.Errorf("expected no failed deps for stage not containing dependent target, got %v", result)
	}
}

// ---- findPlansForException ----

func TestFindPlansForException_ValidException(t *testing.T) {
	r := nilReconciler()
	exc := &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "exc-1", Namespace: "ns1"},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			PlanRef: hibernatorv1alpha1.PlanReference{Name: "my-plan"},
		},
	}

	reqs := r.findPlansForException(context.Background(), exc)
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	if reqs[0].Name != "my-plan" || reqs[0].Namespace != "ns1" {
		t.Errorf("request = %v, want my-plan/ns1", reqs[0])
	}
}

func TestFindPlansForException_WrongType(t *testing.T) {
	r := nilReconciler()
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "plan-1", Namespace: "ns1"},
	}

	reqs := r.findPlansForException(context.Background(), plan)
	if reqs != nil {
		t.Errorf("expected nil requests for wrong type, got %v", reqs)
	}
}

// ---- findPlansForRunnerJob ----

func TestFindPlansForRunnerJob_WithPlanLabel(t *testing.T) {
	r := nilReconciler()
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runner-abc",
			Namespace: "default",
			Labels: map[string]string{
				wellknown.LabelPlan: "my-plan",
			},
		},
	}

	reqs := r.findPlansForRunnerJob(context.Background(), job)
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	if reqs[0].Name != "my-plan" {
		t.Errorf("request.Name = %q, want %q", reqs[0].Name, "my-plan")
	}
}

func TestFindPlansForRunnerJob_WithoutPlanLabel(t *testing.T) {
	r := nilReconciler()
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other-job",
			Namespace: "default",
		},
	}

	reqs := r.findPlansForRunnerJob(context.Background(), job)
	if reqs != nil {
		t.Errorf("expected nil requests for job without plan label, got %v", reqs)
	}
}

func TestFindPlansForRunnerJob_WrongType(t *testing.T) {
	r := nilReconciler()
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "plan-1"},
	}

	reqs := r.findPlansForRunnerJob(context.Background(), plan)
	if reqs != nil {
		t.Errorf("expected nil for wrong type, got %v", reqs)
	}
}

// ---- getStageStatus ----

func TestGetStageStatus_AllCompleted(t *testing.T) {
	r := nilReconciler()
	plan := planWithTargets(target("db", "rds"), target("app", "eks"))
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "db", State: hibernatorv1alpha1.StateCompleted},
		{Target: "app", State: hibernatorv1alpha1.StateCompleted},
	}
	stage := scheduler.ExecutionStage{Targets: []string{"db", "app"}}

	ss := r.getStageStatus(logr.Discard(), plan, stage)
	if !ss.AllTerminal {
		t.Error("expected AllTerminal=true")
	}
	if ss.CompletedCount != 2 {
		t.Errorf("CompletedCount = %d, want 2", ss.CompletedCount)
	}
	if ss.FailedCount != 0 {
		t.Errorf("FailedCount = %d, want 0", ss.FailedCount)
	}
}

func TestGetStageStatus_HasRunning(t *testing.T) {
	r := nilReconciler()
	plan := planWithTargets(target("db", "rds"))
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "db", State: hibernatorv1alpha1.StateRunning},
	}
	stage := scheduler.ExecutionStage{Targets: []string{"db"}}

	ss := r.getStageStatus(logr.Discard(), plan, stage)
	if ss.AllTerminal {
		t.Error("expected AllTerminal=false with running target")
	}
	if !ss.HasRunning {
		t.Error("expected HasRunning=true")
	}
}

func TestGetStageStatus_HasPending(t *testing.T) {
	r := nilReconciler()
	plan := planWithTargets(target("db", "rds"))
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "db", State: hibernatorv1alpha1.StatePending},
	}
	stage := scheduler.ExecutionStage{Targets: []string{"db"}}

	ss := r.getStageStatus(logr.Discard(), plan, stage)
	if !ss.HasPending {
		t.Error("expected HasPending=true")
	}
}

func TestGetStageStatus_HasFailed(t *testing.T) {
	r := nilReconciler()
	plan := planWithTargets(target("db", "rds"), target("app", "eks"))
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "db", State: hibernatorv1alpha1.StateFailed},
		{Target: "app", State: hibernatorv1alpha1.StateCompleted},
	}
	stage := scheduler.ExecutionStage{Targets: []string{"db", "app"}}

	ss := r.getStageStatus(logr.Discard(), plan, stage)
	if !ss.AllTerminal {
		t.Error("expected AllTerminal=true")
	}
	if ss.FailedCount != 1 {
		t.Errorf("FailedCount = %d, want 1", ss.FailedCount)
	}
}

func TestGetStageStatus_TargetNotInExecList(t *testing.T) {
	r := nilReconciler()
	plan := planWithTargets(target("db", "rds"))
	plan.Status.Executions = nil // empty — target not found

	stage := scheduler.ExecutionStage{Targets: []string{"db"}}

	ss := r.getStageStatus(logr.Discard(), plan, stage)
	if !ss.HasPending {
		t.Error("expected HasPending=true when target not yet in executions")
	}
	if ss.AllTerminal {
		t.Error("expected AllTerminal=false")
	}
}

// ---- buildOperationSummary ----

func TestBuildOperationSummary_AllCompleted(t *testing.T) {
	r := nilReconciler()
	plan := planWithTargets(target("db", "rds"), target("app", "eks"))
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "db", Executor: "rds", State: hibernatorv1alpha1.StateCompleted},
		{Target: "app", Executor: "eks", State: hibernatorv1alpha1.StateCompleted},
	}

	summary := r.buildOperationSummary(context.Background(), plan, "shutdown")
	if !summary.Success {
		t.Error("expected Success=true")
	}
	if len(summary.TargetResults) != 2 {
		t.Errorf("TargetResults count = %d, want 2", len(summary.TargetResults))
	}
	if summary.Operation != "shutdown" {
		t.Errorf("Operation = %q, want shutdown", summary.Operation)
	}
}

func TestBuildOperationSummary_WithFailure(t *testing.T) {
	r := nilReconciler()
	plan := planWithTargets(target("db", "rds"))
	plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
		{Target: "db", Executor: "rds", State: hibernatorv1alpha1.StateFailed},
	}

	summary := r.buildOperationSummary(context.Background(), plan, "wakeup")
	if summary.Success {
		t.Error("expected Success=false when a target failed")
	}
}

// Ensure imports are used
var _ = types.NamespacedName{}
var _ = reconcile.Request{}
