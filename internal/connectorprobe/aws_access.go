package connectorprobe

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/smithy-go"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/awsutil"
)

// ErrSimulationUnavailable means the connector's identity cannot be simulated:
// the principal is unresolvable or it lacks the IAM simulation permissions.
var ErrSimulationUnavailable = errors.New("policy simulation unavailable")

// accessDeniedCodes are API error codes meaning the caller is not authorized.
var accessDeniedCodes = map[string]bool{
	"AccessDenied":          true,
	"AccessDeniedException": true,
	"UnauthorizedOperation": true,
	"UnauthorizedAccess":    true,
	"NotAuthorized":         true,
}

const (
	opShutdown = "shutdown"
	opWakeUp   = "wakeup"
)

// ResolvePrincipalARN returns the ARN of the identity the connector authenticates as.
// The ARN must be an IAM user or role, the only principals the simulator accepts.
func ResolvePrincipalARN(cfg *awsutil.AWSConnectorConfig, identityARN string, getenv func(string) string) (string, error) {
	var arn string
	switch {
	case cfg.AssumeRoleArn != "":
		arn = cfg.AssumeRoleArn
	// Mirrors awsutil.BuildAWSConfig: either field selects static credentials.
	case cfg.AccessKeyID != "" || cfg.SecretAccessKey != "":
		arn = identityARN
	default:
		arn = getenv("AWS_ROLE_ARN")
		if arn == "" {
			return "", fmt.Errorf("%w: AWS_ROLE_ARN is not set (EKS Pod Identity is not supported)", ErrSimulationUnavailable)
		}
	}
	if !simulatablePrincipal.MatchString(arn) {
		return "", fmt.Errorf("%w: %q is not an IAM user or role ARN", ErrSimulationUnavailable, arn)
	}
	return arn, nil
}

// simulatablePrincipal matches IAM user and role ARNs in any partition.
var simulatablePrincipal = regexp.MustCompile(`^arn:aws[a-z-]*:iam::\d{12}:(user|role)/.+`)

// opResult is the outcome of simulating one operation.
type opResult struct {
	denied     []string
	unverified bool
}

