/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package connector

import (
	"context"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
		sendResult: func(res connectorprobe.Result, now metav1.Time, gen int64, sv string) {
			send(ApplyResult(res, now, gen, sv))
		},
		sendFailed: func(now metav1.Time, gen int64, sv, msg string) {
			send(ApplyProbeFailed(now, gen, sv, msg))
		},
	}, false, nil
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

func (CloudProviderStrategy) Watches(b *builder.Builder, _ *Reconciler) *builder.Builder { return b }

func cloudProviderSecretRefs(cp *hibernatorv1alpha1.CloudProvider) []types.NamespacedName {
	if cp.Spec.AWS == nil || cp.Spec.AWS.Auth.Static == nil {
		return nil
	}
	ref := cp.Spec.AWS.Auth.Static.SecretRef
	return []types.NamespacedName{{Namespace: defaultNS(ref.Namespace, cp.Namespace), Name: ref.Name}}
}
