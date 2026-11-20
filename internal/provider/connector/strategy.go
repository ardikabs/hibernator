/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package connector

import (
	"context"

	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Strategy is everything the Reconciler needs to know about one connector kind.
// Adding a kind means implementing Strategy; the probe scheduling, Job handling
// and result delivery in Reconciler stay unchanged.
type Strategy interface {
	// Kind is the connector kind, one of the executor.ConnectorKind* values.
	Kind() string
	// Name is the controller name.
	Name() string
	// New returns an empty connector object to fetch.
	New() client.Object
	// Usage reports how active HibernatePlans use the connector.
	Usage(ctx context.Context, r *Reconciler, obj client.Object) (Usage, error)
	// SendNotInUse records that no active plan uses the connector, so it is not probed.
	SendNotInUse(r *Reconciler, obj client.Object)
	// Prepare turns a fetched connector into a probeTarget. When done is true,
	// Prepare has already recorded the outcome and no probe is needed.
	Prepare(ctx context.Context, r *Reconciler, obj client.Object) (target probeTarget, done bool, err error)
	// SecretRequests maps a Secret to the connectors that reference it.
	SecretRequests(ctx context.Context, r *Reconciler, secret client.Object) []reconcile.Request
	// Watches registers the watches this kind needs beyond the connector itself,
	// its Secrets and probe Jobs.
	Watches(b *builder.Builder, r *Reconciler) *builder.Builder
}

// changes reports whether applying m would modify obj, without touching obj.
func changes[T client.Object](obj T, m func(T)) bool {
	applied := obj.DeepCopyObject().(T)
	m(applied)
	return !equality.Semantic.DeepEqual(applied, obj)
}

func defaultNS(ns, fallback string) string {
	if ns == "" {
		return fallback
	}
	return ns
}
