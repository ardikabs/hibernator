//go:build e2e || awsenv || kind

package harness

import (
	"context"
	"testing"
)

// Environment is the provider-neutral test contract: SDK clients plus the
// provider identity. Executor tests must not branch on Provider; capability
// skips carry the provider evidence instead.
type Environment struct {
	Provider    string
	Endpoint    string
	Region      string
	Clients     *Clients
	Provisioner Provisioner
}

// EC2FixtureSpec describes instances to provision.
type EC2FixtureSpec struct {
	Name  string
	Tags  map[string]string
	Count int32
}

// EC2Fixture is a set of provisioned instances owned by one test.
type EC2Fixture struct {
	InstanceIDs []string
}

// RDSFixtureSpec describes a DB instance to provision.
type RDSFixtureSpec struct {
	Prefix string
}

// RDSFixture is a provisioned DB instance owned by one test.
type RDSFixture struct {
	InstanceID string
}

// EKSFixtureSpec describes a cluster plus node group to provision.
type EKSFixtureSpec struct {
	Prefix string
}

// EKSFixture is a provisioned cluster plus node group owned by one test.
type EKSFixture struct {
	ClusterName   string
	NodegroupName string
}

// Provisioner provisions AWS-shaped fixtures for executor tests.
// FlociProvisioner is the only implementation; a future real-AWS backend
// implements this same interface without touching executor tests.
type Provisioner interface {
	ProvisionEC2(t *testing.T, ctx context.Context, spec EC2FixtureSpec) EC2Fixture
	ProvisionRDS(t *testing.T, ctx context.Context, spec RDSFixtureSpec) RDSFixture
	ProvisionEKS(t *testing.T, ctx context.Context, spec EKSFixtureSpec) EKSFixture
}

// FlociProvisioner provisions fixtures through standard AWS SDK calls.
type FlociProvisioner struct {
	Clients *Clients
}

// ProvisionEC2 delegates to the proven SeedEC2 helper.
func (p *FlociProvisioner) ProvisionEC2(t *testing.T, ctx context.Context, spec EC2FixtureSpec) EC2Fixture {
	t.Helper()
	return EC2Fixture{InstanceIDs: SeedEC2(t, ctx, p.Clients, spec.Name, spec.Tags, spec.Count)}
}
