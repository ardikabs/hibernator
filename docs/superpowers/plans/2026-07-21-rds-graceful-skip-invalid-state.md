# RDS Graceful Skip on InvalidDBInstanceState / InvalidDBClusterState — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the RDS executor skip gracefully when AWS returns `InvalidDBInstanceState` or `InvalidDBClusterState`, instead of aborting the entire target.

**Architecture:** Modify the strategy-layer error handling in `instance_strategy.go` and `cluster_strategy.go` to classify these known AWS restrictions as `SkippedStale` (same as "not found"), while keeping all other unexpected errors as fatal. Add unit tests for all four paths.

**Tech Stack:** Go, AWS SDK v2 (`github.com/aws/aws-sdk-go-v2/service/rds`, `github.com/aws/smithy-go`), testify/mock.

**Beads Issue:** hib-qxu

---

### File Mapping

| File | Responsibility |
|------|---------------|
| `internal/executor/rds/instance_strategy.go` | Instance-level Stop/Start with AWS error classification |
| `internal/executor/rds/cluster_strategy.go` | Cluster-level Stop/Start with AWS error classification |
| `internal/executor/rds/rds_test.go` | Unit tests for all skip paths |

---

### Task 1: instanceStrategy.Stop() — Graceful Skip on InvalidDBInstanceState

**Files:**
- Modify: `internal/executor/rds/instance_strategy.go:156-167`

- [ ] **Step 1: Modify error handling in Stop()**

Change the `StopDBInstance` error block from:

```go
if _, err = client.StopDBInstance(ctx, &rds.StopDBInstanceInput{
    DBInstanceIdentifier: aws.String(id),
}); err != nil {
    var apiErr smithy.APIError
    if errors.As(err, &apiErr) && apiErr.ErrorCode() == "DBInstanceNotFound" {
        log.Info("instance not found, skipping ...", "instanceId", id)
        return DBInstanceState{Outcome: operationOutcomeSkippedStale}, nil
    }
    return nil, err
}
```

To:

```go
if _, err = client.StopDBInstance(ctx, &rds.StopDBInstanceInput{
    DBInstanceIdentifier: aws.String(id),
}); err != nil {
    var apiErr smithy.APIError
    if errors.As(err, &apiErr) {
        switch apiErr.ErrorCode() {
        case "DBInstanceNotFound", "InvalidDBInstanceState":
            log.Info("instance is in an invalid state for this operation, skipping ...",
                "instanceId", id, "errorCode", apiErr.ErrorCode())
            return DBInstanceState{Outcome: operationOutcomeSkippedStale}, nil
        }
    }
    return nil, err
}
```

- [ ] **Step 2: Run existing tests**

```bash
go test ./internal/executor/rds/... -run TestShutdown_StopInstance -v
```

Expected: PASS

---

### Task 2: instanceStrategy.Start() — Graceful Skip on InvalidDBInstanceState

**Files:**
- Modify: `internal/executor/rds/instance_strategy.go:240-251`

- [ ] **Step 1: Modify error handling in Start()**

Change the `StartDBInstance` error block from:

```go
_, err = client.StartDBInstance(ctx, &rds.StartDBInstanceInput{
    DBInstanceIdentifier: aws.String(id),
})

if err != nil {
    var apiErr smithy.APIError
    if errors.As(err, &apiErr) && apiErr.ErrorCode() == "DBInstanceNotFound" {
        log.Info("instance not found, skipping ...", "instanceId", id)
        return DBInstanceState{Outcome: operationOutcomeSkippedStale}, nil
    }
    return nil, err
}
```

To:

```go
_, err = client.StartDBInstance(ctx, &rds.StartDBInstanceInput{
    DBInstanceIdentifier: aws.String(id),
})

if err != nil {
    var apiErr smithy.APIError
    if errors.As(err, &apiErr) {
        switch apiErr.ErrorCode() {
        case "DBInstanceNotFound", "InvalidDBInstanceState":
            log.Info("instance is in an invalid state for this operation, skipping ...",
                "instanceId", id, "errorCode", apiErr.ErrorCode())
            return DBInstanceState{Outcome: operationOutcomeSkippedStale}, nil
        }
    }
    return nil, err
}
```

- [ ] **Step 2: Run existing tests**

```bash
go test ./internal/executor/rds/... -run TestWakeUp_StartInstance -v
```

Expected: PASS

---

### Task 3: clusterStrategy.Stop() — Graceful Skip on InvalidDBClusterState

**Files:**
- Modify: `internal/executor/rds/cluster_strategy.go:147-157`

- [ ] **Step 1: Modify error handling in Stop()**

Change the `StopDBCluster` error block from:

```go
if _, err = client.StopDBCluster(ctx, &rds.StopDBClusterInput{
    DBClusterIdentifier: aws.String(id),
}); err != nil {
    var apiErr smithy.APIError
    if errors.As(err, &apiErr) && apiErr.ErrorCode() == "DBClusterNotFoundFault" {
        log.Info("cluster not found, skipping ...", "clusterId", id)
        return DBClusterState{Outcome: operationOutcomeSkippedStale}, nil
    }
    return nil, err
}
```

