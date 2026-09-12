//go:build awsenv

package awsenv

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"

	"github.com/ardikabs/hibernator/internal/executor"
	eksexec "github.com/ardikabs/hibernator/internal/executor/eks"
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/ardikabs/hibernator/test/awsenv/harness"
)

func eksSpec(region, cluster, nodegroup string, restoreTo map[string]json.RawMessage) executor.Spec {
	paramsJSON, err := json.Marshal(executorparams.EKSParameters{
		ClusterName:       cluster,
		NodeGroups:        []executorparams.EKSNodeGroup{{Name: nodegroup}},
		AwaitCompletion:   executorparams.AwaitCompletion{Enabled: false},
	})
	if err != nil {
		panic(err)
	}
	return executor.Spec{
		TargetName: "awsenv-eks",
		TargetType: "eks",
		Parameters: paramsJSON,
		ConnectorConfig: executor.ConnectorConfig{
			AWS: harness.AWSConnectorConfig(region),
		},
		ReportStateCallback: func(key string, value interface{}) error {
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			restoreTo[key] = raw
			return nil
		},
	}
}

func TestEKSNodeGroupLifecycle(t *testing.T) {
	ctx := context.Background()
	env := harness.Setup(t)
	fixture := env.Provisioner.ProvisionEKS(t, ctx, harness.EKSFixtureSpec{Prefix: "awsenv-eks"})
	collected := map[string]json.RawMessage{}

	exec := eksexec.New()
	spec := eksSpec(env.Region, fixture.ClusterName, fixture.NodegroupName, collected)
	require.NoError(t, exec.Validate(spec))

	_, err := exec.Shutdown(ctx, logr.Discard(), spec)
	harness.RequireNoErrorOrSkip(t, harness.CapabilityEKSNodeGroupUpdate, "UpdateNodegroupConfig", err)

	var state eksexec.NodeGroupState
	require.NoError(t, json.Unmarshal(collected[fixture.NodegroupName], &state))
	require.Equal(t, int32(1), state.MinSize)
	require.Equal(t, int32(1), state.DesiredSize)
	require.Equal(t, int32(2), state.MaxSize)
	require.True(t, state.WasScaled)

	harness.WaitForNodegroupState(t, ctx, env.Clients.EKS, fixture.ClusterName, fixture.NodegroupName, 0, 0, 2, 5*time.Minute)

	wakeupRes, err := exec.WakeUp(ctx, logr.Discard(), spec, executor.RestoreData{
		Type:   "eks",
		Data:   collected,
		IsLive: true,
	})
	require.NoError(t, err)
	require.Contains(t, wakeupRes.Message, "restored 1 node group(s)")

	harness.WaitForNodegroupState(t, ctx, env.Clients.EKS, fixture.ClusterName, fixture.NodegroupName, 1, 1, 2, 5*time.Minute)
}

func TestEKSValidateRejectsMissingClusterName(t *testing.T) {
	exec := eksexec.New()
	params, err := json.Marshal(executorparams.EKSParameters{})
	require.NoError(t, err)
	err = exec.Validate(executor.Spec{
		TargetType: "eks",
		Parameters: params,
		ConnectorConfig: executor.ConnectorConfig{
			AWS: harness.AWSConnectorConfig("us-east-1"),
		},
	})
	require.ErrorContains(t, err, "clusterName is required")
}
