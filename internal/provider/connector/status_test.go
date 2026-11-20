/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package connector

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/connectorprobe"
)

var now = metav1.NewTime(time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC))

func entries() []hibernatorv1alpha1.ExecutorAccess {
	return []hibernatorv1alpha1.ExecutorAccess{
		{Type: "rds", Allowed: true, Verification: hibernatorv1alpha1.VerificationPolicySimulation},
		{Type: "ec2", Allowed: false, Reason: "AccessDenied"},
		{Type: "eks", Allowed: true},
	}
}

func TestApplyResult_CloudProvider(t *testing.T) {
	tests := []struct {
		name         string
		res          connectorprobe.Result
		wantReady    bool
		wantAllowed  []string
		wantAccess   int
		wantReadyC   metav1.ConditionStatus
		wantReadyR   string
		wantAccessC  metav1.ConditionStatus
		wantAccessR  string
		wantAccessMs string
	}{
		{
			name:        "ready with entries",
			res:         connectorprobe.Result{Ready: true, Message: "ok", AccessVerified: true, ExecutorAccess: entries()},
			wantReady:   true,
			wantAllowed: []string{"eks", "rds"}, wantAccess: 3,
			wantReadyC: metav1.ConditionTrue, wantReadyR: "Ready",
			wantAccessC: metav1.ConditionTrue, wantAccessR: "Verified",
		},
		{
			name:       "access not verified",
			res:        connectorprobe.Result{Ready: true, AccessVerified: false, AccessReason: "SimulationUnavailable", AccessMessage: "no iam:Simulate"},
			wantReady:  true,
			wantReadyC: metav1.ConditionTrue, wantReadyR: "Ready",
			wantAccessC: metav1.ConditionFalse, wantAccessR: "SimulationUnavailable", wantAccessMs: "no iam:Simulate",
		},
		{
			name:       "verified but no entries is vacuous",
			res:        connectorprobe.Result{Ready: true, AccessVerified: true},
			wantReady:  true,
			wantReadyC: metav1.ConditionTrue, wantReadyR: "Ready",
			wantAccessC: metav1.ConditionTrue, wantAccessR: "NoExecutorsToVerify", wantAccessMs: "no executors require access verification",
		},
		{
			name:       "eligible executors but none checked",
			res:        connectorprobe.Result{Ready: true, AccessVerified: true, EligibleExecutors: 2},
			wantReady:  true,
			wantReadyC: metav1.ConditionTrue, wantReadyR: "Ready",
			wantAccessC: metav1.ConditionFalse, wantAccessR: "Unverified",
			wantAccessMs: "access could not be verified for 2 executor(s); no entries recorded",
		},
		{
			name: "some executors checked",
			res: connectorprobe.Result{Ready: true, AccessVerified: true, EligibleExecutors: 4, CheckedExecutors: 3,
				ExecutorAccess: entries()},
			wantReady:   true,
			wantAllowed: []string{"eks", "rds"}, wantAccess: 3,
			wantReadyC: metav1.ConditionTrue, wantReadyR: "Ready",
			wantAccessC: metav1.ConditionTrue, wantAccessR: "Verified",
			wantAccessMs: "3 of 4 executors verified; executors without an entry are unknown",
		},
		{
			name: "all executors checked",
			res: connectorprobe.Result{Ready: true, AccessVerified: true, EligibleExecutors: 3, CheckedExecutors: 3,
				ExecutorAccess: entries()},
			wantReady:   true,
			wantAllowed: []string{"eks", "rds"}, wantAccess: 3,
			wantReadyC: metav1.ConditionTrue, wantReadyR: "Ready",
			wantAccessC: metav1.ConditionTrue, wantAccessR: "Verified",
		},
		{
			name:       "not ready clears lists",
			res:        connectorprobe.Result{Ready: false, Reason: "InvalidCredentials", Message: "bad", AccessVerified: true, ExecutorAccess: entries()},
			wantReady:  false,
			wantReadyC: metav1.ConditionFalse, wantReadyR: "InvalidCredentials",
			wantAccessC: metav1.ConditionUnknown, wantAccessR: "AuthFailed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp := &hibernatorv1alpha1.CloudProvider{}
			cp.Status.AllowedExecutors = []string{"stale"}
			ApplyResult(tt.res, now, 7, "rv9")(cp)
			st := cp.Status

			assert.Equal(t, tt.wantReady, st.Ready)
			assert.Equal(t, tt.res.Message, st.Message)
			assert.Equal(t, &now, st.LastValidated)
			assert.Equal(t, &now, st.LastProbeTime)
			assert.Equal(t, int64(7), st.ObservedGeneration)
			assert.Equal(t, "rv9", st.ObservedSecretVersion)
			assert.Equal(t, tt.wantAllowed, st.AllowedExecutors)
			assert.Len(t, st.ExecutorAccess, tt.wantAccess)

			rc := meta.FindStatusCondition(st.Conditions, "Ready")
			require.NotNil(t, rc)
			assert.Equal(t, tt.wantReadyC, rc.Status)
			assert.Equal(t, tt.wantReadyR, rc.Reason)
			assert.Equal(t, int64(7), rc.ObservedGeneration)
			ac := meta.FindStatusCondition(st.Conditions, "AuthValid")
			require.NotNil(t, ac)
			assert.Equal(t, tt.wantReadyC, ac.Status)
			xc := meta.FindStatusCondition(st.Conditions, "ExecutorAccessVerified")
			require.NotNil(t, xc)
			assert.Equal(t, tt.wantAccessC, xc.Status)
			assert.Equal(t, tt.wantAccessR, xc.Reason)
			if tt.wantAccessMs != "" {
				assert.Equal(t, tt.wantAccessMs, xc.Message)
			}
		})
	}
}

