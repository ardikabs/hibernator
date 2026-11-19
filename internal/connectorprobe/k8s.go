package connectorprobe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/k8sutil"
)

// defaultVersionTimeout bounds the version call when the probe has no deadline.
const defaultVersionTimeout = 10 * time.Second

type K8SChecker struct {
	BuildClients func(ctx context.Context, cfg *k8sutil.K8SConnectorConfig) (dynamic.Interface, kubernetes.Interface, error)
}

func NewK8SChecker() *K8SChecker {
	return &K8SChecker{BuildClients: k8sutil.BuildClients}
}

// CheckAuth builds clients and calls the version endpoint. Context
// cancellation is not special-cased: callers must check ctx.Err() themselves.
func (c *K8SChecker) CheckAuth(ctx context.Context, cfg *k8sutil.K8SConnectorConfig) (kubernetes.Interface, *AuthError) {
	if cfg == nil {
		return nil, &AuthError{Reason: hibernatorv1alpha1.ReasonClusterUnreachable, Message: "missing cluster configuration"}
	}
	_, cs, err := c.BuildClients(ctx, cfg)
	if err != nil {
		return nil, &AuthError{Reason: hibernatorv1alpha1.ReasonClusterUnreachable, Message: fmt.Sprintf("build Kubernetes clients: %v", err)}
	}
	if err := serverVersion(ctx, cs); err != nil {
		return nil, &AuthError{Reason: hibernatorv1alpha1.ReasonClusterUnreachable, Message: err.Error()}
	}
	return cs, nil
}

// serverVersion calls /version with a deadline shorter than the probe's, so an
// unreachable endpoint yields a verdict before the probe itself times out.
func serverVersion(ctx context.Context, cs kubernetes.Interface) error {
	rc := cs.Discovery().RESTClient()
	if rc == nil {
		// Fake clientsets have no REST client.
		if _, err := cs.Discovery().ServerVersion(); err != nil {
			return fmt.Errorf("query server version: %w", err)
		}
		return nil
	}

	timeout := defaultVersionTimeout
	if dl, ok := ctx.Deadline(); ok {
		timeout = time.Until(dl) / 2
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body, err := rc.Get().AbsPath("/version").Do(reqCtx).Raw()
	if err != nil {
		if ctx.Err() == nil && errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("query server version: timed out after %s", timeout.Round(time.Millisecond))
		}
		return fmt.Errorf("query server version: %w", err)
	}
	var info version.Info
	if err := json.Unmarshal(body, &info); err != nil {
		return fmt.Errorf("query server version: decode: %w", err)
	}
	return nil
}

type reviewKey struct{ group, resource, subresource, verb string }

type reviewResult struct {
	allowed bool
	err     error
}

// CheckAccess reviews each executor's rules via SelfSubjectAccessReview.
// An executor whose review call fails (and has no definite denial) gets no
// entry: unknown is never reported as denied. A review is denied if
// Status.Denied, or if !Allowed with an empty EvaluationError. !Allowed with a
// non-empty EvaluationError and no Denied is "no opinion" and treated like an
// API error.
func (c *K8SChecker) CheckAccess(ctx context.Context, cs kubernetes.Interface, execs map[string]executor.AccessRequirement) []hibernatorv1alpha1.ExecutorAccess {
	types := make([]string, 0, len(execs))
	for t, req := range execs {
		if req.ConnectorKind == executor.ConnectorKindK8SCluster {
			types = append(types, t)
		}
	}
	sort.Strings(types)

	cache := map[reviewKey]reviewResult{}
	review := func(k reviewKey) reviewResult {
		if r, ok := cache[k]; ok {
			return r
		}
		ssar := &authv1.SelfSubjectAccessReview{Spec: authv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authv1.ResourceAttributes{Group: k.group, Resource: k.resource, Subresource: k.subresource, Verb: k.verb},
		}}
		out, err := cs.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, ssar, metav1.CreateOptions{})
		r := reviewResult{err: err}
		if err == nil {
			st := out.Status
			switch {
			case st.Allowed:
				r.allowed = true
			case !st.Denied && st.EvaluationError != "":
				r.err = fmt.Errorf("evaluation error: %s", st.EvaluationError)
			}
		}
		cache[k] = r
		return r
	}

	var result []hibernatorv1alpha1.ExecutorAccess
	for _, t := range types {
		req := execs[t]
		var msgs []string
		unknown := false
		for _, op := range []struct {
			name string
			acc  executor.OperationAccess
		}{{opShutdown, req.Shutdown}, {opWakeUp, req.WakeUp}} {
			for _, rule := range op.acc.K8SRules {
				res := rule.Resource
				if rule.Subresource != "" {
					res += "/" + rule.Subresource
				}
				for _, verb := range rule.Verbs {
					r := review(reviewKey{rule.Group, rule.Resource, rule.Subresource, verb})
					switch {
					case r.err != nil:
						unknown = true
					case !r.allowed:
						msgs = append(msgs, fmt.Sprintf("%s: %s %s (group %s) denied", op.name, verb, res, groupName(rule.Group)))
					}
				}
			}
		}
		switch {
		case len(msgs) > 0:
			result = append(result, hibernatorv1alpha1.ExecutorAccess{
				Type:         t,
				Reason:       hibernatorv1alpha1.ReasonAccessDenied,
				Message:      strings.Join(msgs, "; "),
				Verification: hibernatorv1alpha1.VerificationAccessReview,
			})
		case unknown:
			// Unknown, never denied.
		default:
			result = append(result, hibernatorv1alpha1.ExecutorAccess{
				Type:         t,
				Allowed:      true,
				Verification: hibernatorv1alpha1.VerificationAccessReview,
			})
		}
	}
	return result
}

func groupName(g string) string {
	if g == "" {
		return "core"
	}
	return g
}
