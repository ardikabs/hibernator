package connectorprobe

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/awsutil"
)

const testPrincipal = "arn:aws:iam::111111111111:role/probe"

// decision is a simulated evaluation for one action.
type decision struct {
	eval    iamtypes.PolicyEvaluationDecisionType
	missing []string
}

type fakeIAM struct {
	decisions map[string]decision
	simErrs   []error
	pageSize  int

	simCalls []*iam.SimulatePrincipalPolicyInput
}

func (f *fakeIAM) SimulatePrincipalPolicy(_ context.Context, in *iam.SimulatePrincipalPolicyInput, _ ...func(*iam.Options)) (*iam.SimulatePrincipalPolicyOutput, error) {
	f.simCalls = append(f.simCalls, in)
	if n := len(f.simCalls); n <= len(f.simErrs) && f.simErrs[n-1] != nil {
		return nil, f.simErrs[n-1]
	}

	var results []iamtypes.EvaluationResult
	for _, a := range in.ActionNames {
		d, ok := f.decisions[a]
		if !ok {
			d = decision{eval: iamtypes.PolicyEvaluationDecisionTypeImplicitDeny}
		}
		results = append(results, iamtypes.EvaluationResult{
			EvalActionName:       aws.String(a),
			EvalResourceName:     aws.String("*"),
			EvalDecision:         d.eval,
			MissingContextValues: d.missing,
		})
	}
	if f.pageSize == 0 {
		return &iam.SimulatePrincipalPolicyOutput{EvaluationResults: results}, nil
	}

	start := 0
	if in.Marker != nil {
		for i, a := range in.ActionNames {
			if a == *in.Marker {
				start = i
			}
		}
	}
	end := min(start+f.pageSize, len(results))
	out := &iam.SimulatePrincipalPolicyOutput{EvaluationResults: results[start:end]}
	if end < len(results) {
		out.IsTruncated = true
		out.Marker = aws.String(in.ActionNames[end])
	}
	return out, nil
}

func newTestChecker(f *fakeIAM) *AWSChecker {
	return &AWSChecker{NewIAM: func(aws.Config) IAMAPI { return f }}
}

func testIdentity() *AWSIdentity {
	return &AWSIdentity{AccountID: "111111111111", ARN: testPrincipal, Config: aws.Config{Region: "ap-southeast-1"}}
}

func ec2Requirement() executor.AccessRequirement {
	return executor.AccessRequirement{
		ConnectorKind: executor.ConnectorKindCloudProvider,
		Shutdown:      executor.OperationAccess{AWSActions: []string{"ec2:DescribeInstances", "ec2:StopInstances"}},
		WakeUp:        executor.OperationAccess{AWSActions: []string{"ec2:DescribeInstances", "ec2:StartInstances"}},
	}
}

func allow(actions ...string) map[string]decision {
	m := map[string]decision{}
	for _, a := range actions {
		m[a] = decision{eval: iamtypes.PolicyEvaluationDecisionTypeAllowed}
	}
	return m
}