To:

```go
if _, err = client.StopDBCluster(ctx, &rds.StopDBClusterInput{
    DBClusterIdentifier: aws.String(id),
}); err != nil {
    var apiErr smithy.APIError
    if errors.As(err, &apiErr) {
        switch apiErr.ErrorCode() {
        case "DBClusterNotFoundFault", "InvalidDBClusterState":
            log.Info("cluster is in an invalid state for this operation, skipping ...",
                "clusterId", id, "errorCode", apiErr.ErrorCode())
            return DBClusterState{Outcome: operationOutcomeSkippedStale}, nil
        }
    }
    return nil, err
}
```

- [ ] **Step 2: Run existing tests**

```bash
go test ./internal/executor/rds/... -run TestShutdown_StopCluster -v
```

Expected: PASS

---

### Task 4: clusterStrategy.Start() — Graceful Skip on InvalidDBClusterState

**Files:**
- Modify: `internal/executor/rds/cluster_strategy.go:230-240`

- [ ] **Step 1: Modify error handling in Start()**

Change the `StartDBCluster` error block from:

```go
_, err = client.StartDBCluster(ctx, &rds.StartDBClusterInput{
    DBClusterIdentifier: aws.String(id),
})
if err != nil {
    var apiErr smithy.APIError
    if errors.As(err, &apiErr) && apiErr.ErrorCode() == "DBClusterNotFoundFault" {
        log.Info("cluster not found, skipping ...", "clusterId", id)
        return DBClusterState{Outcome: operationOutcomeSkippedStale}, nil
    }
    return nil, err
}
```

To:

```go
_, err = client.StartDBCluster(ctx, &rds.StartDBClusterInput{
    DBClusterIdentifier: aws.String(id),
})
if err != nil {
    var apiErr smithy.APIError
    if errors.As(err, &apiErr) {
        switch apiErr.ErrorCode() {
        case "DBClusterNotFoundFault", "InvalidDBClusterState":
            log.Info("cluster is in an invalid state for this operation, skipping ...",
                "clusterId", id, "errorCode", apiErr.ErrorCode())
            return DBClusterState{Outcome: operationOutcomeSkippedStale}, nil
        }
    }
    return nil, err
}
```

- [ ] **Step 2: Run existing tests**

```bash
go test ./internal/executor/rds/... -run TestWakeUp_StartCluster -v
```

Expected: PASS

---

### Task 5: Add Unit Tests for All Four Skip Paths

**Files:**
- Modify: `internal/executor/rds/rds_test.go`

- [ ] **Step 1: Write test for InvalidDBInstanceState during Shutdown**

Append to `rds_test.go`:

