//go:build e2e || awsenv || kind

package harness

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/stretchr/testify/require"
)

// ProvisionEKS creates a cluster and one node group ({min:1, desired:1, max:2}),
// waits until both are active, and registers reverse-order deletion cleanup.
//
// Final CreateCluster shape (adaptation (a) from the task brief): the minimal
// {Name, RoleArn} shape is rejected by the backend with
// "missing required field, CreateClusterInput.ResourcesVpcConfig", so the
// fixture discovers subnet IDs via EC2.DescribeSubnets (default VPC) and
// passes them as ResourcesVpcConfig.SubnetIds plus CreateNodegroup.Subnets.
// Both fields are also required by real AWS, so this is not a Floci-only
// encoding. No Tags adaptation was needed.
func (p *FlociProvisioner) ProvisionEKS(t *testing.T, ctx context.Context, spec EKSFixtureSpec) EKSFixture {
	t.Helper()
	stamp := time.Now().UnixNano()
	cluster := uniqueName(spec.Prefix, stamp)
	ng := uniqueName("ng", stamp)

	subnetCtx, subnetCancel := context.WithTimeout(ctx, 30*time.Second)
	defer subnetCancel()
	subnetsOut, err := p.Clients.EC2.DescribeSubnets(subnetCtx, &ec2.DescribeSubnetsInput{})
	if err != nil {
		t.Fatalf("discover subnets for EKS fixture: %v", err)
	}
	var subnetIDs []string
	for _, sn := range subnetsOut.Subnets {
		if id := aws.ToString(sn.SubnetId); id != "" {
			subnetIDs = append(subnetIDs, id)
		}
	}
	if len(subnetIDs) < 2 {
		t.Fatalf("EKS fixture needs >=2 subnets, found %d", len(subnetIDs))
	}

	requestCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	_, err = p.Clients.EKS.CreateCluster(requestCtx, &eks.CreateClusterInput{
		Name:    aws.String(cluster),
		RoleArn: aws.String("arn:aws:iam::000000000000:role/awsenv-test"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{
			SubnetIds: subnetIDs,
		},
	})
	// Cleanup is registered immediately after a successful create, before any
	// wait or probe, so skips and failures still clean up. Deletion always
	// proceeds in reverse order (node group, then cluster) and always waits,
	// even when a delete call itself errors.
	nodegroupCreated := false
	if err == nil {
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cleanupCancel()
			if nodegroupCreated {
				if _, err := p.Clients.EKS.DeleteNodegroup(cleanupCtx, &eks.DeleteNodegroupInput{
					ClusterName:   aws.String(cluster),
					NodegroupName: aws.String(ng),
				}); err != nil {
					t.Errorf("cleanup delete node group %s/%s: %v", cluster, ng, err)
				}
				WaitForNodegroupGone(t, cleanupCtx, p.Clients.EKS, cluster, ng, 3*time.Minute)
			}
			if _, err := p.Clients.EKS.DeleteCluster(cleanupCtx, &eks.DeleteClusterInput{
				Name: aws.String(cluster),
			}); err != nil {
				t.Errorf("cleanup delete cluster %s: %v", cluster, err)
			}
			WaitForClusterGone(t, cleanupCtx, p.Clients.EKS, cluster, 3*time.Minute)
		})
	}
	RequireNoErrorOrSkip(t, CapabilityEKSNodeGroupUpdate, "CreateCluster", err)

	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer waitCancel()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
waitCluster:
	for {
		out, err := p.Clients.EKS.DescribeCluster(waitCtx, &eks.DescribeClusterInput{
			Name: aws.String(cluster),
		})
		if err == nil && out.Cluster != nil && out.Cluster.Status == ekstypes.ClusterStatusActive {
			break waitCluster
		}
		select {
		case <-waitCtx.Done():
			t.Fatalf("EKS cluster %s not active: %v", cluster, err)
		case <-ticker.C:
		}
	}

	// Capability probe that doubles as endpoint validation: the Hibernator EKS
	// executor always builds a Kubernetes client from DescribeCluster, so a
	// fixture without endpoint+CA cannot run the lifecycle.
	clusterOut, err := p.Clients.EKS.DescribeCluster(ctx, &eks.DescribeClusterInput{
		Name: aws.String(cluster),
	})
	require.NoError(t, err)
	if aws.ToString(clusterOut.Cluster.Endpoint) == "" || aws.ToString(clusterOut.Cluster.CertificateAuthority.Data) == "" {
		provider, _ := resolveProvider()
		t.Skipf("provider %q lacks %s (DescribeCluster returned no usable endpoint/CA)", provider, CapabilityEKSNodeGroupUpdate)
	}

	nodeCtx, nodeCancel := context.WithTimeout(ctx, 60*time.Second)
	defer nodeCancel()
	_, err = p.Clients.EKS.CreateNodegroup(nodeCtx, &eks.CreateNodegroupInput{
		ClusterName:   aws.String(cluster),
		NodegroupName: aws.String(ng),
		NodeRole:      aws.String("arn:aws:iam::000000000000:role/awsenv-node"),
		Subnets:       subnetIDs,
		ScalingConfig: &ekstypes.NodegroupScalingConfig{
			MinSize:     aws.Int32(1),
			MaxSize:     aws.Int32(2),
			DesiredSize: aws.Int32(1),
		},
	})
	if err == nil {
		nodegroupCreated = true
	}
	RequireNoErrorOrSkip(t, CapabilityEKSNodeGroupUpdate, "CreateNodegroup", err)

	WaitForNodegroupState(t, ctx, p.Clients.EKS, cluster, ng, 1, 1, 2, 5*time.Minute)

	return EKSFixture{ClusterName: cluster, NodegroupName: ng}
}

