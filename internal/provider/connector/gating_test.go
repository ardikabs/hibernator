/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package connector

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/connectorprobe"
)

// gated returns a fixture whose reconcilers skip connectors that no active plan uses.
func gated(t *testing.T, objs ...client.Object) *fixture {
	t.Helper()
	f := newFixture(t, objs...)
	f.r.ProbeUnreferenced = false
	f.k.ProbeUnreferenced = false
	return f
}

func condition(t *testing.T, conds []metav1.Condition, typ string) *metav1.Condition {
	t.Helper()
	c := meta.FindStatusCondition(conds, typ)
	require.NotNil(t, c, "condition %s", typ)
	return c
}

func TestReconcile_UnreferencedConnectorIsNotProbed(t *testing.T) {
	f := gated(t, cloudProvider("aws"))

	res, err := f.r.Reconcile(context.Background(), req("aws"))
	require.NoError(t, err)
	assert.Zero(t, res.RequeueAfter, "the plan watch requeues it, not a timer")
	assert.Empty(t, listProbeJobs(t, f.c))

	cp := getCP(t, f.c, "aws")
	assert.False(t, cp.Status.Ready)
	assert.Zero(t, cp.Status.ObservedGeneration)
	assert.Nil(t, cp.Status.LastProbeTime)
	for _, typ := range []string{hibernatorv1alpha1.ConditionReady, hibernatorv1alpha1.ConditionAuthValid,
		hibernatorv1alpha1.ConditionExecutorAccessVerified} {
		c := condition(t, cp.Status.Conditions, typ)
		assert.Equal(t, metav1.ConditionUnknown, c.Status, typ)
		assert.Equal(t, hibernatorv1alpha1.ReasonNotInUse, c.Reason, typ)
	}
}

func TestReconcile_NotInUseKeepsLastProbeTimeAndDropsVerdict(t *testing.T) {
	cp := cloudProvider("aws")
	lp := metav1.NewTime(testNow.Add(-time.Minute))
	cp.Status = hibernatorv1alpha1.CloudProviderStatus{
		Ready: true, LastProbeTime: &lp, ObservedGeneration: 1,
		AllowedExecutors: []string{"ec2"},
		ExecutorAccess:   []hibernatorv1alpha1.ExecutorAccess{{Type: "ec2", Allowed: true}},
	}
	f := gated(t, cp)

	_, err := f.r.Reconcile(context.Background(), req("aws"))
	require.NoError(t, err)

	got := getCP(t, f.c, "aws").Status
	assert.False(t, got.Ready)
	assert.Empty(t, got.AllowedExecutors)
	assert.Empty(t, got.ExecutorAccess)
	require.NotNil(t, got.LastProbeTime)
	assert.True(t, lp.Equal(got.LastProbeTime))
}

func TestReconcile_NotInUseRemovesProbeJobs(t *testing.T) {
	f := gated(t, cloudProvider("aws"))
	require.NoError(t, f.c.Create(context.Background(), probeJob(f, kindCP, "aws", "job-aws", "")))

	_, err := f.r.Reconcile(context.Background(), req("aws"))
	require.NoError(t, err)
	assert.Empty(t, listProbeJobs(t, f.c))
}

func TestReconcile_ReferencedConnectorIsProbed(t *testing.T) {
	tests := []struct {
		name  string
		plans []client.Object
	}{
		{"active plan", []client.Object{hibernatePlan("p", target("ec2", kindCP, "aws"))}},
		{"one active plan among suspended ones", []client.Object{
			suspended(hibernatePlan("p1", target("ec2", kindCP, "aws"))),
			hibernatePlan("p2", target("rds", kindCP, "aws")),
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := gated(t, append(tc.plans, cloudProvider("aws"))...)
			_, err := f.r.Reconcile(context.Background(), req("aws"))
			require.NoError(t, err)
			assert.Len(t, listProbeJobs(t, f.c), 1)
		})
	}
}

func TestReconcile_SuspendedOrDeletingPlansDoNotCount(t *testing.T) {
	f := gated(t, cloudProvider("aws"),
		suspended(hibernatePlan("p1", target("ec2", kindCP, "aws"))),
		deleting(hibernatePlan("p2", target("rds", kindCP, "aws"))))

	_, err := f.r.Reconcile(context.Background(), req("aws"))
	require.NoError(t, err)
	assert.Empty(t, listProbeJobs(t, f.c))
}