func TestResolvePrincipalARN(t *testing.T) {
	const roleEnv = "arn:aws:iam::111111111111:role/irsa"
	tests := []struct {
		name    string
		cfg     *awsutil.AWSConnectorConfig
		env     string
		want    string
		wantErr bool
		noEnv   bool

		identity string
	}{
		{
			name: "assume role wins",
			cfg:  &awsutil.AWSConnectorConfig{AssumeRoleArn: "arn:aws:iam::222222222222:role/target", AccessKeyID: "AKIA"},
			env:  roleEnv,
			want: "arn:aws:iam::222222222222:role/target",
		},
		{
			name:  "static credentials use identity and ignore env",
			cfg:   &awsutil.AWSConnectorConfig{AccessKeyID: "AKIA", SecretAccessKey: "s"},
			env:   roleEnv,
			want:  "arn:aws:iam::111111111111:user/static",
			noEnv: true,
		},
		{
			name: "irsa reads AWS_ROLE_ARN",
			cfg:  &awsutil.AWSConnectorConfig{},
			env:  roleEnv,
			want: roleEnv,
		},
		{
			name:     "static session credentials with assumed-role identity are unresolvable",
			cfg:      &awsutil.AWSConnectorConfig{AccessKeyID: "ASIA", SecretAccessKey: "s", SessionToken: "t"},
			env:      roleEnv,
			identity: "arn:aws:sts::111111111111:assumed-role/r/session",
			wantErr:  true,
			noEnv:    true,
		},
		{
			name:    "malformed AWS_ROLE_ARN is unresolvable",
			cfg:     &awsutil.AWSConnectorConfig{},
			env:     "not-an-arn",
			wantErr: true,
		},
		{
			name:    "empty env is unresolvable",
			cfg:     &awsutil.AWSConnectorConfig{},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			read := false
			getenv := func(k string) string {
				read = true
				if k == "AWS_ROLE_ARN" {
					return tt.env
				}
				return ""
			}
			identity := tt.identity
			if identity == "" {
				identity = "arn:aws:iam::111111111111:user/static"
			}
			got, err := ResolvePrincipalARN(tt.cfg, identity, getenv)
			if tt.noEnv {
				assert.False(t, read, "env must not be read with static credentials")
			}
			if tt.wantErr {
				require.Error(t, err)
				assert.True(t, errors.Is(err, ErrSimulationUnavailable))
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCheckAccess_AllAllowed(t *testing.T) {
	f := &fakeIAM{decisions: allow("ec2:DescribeInstances", "ec2:StopInstances", "ec2:StartInstances")}
	got, err := newTestChecker(f).CheckAccess(context.Background(), testIdentity(), testPrincipal,
		map[string]executor.AccessRequirement{"ec2": ec2Requirement()})
	require.NoError(t, err)
	assert.Equal(t, []hibernatorv1alpha1.ExecutorAccess{
		{Type: "ec2", Allowed: true, Verification: hibernatorv1alpha1.VerificationPolicySimulation},
	}, got)

	require.Len(t, f.simCalls, 2)
	for _, c := range f.simCalls {
		assert.Equal(t, testPrincipal, aws.ToString(c.PolicySourceArn))
		assert.Equal(t, []string{"*"}, c.ResourceArns)
	}
	assert.Equal(t, []string{"ec2:DescribeInstances", "ec2:StopInstances"}, f.simCalls[0].ActionNames)
	assert.Equal(t, []string{"ec2:DescribeInstances", "ec2:StartInstances"}, f.simCalls[1].ActionNames)
}

func TestCheckAccess_ShutdownOnlyDenied(t *testing.T) {
	f := &fakeIAM{decisions: allow("ec2:DescribeInstances", "ec2:StartInstances")}
	got, err := newTestChecker(f).CheckAccess(context.Background(), testIdentity(), testPrincipal,
		map[string]executor.AccessRequirement{"ec2": ec2Requirement()})
	require.NoError(t, err)
	require.Len(t, got, 1)
	e := got[0]
	assert.Equal(t, "ec2", e.Type)
	assert.False(t, e.Allowed)
	assert.Equal(t, hibernatorv1alpha1.ReasonAccessDenied, e.Reason)
	assert.Equal(t, hibernatorv1alpha1.VerificationPolicySimulation, e.Verification)
	assert.Contains(t, e.Message, "ec2:StopInstances")
	assert.Contains(t, e.Message, "shutdown")
	assert.NotContains(t, e.Message, "wakeup")
}

func TestCheckAccess_Decisions(t *testing.T) {
	tests := []struct {
		name        string
		decisions   map[string]decision
		wantEntry   bool
		wantAllowed bool
		wantInMsg   []string
		notInMsg    []string
	}{
		{
			name:      "wakeup explicit deny",
			decisions: mergeDecisions(allow("ec2:DescribeInstances", "ec2:StopInstances"), "ec2:StartInstances", decision{eval: iamtypes.PolicyEvaluationDecisionTypeExplicitDeny}),
			wantEntry: true,
			wantInMsg: []string{"ec2:StartInstances [explicitDeny]", "wakeup"},
		},
		{
			name:      "both operations denied",
			decisions: allow("ec2:DescribeInstances"),
			wantEntry: true,
			wantInMsg: []string{"ec2:StopInstances", "shutdown", "ec2:StartInstances", "wakeup"},
		},
		{
			name: "implicit deny with missing context is unverified",
			decisions: mergeDecisions(allow("ec2:DescribeInstances", "ec2:StartInstances"), "ec2:StopInstances",
				decision{eval: iamtypes.PolicyEvaluationDecisionTypeImplicitDeny, missing: []string{"aws:SourceVpc"}}),
		},
		{
			name: "allowed with missing context is unverified",
			decisions: mergeDecisions(allow("ec2:DescribeInstances", "ec2:StopInstances"), "ec2:StartInstances",
				decision{eval: iamtypes.PolicyEvaluationDecisionTypeAllowed, missing: []string{"aws:SourceVpc"}}),
		},
		{
			name: "explicit deny with missing context is unverified",
			decisions: mergeDecisions(allow("ec2:DescribeInstances", "ec2:StartInstances"), "ec2:StopInstances",
				decision{eval: iamtypes.PolicyEvaluationDecisionTypeExplicitDeny, missing: []string{"aws:SourceVpc"}}),
		},
		{
			name: "definite denial outweighs explicit deny with missing context",
			decisions: mergeDecisions(allow("ec2:DescribeInstances"), "ec2:StartInstances",
				decision{eval: iamtypes.PolicyEvaluationDecisionTypeExplicitDeny, missing: []string{"aws:SourceVpc"}}),
			wantEntry: true,
			wantInMsg: []string{"ec2:StopInstances", "shutdown"},
			notInMsg:  []string{"ec2:StartInstances", "wakeup"},
		},
		{
			name: "definite denial outweighs unverified operation",
			decisions: mergeDecisions(allow("ec2:DescribeInstances"), "ec2:StartInstances",
				decision{eval: iamtypes.PolicyEvaluationDecisionTypeImplicitDeny, missing: []string{"aws:SourceVpc"}}),
			wantEntry: true,
			wantInMsg: []string{"ec2:StopInstances", "shutdown"},
			notInMsg:  []string{"ec2:StartInstances", "wakeup"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeIAM{decisions: tt.decisions}
			got, err := newTestChecker(f).CheckAccess(context.Background(), testIdentity(), testPrincipal,
				map[string]executor.AccessRequirement{"ec2": ec2Requirement()})
			require.NoError(t, err)
			if !tt.wantEntry {
				assert.Empty(t, got)
				return
			}
			require.Len(t, got, 1)
			assert.Equal(t, tt.wantAllowed, got[0].Allowed)
			assert.Equal(t, hibernatorv1alpha1.ReasonAccessDenied, got[0].Reason)
			for _, s := range tt.wantInMsg {
				assert.Contains(t, got[0].Message, s)
			}
			for _, s := range tt.notInMsg {
				assert.NotContains(t, got[0].Message, s)
			}
		})
	}
}

func mergeDecisions(m map[string]decision, action string, d decision) map[string]decision {
	m[action] = d
	return m
}

func TestCheckAccess_SimulateAccessDenied(t *testing.T) {
	for _, code := range []string{"AccessDenied", "AccessDeniedException", "UnauthorizedOperation"} {
		t.Run(code, func(t *testing.T) {
			f := &fakeIAM{
				decisions: allow("ec2:DescribeInstances", "ec2:StopInstances", "ec2:StartInstances"),
				simErrs:   []error{&smithy.GenericAPIError{Code: code, Message: "not authorized"}},
			}
			got, err := newTestChecker(f).CheckAccess(context.Background(), testIdentity(), testPrincipal,
				map[string]executor.AccessRequirement{"ec2": ec2Requirement()})
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrSimulationUnavailable))
			assert.Empty(t, got)
		})
	}
}

func TestCheckAccess_UnresolvedPrincipal(t *testing.T) {
	f := &fakeIAM{}
	got, err := newTestChecker(f).CheckAccess(context.Background(), testIdentity(), "",
		map[string]executor.AccessRequirement{"ec2": ec2Requirement()})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSimulationUnavailable))
	assert.Empty(t, got)
	assert.Empty(t, f.simCalls)
}

