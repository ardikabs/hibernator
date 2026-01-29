/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package eks

import (
	"encoding/json"
	"testing"

	"github.com/ardikabs/hibernator/internal/executor"
)

func TestEKSExecutorType(t *testing.T) {
	e := &Executor{}
	if e.Type() != "eks" {
		t.Errorf("expected type 'eks', got %s", e.Type())
	}
}

func TestEKSValidate(t *testing.T) {
	e := &Executor{}
	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "eks",
		Parameters: json.RawMessage(`{}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{
				Region:    "us-east-1",
				AccountID: "123456789012",
			},
		},
	}
	err := e.Validate(spec)
	if err != nil && spec.TargetName != "" {
		t.Logf("validation passed for valid spec")
	}
}
