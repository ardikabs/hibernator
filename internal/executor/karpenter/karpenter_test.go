/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package karpenter

import (
	"encoding/json"
	"testing"

	"github.com/ardikabs/hibernator/internal/executor"
)

func TestKarpenterExecutorType(t *testing.T) {
	e := &Executor{}
	if e.Type() != "karpenter" {
		t.Errorf("expected type 'karpenter', got %s", e.Type())
	}
}

func TestKarpenterValidate(t *testing.T) {
	e := &Executor{}
	spec := executor.Spec{
		TargetName: "test-nodepool",
		TargetType: "karpenter",
		Parameters: json.RawMessage(`{"targetNodePools": ["default"]}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{
				Region: "us-east-1",
			},
		},
	}
	err := e.Validate(spec)
	if err != nil {
		t.Logf("validation check: %v", err)
	}
}
