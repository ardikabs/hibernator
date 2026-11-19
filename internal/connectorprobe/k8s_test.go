package connectorprobe

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/k8sutil"
)

func accessStatus(b bool) authv1.SubjectAccessReviewStatus {
	return authv1.SubjectAccessReviewStatus{Allowed: b}
}

func k8sReq(shutdown, wakeup []executor.K8SRule) executor.AccessRequirement {
	return executor.AccessRequirement{
		ConnectorKind: executor.ConnectorKindK8SCluster,
		Shutdown:      executor.OperationAccess{K8SRules: shutdown},
		WakeUp:        executor.OperationAccess{K8SRules: wakeup},
	}
}

// reviewClient answers each SSAR via decide; returns the clientset and a call counter.
func reviewClient(decide func(a *authv1.ResourceAttributes) (authv1.SubjectAccessReviewStatus, error)) (*fake.Clientset, *int) {
	cs := fake.NewSimpleClientset()
	calls := 0
	cs.PrependReactor("create", "selfsubjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		calls++
		ssar := action.(k8stesting.CreateAction).GetObject().(*authv1.SelfSubjectAccessReview)
		st, err := decide(ssar.Spec.ResourceAttributes)
		if err != nil {
			return true, nil, err
		}
		return true, &authv1.SelfSubjectAccessReview{Status: st}, nil
	})
	return cs, &calls
}

func TestK8SChecker_CheckAccess(t *testing.T) {
	scale := executor.K8SRule{Group: "apps", Resource: "deployments", Subresource: "scale", Verbs: []string{"get", "update"}}
	nodes := executor.K8SRule{Resource: "nodes", Verbs: []string{"list"}}

	tests := []struct {
		name    string
		execs   map[string]executor.AccessRequirement
		decide  func(a *authv1.ResourceAttributes) (authv1.SubjectAccessReviewStatus, error)
		want    []hibernatorv1alpha1.ExecutorAccess
		wantMsg []string
	}{
		{
			name:  "all allowed",
			execs: map[string]executor.AccessRequirement{"workloadscaler": k8sReq([]executor.K8SRule{scale}, []executor.K8SRule{scale})},
			decide: func(*authv1.ResourceAttributes) (authv1.SubjectAccessReviewStatus, error) {
				return accessStatus(true), nil
			},
			want: []hibernatorv1alpha1.ExecutorAccess{
				{Type: "workloadscaler", Allowed: true, Verification: hibernatorv1alpha1.VerificationAccessReview},
			},
		},
		{
			name:  "denied shutdown verb names group resource verb and operation",
			execs: map[string]executor.AccessRequirement{"workloadscaler": k8sReq([]executor.K8SRule{scale}, nil)},
			decide: func(a *authv1.ResourceAttributes) (authv1.SubjectAccessReviewStatus, error) {
				return accessStatus(a.Verb != "update"), nil
			},
			want: []hibernatorv1alpha1.ExecutorAccess{
				{Type: "workloadscaler", Reason: hibernatorv1alpha1.ReasonAccessDenied, Verification: hibernatorv1alpha1.VerificationAccessReview},
			},
			wantMsg: []string{"shutdown", "apps", "deployments/scale", "update"},
		},
		{
			name:  "denied wakeup only still fails",
			execs: map[string]executor.AccessRequirement{"karpenter": k8sReq([]executor.K8SRule{nodes}, []executor.K8SRule{scale})},
			decide: func(a *authv1.ResourceAttributes) (authv1.SubjectAccessReviewStatus, error) {
				return accessStatus(a.Resource != "deployments"), nil
			},
			want: []hibernatorv1alpha1.ExecutorAccess{
				{Type: "karpenter", Reason: hibernatorv1alpha1.ReasonAccessDenied, Verification: hibernatorv1alpha1.VerificationAccessReview},
			},
			wantMsg: []string{"wakeup", "deployments/scale"},
		},
		{
			name:  "api error skips entry",
			execs: map[string]executor.AccessRequirement{"workloadscaler": k8sReq([]executor.K8SRule{scale}, nil)},
			decide: func(*authv1.ResourceAttributes) (authv1.SubjectAccessReviewStatus, error) {
				return authv1.SubjectAccessReviewStatus{}, errors.New("connection refused")
			},
			want: nil,
		},
		{
			name:  "evaluation error without denial gives no entry",
			execs: map[string]executor.AccessRequirement{"karpenter": k8sReq([]executor.K8SRule{nodes}, nil)},
			decide: func(*authv1.ResourceAttributes) (authv1.SubjectAccessReviewStatus, error) {
				return authv1.SubjectAccessReviewStatus{EvaluationError: "boom"}, nil
			},
			want: nil,
		},
		{
			name:  "denied with evaluation error is denied",
			execs: map[string]executor.AccessRequirement{"karpenter": k8sReq([]executor.K8SRule{nodes}, nil)},
			decide: func(*authv1.ResourceAttributes) (authv1.SubjectAccessReviewStatus, error) {
				return authv1.SubjectAccessReviewStatus{Denied: true, EvaluationError: "boom"}, nil
			},
			want: []hibernatorv1alpha1.ExecutorAccess{
				{Type: "karpenter", Reason: hibernatorv1alpha1.ReasonAccessDenied, Verification: hibernatorv1alpha1.VerificationAccessReview},
			},
			wantMsg: []string{"shutdown", "nodes"},
		},
		{
			name:  "plain not allowed is denied",
			execs: map[string]executor.AccessRequirement{"karpenter": k8sReq([]executor.K8SRule{nodes}, nil)},
			decide: func(*authv1.ResourceAttributes) (authv1.SubjectAccessReviewStatus, error) {
				return accessStatus(false), nil
			},
			want: []hibernatorv1alpha1.ExecutorAccess{
				{Type: "karpenter", Reason: hibernatorv1alpha1.ReasonAccessDenied, Verification: hibernatorv1alpha1.VerificationAccessReview},
			},
			wantMsg: []string{"shutdown", "nodes"},
		},
		{
			name: "cloud provider executors ignored, output sorted",
			execs: map[string]executor.AccessRequirement{
				"workloadscaler": k8sReq([]executor.K8SRule{scale}, nil),
				"ec2":            {ConnectorKind: executor.ConnectorKindCloudProvider},
				"karpenter":      k8sReq([]executor.K8SRule{nodes}, nil),
			},
			decide: func(*authv1.ResourceAttributes) (authv1.SubjectAccessReviewStatus, error) {
				return accessStatus(true), nil
			},
			want: []hibernatorv1alpha1.ExecutorAccess{
				{Type: "karpenter", Allowed: true, Verification: hibernatorv1alpha1.VerificationAccessReview},
				{Type: "workloadscaler", Allowed: true, Verification: hibernatorv1alpha1.VerificationAccessReview},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs, _ := reviewClient(tt.decide)
			got := NewK8SChecker().CheckAccess(context.Background(), cs, tt.execs)
			require.Len(t, got, len(tt.want))
			for i, w := range tt.want {
				msg := got[i].Message
				got[i].Message = ""
				assert.Equal(t, w, got[i])
				for _, s := range tt.wantMsg {
					assert.Contains(t, msg, s)
				}
			}
		})
	}
}