func TestCheckAccess_OtherErrors(t *testing.T) {
	throttle := &smithy.GenericAPIError{Code: "Throttling", Message: "rate exceeded"}
	tests := []struct {
		name    string
		simErrs []error
		ctxErr  error
	}{
		{name: "throttled first call", simErrs: []error{throttle}},
		{name: "network error", simErrs: []error{errors.New("dial tcp: i/o timeout")}},
		{name: "access denied after first call", simErrs: []error{nil, &smithy.GenericAPIError{Code: "AccessDenied"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeIAM{
				decisions: allow("ec2:DescribeInstances", "ec2:StopInstances", "ec2:StartInstances"),
				simErrs:   tt.simErrs,
			}
			got, err := newTestChecker(f).CheckAccess(context.Background(), testIdentity(), testPrincipal,
				map[string]executor.AccessRequirement{"ec2": ec2Requirement()})
			require.Error(t, err)
			assert.False(t, errors.Is(err, ErrSimulationUnavailable))
			assert.Empty(t, got)
		})
	}
}

func TestCheckAccess_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fakeIAM{decisions: allow("ec2:DescribeInstances", "ec2:StopInstances", "ec2:StartInstances")}
	got, err := newTestChecker(f).CheckAccess(ctx, testIdentity(), testPrincipal,
		map[string]executor.AccessRequirement{"ec2": ec2Requirement()})
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
	assert.False(t, errors.Is(err, ErrSimulationUnavailable))
	assert.Empty(t, got)
}

