/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package eks

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ardikabs/hibernator/internal/executor"
)

func TestExecutor_Requires(t *testing.T) {
	req := New().Requires()

	require.NoError(t, req.Validate())
	assert.Equal(t, executor.ConnectorKindCloudProvider, req.ConnectorKind)
	assert.Subset(t, req.Shutdown.AWSActions, []string{
		"eks:DescribeCluster", "eks:ListNodegroups", "eks:DescribeNodegroup", "eks:UpdateNodegroupConfig",
	})
	assert.Subset(t, req.WakeUp.AWSActions, []string{"eks:DescribeNodegroup", "eks:UpdateNodegroupConfig"})
	assert.Empty(t, req.Shutdown.K8SRules)
}
