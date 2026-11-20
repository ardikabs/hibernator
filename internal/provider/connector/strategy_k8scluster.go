/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package connector

import (
	"context"
	"fmt"
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/connectorprobe"
	"github.com/ardikabs/hibernator/internal/executor"
	statusmanager "github.com/ardikabs/hibernator/internal/provider/status"
)

func (K8SClusterStrategy) Kind() string { return executor.ConnectorKindK8SCluster }

func (K8SClusterStrategy) Name() string { return "k8scluster-connector" }

func (K8SClusterStrategy) New() client.Object { return &hibernatorv1alpha1.K8SCluster{} }

func (s K8SClusterStrategy) Prepare(ctx context.Context, r *Reconciler, obj client.Object) (probeTarget, bool, error) {
	kc := obj.(*hibernatorv1alpha1.K8SCluster)
	key := client.ObjectKeyFromObject(kc)
	clusterType := ClusterTypeOf(kc.Spec)
	gen := kc.Generation

	send := func(m func(*hibernatorv1alpha1.K8SCluster)) {
		r.Statuses.K8SClusterStatuses.Send(statusmanager.Update[*hibernatorv1alpha1.K8SCluster]{
			NamespacedName: key,
			Resource:       kc,
			Mutator: statusmanager.MutatorFunc[*hibernatorv1alpha1.K8SCluster](func(o *hibernatorv1alpha1.K8SCluster) {
				m(o)
				o.Status.ClusterType = clusterType
			}),
		})
	}

	if kc.Spec.GKE != nil {
		// GKE clients are not implemented, so a probe would test the home cluster.
		if err := r.deleteProbeJobs(ctx, s.Kind(), key); err != nil {
			return probeTarget{}, false, err
		}
		send(ApplyK8SProbeUnsupported(gen))
		return probeTarget{}, true, nil
	}

	if kc.Spec.EKS != nil && kc.Spec.ProviderRef != nil {
		msg, status, err := providerNotReady(ctx, r.Client, kc)
		if err != nil {
			return probeTarget{}, false, err
		}
		if msg != "" {
			// Requeued by the CloudProvider watch when the provider changes.
			send(ApplyK8SProviderNotReady(gen, status, msg))
			return probeTarget{}, true, nil
		}
	}

	ready := meta.FindStatusCondition(kc.Status.Conditions, hibernatorv1alpha1.ConditionReady)

	return probeTarget{
		kind:           s.Kind(),
		key:            key,
		generation:     gen,
		lastProbe:      kc.Status.LastProbeTime,
		observedGen:    kc.Status.ObservedGeneration,
		observedSecret: kc.Status.ObservedSecretVersion,
		forceDue:       ready != nil && ready.Reason == hibernatorv1alpha1.ReasonProviderNotReady,
		secretRefs:     k8sClusterSecretRefs(kc),
		sendResult: func(res connectorprobe.Result, now metav1.Time, gen int64, sv string, referenced []string) {
			send(func(o *hibernatorv1alpha1.K8SCluster) {
				ApplyK8SResult(res, now, gen, sv)(o)
				ApplyK8SReferencedAccess(referenced, gen)(o)
			})
		},
		sendFailed: func(now metav1.Time, gen int64, sv, msg string) {
			send(ApplyK8SProbeFailed(now, gen, sv, msg))
		},
		sendReferenced: func(gen int64, referenced []string) {
			if m := ApplyK8SReferencedAccess(referenced, gen); changes(kc, m) {
				send(m)
			}
		},
	}, false, nil
}

func (K8SClusterStrategy) Usage(ctx context.Context, r *Reconciler, obj client.Object) (Usage, error) {
	return planUsage(ctx, r.Client, executor.ConnectorKindK8SCluster, client.ObjectKeyFromObject(obj))
}

func (K8SClusterStrategy) SendNotInUse(r *Reconciler, obj client.Object) {
	kc := obj.(*hibernatorv1alpha1.K8SCluster)
	r.Statuses.K8SClusterStatuses.Send(statusmanager.Update[*hibernatorv1alpha1.K8SCluster]{
		NamespacedName: client.ObjectKeyFromObject(kc),
		Resource:       kc,
		Mutator: statusmanager.MutatorFunc[*hibernatorv1alpha1.K8SCluster](func(o *hibernatorv1alpha1.K8SCluster) {
			ApplyK8SNotInUse(kc.Generation)(o)
			o.Status.ClusterType = ClusterTypeOf(kc.Spec)
		}),
	})
}

