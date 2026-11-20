/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package connector

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

func target(typ, kind, connector string) hibernatorv1alpha1.Target {
	return hibernatorv1alpha1.Target{
		Name:         typ + "-" + connector,
		Type:         typ,
		ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: kind, Name: connector},
	}
}

func hibernatePlan(name string, targets ...hibernatorv1alpha1.Target) *hibernatorv1alpha1.HibernatePlan {
	return &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testConnNS},
		Spec:       hibernatorv1alpha1.HibernatePlanSpec{Targets: targets},
	}
}

func suspended(p *hibernatorv1alpha1.HibernatePlan) *hibernatorv1alpha1.HibernatePlan {
	p.Spec.Suspend = true
	return p
}

func deleting(p *hibernatorv1alpha1.HibernatePlan) *hibernatorv1alpha1.HibernatePlan {
	now := metav1.Now()
	p.DeletionTimestamp = &now
	p.Finalizers = []string{"hibernator.ardikabs.com/finalizer"}
	return p
}

func TestPlanUsage(t *testing.T) {
	crossNS := hibernatePlan("cross", target("rds", kindCP, "aws"))
	crossNS.Namespace = "team-b"
	crossNS.Spec.Targets[0].ConnectorRef.Namespace = testConnNS

	tests := []struct {
		name  string
		plans []*hibernatorv1alpha1.HibernatePlan
		want  Usage
	}{
		{name: "no plans"},
		{name: "active plan",
			plans: []*hibernatorv1alpha1.HibernatePlan{hibernatePlan("p", target("ec2", kindCP, "aws"))},
			want:  Usage{InUse: true, ReferencedTypes: []string{"ec2"}}},
		{name: "types are merged, sorted and deduplicated",
			plans: []*hibernatorv1alpha1.HibernatePlan{
				hibernatePlan("p1", target("rds", kindCP, "aws"), target("ec2", kindCP, "aws")),
				hibernatePlan("p2", target("ec2", kindCP, "aws")),
			},
			want: Usage{InUse: true, ReferencedTypes: []string{"ec2", "rds"}}},
		{name: "one active plan is enough",
			plans: []*hibernatorv1alpha1.HibernatePlan{
				suspended(hibernatePlan("p1", target("rds", kindCP, "aws"))),
				hibernatePlan("p2", target("ec2", kindCP, "aws")),
			},
			want: Usage{InUse: true, ReferencedTypes: []string{"ec2"}}},
		{name: "suspended plan does not count",
			plans: []*hibernatorv1alpha1.HibernatePlan{suspended(hibernatePlan("p", target("ec2", kindCP, "aws")))}},
		{name: "deleting plan does not count",
			plans: []*hibernatorv1alpha1.HibernatePlan{deleting(hibernatePlan("p", target("ec2", kindCP, "aws")))}},
		{name: "other connector name",
			plans: []*hibernatorv1alpha1.HibernatePlan{hibernatePlan("p", target("ec2", kindCP, "other"))}},
		{name: "same name, other kind",
			plans: []*hibernatorv1alpha1.HibernatePlan{hibernatePlan("p", target("workloadscaler", kindCluster, "aws"))}},
		{name: "plan from another namespace with an explicit connector namespace",
			plans: []*hibernatorv1alpha1.HibernatePlan{crossNS},
			want:  Usage{InUse: true, ReferencedTypes: []string{"rds"}}},
		{name: "connector namespace defaults to the plan namespace",
			plans: []*hibernatorv1alpha1.HibernatePlan{func() *hibernatorv1alpha1.HibernatePlan {
				p := hibernatePlan("p", target("ec2", kindCP, "aws"))
				p.Namespace = "team-b"
				return p
			}()}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var objs []client.Object
			for _, p := range tc.plans {
				objs = append(objs, p)
			}
			f := newFixture(t, objs...)
			got, err := planUsage(context.Background(), f.c, kindCP, types.NamespacedName{Namespace: testConnNS, Name: "aws"})
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPlanConnectorIndexValues(t *testing.T) {
	p := hibernatePlan("p", target("ec2", kindCP, "aws"), target("rds", kindCP, "aws"), target("workloadscaler", kindCluster, "c1"))
	assert.Equal(t, []string{"CloudProvider/team-a/aws", "K8SCluster/team-a/c1"}, planConnectorIndexValues(p))
	assert.Empty(t, planConnectorIndexValues(suspended(p.DeepCopy())))
	assert.Empty(t, planConnectorIndexValues(deleting(p.DeepCopy())))
	assert.Empty(t, planConnectorIndexValues(&hibernatorv1alpha1.CloudProvider{}))
}

func TestConnectorRequestsForPlan(t *testing.T) {
	p := hibernatePlan("p", target("ec2", kindCP, "aws"), target("workloadscaler", kindCluster, "c1"))
	p.Spec.Targets[0].ConnectorRef.Namespace = "shared"

	assert.Equal(t, []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: "shared", Name: "aws"}}},
		connectorRequestsForPlan(kindCP, p))
	assert.Equal(t, []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: testConnNS, Name: "c1"}}},
		connectorRequestsForPlan(kindCluster, p))
	assert.Empty(t, connectorRequestsForPlan("Other", p))
}