func TestApplyResult_K8SClusterKeepsClusterType(t *testing.T) {
	kc := &hibernatorv1alpha1.K8SCluster{}
	kc.Status.ClusterType = "eks"
	res := connectorprobe.Result{Ready: true, AccessVerified: true, ExecutorAccess: entries()}
	ApplyK8SResult(res, now, 3, "v")(kc)

	assert.Equal(t, hibernatorv1alpha1.K8SClusterType("eks"), kc.Status.ClusterType)
	assert.True(t, kc.Status.Ready)
	assert.Equal(t, []string{"eks", "rds"}, kc.Status.AllowedExecutors)
	assert.Len(t, kc.Status.ExecutorAccess, 3)
	assert.Equal(t, &now, kc.Status.LastValidated)
}

func TestApplyProbeFailed(t *testing.T) {
	prev := metav1.NewTime(now.Add(-time.Hour))
	cp := &hibernatorv1alpha1.CloudProvider{}
	cp.Status = hibernatorv1alpha1.CloudProviderStatus{
		Ready: true, LastValidated: &prev,
		AllowedExecutors: []string{"rds"},
		ExecutorAccess:   entries(),
	}
	ApplyProbeFailed(now, 4, "rv7", "job failed")(cp)

	st := cp.Status
	assert.False(t, st.Ready)
	assert.Equal(t, "probe failed: job failed", st.Message)
	assert.Equal(t, &prev, st.LastValidated)
	assert.Equal(t, &now, st.LastProbeTime)
	assert.Equal(t, int64(4), st.ObservedGeneration)
	assert.Equal(t, "rv7", st.ObservedSecretVersion)
	assert.Empty(t, st.AllowedExecutors)
	assert.Empty(t, st.ExecutorAccess)
	for _, typ := range []string{"Ready", "AuthValid", "ExecutorAccessVerified"} {
		c := meta.FindStatusCondition(st.Conditions, typ)
		require.NotNil(t, c, typ)
		assert.Equal(t, metav1.ConditionUnknown, c.Status)
		assert.Equal(t, "ProbeFailed", c.Reason)
	}
}

