//go:build awsenv

package awsenv

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"

	"github.com/ardikabs/hibernator/internal/executor"
	ec2exec "github.com/ardikabs/hibernator/internal/executor/ec2"
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/ardikabs/hibernator/test/awsenv/harness"
)

func ec2Spec(cRegion string, tags map[string]string, restoreTo map[string]json.RawMessage) executor.Spec {
	paramsJSON, err := json.Marshal(executorparams.EC2Parameters{
		Selector:        executorparams.EC2Selector{Tags: tags},
		AwaitCompletion: executorparams.AwaitCompletion{Enabled: true, Timeout: "3m"},
	})
	if err != nil {
		panic(err)
	}
	return executor.Spec{
		TargetName: "awsenv-ec2",
		TargetType: "ec2",
		Parameters: paramsJSON,
		ConnectorConfig: executor.ConnectorConfig{
			AWS: harness.AWSConnectorConfig(cRegion),
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

func TestEC2_FullLifecycle(t *testing.T) {
	ctx := context.Background()
	c := harness.Setup(t)
	tags := map[string]string{"awsenv-e2e": harness.Nonce(), "Role": "bastion"}

	ids := c.Provisioner.ProvisionEC2(t, ctx, harness.EC2FixtureSpec{Name: "awsenv-lifecycle", Tags: tags, Count: 2}).InstanceIDs
	require.Len(t, ids, 2)
	_, err := c.Clients.EC2.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: []string{ids[1]}})
	require.NoError(t, err)
	harness.WaitForInstanceState(t, ctx, c.Clients.EC2, []string{ids[1]}, ec2types.InstanceStateNameStopped, 3*time.Minute)

	exec := ec2exec.New()
	collected := map[string]json.RawMessage{}
	spec := ec2Spec(c.Clients.Region, tags, collected)
	require.NoError(t, exec.Validate(spec))

	shutdownRes, err := exec.Shutdown(ctx, logr.Discard(), spec)
	harness.RequireNoErrorOrSkip(t, harness.CapabilityEC2Lifecycle, "StopInstances", err)
	require.Contains(t, shutdownRes.Message, "stopped 1 of 2 EC2 instance(s)")
	require.Contains(t, shutdownRes.Message, "all instances confirmed stopped")
	require.Len(t, collected, 2, "restore data must be reported for every discovered instance")
	var originallyRunning, originallyStopped ec2exec.InstanceState
	require.NoError(t, json.Unmarshal(collected[ids[0]], &originallyRunning))
	require.NoError(t, json.Unmarshal(collected[ids[1]], &originallyStopped))
	require.True(t, originallyRunning.WasRunning)
	require.False(t, originallyStopped.WasRunning)

	harness.WaitForInstanceState(t, ctx, c.Clients.EC2, ids, ec2types.InstanceStateNameStopped, 3*time.Minute)

	wakeupRes, err := exec.WakeUp(ctx, logr.Discard(), spec, executor.RestoreData{
		Type:   "ec2",
		Data:   collected,
		IsLive: true,
	})
	require.NoError(t, err)
	require.Contains(t, wakeupRes.Message, "started 1 EC2 instance(s)")
	require.Contains(t, wakeupRes.Message, "all instances confirmed running")

	harness.WaitForInstanceState(t, ctx, c.Clients.EC2, []string{ids[0]}, ec2types.InstanceStateNameRunning, 3*time.Minute)
	harness.WaitForInstanceState(t, ctx, c.Clients.EC2, []string{ids[1]}, ec2types.InstanceStateNameStopped, 30*time.Second)
}

func TestEC2_ValidateRejectsEmptySelector(t *testing.T) {
	exec := ec2exec.New()
	params, err := json.Marshal(executorparams.EC2Parameters{})
	require.NoError(t, err)
	err = exec.Validate(executor.Spec{
		TargetType: "ec2",
		Parameters: params,
		ConnectorConfig: executor.ConnectorConfig{
			AWS: harness.AWSConnectorConfig("us-east-1"),
		},
	})
	require.ErrorContains(t, err, "either tags, tagSelector, or instanceIds")
}

func TestEC2_NoMatchIsNoop(t *testing.T) {
	ctx := context.Background()
	c := harness.Setup(t)
	tags := map[string]string{"awsenv-e2e": "no-such-" + harness.Nonce()}

	exec := ec2exec.New()
	collected := map[string]json.RawMessage{}

	shutdownRes, err := exec.Shutdown(ctx, logr.Discard(), ec2Spec(c.Clients.Region, tags, collected))
	require.NoError(t, err)
	require.Contains(t, shutdownRes.Message, "stopped 0 of 0 EC2 instance(s)")
	require.Empty(t, collected)

	wakeupRes, err := exec.WakeUp(ctx, logr.Discard(), ec2Spec(c.Clients.Region, tags, collected), executor.RestoreData{})
	require.NoError(t, err)
	require.Contains(t, wakeupRes.Message, "wakeup completed for EC2 (no restore data)")
}
