/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package connector

import (
	"context"
	"fmt"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/connectorprobe"
	"github.com/ardikabs/hibernator/internal/executor"
	statusmanager "github.com/ardikabs/hibernator/internal/provider/status"
)

func (CloudProviderStrategy) Kind() string { return executor.ConnectorKindCloudProvider }

func (CloudProviderStrategy) Name() string { return "cloudprovider-connector" }

func (CloudProviderStrategy) New() client.Object { return &hibernatorv1alpha1.CloudProvider{} }

func (CloudProviderStrategy) Prepare(_ context.Context, r *Reconciler, obj client.Object) (probeTarget, bool, error) {
	cp := obj.(*hibernatorv1alpha1.CloudProvider)
	key := client.ObjectKeyFromObject(cp)

	send := func(m func(*hibernatorv1alpha1.CloudProvider)) {
		r.Statuses.CloudProviderStatuses.Send(statusmanager.Update[*hibernatorv1alpha1.CloudProvider]{
			NamespacedName: key,
			Resource:       cp,
			Mutator:        statusmanager.MutatorFunc[*hibernatorv1alpha1.CloudProvider](m),
		})
	}

	return probeTarget{
		kind:           executor.ConnectorKindCloudProvider,
		key:            key,
		generation:     cp.Generation,
		lastProbe:      cp.Status.LastProbeTime,
		observedGen:    cp.Status.ObservedGeneration,
		observedSecret: cp.Status.ObservedSecretVersion,
		secretRefs:     cloudProviderSecretRefs(cp),
		sendResult: func(res connectorprobe.Result, now metav1.Time, gen int64, sv string, referenced []string) {
			send(func(o *hibernatorv1alpha1.CloudProvider) {
				ApplyResult(res, now, gen, sv)(o)
				ApplyReferencedAccess(referenced, gen)(o)
			})
		},
		sendFailed: func(now metav1.Time, gen int64, sv, msg string) {
			send(ApplyProbeFailed(now, gen, sv, msg))
		},
		sendReferenced: func(gen int64, referenced []string) {
			if m := ApplyReferencedAccess(referenced, gen); changes(cp, m) {
				send(m)
			}
		},
	}, false, nil
}

// Usage reports whether an active plan uses the CloudProvider directly, or
// through a K8SCluster that an active plan uses. Only direct references count
// towards the referenced executor types: a cluster uses its provider to
// authenticate, not to run the provider's executors.
func (CloudProviderStrategy) Usage(ctx context.Context, r *Reconciler, obj client.Object) (Usage, error) {
	key := client.ObjectKeyFromObject(obj)
	u, err := planUsage(ctx, r.Client, executor.ConnectorKindCloudProvider, key)
	if err != nil || u.InUse {
		return u, err
	}

	var clusters hibernatorv1alpha1.K8SClusterList
	if err := r.Client.List(ctx, &clusters); err != nil {
		return Usage{}, fmt.Errorf("list k8sclusters: %w", err)
	}
	for i := range clusters.Items {
		kc := &clusters.Items[i]
		if kc.Spec.ProviderRef == nil || providerKey(kc) != key {
			continue
		}
		cu, err := planUsage(ctx, r.Client, executor.ConnectorKindK8SCluster, client.ObjectKeyFromObject(kc))
		if err != nil {
			return Usage{}, err
		}
		if cu.InUse {
			u.InUse = true
			break
		}
	}
	return u, nil
}

func (CloudProviderStrategy) SendNotInUse(r *Reconciler, obj client.Object) {
	cp := obj.(*hibernatorv1alpha1.CloudProvider)
	r.Statuses.CloudProviderStatuses.Send(statusmanager.Update[*hibernatorv1alpha1.CloudProvider]{
		NamespacedName: client.ObjectKeyFromObject(cp),
		Resource:       cp,
		Mutator:        statusmanager.MutatorFunc[*hibernatorv1alpha1.CloudProvider](ApplyNotInUse(cp.Generation)),
	})
}

func (CloudProviderStrategy) SecretRequests(ctx context.Context, r *Reconciler, secret client.Object) []reconcile.Request {
	var list hibernatorv1alpha1.CloudProviderList
	if err := r.Client.List(ctx, &list); err != nil {
		r.Log.Error(err, "list cloudproviders for secret")
		return nil
	}
	key := client.ObjectKeyFromObject(secret)
	var reqs []reconcile.Request
	for i := range list.Items {
		cp := &list.Items[i]
		if slices.Contains(cloudProviderSecretRefs(cp), key) {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(cp)})
		}
	}
	return reqs
}

// Watches maps a K8SCluster to the CloudProvider it references, so a provider is
// reconciled when a cluster that uses it is added, removed or repointed.
func (CloudProviderStrategy) Watches(b *builder.Builder, _ *Reconciler) *builder.Builder {
	return b.Watches(&hibernatorv1alpha1.K8SCluster{},
		handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
			kc, ok := obj.(*hibernatorv1alpha1.K8SCluster)
			if !ok || kc.Spec.ProviderRef == nil {
				return nil
			}
			return []reconcile.Request{{NamespacedName: providerKey(kc)}}
		}),
		builder.WithPredicates(predicate.GenerationChangedPredicate{}))
}

func cloudProviderSecretRefs(cp *hibernatorv1alpha1.CloudProvider) []types.NamespacedName {
	if cp.Spec.AWS == nil || cp.Spec.AWS.Auth.Static == nil {
		return nil
	}
	ref := cp.Spec.AWS.Auth.Static.SecretRef
	return []types.NamespacedName{{Namespace: defaultNS(ref.Namespace, cp.Namespace), Name: ref.Name}}
}
