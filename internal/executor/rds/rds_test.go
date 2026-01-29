/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package rds

import (
	"encoding/json"
	"testing"

	"github.com/ardikabs/hibernator/internal/executor"
)

func TestRDSExecutorType(t *testing.T) {
	e := &Executor{}
	if e.Type() != "rds" {
		t.Errorf("expected type 'rds', got %s", e.Type())
	}
}

func TestRDSValidate(t *testing.T) {
	e := &Executor{}
	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		Parameters: json.RawMessage(`{"snapshotBeforeStop": true}`),
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