func TestReconcile_ProbeUnreferencedOptOut(t *testing.T) {
	f := newFixture(t, cloudProvider("aws"))
	require.True(t, f.r.ProbeUnreferenced)

	_, err := f.r.Reconcile(context.Background(), req("aws"))
	require.NoError(t, err)
	assert.Len(t, listProbeJobs(t, f.c), 1)
}

func TestReconcile_ProviderUsedThroughClusterIsProbed(t *testing.T) {
	eks := k8sCluster("eks", hibernatorv1alpha1.K8SClusterSpec{
		EKS:         &hibernatorv1alpha1.EKSConfig{Name: "c", Region: "us-east-1"},
		ProviderRef: &hibernatorv1alpha1.ProviderRef{Name: "aws"},
	})

	t.Run("cluster used by an active plan", func(t *testing.T) {
		f := gated(t, cloudProvider("aws"), eks, hibernatePlan("p", target("workloadscaler", kindCluster, "eks")))
		_, err := f.r.Reconcile(context.Background(), req("aws"))
		require.NoError(t, err)
		assert.Len(t, listProbeJobs(t, f.c), 1)
	})

	t.Run("cluster used by no plan", func(t *testing.T) {
		f := gated(t, cloudProvider("aws"), eks)
		_, err := f.r.Reconcile(context.Background(), req("aws"))
		require.NoError(t, err)
		assert.Empty(t, listProbeJobs(t, f.c))
	})

	t.Run("cluster used by a suspended plan", func(t *testing.T) {
		f := gated(t, cloudProvider("aws"), eks, suspended(hibernatePlan("p", target("workloadscaler", kindCluster, "eks"))))
		_, err := f.r.Reconcile(context.Background(), req("aws"))
		require.NoError(t, err)
		assert.Empty(t, listProbeJobs(t, f.c))
	})

	t.Run("a provider used only through a cluster has no referenced executors", func(t *testing.T) {
		f := gated(t, cloudProvider("aws"), eks, hibernatePlan("p", target("workloadscaler", kindCluster, "eks")))
		u, err := CloudProviderStrategy{}.Usage(context.Background(), f.r, cloudProvider("aws"))
		require.NoError(t, err)
		assert.Equal(t, Usage{InUse: true}, u)
	})
}

func TestReconcile_K8SClusterGating(t *testing.T) {
	spec := hibernatorv1alpha1.K8SClusterSpec{K8S: &hibernatorv1alpha1.K8SAccessConfig{InCluster: true}}

	f := gated(t, k8sCluster("c1", spec))
	_, err := f.k.Reconcile(context.Background(), req("c1"))
	require.NoError(t, err)
	assert.Empty(t, listProbeJobs(t, f.c))
	kc := getKC(t, f.c, "c1")
	assert.Equal(t, hibernatorv1alpha1.ReasonNotInUse, condition(t, kc.Status.Conditions, hibernatorv1alpha1.ConditionReady).Reason)
	assert.Equal(t, hibernatorv1alpha1.ClusterTypeK8S, kc.Status.ClusterType)

	f = gated(t, k8sCluster("c1", spec), hibernatePlan("p", target("workloadscaler", kindCluster, "c1")))
	_, err = f.k.Reconcile(context.Background(), req("c1"))
	require.NoError(t, err)
	assert.Len(t, listProbeJobs(t, f.c), 1)
}

func TestReconcile_ConnectorIsProbedAsSoonAsAPlanUsesIt(t *testing.T) {
	// A fresh verdict that would otherwise not be due for another resync interval.
	cp := cloudProvider("aws")
	lp := metav1.NewTime(testNow.Add(-time.Minute))
	cp.Status = hibernatorv1alpha1.CloudProviderStatus{Ready: true, LastProbeTime: &lp, ObservedGeneration: 1}
	f := gated(t, cp)
	ctx := context.Background()

	_, err := f.r.Reconcile(ctx, req("aws"))
	require.NoError(t, err)
	require.Empty(t, listProbeJobs(t, f.c))

	require.NoError(t, f.c.Create(ctx, hibernatePlan("p", target("ec2", kindCP, "aws"))))
	_, err = f.r.Reconcile(ctx, req("aws"))
	require.NoError(t, err)
	assert.Len(t, listProbeJobs(t, f.c), 1)
}

