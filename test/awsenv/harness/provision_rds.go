//go:build e2e || awsenv || kind

package harness

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
)

// ProvisionRDS creates a uniquely named PostgreSQL instance, waits until it
// is available, and registers reverse-order deletion cleanup.
func (p *FlociProvisioner) ProvisionRDS(t *testing.T, ctx context.Context, spec RDSFixtureSpec) RDSFixture {
	t.Helper()
	id := uniqueName(spec.Prefix, time.Now().UnixNano())
	requestCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	_, err := p.Clients.RDS.CreateDBInstance(requestCtx, &rds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String(id),
		DBInstanceClass:      aws.String("db.t3.micro"),
		Engine:               aws.String("postgres"),
		MasterUsername:       aws.String("admin"),
		// Test-only credential for the ephemeral in-memory backend; never
		// used against real infrastructure.
		MasterUserPassword: aws.String("Secret123!"),
		AllocatedStorage:   aws.Int32(20),
	})
	if err == nil {
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cleanupCancel()
			// Spec: delete test-created snapshots before deleting the
			// instance. Best-effort: DeleteDBSnapshot failures are
			// reported but never block instance deletion. Describe
			// failures are ignored (no snapshots, or backend without
			// snapshot support such as current Floci where StopDBInstance
			// is unsupported so no snapshots will exist).
			if snapOut, snapErr := p.Clients.RDS.DescribeDBSnapshots(cleanupCtx, &rds.DescribeDBSnapshotsInput{
				DBInstanceIdentifier: aws.String(id),
			}); snapErr == nil {
				for _, snap := range snapOut.DBSnapshots {
					snapID := aws.ToString(snap.DBSnapshotIdentifier)
					if snapID == "" {
						continue
					}
					if _, err := p.Clients.RDS.DeleteDBSnapshot(cleanupCtx, &rds.DeleteDBSnapshotInput{
						DBSnapshotIdentifier: aws.String(snapID),
					}); err != nil {
						t.Errorf("cleanup delete DB snapshot %s: %v", snapID, err)
					}
				}
			}
			_, err := p.Clients.RDS.DeleteDBInstance(cleanupCtx, &rds.DeleteDBInstanceInput{
				DBInstanceIdentifier: aws.String(id),
				SkipFinalSnapshot:    aws.Bool(true),
			})
			if err != nil {
				t.Errorf("cleanup delete DB instance %s: %v", id, err)
				return
			}
			pollCtx, pollCancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer pollCancel()
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			var lastErr error
			for {
				_, err := p.Clients.RDS.DescribeDBInstances(pollCtx, &rds.DescribeDBInstancesInput{
					DBInstanceIdentifier: aws.String(id),
				})
				if isNotFoundError(err) {
					return
				}
				if err != nil {
					lastErr = err
				}
				select {
				case <-pollCtx.Done():
					t.Errorf("cleanup: DB instance %s still present (last error: %v)", id, lastErr)
					return
				case <-ticker.C:
				}
			}
		})
	}
	RequireNoErrorOrSkip(t, CapabilityRDSLifecycle, "CreateDBInstance", err)

	WaitForDBInstanceState(t, ctx, p.Clients.RDS, id, "available", 10*time.Minute)

	return RDSFixture{InstanceID: id}
}

// WaitForDBInstanceState polls DescribeDBInstances until the instance reports
// want, and fails with the last-seen status on timeout.
func WaitForDBInstanceState(t *testing.T, ctx context.Context, client *rds.Client, id, want string, timeout time.Duration) {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var last string
	var lastErr error
	for {
		requestCtx, requestCancel := context.WithTimeout(waitCtx, 20*time.Second)
		out, err := client.DescribeDBInstances(requestCtx, &rds.DescribeDBInstancesInput{
			DBInstanceIdentifier: aws.String(id),
		})
		requestCancel()
		if err == nil && len(out.DBInstances) > 0 {
			last = aws.ToString(out.DBInstances[0].DBInstanceStatus)
			if last == want {
				return
			}
		} else if err != nil {
			lastErr = err
		}
		select {
		case <-waitCtx.Done():
			t.Fatalf("DB instance %s did not reach %q within %s (last: %q, last error: %v)", id, want, timeout, last, lastErr)
		case <-ticker.C:
		}
	}
}