func TestResultFromTerminationMessage(t *testing.T) {
	tests := []struct {
		name    string
		msg     string
		wantErr bool
		want    connectorprobe.Result
	}{
		{name: "empty", msg: "", wantErr: true},
		{name: "invalid", msg: "{oops", wantErr: true},
		{name: "empty object", msg: "{}", wantErr: true},
		{name: "null", msg: "null", wantErr: true},
		{name: "not ready without reason", msg: `{"ready":false,"message":"x"}`, wantErr: true},
		{name: "not ready with reason", msg: `{"ready":false,"reason":"SecretNotFound"}`, want: connectorprobe.Result{Reason: "SecretNotFound"}},
		{name: "ready", msg: `{"ready":true}`, want: connectorprobe.Result{Ready: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResultFromTerminationMessage(tt.msg)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestProbeDue(t *testing.T) {
	n := now.Time
	recent := metav1.NewTime(n.Add(-10 * time.Minute))
	old := metav1.NewTime(n.Add(-2 * time.Hour))
	jit := func() time.Duration { return 5 * time.Minute }
	resync := time.Hour

	tests := []struct {
		name              string
		last              *metav1.Time
		obsGen, gen       int64
		obsSecret, secret string
		want              bool
	}{
		{"never probed", nil, 1, 1, "a", "a", true},
		{"generation changed", &recent, 1, 2, "a", "a", true},
		{"secret changed", &recent, 1, 1, "a", "b", true},
		{"within resync", &recent, 1, 1, "a", "a", false},
		{"past resync plus jitter", &old, 1, 1, "a", "a", true},
		{"past resync but within jitter", ptr.To(metav1.NewTime(n.Add(-62 * time.Minute))), 1, 1, "a", "a", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ProbeDue(tt.last, tt.obsGen, tt.gen, tt.obsSecret, tt.secret, resync, n, jit)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestApplyK8SProbeFailed(t *testing.T) {
	prev := metav1.NewTime(now.Add(-time.Hour))
	kc := &hibernatorv1alpha1.K8SCluster{}
	kc.Status.LastValidated = &prev
	kc.Status.ClusterType = "gke"
	kc.Status.AllowedExecutors = []string{"workloadscaler"}
	ApplyK8SProbeFailed(now, 2, "rv3", "boom")(kc)

	assert.Equal(t, "probe failed: boom", kc.Status.Message)
	assert.Equal(t, "rv3", kc.Status.ObservedSecretVersion)
	assert.Empty(t, kc.Status.AllowedExecutors)
	assert.Equal(t, &prev, kc.Status.LastValidated)
	assert.Equal(t, hibernatorv1alpha1.K8SClusterType("gke"), kc.Status.ClusterType)
	assert.Equal(t, metav1.ConditionUnknown, meta.FindStatusCondition(kc.Status.Conditions, "Ready").Status)
}

func TestApplyK8SProviderNotReady(t *testing.T) {
	for _, status := range []metav1.ConditionStatus{metav1.ConditionFalse, metav1.ConditionUnknown} {
		t.Run(string(status), func(t *testing.T) {
			prev := metav1.NewTime(now.Add(-time.Hour))
			kc := &hibernatorv1alpha1.K8SCluster{}
			kc.Status = hibernatorv1alpha1.K8SClusterStatus{Ready: true, LastValidated: &prev,
				AllowedExecutors: []string{"karpenter"}, ExecutorAccess: entries()}
			ApplyK8SProviderNotReady(5, status, "CloudProvider team-a/aws is not ready")(kc)

			st := kc.Status
			assert.False(t, st.Ready)
			assert.Equal(t, &prev, st.LastValidated)
			assert.Nil(t, st.LastProbeTime)
			assert.Empty(t, st.AllowedExecutors)
			assert.Empty(t, st.ExecutorAccess)
			want := map[string]metav1.ConditionStatus{
				"Ready": status, "AuthValid": metav1.ConditionUnknown, "ExecutorAccessVerified": metav1.ConditionUnknown,
			}
			for typ, ws := range want {
				c := meta.FindStatusCondition(st.Conditions, typ)
				require.NotNil(t, c, typ)
				assert.Equal(t, ws, c.Status, typ)
				assert.Equal(t, "ProviderNotReady", c.Reason, typ)
			}
		})
	}
}

func TestApplyK8SProbeUnsupported(t *testing.T) {
	prev := metav1.NewTime(now.Add(-time.Hour))
	kc := &hibernatorv1alpha1.K8SCluster{}
	kc.Status = hibernatorv1alpha1.K8SClusterStatus{Ready: true, LastValidated: &prev,
		AllowedExecutors: []string{"karpenter"}, ExecutorAccess: entries()}
	ApplyK8SProbeUnsupported(3)(kc)

	st := kc.Status
	assert.False(t, st.Ready)
	assert.Equal(t, "GKE clusters are not probed yet", st.Message)
	assert.Equal(t, int64(3), st.ObservedGeneration)
	assert.Nil(t, st.LastProbeTime)
	assert.Empty(t, st.AllowedExecutors)
	assert.Empty(t, st.ExecutorAccess)
	for _, typ := range []string{"Ready", "AuthValid", "ExecutorAccessVerified"} {
		c := meta.FindStatusCondition(st.Conditions, typ)
		require.NotNil(t, c, typ)
		assert.Equal(t, metav1.ConditionUnknown, c.Status, typ)
		assert.Equal(t, "ProbeUnsupported", c.Reason, typ)
		assert.Equal(t, "GKE clusters are not probed yet", c.Message, typ)
	}

	// A second write changes nothing, so the status manager skips it.
	before := kc.DeepCopy()
	ApplyK8SProbeUnsupported(3)(kc)
	assert.Equal(t, before, kc)
}