```go
func TestShutdown_StopInstance_InvalidDBInstanceState(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	mockRDS.On("DescribeDBInstances", mock.Anything, mock.Anything).Return(&rds.DescribeDBInstancesOutput{
		DBInstances: []types.DBInstance{
			{
				DBInstanceIdentifier: aws.String("db-instance-1"),
				DBInstanceStatus:     aws.String("available"),
				DBInstanceClass:      aws.String("db.t3.medium"),
				DBInstanceArn:        aws.String("arn:aws:rds:us-east-1:123456789012:db:db-instance-1"),
			},
		},
	}, nil)
	mockRDS.On("StopDBInstance", mock.Anything, mock.Anything).Return(
		&rds.StopDBInstanceOutput{},
		&smithy.GenericAPIError{Code: "InvalidDBInstanceState", Message: "Cannot stop a Read Replica source"},
	)

	e := NewWithClients(
		func(cfg aws.Config) RDSClient { return mockRDS },
		func(cfg aws.Config) STSClient { return mockSTS },
		nil,
	)

	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		Parameters: json.RawMessage(`{"selector": {"InstanceIds": ["db-instance-1"]}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	result, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Contains(t, result.Message, "skipped")

	mockRDS.AssertExpectations(t)
}
```

- [ ] **Step 2: Write test for InvalidDBInstanceState during WakeUp**

Append to `rds_test.go`:

```go
func TestWakeUp_StartInstance_InvalidDBInstanceState(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	mockRDS.On("DescribeDBInstances", mock.Anything, mock.Anything).Return(&rds.DescribeDBInstancesOutput{
		DBInstances: []types.DBInstance{
			{
				DBInstanceIdentifier: aws.String("db-instance-1"),
				DBInstanceStatus:     aws.String("stopped"),
				DBInstanceClass:      aws.String("db.t3.medium"),
				DBInstanceArn:        aws.String("arn:aws:rds:us-east-1:123456789012:db:db-instance-1"),
			},
		},
	}, nil)
	mockRDS.On("StartDBInstance", mock.Anything, mock.Anything).Return(
		&rds.StartDBInstanceOutput{},
		&smithy.GenericAPIError{Code: "InvalidDBInstanceState", Message: "Cannot start in current state"},
	)

	e := NewWithClients(
		func(cfg aws.Config) RDSClient { return mockRDS },
		func(cfg aws.Config) STSClient { return mockSTS },
		nil,
	)

	spec := executor.Spec{
		TargetName: "test-db",
		TargetType: "rds",
		Parameters: json.RawMessage(`{"selector": {"InstanceIds": ["db-instance-1"]}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore := executor.RestoreData{
		Data: map[string]json.RawMessage{
			"instance:db-instance-1": json.RawMessage(`{"instanceId":"db-instance-1","wasRunning":true}`),
		},
	}

	result, err := e.WakeUp(ctx, logr.Discard(), spec, restore)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Contains(t, result.Message, "skipped")

	mockRDS.AssertExpectations(t)
}
```

- [ ] **Step 3: Write test for InvalidDBClusterState during Shutdown**

Append to `rds_test.go`:

```go
func TestShutdown_StopCluster_InvalidDBClusterState(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	mockRDS.On("DescribeDBClusters", mock.Anything, mock.Anything).Return(&rds.DescribeDBClustersOutput{
		DBClusters: []types.DBCluster{
			{
				DBClusterIdentifier: aws.String("cluster-1"),
				Status:              aws.String("available"),
				DBClusterArn:        aws.String("arn:aws:rds:us-east-1:123456789012:cluster:cluster-1"),
			},
		},
	}, nil)
	mockRDS.On("StopDBCluster", mock.Anything, mock.Anything).Return(
		&rds.StopDBClusterOutput{},
		&smithy.GenericAPIError{Code: "InvalidDBClusterState", Message: "Cannot stop cluster in current state"},
	)

	e := NewWithClients(
		func(cfg aws.Config) RDSClient { return mockRDS },
		func(cfg aws.Config) STSClient { return mockSTS },
		nil,
	)

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "rds",
		Parameters: json.RawMessage(`{"selector": {"ClusterIds": ["cluster-1"]}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	result, err := e.Shutdown(ctx, logr.Discard(), spec)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Contains(t, result.Message, "skipped")

	mockRDS.AssertExpectations(t)
}
```

- [ ] **Step 4: Write test for InvalidDBClusterState during WakeUp**

Append to `rds_test.go`:

```go
func TestWakeUp_StartCluster_InvalidDBClusterState(t *testing.T) {
	ctx := context.Background()
	mockRDS := &mocks.RDSClient{}
	mockSTS := &mocks.STSClient{}

	mockRDS.On("DescribeDBClusters", mock.Anything, mock.Anything).Return(&rds.DescribeDBClustersOutput{
		DBClusters: []types.DBCluster{
			{
				DBClusterIdentifier: aws.String("cluster-1"),
				Status:              aws.String("stopped"),
				DBClusterArn:        aws.String("arn:aws:rds:us-east-1:123456789012:cluster:cluster-1"),
			},
		},
	}, nil)
	mockRDS.On("StartDBCluster", mock.Anything, mock.Anything).Return(
		&rds.StartDBClusterOutput{},
		&smithy.GenericAPIError{Code: "InvalidDBClusterState", Message: "Cannot start cluster in current state"},
	)

	e := NewWithClients(
		func(cfg aws.Config) RDSClient { return mockRDS },
		func(cfg aws.Config) STSClient { return mockSTS },
		nil,
	)

	spec := executor.Spec{
		TargetName: "test-cluster",
		TargetType: "rds",
		Parameters: json.RawMessage(`{"selector": {"ClusterIds": ["cluster-1"]}}`),
		ConnectorConfig: executor.ConnectorConfig{
			AWS: &executor.AWSConnectorConfig{Region: "us-east-1"},
		},
	}

	restore := executor.RestoreData{
		Data: map[string]json.RawMessage{
			"cluster:cluster-1": json.RawMessage(`{"clusterId":"cluster-1","wasRunning":true}`),
		},
	}

	result, err := e.WakeUp(ctx, logr.Discard(), spec, restore)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Contains(t, result.Message, "skipped")

	mockRDS.AssertExpectations(t)
}
```

- [ ] **Step 5: Run all new tests**

```bash
go test ./internal/executor/rds/... -run "TestShutdown_StopInstance_InvalidDBInstanceState|TestWakeUp_StartInstance_InvalidDBInstanceState|TestShutdown_StopCluster_InvalidDBClusterState|TestWakeUp_StartCluster_InvalidDBClusterState" -v
```

Expected: All 4 PASS

---

### Task 6: Full Test Suite Verification

**Files:**
- Test: `internal/executor/rds/...`

- [ ] **Step 1: Run full RDS test suite**

```bash
go test ./internal/executor/rds/... -v -count=1
```

Expected: All tests PASS. No regressions.

- [ ] **Step 2: Build check**

```bash
go build ./internal/executor/rds/...
```

Expected: No compilation errors.

---

## Spec Coverage Checklist

| Spec Requirement | Task |
|-----------------|------|
| `InvalidDBInstanceState` graceful skip on Stop | Task 1 |
| `InvalidDBInstanceState` graceful skip on Start | Task 2 |
| `InvalidDBClusterState` graceful skip on Stop | Task 3 |
| `InvalidDBClusterState` graceful skip on Start | Task 4 |
| Unit tests for all four paths | Task 5 |
| Existing behavior unchanged | Task 6 |
