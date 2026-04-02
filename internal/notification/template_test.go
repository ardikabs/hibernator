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

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

func testSinkPayload(event string) Payload {
	return Payload{
		Plan: PlanInfo{
			Name:      "test-plan",
			Namespace: "default",
			Labels:    map[string]string{"env": "staging"},
		},
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

func TestPayloadTemplateAccess(t *testing.T) {
	engine := NewTemplateEngine(logr.Discard())

	p := Payload{
		Plan: PlanInfo{
			Name:      "plan-a",
			Namespace: "prod",
			Labels:    map[string]string{"env": "prod"},
		},
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

	tmpl := `{{ .Plan.Name }} {{ .Plan.Namespace }} {{ .Phase }} {{ .Operation }}`
	msg := engine.Render(context.Background(), tmpl, p)

	assert.Equal(t, "plan-a prod Error shutdown", msg)
}

func TestPlainFallback(t *testing.T) {
	engine := NewTemplateEngine(logr.Discard())

	p := Payload{
		Event:        string(hibernatorv1alpha1.EventFailure),
		Phase:        string(hibernatorv1alpha1.PhaseError),
		Operation:    string(hibernatorv1alpha1.OperationHibernate),
		ErrorMessage: "something broke",
		Plan: PlanInfo{
			Name:      "critical-plan",
			Namespace: "prod",
		},
	}

	msg := engine.plainFallback(p)

	assert.Equal(t, "[Failure] shutdown — prod/critical-plan | Phase: Error | Error: something broke", msg)
}

func TestPlainFallbackMinimal(t *testing.T) {
	engine := NewTemplateEngine(logr.Discard())

	p := Payload{
		Event:     string(hibernatorv1alpha1.EventStart),
		Operation: string(hibernatorv1alpha1.OperationWakeUp),
		Plan: PlanInfo{
			Name:      "plan-a",
			Namespace: "ns",
		},
	}

	msg := engine.plainFallback(p)

	assert.Equal(t, "[Start] wakeup — ns/plan-a", msg)
}

func TestRendererRenderWithConnectorInfo(t *testing.T) {
	engine := NewTemplateEngine(logr.Discard())

	p := testSinkPayload("Success")
	p.Targets = []TargetInfo{
		{
			Name:     "eks-prod",
			Executor: "eks",
			State:    "Completed",
			Connector: ConnectorInfo{
				Kind:        "K8SCluster",
				Name:        "prod-cluster",
				Provider:    "aws",
				AccountID:   "123456789012",
				Region:      "us-east-1",
				ClusterName: "prod-eks",
			},
		},
		{
			Name:     "rds-main",
			Executor: "rds",
			State:    "Completed",
			Connector: ConnectorInfo{
				Kind:      "CloudProvider",
				Name:      "aws-prod",
				Provider:  "aws",
				AccountID: "123456789012",
				Region:    "us-west-2",
			},
		},
	}

	tmpl := `{{ range .Targets }}{{ .Name }}={{ .Connector.AccountID }}/{{ .Connector.Region }}{{ if .Connector.ClusterName }}({{ .Connector.ClusterName }}){{ end }} {{ end }}`
	msg := engine.Render(context.Background(), tmpl, p)

	assert.Contains(t, msg, "eks-prod=123456789012/us-east-1(prod-eks)")
	assert.Contains(t, msg, "rds-main=123456789012/us-west-2")
	assert.NotContains(t, msg, "rds-main=123456789012/us-west-2(")
}

func TestRendererRenderWithEmptyConnectorInfo(t *testing.T) {
	engine := NewTemplateEngine(logr.Discard())

	p := testSinkPayload("Success")
	p.Targets = []TargetInfo{
		{Name: "noop-target", Executor: "noop", State: "Completed"},
	}

	tmpl := `{{ range .Targets }}{{ .Name }}{{ if .Connector.AccountID }}:{{ .Connector.AccountID }}{{ end }}{{ end }}`
	msg := engine.Render(context.Background(), tmpl, p)

	assert.Equal(t, "noop-target", msg)
}

func TestNewTemplateEngineDoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		NewTemplateEngine(logr.Discard())
	})
}