func (s K8SClusterStrategy) SecretRequests(ctx context.Context, r *Reconciler, secret client.Object) []reconcile.Request {
	key := client.ObjectKeyFromObject(secret)
	return s.requestsMatching(ctx, r, func(kc *hibernatorv1alpha1.K8SCluster) bool {
		return slices.Contains(k8sClusterSecretRefs(kc), key)
	})
}

// Watches adds the CloudProvider watch, so clusters follow their provider's readiness.
func (s K8SClusterStrategy) Watches(b *builder.Builder, r *Reconciler) *builder.Builder {
	return b.Watches(&hibernatorv1alpha1.CloudProvider{},
		handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
			return s.requestsForCloudProvider(ctx, r, obj)
		}),
		builder.WithPredicates(predicate.Or(predicate.GenerationChangedPredicate{}, providerReadyChangedPredicate)))
}

// requestsForCloudProvider maps a CloudProvider to the K8SClusters referencing it.
func (s K8SClusterStrategy) requestsForCloudProvider(ctx context.Context, r *Reconciler, cp client.Object) []reconcile.Request {
	key := client.ObjectKeyFromObject(cp)
	return s.requestsMatching(ctx, r, func(kc *hibernatorv1alpha1.K8SCluster) bool {
		return kc.Spec.ProviderRef != nil && providerKey(kc) == key
	})
}

func (K8SClusterStrategy) requestsMatching(ctx context.Context, r *Reconciler, match func(*hibernatorv1alpha1.K8SCluster) bool) []reconcile.Request {
	var list hibernatorv1alpha1.K8SClusterList
	if err := r.Client.List(ctx, &list); err != nil {
		r.Log.Error(err, "list k8sclusters")
		return nil
	}
	var reqs []reconcile.Request
	for i := range list.Items {
		if match(&list.Items[i]) {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
		}
	}
	return reqs
}

// providerNotReady returns a message when the cluster's CloudProvider is missing
// or not ready, with the Ready status to mirror: False when the provider is
// missing or failed, Unknown when its own Ready condition is Unknown or unset.
func providerNotReady(ctx context.Context, c client.Reader, kc *hibernatorv1alpha1.K8SCluster) (string, metav1.ConditionStatus, error) {
	key := providerKey(kc)
	var cp hibernatorv1alpha1.CloudProvider
	if err := c.Get(ctx, key, &cp); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Sprintf("CloudProvider %s not found", key), metav1.ConditionFalse, nil
		}
		return "", "", err
	}
	if cp.Status.Ready {
		return "", "", nil
	}
	if providerReadyStatus(&cp) == metav1.ConditionFalse {
		return fmt.Sprintf("CloudProvider %s is not ready", key), metav1.ConditionFalse, nil
	}
	return fmt.Sprintf("CloudProvider %s readiness is unknown", key), metav1.ConditionUnknown, nil
}

// providerReadyStatus is the CloudProvider's Ready condition status, Unknown when unset.
func providerReadyStatus(cp *hibernatorv1alpha1.CloudProvider) metav1.ConditionStatus {
	if c := meta.FindStatusCondition(cp.Status.Conditions, hibernatorv1alpha1.ConditionReady); c != nil {
		return c.Status
	}
	return metav1.ConditionUnknown
}

// providerReadyChangedPredicate passes CloudProvider updates that flip
// status.ready or change the Ready condition status.
var providerReadyChangedPredicate = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldCP, okOld := e.ObjectOld.(*hibernatorv1alpha1.CloudProvider)
		newCP, okNew := e.ObjectNew.(*hibernatorv1alpha1.CloudProvider)
		return okOld && okNew && (oldCP.Status.Ready != newCP.Status.Ready ||
			providerReadyStatus(oldCP) != providerReadyStatus(newCP))
	},
}

func k8sClusterSecretRefs(kc *hibernatorv1alpha1.K8SCluster) []types.NamespacedName {
	if kc.Spec.K8S == nil || kc.Spec.K8S.KubeconfigRef == nil {
		return nil
	}
	ref := kc.Spec.K8S.KubeconfigRef
	return []types.NamespacedName{{Namespace: defaultNS(ref.Namespace, kc.Namespace), Name: ref.Name}}
}

func providerKey(kc *hibernatorv1alpha1.K8SCluster) types.NamespacedName {
	ref := kc.Spec.ProviderRef
	return types.NamespacedName{Namespace: defaultNS(ref.Namespace, kc.Namespace), Name: ref.Name}
}
