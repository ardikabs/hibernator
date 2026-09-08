//go:build awsenv

package awsenv

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"

	"github.com/ardikabs/hibernator/internal/executor"
	rdsexec "github.com/ardikabs/hibernator/internal/executor/rds"
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/ardikabs/hibernator/test/awsenv/harness"
)

func rdsSpec(region, instanceID string, snapshotBefore bool, restoreTo map[string]json.RawMessage) executor.Spec {
	paramsJSON, err := json.Marshal(executorparams.RDSParameters{
		Selector:           executorparams.RDSSelector{InstanceIds: []string{instanceID}},
		SnapshotBeforeStop: snapshotBefore,
		AwaitCompletion:    executorparams.AwaitCompletion{Enabled: true, Timeout: "10m"},
	})
	if err != nil {
		panic(err)
	}
	return executor.Spec{
		TargetName: "awsenv-rds",
		TargetType: "rds",
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

func TestRDSInstanceLifecycle(t *testing.T) {
	ctx := context.Background()
	env := harness.Setup(t)
	fixture := env.Provisioner.ProvisionRDS(t, ctx, harness.RDSFixtureSpec{Prefix: "awsenv-rds"})
	collected := map[string]json.RawMessage{}

	exec := rdsexec.New()
	spec := rdsSpec(env.Region, fixture.InstanceID, false, collected)
	require.NoError(t, exec.Validate(spec))

	_, err := exec.Shutdown(ctx, logr.Discard(), spec)
	harness.RequireNoErrorOrSkip(t, harness.CapabilityRDSLifecycle, "StopDBInstance", err)

	key := "instance:" + fixture.InstanceID
	require.Contains(t, collected, key, "restore data must be reported for the instance")
	var state rdsexec.DBInstanceState
	require.NoError(t, json.Unmarshal(collected[key], &state))
	require.Equal(t, fixture.InstanceID, state.InstanceId)
	require.True(t, state.WasRunning)

	harness.WaitForDBInstanceState(t, ctx, env.Clients.RDS, fixture.InstanceID, "stopped", 10*time.Minute)

	wakeupRes, err := exec.WakeUp(ctx, logr.Discard(), spec, executor.RestoreData{
		Type:   "rds",
		Data:   collected,
		IsLive: true,
	})
	require.NoError(t, err)
	require.Contains(t, wakeupRes.Message, "started")

	harness.WaitForDBInstanceState(t, ctx, env.Clients.RDS, fixture.InstanceID, "available", 10*time.Minute)
}

func TestRDSInstanceSnapshotLifecycle(t *testing.T) {
	ctx := context.Background()
	env := harness.Setup(t)
	fixture := env.Provisioner.ProvisionRDS(t, ctx, harness.RDSFixtureSpec{Prefix: "awsenv-rds-snap"})
	collected := map[string]json.RawMessage{}

	exec := rdsexec.New()
	spec := rdsSpec(env.Region, fixture.InstanceID, true, collected)
	require.NoError(t, exec.Validate(spec))

	_, err := exec.Shutdown(ctx, logr.Discard(), spec)
	harness.RequireNoErrorOrSkip(t, harness.CapabilityRDSLifecycle, "StopDBInstance", err)

	key := "instance:" + fixture.InstanceID
	var state rdsexec.DBInstanceState
	require.NoError(t, json.Unmarshal(collected[key], &state))
	require.NotEmpty(t, state.SnapshotId, "snapshotBeforeStop must record a snapshot id")

	out, err := env.Clients.RDS.DescribeDBSnapshots(ctx, &rds.DescribeDBSnapshotsInput{
		DBSnapshotIdentifier: aws.String(state.SnapshotId),
	})
	harness.RequireNoErrorOrSkip(t, harness.CapabilityRDSSnapshot, "DescribeDBSnapshots", err)
	require.Len(t, out.DBSnapshots, 1)

	wakeupRes, err := exec.WakeUp(ctx, logr.Discard(), spec, executor.RestoreData{
		Type:   "rds",
		Data:   collected,
		IsLive: true,
	})
	require.NoError(t, err)
	require.Contains(t, wakeupRes.Message, "started")
	harness.WaitForDBInstanceState(t, ctx, env.Clients.RDS, fixture.InstanceID, "available", 10*time.Minute)
}

func TestRDSValidateRejectsEmptySelector(t *testing.T) {
	exec := rdsexec.New()
	params, err := json.Marshal(executorparams.RDSParameters{})
	require.NoError(t, err)
	err = exec.Validate(executor.Spec{
		TargetType: "rds",
		Parameters: params,
		ConnectorConfig: executor.ConnectorConfig{
			AWS: harness.AWSConnectorConfig("us-east-1"),
		},
	})
	require.ErrorContains(t, err, "selector must specify at least one of")
}
