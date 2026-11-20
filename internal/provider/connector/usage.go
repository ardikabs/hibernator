/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package connector

import (
	"context"
	"fmt"
	"slices"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// Usage describes how active HibernatePlans use a connector.
type Usage struct {
	// InUse is true when an active plan needs the connector, directly or, for a
	// CloudProvider, through a K8SCluster that an active plan uses.
	InUse bool
	// ReferencedTypes are the executor types of the targets of active plans that
	// reference the connector directly, sorted and without duplicates.
	ReferencedTypes []string
}

// planActive reports whether a plan counts as using its connectors. A suspended
// or deleting plan does not.
func planActive(plan *hibernatorv1alpha1.HibernatePlan) bool {
	return !plan.Spec.Suspend && plan.DeletionTimestamp == nil
}

func connectorIndexKey(kind string, key types.NamespacedName) string {
	return fmt.Sprintf("%s/%s/%s", kind, key.Namespace, key.Name)
}

// targetConnectorKey returns the index key of the connector a target references.
func targetConnectorKey(plan *hibernatorv1alpha1.HibernatePlan, t *hibernatorv1alpha1.Target) string {
	ref := t.ConnectorRef
	return connectorIndexKey(ref.Kind, types.NamespacedName{Namespace: defaultNS(ref.Namespace, plan.Namespace), Name: ref.Name})
}

func planConnectorIndexValues(obj client.Object) []string {
	plan, ok := obj.(*hibernatorv1alpha1.HibernatePlan)
	if !ok || !planActive(plan) {
		return nil
	}
	keys := make([]string, 0, len(plan.Spec.Targets))
	for i := range plan.Spec.Targets {
		if k := targetConnectorKey(plan, &plan.Spec.Targets[i]); !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	return keys
}

// registerPlanIndex indexes active HibernatePlans by the connectors their targets reference.
func registerPlanIndex(mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(context.Background(), &hibernatorv1alpha1.HibernatePlan{},
		wellknown.FieldIndexPlanConnectorRef, planConnectorIndexValues)
}

// planUsage reports how active plans use the connector of the given kind and key.
func planUsage(ctx context.Context, c client.Reader, kind string, key types.NamespacedName) (Usage, error) {
	var plans hibernatorv1alpha1.HibernatePlanList
	want := connectorIndexKey(kind, key)
	if err := c.List(ctx, &plans, client.MatchingFields{wellknown.FieldIndexPlanConnectorRef: want}); err != nil {
		return Usage{}, fmt.Errorf("list plans for %s: %w", want, err)
	}

	var u Usage
	for i := range plans.Items {
		plan := &plans.Items[i]
		if !planActive(plan) {
			continue
		}
		for j := range plan.Spec.Targets {
			t := &plan.Spec.Targets[j]
			if targetConnectorKey(plan, t) != want {
				continue
			}
			u.InUse = true
			if !slices.Contains(u.ReferencedTypes, t.Type) {
				u.ReferencedTypes = append(u.ReferencedTypes, t.Type)
			}
		}
	}
	slices.Sort(u.ReferencedTypes)
	return u, nil
}

// connectorRequestsForPlan maps a plan to the connectors of one kind it references.
func connectorRequestsForPlan(kind string, plan *hibernatorv1alpha1.HibernatePlan) []reconcile.Request {
	var reqs []reconcile.Request
	for i := range plan.Spec.Targets {
		ref := plan.Spec.Targets[i].ConnectorRef
		if ref.Kind != kind {
			continue
		}
		reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{
			Namespace: defaultNS(ref.Namespace, plan.Namespace), Name: ref.Name}})
	}
	return reqs
}
