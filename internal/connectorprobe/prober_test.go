package connectorprobe

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/awsutil"
	"github.com/ardikabs/hibernator/pkg/k8sutil"
)

type stubAWS struct {
	authErr      *AuthError
	onAuth       func()
	id           *AWSIdentity
	accessErr    error
	entries      []hibernatorv1alpha1.ExecutorAccess
	authCalls    int
	accessCalls  int
	gotPrincipal string
	gotExecs     map[string]executor.AccessRequirement
}

func (s *stubAWS) CheckAuth(context.Context, *awsutil.AWSConnectorConfig) (*AWSIdentity, *AuthError) {
	s.authCalls++
	if s.onAuth != nil {
		s.onAuth()
	}
	if s.authErr != nil {
		return nil, s.authErr
	}
	return s.id, nil
}

func (s *stubAWS) CheckAccess(_ context.Context, _ *AWSIdentity, principal string, execs map[string]executor.AccessRequirement) ([]hibernatorv1alpha1.ExecutorAccess, error) {
	s.accessCalls++
	s.gotPrincipal = principal
	s.gotExecs = execs
	return s.entries, s.accessErr
}

type stubK8S struct {
	authErr     *AuthError
	entries     []hibernatorv1alpha1.ExecutorAccess
	authCalls   int
	accessCalls int
	gotExecs    map[string]executor.AccessRequirement
}

func (s *stubK8S) CheckAuth(context.Context, *k8sutil.K8SConnectorConfig) (kubernetes.Interface, *AuthError) {
	s.authCalls++
	if s.authErr != nil {
		return nil, s.authErr
	}
	return fake.NewSimpleClientset(), nil
}

func (s *stubK8S) CheckAccess(_ context.Context, _ kubernetes.Interface, execs map[string]executor.AccessRequirement) []hibernatorv1alpha1.ExecutorAccess {
	s.accessCalls++
	s.gotExecs = execs
	return s.entries
}

type fakeExec struct {
	executor.Executor
	typ string
}

func (f fakeExec) Type() string { return f.typ }

type reqExec struct {
	fakeExec
	req executor.AccessRequirement
}

func (r reqExec) Requires() executor.AccessRequirement { return r.req }

func cpReq() executor.AccessRequirement {
	return executor.AccessRequirement{
		ConnectorKind: executor.ConnectorKindCloudProvider,
		Shutdown:      executor.OperationAccess{AWSActions: []string{"ec2:StopInstances"}},
	}
}

func k8sExecReq() executor.AccessRequirement {
	return executor.AccessRequirement{
		ConnectorKind: executor.ConnectorKindK8SCluster,
		Shutdown:      executor.OperationAccess{K8SRules: []executor.K8SRule{{Resource: "pods", Verbs: []string{"list"}}}},
	}
}

func mkExec(typ string, req executor.AccessRequirement) executor.Executor {
	return reqExec{fakeExec{typ: typ}, req}
}

const roleARN = "arn:aws:iam::123456789012:role/r"

func awsCfg() executor.ConnectorConfig {
	return executor.ConnectorConfig{AWS: &awsutil.AWSConnectorConfig{AssumeRoleArn: roleARN}}
}

func newProber(a *stubAWS, k *stubK8S) *Prober {
	return &Prober{AWS: a, K8S: k, Getenv: func(string) string { return "" }}
}

func TestProbe_AWSAuthFailure(t *testing.T) {
	a := &stubAWS{authErr: &AuthError{Reason: hibernatorv1alpha1.ReasonInvalidCredentials, Message: "bad"}}
	res, err := newProber(a, nil).Probe(context.Background(), executor.ConnectorKindCloudProvider, awsCfg(),
		[]executor.Executor{mkExec("ec2", cpReq())})
	require.NoError(t, err)
	assert.Equal(t, Result{Ready: false, Reason: hibernatorv1alpha1.ReasonInvalidCredentials, Message: "bad"}, res)
	assert.Zero(t, a.accessCalls)
}

func TestProbe_K8SAuthFailure(t *testing.T) {
	k := &stubK8S{authErr: &AuthError{Reason: hibernatorv1alpha1.ReasonClusterUnreachable, Message: "down"}}
	res, err := newProber(nil, k).Probe(context.Background(), executor.ConnectorKindK8SCluster,
		executor.ConnectorConfig{K8S: &k8sutil.K8SConnectorConfig{}}, []executor.Executor{mkExec("workloadscaler", k8sExecReq())})
	require.NoError(t, err)
	assert.False(t, res.Ready)
	assert.Equal(t, hibernatorv1alpha1.ReasonClusterUnreachable, res.Reason)
	assert.Zero(t, k.accessCalls)
}

