//go:build e2e || floci || kind

// Package harness owns everything Floci executor E2E suites share:
// environment gating, AWS SDK endpoint injection, Floci readiness,
// instance seeding, state polling, and cleanup.
//
// To onboard a new executor: add its client to Clients, add a
// Seed<X> helper, and write test/floci/<x>_lifecycle_test.go.
package harness

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/ardikabs/hibernator/pkg/awsutil"
)

const (
	defaultEndpoint = "http://localhost:4566"
	defaultRegion   = "us-east-1"
	testAccessKey   = "test"
	testSecretKey   = "test"
	testAccountID   = "000000000000"
)

// Clients holds one AWS client per emulated service.
// New executors add their client as a new field (RDS, EKS, ...).
type Clients struct {
	EC2      *ec2.Client
	STS      *sts.Client
	Region   string
	Endpoint string
}

// Setup gates on FLOCI_ENABLED=1 (otherwise the test skips), points the
// AWS SDK at Floci via environment, and waits for Floci readiness.
// It must be called before any AWS call in every Floci suite test.
func Setup(t *testing.T) *Clients {
	t.Helper()
	if os.Getenv("FLOCI_ENABLED") != "1" {
		t.Fatal("Floci test requires FLOCI_ENABLED=1 with Floci on :4566 (see test/floci/compose.yml)")
	}

	endpoint := os.Getenv("FLOCI_ENDPOINT")
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	// Generic AWS_ENDPOINT_URL redirects every service client built from
	// this process, including ones constructed inside executors via
	// awsutil.BuildAWSConfig -> config.LoadDefaultConfig.
	t.Setenv("AWS_ENDPOINT_URL", endpoint)
	t.Setenv("AWS_ACCESS_KEY_ID", testAccessKey)
	t.Setenv("AWS_SECRET_ACCESS_KEY", testSecretKey)
	t.Setenv("AWS_REGION", defaultRegion)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(defaultRegion))
	if err != nil {
		t.Fatalf("load AWS config for Floci: %v", err)
	}

	c := &Clients{
		EC2:      ec2.NewFromConfig(cfg),
		STS:      sts.NewFromConfig(cfg),
		Region:   defaultRegion,
		Endpoint: endpoint,
	}

	// Readiness: Floci answers STS once its HTTP router is up.
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := c.STS.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{}); err == nil {
			return c
		} else if time.Now().After(deadline) {
			t.Fatalf("floci not ready at %s after 60s: %v", endpoint, err)
		}
		time.Sleep(2 * time.Second)
	}
}

// Nonce returns a per-run tag value so parallel/sequential runs never
// select each other's instances.
func Nonce() string {
	return fmt.Sprintf("floci-%d", time.Now().UnixNano())
}

// AWSConnectorConfig builds the executor connector config for Floci:
// static test credentials, no role assumption (Floci accepts any non-empty creds).
func AWSConnectorConfig(region string) *awsutil.AWSConnectorConfig {
	return &awsutil.AWSConnectorConfig{
		Region:          region,
		AccountID:       testAccountID,
		AccessKeyID:     testAccessKey,
		SecretAccessKey: testSecretKey,
	}
}

// SeedEC2 launches count instances tagged with tags (plus Name=name),
// waits until all are running, and registers termination cleanup.
// Floci maps ami-amazonlinux2023 to a real container image and seeds a
// default VPC/subnet per region on first use.
func SeedEC2(t *testing.T, ctx context.Context, c *Clients, name string, tags map[string]string, count int32) []string {
	t.Helper()
	ec2Tags := make([]ec2types.Tag, 0, len(tags)+1)
	for k, v := range tags {
		ec2Tags = append(ec2Tags, ec2types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	ec2Tags = append(ec2Tags, ec2types.Tag{Key: aws.String("Name"), Value: aws.String(name)})

	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := c.EC2.RunInstances(requestCtx, &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-amazonlinux2023"),
		InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount:     aws.Int32(count),
		MaxCount:     aws.Int32(count),
		TagSpecifications: []ec2types.TagSpecification{
			{ResourceType: ec2types.ResourceTypeInstance, Tags: ec2Tags},
		},
	})
	if err != nil {
		t.Fatalf("seed EC2 instances: %v", err)
	}
	ids := make([]string, 0, len(out.Instances))
	for _, inst := range out.Instances {
		ids = append(ids, aws.ToString(inst.InstanceId))
	}
	if len(ids) == 0 {
		t.Fatal("seed EC2 instances: Floci returned zero instances")
	}

	WaitForInstanceState(t, ctx, c.EC2, ids, ec2types.InstanceStateNameRunning, 3*time.Minute)

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		var terminateErr error
		for cleanupCtx.Err() == nil {
			terminateErr = TerminateEC2(cleanupCtx, c.EC2, ids)
			if terminateErr == nil {
				break
			}
			select {
			case <-cleanupCtx.Done():
			case <-time.After(2 * time.Second):
			}
		}
		if terminateErr != nil {
			t.Errorf("cleanup terminate instances %v: %v", ids, terminateErr)
			return
		}
		WaitForInstanceState(t, cleanupCtx, c.EC2, ids, ec2types.InstanceStateNameTerminated, 90*time.Second)
	})
	return ids
}

// WaitForInstanceState polls DescribeInstances until every id is in want
// state, and fails the test with last-seen states on timeout.
func WaitForInstanceState(t *testing.T, ctx context.Context, client *ec2.Client, ids []string, want ec2types.InstanceStateName, timeout time.Duration) {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var last map[string]ec2types.InstanceStateName
	var lastErr error
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		requestCtx, requestCancel := context.WithTimeout(waitCtx, 15*time.Second)
		states, err := describeStates(requestCtx, client, ids)
		requestCancel()
		if err == nil {
			last = states
			allMatch := true
			for _, id := range ids {
				if last[id] != want {
					allMatch = false
					break
				}
			}
			if allMatch {
				return
			}
		} else {
			lastErr = err
		}
		select {
		case <-waitCtx.Done():
			t.Fatalf("instances %v did not reach %q within %s (last: %v, last error: %v)", ids, want, timeout, last, lastErr)
		case <-ticker.C:
		}
	}
}

func describeStates(ctx context.Context, client *ec2.Client, ids []string) (map[string]ec2types.InstanceStateName, error) {
	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: ids})
	if err != nil {
		return nil, err
	}
	states := make(map[string]ec2types.InstanceStateName, len(ids))
	for _, r := range out.Reservations {
		for _, inst := range r.Instances {
			states[aws.ToString(inst.InstanceId)] = inst.State.Name
		}
	}
	return states, nil
}

// TerminateEC2 terminates ids. Best-effort: callers log (not fail) on error.
func TerminateEC2(ctx context.Context, client *ec2.Client, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: ids})
	return err
}