func TestK8SChecker_CheckAccess_Dedup(t *testing.T) {
	rule := executor.K8SRule{Group: "apps", Resource: "deployments", Verbs: []string{"get"}}
	cs, calls := reviewClient(func(*authv1.ResourceAttributes) (authv1.SubjectAccessReviewStatus, error) {
		return accessStatus(true), nil
	})
	execs := map[string]executor.AccessRequirement{
		"a": k8sReq([]executor.K8SRule{rule}, []executor.K8SRule{rule}),
		"b": k8sReq([]executor.K8SRule{rule}, nil),
	}
	got := NewK8SChecker().CheckAccess(context.Background(), cs, execs)
	require.Len(t, got, 2)
	assert.Equal(t, 1, *calls)
}

func TestK8SChecker_CheckAuth(t *testing.T) {
	cfg := &k8sutil.K8SConnectorConfig{ClusterName: "c"}
	build := func(cs kubernetes.Interface, err error) func(context.Context, *k8sutil.K8SConnectorConfig) (dynamic.Interface, kubernetes.Interface, error) {
		return func(context.Context, *k8sutil.K8SConnectorConfig) (dynamic.Interface, kubernetes.Interface, error) {
			return nil, cs, err
		}
	}
	t.Run("success", func(t *testing.T) {
		cs := fake.NewSimpleClientset()
		c := &K8SChecker{BuildClients: build(cs, nil)}
		got, aerr := c.CheckAuth(context.Background(), cfg)
		require.Nil(t, aerr)
		assert.Equal(t, kubernetes.Interface(cs), got)
	})
	t.Run("build error is unreachable", func(t *testing.T) {
		c := &K8SChecker{BuildClients: build(nil, errors.New("bad kubeconfig"))}
		got, aerr := c.CheckAuth(context.Background(), cfg)
		assert.Nil(t, got)
		require.NotNil(t, aerr)
		assert.Equal(t, hibernatorv1alpha1.ReasonClusterUnreachable, aerr.Reason)
		assert.Contains(t, aerr.Message, "bad kubeconfig")
	})
	t.Run("nil config", func(t *testing.T) {
		c := &K8SChecker{BuildClients: build(fake.NewSimpleClientset(), nil)}
		_, aerr := c.CheckAuth(context.Background(), nil)
		require.NotNil(t, aerr)
		assert.Equal(t, hibernatorv1alpha1.ReasonClusterUnreachable, aerr.Reason)
	})
}

func TestK8SChecker_CheckAuth_ServerVersion(t *testing.T) {
	cfg := &k8sutil.K8SConnectorConfig{ClusterName: "c"}
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr string
	}{
		{name: "ok", handler: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"major":"1","minor":"31","gitVersion":"v1.31.0"}`))
		}},
		{name: "server error", handler: func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "nope", http.StatusInternalServerError)
		}, wantErr: "query server version"},
		{name: "blackholed endpoint times out before the parent", handler: func(_ http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}, wantErr: "timed out"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()
			cs, err := kubernetes.NewForConfig(&rest.Config{Host: srv.URL})
			require.NoError(t, err)
			c := &K8SChecker{BuildClients: func(context.Context, *k8sutil.K8SConnectorConfig) (dynamic.Interface, kubernetes.Interface, error) {
				return nil, cs, nil
			}}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			got, aerr := c.CheckAuth(ctx, cfg)
			if tt.wantErr == "" {
				require.Nil(t, aerr)
				assert.NotNil(t, got)
				return
			}
			require.NotNil(t, aerr)
			assert.Equal(t, hibernatorv1alpha1.ReasonClusterUnreachable, aerr.Reason)
			assert.Contains(t, aerr.Message, tt.wantErr)
			assert.NoError(t, ctx.Err(), "the version request must end before the probe deadline")
		})
	}
}