func TestProbe_CancelledContext(t *testing.T) {
	tests := []struct {
		name string
		a    *stubAWS
	}{
		{"auth failure", &stubAWS{authErr: &AuthError{Reason: hibernatorv1alpha1.ReasonInvalidCredentials, Message: "ctx"}}},
		{"access error", &stubAWS{id: &AWSIdentity{ARN: roleARN}, accessErr: errors.New("boom")}},
		{"simulation unavailable", &stubAWS{id: &AWSIdentity{ARN: roleARN}, accessErr: fmt.Errorf("%w: x", ErrSimulationUnavailable)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			tt.a.onAuth = cancel
			res, err := newProber(tt.a, nil).Probe(ctx, executor.ConnectorKindCloudProvider, awsCfg(),
				[]executor.Executor{mkExec("ec2", cpReq())})
			require.Error(t, err)
			assert.ErrorIs(t, err, context.Canceled)
			assert.Equal(t, Result{}, res)
		})
	}
}

func TestProbe_AWSSimulationUnavailable(t *testing.T) {
	execs := []executor.Executor{mkExec("ec2", cpReq())}
	tests := []struct {
		name string
		cfg  executor.ConnectorConfig
		a    *stubAWS
		want int // CheckAccess calls
	}{
		{"unresolved principal", executor.ConnectorConfig{AWS: &awsutil.AWSConnectorConfig{}}, &stubAWS{id: &AWSIdentity{}}, 0},
		{"checker reports unavailable", awsCfg(), &stubAWS{id: &AWSIdentity{}, accessErr: fmt.Errorf("%w: denied", ErrSimulationUnavailable)}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := newProber(tt.a, nil).Probe(context.Background(), executor.ConnectorKindCloudProvider, tt.cfg, execs)
			require.NoError(t, err)
			assert.True(t, res.Ready)
			assert.Empty(t, res.Reason)
			assert.False(t, res.AccessVerified)
			assert.Equal(t, hibernatorv1alpha1.ReasonSimulationUnavailable, res.AccessReason)
			assert.Contains(t, res.AccessMessage, "iam:SimulatePrincipalPolicy")
			assert.Contains(t, res.AccessMessage, "iam:GetContextKeysForPrincipalPolicy")
			assert.Empty(t, res.ExecutorAccess)
			assert.Equal(t, tt.want, tt.a.accessCalls)
		})
	}
}

func TestProbe_AWSOtherAccessError(t *testing.T) {
	a := &stubAWS{id: &AWSIdentity{}, accessErr: errors.New("throttled")}
	res, err := newProber(a, nil).Probe(context.Background(), executor.ConnectorKindCloudProvider, awsCfg(),
		[]executor.Executor{mkExec("ec2", cpReq())})
	require.NoError(t, err)
	assert.True(t, res.Ready)
	assert.False(t, res.AccessVerified)
	assert.Equal(t, hibernatorv1alpha1.ReasonProbeError, res.AccessReason)
	assert.Contains(t, res.AccessMessage, "throttled")
	assert.Empty(t, res.ExecutorAccess)
}

func TestProbe_AWSSuccess(t *testing.T) {
	entries := []hibernatorv1alpha1.ExecutorAccess{{Type: "ec2", Allowed: true}}
	a := &stubAWS{id: &AWSIdentity{}, entries: entries}
	execs := []executor.Executor{
		mkExec("ec2", cpReq()),
		fakeExec{typ: "plain"},
		mkExec("workloadscaler", k8sExecReq()),
		mkExec("bad", executor.AccessRequirement{ConnectorKind: executor.ConnectorKindCloudProvider,
			Shutdown: executor.OperationAccess{K8SRules: []executor.K8SRule{{Resource: "pods"}}}}),
	}
	res, err := newProber(a, nil).Probe(context.Background(), executor.ConnectorKindCloudProvider, awsCfg(), execs)
	require.NoError(t, err)
	assert.True(t, res.Ready)
	assert.Empty(t, res.Reason)
	assert.True(t, res.AccessVerified)
	assert.Equal(t, entries, res.ExecutorAccess)
	assert.Equal(t, 1, res.EligibleExecutors)
	assert.Equal(t, 1, res.CheckedExecutors)
	assert.Equal(t, roleARN, a.gotPrincipal)
	assert.Equal(t, map[string]executor.AccessRequirement{"ec2": cpReq()}, a.gotExecs)
}

