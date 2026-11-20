package connectorprobe

import (
	"context"
	"errors"
	"fmt"
	"os"

	"k8s.io/client-go/kubernetes"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/awsutil"
	"github.com/ardikabs/hibernator/pkg/k8sutil"
)

const simulationUnavailableMessage = "Executor access could not be verified by policy simulation. " +
	"Grant iam:SimulatePrincipalPolicy to the identity the connector authenticates as."

type awsChecker interface {
	CheckAuth(ctx context.Context, cfg *awsutil.AWSConnectorConfig) (*AWSIdentity, *AuthError)
	CheckAccess(ctx context.Context, id *AWSIdentity, principalARN string, execs map[string]executor.AccessRequirement) ([]hibernatorv1alpha1.ExecutorAccess, error)
}

type k8sChecker interface {
	CheckAuth(ctx context.Context, cfg *k8sutil.K8SConnectorConfig) (kubernetes.Interface, *AuthError)
	CheckAccess(ctx context.Context, cs kubernetes.Interface, execs map[string]executor.AccessRequirement) []hibernatorv1alpha1.ExecutorAccess
}

// Prober runs the two-step connector check: authentication, then executor access.
type Prober struct {
	AWS    awsChecker
	K8S    k8sChecker
	Getenv func(string) string
}

// NewProber returns a Prober wired to the real checkers.
func NewProber() *Prober {
	return &Prober{AWS: NewAWSChecker(), K8S: NewK8SChecker(), Getenv: os.Getenv}
}

// Probe checks a connector of the given kind. A non-nil error means the probe
// could not complete (context cancelled or expired, unsupported kind, or missing config for the kind); the
// Result is then zero and must not be read as a verdict. The caller should
// exit non-zero so the controller records a probe failure instead of Ready=false.
func (p *Prober) Probe(ctx context.Context, kind string, cfg executor.ConnectorConfig, execs []executor.Executor) (Result, error) {
	reqs := eligible(kind, cfg.Type, execs)
	switch kind {
	case executor.ConnectorKindCloudProvider:
		return p.probeAWS(ctx, cfg.AWS, reqs)
	case executor.ConnectorKindK8SCluster:
		return p.probeK8S(ctx, cfg.K8S, reqs)
	default:
		return Result{}, fmt.Errorf("unsupported connector kind %q", kind)
	}
}

func (p *Prober) probeAWS(ctx context.Context, cfg *awsutil.AWSConnectorConfig, reqs map[string]executor.AccessRequirement) (Result, error) {
	if cfg == nil {
		return Result{}, fmt.Errorf("missing %s connector configuration", executor.ConnectorKindCloudProvider)
	}
	id, ae := p.AWS.CheckAuth(ctx, cfg)
	if ae != nil {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		return Result{Reason: ae.Reason, Message: ae.Message}, nil
	}

	ready := Result{Ready: true, EligibleExecutors: len(reqs)}
	unavailable := func() (Result, error) {
		ready.AccessReason = hibernatorv1alpha1.ReasonSimulationUnavailable
		ready.AccessMessage = simulationUnavailableMessage
		return ready, nil
	}

	principal, err := ResolvePrincipalARN(cfg, id.ARN, p.Getenv)
	if err != nil {
		return unavailable()
	}
	if len(reqs) == 0 {
		ready.AccessVerified = true
		return ready, nil
	}
	entries, err := p.AWS.CheckAccess(ctx, id, principal, reqs)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return Result{}, cerr
		}
		if errors.Is(err, ErrSimulationUnavailable) {
			return unavailable()
		}
		ready.AccessReason = hibernatorv1alpha1.ReasonProbeError
		ready.AccessMessage = fmt.Sprintf("executor access check failed: %v", err)
		return ready, nil
	}
	ready.AccessVerified = true
	ready.ExecutorAccess = entries
	ready.CheckedExecutors = len(entries)
	return ready, nil
}

func (p *Prober) probeK8S(ctx context.Context, cfg *k8sutil.K8SConnectorConfig, reqs map[string]executor.AccessRequirement) (Result, error) {
	if cfg == nil {
		return Result{}, fmt.Errorf("missing %s connector configuration", executor.ConnectorKindK8SCluster)
	}
	cs, ae := p.K8S.CheckAuth(ctx, cfg)
	if ae != nil {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		return Result{Reason: ae.Reason, Message: ae.Message}, nil
	}
	entries := p.K8S.CheckAccess(ctx, cs, reqs)
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return Result{Ready: true, AccessVerified: true, ExecutorAccess: entries,
		EligibleExecutors: len(reqs), CheckedExecutors: len(entries)}, nil
}

// eligible keeps executors that declare a valid requirement for the connector's
// kind and type, keyed by executor type.
func eligible(kind, connectorType string, execs []executor.Executor) map[string]executor.AccessRequirement {
	out := make(map[string]executor.AccessRequirement)
	for _, e := range execs {
		r, ok := e.(executor.ConnectorRequirer)
		if !ok {
			continue
		}
		req := r.Requires()
		if req.ConnectorKind != kind || !req.AppliesTo(connectorType) || req.Validate() != nil {
			continue
		}
		out[e.Type()] = req
	}
	return out
}
