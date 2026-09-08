//go:build e2e || awsenv || kind

// Package harness owns everything the awsenv executor integration suites
// share: environment gating, AWS SDK endpoint injection, backend readiness,
// instance seeding, state polling, and cleanup.
//
// To onboard a new executor: add its client to Clients, add a
// Seed<X> helper, and write test/awsenv/<x>_lifecycle_test.go.
package harness

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/rds"
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
type Clients struct {
	EC2      *ec2.Client
	RDS      *rds.Client
	EKS      *eks.Client
	STS      *sts.Client
	Region   string
	Endpoint string
}

// Setup gates on AWSENV_ENABLED=1 (otherwise the test fails), points the
// AWS SDK at the configured endpoint via environment, and waits for backend
// readiness. It must be called before any AWS call in every awsenv suite test.
func Setup(t *testing.T) *Environment {
	t.Helper()
	if os.Getenv("AWSENV_ENABLED") != "1" {
		t.Fatal("AWS environment test requires AWSENV_ENABLED=1 with an endpoint on :4566 (see test/awsenv/environments/floci/compose.yml)")
	}

	provider, err := resolveProvider()
	if err != nil {
		t.Fatal(err.Error())
	}

	endpoint := os.Getenv("AWSENV_ENDPOINT_URL")
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
		t.Fatalf("load AWS config: %v", err)
	}

	clients := &Clients{
		EC2:      ec2.NewFromConfig(cfg),
		RDS:      rds.NewFromConfig(cfg),
		EKS:      eks.NewFromConfig(cfg),
		STS:      sts.NewFromConfig(cfg),
		Region:   defaultRegion,
		Endpoint: endpoint,
	}

	// Readiness: the backend answers STS once its HTTP router is up. Each
	// attempt gets its own deadline so a hung request cannot mask the backend
	// error behind the shared context timeout.
	deadline := time.Now().Add(60 * time.Second)
	var lastErr error
	for {
		attemptCtx, attemptCancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err := clients.STS.GetCallerIdentity(attemptCtx, &sts.GetCallerIdentityInput{})
		attemptCancel()
		if err == nil {
			return &Environment{Provider: provider, Endpoint: endpoint, Region: defaultRegion, Clients: clients, Provisioner: &FlociProvisioner{Clients: clients}}
		}
		lastErr = err
		if time.Now().After(deadline) {
			t.Fatalf("%s not ready at %s after 60s (last error: %v)", provider, endpoint, lastErr)
		}
		time.Sleep(2 * time.Second)
	}
}

// Nonce returns a per-run tag value so parallel/sequential runs never
// select each other's instances. The random suffix guards against two tests
// starting within the same nanosecond.
func Nonce() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("awsenv-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("awsenv-%d-%x", time.Now().UnixNano(), b)
}

// notFoundCodes are AWS "resource does not exist" error codes. Deletion
// confirmation must only treat these (plus "not found" messages) as gone:
// auth, throttling, validation, timeout, connection, and server errors mean
// "unknown", never "deleted".
var notFoundCodes = []string{
	"NotFound",
	"NotFoundException",
	"ResourceNotFoundException",
	"DBInstanceNotFound",
	"DBSnapshotNotFound",
}

// isNotFoundError reports whether err positively identifies a missing
// resource. Anything else — including transport errors — returns false so
// callers keep polling instead of declaring success.
func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, code := range notFoundCodes {
		if strings.Contains(msg, code) {
			return true
		}
	}
	lowered := strings.ToLower(msg)
	return strings.Contains(lowered, "not found") || strings.Contains(lowered, "does not exist")
}

// uniqueName builds a resource name that preserves the full UnixNano stamp
// (the uniqueness source) and trims the human prefix instead of amputating
// the stamp. Result is lowercase and capped at 63 characters.
func uniqueName(prefix string, stamp int64) string {
	stampStr := fmt.Sprintf("%d", stamp)
	prefix = strings.ToLower(prefix)
	maxPrefix := 63 - len(stampStr) - 1
	if maxPrefix < 0 {
		maxPrefix = 0
	}
	if len(prefix) > maxPrefix {
		prefix = prefix[:maxPrefix]
	}
	return prefix + "-" + stampStr
}

// AWSConnectorConfig builds the executor connector config for the test backend:
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
		t.Fatal("seed EC2 instances: backend returned zero instances")
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