// WaitForNodegroupState polls DescribeNodegroup until scaling matches.
func WaitForNodegroupState(t *testing.T, ctx context.Context, client *eks.Client, cluster, ng string, wantMin, wantDesired, wantMax int32, timeout time.Duration) {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var last string
	var lastErr error
	for {
		requestCtx, requestCancel := context.WithTimeout(waitCtx, 20*time.Second)
		out, err := client.DescribeNodegroup(requestCtx, &eks.DescribeNodegroupInput{
			ClusterName:   aws.String(cluster),
			NodegroupName: aws.String(ng),
		})
		requestCancel()
		if err == nil && out.Nodegroup != nil {
			if out.Nodegroup.ScalingConfig == nil {
				last = fmt.Sprintf("nil scalingConfig,status=%s", out.Nodegroup.Status)
			} else {
				got := out.Nodegroup.ScalingConfig
				last = fmt.Sprintf("min=%d,desired=%d,max=%d,status=%s", aws.ToInt32(got.MinSize), aws.ToInt32(got.DesiredSize), aws.ToInt32(got.MaxSize), out.Nodegroup.Status)
				if aws.ToInt32(got.MinSize) == wantMin && aws.ToInt32(got.DesiredSize) == wantDesired && aws.ToInt32(got.MaxSize) == wantMax {
					return
				}
			}
		} else if err != nil {
			lastErr = err
		}
		select {
		case <-waitCtx.Done():
			t.Fatalf("node group %s/%s did not reach min=%d,desired=%d,max=%d within %s (last: %s, last error: %v)", cluster, ng, wantMin, wantDesired, wantMax, timeout, last, lastErr)
		case <-ticker.C:
		}
	}
}

// FlociProvisioner satisfies Provisioner once all three methods exist.
var _ Provisioner = (*FlociProvisioner)(nil)

// WaitForNodegroupGone polls until DescribeNodegroup positively reports the
// node group missing. Only not-found errors count as gone; anything else
// keeps polling so auth, throttling, or transport failures surface on timeout
// instead of masquerading as clean deletion.
func WaitForNodegroupGone(t *testing.T, ctx context.Context, client *eks.Client, cluster, ng string, timeout time.Duration) {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var lastErr error
	for {
		requestCtx, requestCancel := context.WithTimeout(waitCtx, 20*time.Second)
		_, err := client.DescribeNodegroup(requestCtx, &eks.DescribeNodegroupInput{
			ClusterName:   aws.String(cluster),
			NodegroupName: aws.String(ng),
		})
		requestCancel()
		if isNotFoundError(err) {
			return
		}
		if err != nil {
			lastErr = err
		}
		select {
		case <-waitCtx.Done():
			t.Errorf("node group %s/%s still present after %s (last error: %v)", cluster, ng, timeout, lastErr)
			return
		case <-ticker.C:
		}
	}
}

// WaitForClusterGone polls DescribeCluster until not-found/terminal, with the
// same not-found-only semantics as WaitForNodegroupGone.
func WaitForClusterGone(t *testing.T, ctx context.Context, client *eks.Client, cluster string, timeout time.Duration) {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var lastErr error
	for {
		requestCtx, requestCancel := context.WithTimeout(waitCtx, 20*time.Second)
		_, err := client.DescribeCluster(requestCtx, &eks.DescribeClusterInput{
			Name: aws.String(cluster),
		})
		requestCancel()
		if isNotFoundError(err) {
			return
		}
		if err != nil {
			lastErr = err
		}
		select {
		case <-waitCtx.Done():
			t.Errorf("cluster %s still present after %s (last error: %v)", cluster, timeout, lastErr)
			return
		case <-ticker.C:
		}
	}
}
