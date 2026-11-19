/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package workloadscaler

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
	for _, resource := range []string{"deployments", "statefulsets", "replicasets"} {
		assert.Contains(t, req.Shutdown.K8SRules, executor.K8SRule{
			Group: "apps", Resource: resource, Verbs: []string{"list"},
		})
		assert.Contains(t, req.Shutdown.K8SRules, executor.K8SRule{
			Group: "apps", Resource: resource, Subresource: "scale", Verbs: []string{"get", "update"},
		})
		assert.Contains(t, req.WakeUp.K8SRules, executor.K8SRule{
			Group: "apps", Resource: resource, Subresource: "scale", Verbs: []string{"get", "update"},
		})
	}
	assert.Contains(t, req.Shutdown.K8SRules, executor.K8SRule{Resource: "namespaces", Verbs: []string{"list"}})
	assert.Empty(t, req.Shutdown.AWSActions)
}
