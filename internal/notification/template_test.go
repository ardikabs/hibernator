/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

func testSinkPayload(event string) Payload {
	return Payload{
		ID:        types.NamespacedName{Namespace: "default", Name: "test-plan"},
		Labels:    map[string]string{"env": "staging"},
		Event:     event,
		Timestamp: time.Date(2026, 3, 28, 10, 0, 0, 0, time.UTC),
		Phase:     string(hibernatorv1alpha1.PhaseHibernating),
		Operation: string(hibernatorv1alpha1.OperationHibernate),
		CycleID:   "abc123",
		SinkName:  "test-sink",
		SinkType:  "slack",
	}
}

func TestRendererRenderWithSlackTemplate(t *testing.T) {
	engine := NewTemplateEngine(logr.Discard())

	p := testSinkPayload("Start")
	tmpl := `{{ if eq .Event "Start" -}}Starting{{ end }} {{ .Plan.Name }}`
	msg := engine.Render(context.Background(), tmpl, p)

	assert.Equal(t, "Starting test-plan", msg)
}

func TestRendererRenderWithTargets(t *testing.T) {
	engine := NewTemplateEngine(logr.Discard())

	p := testSinkPayload("Success")
	p.Targets = []TargetInfo{
		{Name: "eks-prod", Executor: "eks", State: "Completed"},
		{Name: "rds-main", Executor: "rds", State: "Completed"},
	}

	tmpl := `{{ range .Targets }}{{ .Name }}({{ .Executor }}):{{ .State }} {{ end }}`
	msg := engine.Render(context.Background(), tmpl, p)

	assert.Contains(t, msg, "eks-prod(eks):Completed")
	assert.Contains(t, msg, "rds-main(rds):Completed")
}

func TestRendererRenderWithSprig(t *testing.T) {
	engine := NewTemplateEngine(logr.Discard())

	p := testSinkPayload("Failure")
	tmpl := `{{ .Plan.Name | upper }} - {{ .Event | lower }}`
	msg := engine.Render(context.Background(), tmpl, p)

	assert.Equal(t, "TEST-PLAN - failure", msg)
}

func TestRendererRenderInvalidTemplateFallsBack(t *testing.T) {
	engine := NewTemplateEngine(logr.Discard())

	p := testSinkPayload("Start")
	tmpl := `{{ .DoesNotExist | nonexistentFunc }}`
	msg := engine.Render(context.Background(), tmpl, p)

	// Falls back to plain text.
	assert.Contains(t, msg, "[Start]")
	assert.Contains(t, msg, hibernatorv1alpha1.OperationHibernate)
	assert.Contains(t, msg, "test-plan")
}

func TestRendererRenderWithPreviousPhase(t *testing.T) {
	engine := NewTemplateEngine(logr.Discard())

	p := testSinkPayload("PhaseChange")
	p.PreviousPhase = "Active"

	tmpl := `{{ .Phase }} from {{ .PreviousPhase }}`
	msg := engine.Render(context.Background(), tmpl, p)

	assert.Equal(t, "Hibernating from Active", msg)
}

func TestRendererRenderWithError(t *testing.T) {
	engine := NewTemplateEngine(logr.Discard())

	p := testSinkPayload("Failure")
	p.ErrorMessage = "connection refused"
	p.RetryCount = 3

	tmpl := `Error: {{ .ErrorMessage }} (retry {{ .RetryCount }})`
	msg := engine.Render(context.Background(), tmpl, p)

	assert.Equal(t, "Error: connection refused (retry 3)", msg)
}

func TestPayloadToContext(t *testing.T) {
	p := Payload{
		ID:            types.NamespacedName{Namespace: "prod", Name: "plan-a"},
		Labels:        map[string]string{"env": "prod"},
		Event:         string(hibernatorv1alpha1.EventFailure),
		Phase:         string(hibernatorv1alpha1.PhaseError),
		PreviousPhase: string(hibernatorv1alpha1.PhaseHibernating),
		Operation:     string(hibernatorv1alpha1.OperationHibernate),
		CycleID:       "c1",
		ErrorMessage:  "timeout",
		RetryCount:    2,
		SinkName:      "slack-alerts",
		SinkType:      "slack",
		Targets: []TargetInfo{
			{Name: "db", Executor: "rds", State: "Failed", Message: "timeout"},
		},
	}

	nc := payloadToContext(p)

	assert.Equal(t, "plan-a", nc.Plan.Name)
	assert.Equal(t, "prod", nc.Plan.Namespace)
	assert.Equal(t, map[string]string{"env": "prod"}, nc.Plan.Labels)
	assert.Equal(t, string(hibernatorv1alpha1.EventFailure), nc.Event)
	assert.Equal(t, string(hibernatorv1alpha1.PhaseError), nc.Phase)
	assert.Equal(t, string(hibernatorv1alpha1.PhaseHibernating), nc.PreviousPhase)
	assert.Equal(t, string(hibernatorv1alpha1.OperationHibernate), nc.Operation)
	assert.Equal(t, "c1", nc.CycleID)
	assert.Equal(t, "timeout", nc.ErrorMessage)
	assert.Equal(t, int32(2), nc.RetryCount)
	assert.Equal(t, "slack-alerts", nc.SinkName)
	assert.Equal(t, "slack", nc.SinkType)
	require.Len(t, nc.Targets, 1)
	assert.Equal(t, "db", nc.Targets[0].Name)
}

func TestPlainFallback(t *testing.T) {
	engine := NewTemplateEngine(logr.Discard())

	nc := NotificationContext{
		Event:        string(hibernatorv1alpha1.EventFailure),
		Phase:        string(hibernatorv1alpha1.PhaseError),
		Operation:    string(hibernatorv1alpha1.OperationHibernate),
		ErrorMessage: "something broke",
		Plan: PlanInfo{
			Name:      "critical-plan",
			Namespace: "prod",
		},
	}

	msg := engine.plainFallback(nc)

	assert.Equal(t, "[Failure] shutdown — prod/critical-plan | Phase: Error | Error: something broke", msg)
}

func TestPlainFallbackMinimal(t *testing.T) {
	engine := NewTemplateEngine(logr.Discard())

	nc := NotificationContext{
		Event:     string(hibernatorv1alpha1.EventStart),
		Operation: string(hibernatorv1alpha1.OperationWakeUp),
		Plan: PlanInfo{
			Name:      "plan-a",
			Namespace: "ns",
		},
	}

	msg := engine.plainFallback(nc)

	assert.Equal(t, "[Start] wakeup — ns/plan-a", msg)
}

func TestNewTemplateEngineDoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		NewTemplateEngine(logr.Discard())
	})
}
