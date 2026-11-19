/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package karpenter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ardikabs/hibernator/internal/executor"
)

func TestExecutor_Requires(t *testing.T) {
	req := New().Requires()

	require.NoError(t, req.Validate())
	assert.Equal(t, executor.ConnectorKindK8SCluster, req.ConnectorKind)
	assert.Contains(t, req.Shutdown.K8SRules, executor.K8SRule{
		Group: "karpenter.sh", Resource: "nodepools", Verbs: []string{"get", "list", "delete"},
	})
	assert.Contains(t, req.Shutdown.K8SRules, executor.K8SRule{Resource: "nodes", Verbs: []string{"list"}})
	assert.Contains(t, req.WakeUp.K8SRules, executor.K8SRule{
		Group: "karpenter.sh", Resource: "nodepools", Verbs: []string{"get", "create"},
	})
	assert.Empty(t, req.Shutdown.AWSActions)
}
