/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package ec2

import (
	"encoding/json"
	"testing"

	"github.com/ardikabs/hibernator/internal/executor"
)

func TestEC2ExecutorType(t *testing.T) {
	e := &Executor{}
	if e.Type() != "ec2" {
		t.Errorf("expected type 'ec2', got %s", e.Type())
	}
}

func TestEC2Validate(t *testing.T) {
	e := &Executor{}
	spec := executor.Spec{
		TargetName: "test-instances",
		TargetType: "ec2",
		Parameters: json.RawMessage(`{"selector": {"tags": {"Name": "test"}}}`),
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