// CheckAccess simulates each CloudProvider executor's shutdown and wakeup actions
// against principalARN. Executors whose result depends on condition values that
// could not be supplied get no entry.
func (c *AWSChecker) CheckAccess(ctx context.Context, id *AWSIdentity, principalARN string, execs map[string]executor.AccessRequirement) ([]hibernatorv1alpha1.ExecutorAccess, error) {
	if principalARN == "" {
		return nil, fmt.Errorf("%w: principal ARN is unresolved", ErrSimulationUnavailable)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	client := c.NewIAM(id.Config)
	entries := contextEntries(c.region(id), id, principalARN)

	types := make([]string, 0, len(execs))
	for t, req := range execs {
		if req.ConnectorKind == executor.ConnectorKindCloudProvider {
			types = append(types, t)
		}
	}
	sort.Strings(types)

	s := &simulator{
		client:    client,
		principal: principalARN,
		entries:   entries,
	}
	var out []hibernatorv1alpha1.ExecutorAccess
	for _, t := range types {
		req := execs[t]
		shutdown, err := s.simulate(ctx, req.Shutdown.AWSActions)
		if err != nil {
			return nil, err
		}
		wakeup, err := s.simulate(ctx, req.WakeUp.AWSActions)
		if err != nil {
			return nil, err
		}

		var msgs []string
		if len(shutdown.denied) > 0 {
			msgs = append(msgs, denialMessage(opShutdown, shutdown.denied))
		}
		if len(wakeup.denied) > 0 {
			msgs = append(msgs, denialMessage(opWakeUp, wakeup.denied))
		}
		switch {
		case len(msgs) > 0:
			out = append(out, hibernatorv1alpha1.ExecutorAccess{
				Type:         t,
				Reason:       hibernatorv1alpha1.ReasonAccessDenied,
				Message:      strings.Join(msgs, "; "),
				Verification: hibernatorv1alpha1.VerificationPolicySimulation,
			})
		case shutdown.unverified || wakeup.unverified:
			// Unknown, never denied.
		default:
			out = append(out, hibernatorv1alpha1.ExecutorAccess{
				Type:         t,
				Allowed:      true,
				Verification: hibernatorv1alpha1.VerificationPolicySimulation,
			})
		}
	}
	return out, nil
}

type simulator struct {
	client    IAMAPI
	principal string
	entries   []iamtypes.ContextEntry
	calls     int
}

// simulate evaluates actions in one paginated SimulatePrincipalPolicy call.
func (s *simulator) simulate(ctx context.Context, actions []string) (opResult, error) {
	var res opResult
	if len(actions) == 0 {
		return res, nil
	}

	decisions := map[string]iamtypes.EvaluationResult{}
	var marker *string
	for {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		first := s.calls == 0
		s.calls++
		out, err := s.client.SimulatePrincipalPolicy(ctx, &iam.SimulatePrincipalPolicyInput{
			PolicySourceArn: aws.String(s.principal),
			ActionNames:     actions,
			ResourceArns:    []string{"*"},
			ContextEntries:  s.entries,
			Marker:          marker,
		})
		if err != nil {
			return res, classifyIAMError(ctx, "iam:SimulatePrincipalPolicy", err, first)
		}
		for _, r := range out.EvaluationResults {
			decisions[aws.ToString(r.EvalActionName)] = r
		}
		if !out.IsTruncated || out.Marker == nil {
			break
		}
		marker = out.Marker
	}

	for _, a := range actions {
		r, ok := decisions[a]
		switch {
		case !ok:
			res.unverified = true
		case len(r.MissingContextValues) > 0:
			// Any decision may hinge on unsupplied keys; negated operators
			// match a missing key, so even explicitDeny can be false.
			res.unverified = true
		case r.EvalDecision != iamtypes.PolicyEvaluationDecisionTypeAllowed:
			res.denied = appendUnique(res.denied, denialLabel(a, r))
		}
	}
	return res, nil
}

// region is the region the connector calls AWS in, falling back best-effort to
// AWS_DEFAULT_REGION when the connector config sets none.
func (c *AWSChecker) region(id *AWSIdentity) string {
	if id.Config.Region != "" || c.Getenv == nil {
		return id.Config.Region
	}
	return c.Getenv("AWS_DEFAULT_REGION")
}

// contextEntries returns the condition values the probe knows truthfully. They
// are sent on every simulation, whether or not a policy references them: the
// simulator also evaluates Organizations SCPs, whose condition keys cannot be
// discovered from the principal's own policies (e.g. a region-restriction SCP
// denies every action when aws:RequestedRegion is absent). Unreferenced entries
// are ignored; other keys stay missing and make results unverified.
func contextEntries(region string, id *AWSIdentity, principalARN string) []iamtypes.ContextEntry {
	candidates := []struct {
		key   string
		value string
		typ   iamtypes.ContextKeyTypeEnum
	}{
		{"aws:RequestedRegion", region, iamtypes.ContextKeyTypeEnumString},
		{"aws:PrincipalAccount", id.AccountID, iamtypes.ContextKeyTypeEnumString},
		{"aws:PrincipalArn", principalARN, iamtypes.ContextKeyTypeEnumString},
		{"aws:SecureTransport", "true", iamtypes.ContextKeyTypeEnumBoolean},
	}

	var entries []iamtypes.ContextEntry
	for _, c := range candidates {
		if c.value == "" {
			continue
		}
		entries = append(entries, iamtypes.ContextEntry{
			ContextKeyName:   aws.String(c.key),
			ContextKeyType:   c.typ,
			ContextKeyValues: []string{c.value},
		})
	}
	return entries
}

// classifyIAMError wraps ErrSimulationUnavailable only for an authorization
// failure on a first call; cancellation and other failures stay distinct.
func classifyIAMError(ctx context.Context, op string, err error, first bool) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s: %w", op, ctxErr)
	}
	var apiErr smithy.APIError
	if first && errors.As(err, &apiErr) && accessDeniedCodes[apiErr.ErrorCode()] {
		return fmt.Errorf("%w: %s: %v", ErrSimulationUnavailable, op, err)
	}
	return fmt.Errorf("%s: %w", op, err)
}

// denialLabel annotates a denied action with the simulator's decision so a
// surprising denial (e.g. on an admin role) can be traced to its source.
func denialLabel(action string, r iamtypes.EvaluationResult) string {
	reasons := []string{string(r.EvalDecision)}
	if d := r.PermissionsBoundaryDecisionDetail; d != nil && !d.AllowedByPermissionsBoundary {
		reasons = append(reasons, "permissions boundary")
	}
	if d := r.OrganizationsDecisionDetail; d != nil && !d.AllowedByOrganizations {
		reasons = append(reasons, "organizations")
	}
	return fmt.Sprintf("%s [%s]", action, strings.Join(reasons, ", "))
}

func denialMessage(op string, actions []string) string {
	sorted := append([]string(nil), actions...)
	sort.Strings(sorted)
	return fmt.Sprintf("%s denied for %s (policy simulation)", strings.Join(sorted, ", "), op)
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}