func TestCheckAccess_Pagination(t *testing.T) {
	req := executor.AccessRequirement{
		ConnectorKind: executor.ConnectorKindCloudProvider,
		Shutdown:      executor.OperationAccess{AWSActions: []string{"rds:DescribeDBInstances", "rds:StopDBInstance", "rds:StopDBCluster"}},
		WakeUp:        executor.OperationAccess{AWSActions: []string{"rds:DescribeDBInstances"}},
	}
	tests := []struct {
		name        string
		decisions   map[string]decision
		wantAllowed bool
	}{
		{name: "allowed across pages", decisions: allow("rds:DescribeDBInstances", "rds:StopDBInstance", "rds:StopDBCluster"), wantAllowed: true},
		{name: "denial on last page", decisions: allow("rds:DescribeDBInstances", "rds:StopDBInstance")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeIAM{decisions: tt.decisions, pageSize: 1}
			got, err := newTestChecker(f).CheckAccess(context.Background(), testIdentity(), testPrincipal,
				map[string]executor.AccessRequirement{"rds": req})
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, tt.wantAllowed, got[0].Allowed)
			if !tt.wantAllowed {
				assert.Contains(t, got[0].Message, "rds:StopDBCluster")
			}
			// 3 shutdown pages plus 1 wakeup page.
			assert.Len(t, f.simCalls, 4)
			assert.Equal(t, "rds:StopDBInstance", aws.ToString(f.simCalls[1].Marker))
		})
	}
}

func TestCheckAccess_SuppliesContextEntries(t *testing.T) {
	f := &fakeIAM{decisions: allow("ec2:DescribeInstances", "ec2:StopInstances", "ec2:StartInstances")}
	_, err := newTestChecker(f).CheckAccess(context.Background(), testIdentity(), testPrincipal,
		map[string]executor.AccessRequirement{"ec2": ec2Requirement()})
	require.NoError(t, err)

	require.NotEmpty(t, f.simCalls)
	entries := map[string][]string{}
	for _, e := range f.simCalls[0].ContextEntries {
		entries[aws.ToString(e.ContextKeyName)] = e.ContextKeyValues
	}
	assert.Equal(t, map[string][]string{
		"aws:RequestedRegion":  {"ap-southeast-1"},
		"aws:PrincipalAccount": {"111111111111"},
		"aws:PrincipalArn":     {testPrincipal},
		"aws:SecureTransport":  {"true"},
	}, entries)
}

func TestContextEntries_SkipsEmptyValues(t *testing.T) {
	var names []string
	for _, e := range contextEntries("", testIdentity(), testPrincipal) {
		names = append(names, aws.ToString(e.ContextKeyName))
	}
	assert.NotContains(t, names, "aws:RequestedRegion")
	assert.Contains(t, names, "aws:PrincipalAccount")
}

func TestAWSChecker_RegionFallback(t *testing.T) {
	env := func(k string) string {
		if k == "AWS_DEFAULT_REGION" {
			return "eu-west-1"
		}
		return ""
	}
	noRegion := &AWSIdentity{}
	withRegion := testIdentity()

	assert.Equal(t, "eu-west-1", (&AWSChecker{Getenv: env}).region(noRegion))
	assert.Equal(t, "ap-southeast-1", (&AWSChecker{Getenv: env}).region(withRegion))
	assert.Equal(t, "", (&AWSChecker{}).region(noRegion))
}

func TestCheckAccess_SkipsNonCloudProvider(t *testing.T) {
	f := &fakeIAM{decisions: allow("ec2:DescribeInstances", "ec2:StopInstances", "ec2:StartInstances")}
	got, err := newTestChecker(f).CheckAccess(context.Background(), testIdentity(), testPrincipal,
		map[string]executor.AccessRequirement{
			"ec2":            ec2Requirement(),
			"workloadscaler": {ConnectorKind: executor.ConnectorKindK8SCluster},
		})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "ec2", got[0].Type)
}

func TestDenialLabel(t *testing.T) {
	r := iamtypes.EvaluationResult{
		EvalDecision:                      iamtypes.PolicyEvaluationDecisionTypeImplicitDeny,
		PermissionsBoundaryDecisionDetail: &iamtypes.PermissionsBoundaryDecisionDetail{AllowedByPermissionsBoundary: false},
	}
	assert.Equal(t, "ec2:StopInstances [implicitDeny, permissions boundary]", denialLabel("ec2:StopInstances", r))

	r = iamtypes.EvaluationResult{EvalDecision: iamtypes.PolicyEvaluationDecisionTypeExplicitDeny}
	assert.Equal(t, "ec2:StopInstances [explicitDeny]", denialLabel("ec2:StopInstances", r))
}