func TestReconcile_VerdictIsScopedToReferencedExecutors(t *testing.T) {
	result := connectorprobe.Result{
		Ready: true, AccessVerified: true, EligibleExecutors: 3, CheckedExecutors: 3,
		ExecutorAccess: []hibernatorv1alpha1.ExecutorAccess{
			{Type: "ec2", Allowed: true},
			{Type: "eks", Allowed: false, Reason: hibernatorv1alpha1.ReasonAccessDenied},
			{Type: "rds", Allowed: false, Reason: hibernatorv1alpha1.ReasonAccessDenied},
		},
	}.Marshal()

	tests := []struct {
		name       string
		plans      []client.Object
		wantStatus metav1.ConditionStatus
		wantReason string
		wantMsg    string
	}{
		{name: "only allowed executors used, denials of unused ones do not count",
			plans:      []client.Object{hibernatePlan("p", target("ec2", kindCP, "aws"))},
			wantStatus: metav1.ConditionTrue, wantReason: "Verified"},
		{name: "a used executor is denied",
			plans:      []client.Object{hibernatePlan("p", target("ec2", kindCP, "aws"), target("rds", kindCP, "aws"))},
			wantStatus: metav1.ConditionFalse, wantReason: hibernatorv1alpha1.ReasonAccessDenied,
			wantMsg: "access denied for executors used by active plans: rds"},
		{name: "a denied executor used only by a suspended plan does not count",
			plans: []client.Object{hibernatePlan("p1", target("ec2", kindCP, "aws")),
				suspended(hibernatePlan("p2", target("rds", kindCP, "aws")))},
			wantStatus: metav1.ConditionTrue, wantReason: "Verified"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := gated(t, append(tc.plans, cloudProvider("aws"))...)
			job := probeJob(f, kindCP, "aws", "job-aws", batchv1.JobComplete)
			require.NoError(t, f.c.Create(context.Background(), job))
			require.NoError(t, f.c.Create(context.Background(), probePod(job, result)))

			_, err := f.r.Reconcile(context.Background(), req("aws"))
			require.NoError(t, err)

			cp := getCP(t, f.c, "aws")
			c := condition(t, cp.Status.Conditions, hibernatorv1alpha1.ConditionExecutorAccessVerified)
			assert.Equal(t, tc.wantStatus, c.Status)
			assert.Equal(t, tc.wantReason, c.Reason)
			assert.Equal(t, tc.wantMsg, c.Message)
			assert.True(t, cp.Status.Ready, "executor denials never change Ready")
			assert.Len(t, cp.Status.ExecutorAccess, 3, "the full matrix stays visible")
		})
	}
}

func TestReconcile_PlanChangeRescopesVerdictWithoutAProbe(t *testing.T) {
	cp := cloudProvider("aws")
	lp := metav1.NewTime(testNow.Add(-time.Minute))
	cp.Status = hibernatorv1alpha1.CloudProviderStatus{
		Ready: true, LastProbeTime: &lp, ObservedGeneration: 1,
		ExecutorAccess: []hibernatorv1alpha1.ExecutorAccess{
			{Type: "ec2", Allowed: true}, {Type: "rds", Allowed: false},
		},
		Conditions: []metav1.Condition{{
			Type: hibernatorv1alpha1.ConditionExecutorAccessVerified, Status: metav1.ConditionTrue,
			Reason: "Verified", Message: "ok", LastTransitionTime: lp,
		}},
	}
	f := gated(t, cp, hibernatePlan("p1", target("ec2", kindCP, "aws")))
	ctx := context.Background()
	accessCondition := func() *metav1.Condition {
		return condition(t, getCP(t, f.c, "aws").Status.Conditions, hibernatorv1alpha1.ConditionExecutorAccessVerified)
	}

	_, err := f.r.Reconcile(ctx, req("aws"))
	require.NoError(t, err)
	assert.Equal(t, "ok", accessCondition().Message, "an unchanged verdict is not rewritten")
	assert.Empty(t, f.cps.updates)

	require.NoError(t, f.c.Create(ctx, hibernatePlan("p2", target("rds", kindCP, "aws"))))
	_, err = f.r.Reconcile(ctx, req("aws"))
	require.NoError(t, err)
	assert.Equal(t, metav1.ConditionFalse, accessCondition().Status)
	assert.Contains(t, accessCondition().Message, "rds")
	assert.Empty(t, listProbeJobs(t, f.c), "no new probe")

	var p2 hibernatorv1alpha1.HibernatePlan
	require.NoError(t, f.c.Get(ctx, types.NamespacedName{Namespace: testConnNS, Name: "p2"}, &p2))
	p2.Spec.Suspend = true
	require.NoError(t, f.c.Update(ctx, &p2))
	_, err = f.r.Reconcile(ctx, req("aws"))
	require.NoError(t, err)
	assert.Equal(t, metav1.ConditionTrue, accessCondition().Status)
	assert.Equal(t, "Verified", accessCondition().Reason)
	assert.Empty(t, listProbeJobs(t, f.c))
}