func TestProbe_NoEligibleExecutorsSkipsIAM(t *testing.T) {
	a := &stubAWS{id: &AWSIdentity{}}
	res, err := newProber(a, nil).Probe(context.Background(), executor.ConnectorKindCloudProvider, awsCfg(),
		[]executor.Executor{fakeExec{typ: "plain"}, mkExec("workloadscaler", k8sExecReq())})
	require.NoError(t, err)
	assert.True(t, res.Ready)
	assert.True(t, res.AccessVerified)
	assert.Empty(t, res.ExecutorAccess)
	assert.Zero(t, res.EligibleExecutors)
	assert.Zero(t, res.CheckedExecutors)
	assert.Zero(t, a.accessCalls)
}

func TestProbe_ExecutorCounts(t *testing.T) {
	two := []hibernatorv1alpha1.ExecutorAccess{{Type: "a", Allowed: true}, {Type: "b"}}
	tests := []struct {
		name        string
		kind        string
		entries     []hibernatorv1alpha1.ExecutorAccess
		execs       []executor.Executor
		wantElig    int
		wantChecked int
	}{
		{"aws all unverified", executor.ConnectorKindCloudProvider, nil,
			[]executor.Executor{mkExec("ec2", cpReq()), mkExec("rds", cpReq())}, 2, 0},
		{"aws partial", executor.ConnectorKindCloudProvider, two[:1],
			[]executor.Executor{mkExec("ec2", cpReq()), mkExec("rds", cpReq())}, 2, 1},
		{"k8s all unverified", executor.ConnectorKindK8SCluster, nil,
			[]executor.Executor{mkExec("workloadscaler", k8sExecReq())}, 1, 0},
		{"k8s all checked", executor.ConnectorKindK8SCluster, two,
			[]executor.Executor{mkExec("a", k8sExecReq()), mkExec("b", k8sExecReq())}, 2, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &stubAWS{id: &AWSIdentity{}, entries: tt.entries}
			k := &stubK8S{entries: tt.entries}
			cfg := awsCfg()
			cfg.K8S = &k8sutil.K8SConnectorConfig{}
			res, err := newProber(a, k).Probe(context.Background(), tt.kind, cfg, tt.execs)
			require.NoError(t, err)
			assert.True(t, res.AccessVerified)
			assert.Equal(t, tt.wantElig, res.EligibleExecutors)
			assert.Equal(t, tt.wantChecked, res.CheckedExecutors)
		})
	}
}

func TestProbe_K8SSuccess(t *testing.T) {
	entries := []hibernatorv1alpha1.ExecutorAccess{{Type: "workloadscaler", Allowed: true}}
	k := &stubK8S{entries: entries}
	res, err := newProber(nil, k).Probe(context.Background(), executor.ConnectorKindK8SCluster,
		executor.ConnectorConfig{K8S: &k8sutil.K8SConnectorConfig{}},
		[]executor.Executor{mkExec("workloadscaler", k8sExecReq()), mkExec("ec2", cpReq()), fakeExec{typ: "plain"}})
	require.NoError(t, err)
	assert.True(t, res.Ready)
	assert.True(t, res.AccessVerified)
	assert.Equal(t, entries, res.ExecutorAccess)
	assert.Equal(t, 1, res.EligibleExecutors)
	assert.Equal(t, 1, res.CheckedExecutors)
	assert.Equal(t, map[string]executor.AccessRequirement{"workloadscaler": k8sExecReq()}, k.gotExecs)
}

func TestProbe_MissingConfig(t *testing.T) {
	for _, kind := range []string{executor.ConnectorKindCloudProvider, executor.ConnectorKindK8SCluster} {
		t.Run(kind, func(t *testing.T) {
			a, k := &stubAWS{}, &stubK8S{}
			res, err := newProber(a, k).Probe(context.Background(), kind, executor.ConnectorConfig{}, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "missing "+kind+" connector configuration")
			assert.Equal(t, Result{}, res)
			assert.Zero(t, a.authCalls+k.authCalls)
		})
	}
}

func TestProbe_UnknownKind(t *testing.T) {
	_, err := newProber(&stubAWS{}, &stubK8S{}).Probe(context.Background(), "Nope", executor.ConnectorConfig{}, nil)
	require.Error(t, err)
}

func TestNewProber_Wired(t *testing.T) {
	p := NewProber()
	assert.NotNil(t, p.AWS)
	assert.NotNil(t, p.K8S)
	assert.NotNil(t, p.Getenv)
}
