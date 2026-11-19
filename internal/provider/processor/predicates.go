/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package provider

import (
	"maps"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// configMapDataChangedPredicate fires only when a ConfigMap's Data or BinaryData
// changes, ignoring annotation/label-only updates.
//
// During a wakeup cycle each runner calls MarkTargetRestored(), which patches the
// restore ConfigMap's annotations (not its Data). Without this predicate every
// such annotation write would trigger a provider reconcile even though HasRestoreData
// would return the same answer — producing one spurious reconcile per wakeup stage.
var configMapDataChangedPredicate = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldCM, okOld := e.ObjectOld.(*corev1.ConfigMap)
		newCM, okNew := e.ObjectNew.(*corev1.ConfigMap)
		if !okOld || !okNew {
			return true // pass unknown types through
		}
		return !maps.Equal(oldCM.Data, newCM.Data)
	},
	CreateFunc:  func(_ event.CreateEvent) bool { return true },
	DeleteFunc:  func(_ event.DeleteEvent) bool { return true },
	GenericFunc: func(_ event.GenericEvent) bool { return false },
}

// notificationDeletionPredicate passes through updates where DeletionTimestamp
// transitions from zero to non-zero. This ensures plan reconciles fire so the
// lifecycle processor can clean up watchedPlans and remove the notification finalizer.
var notificationDeletionPredicate = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		if e.ObjectOld == nil || e.ObjectNew == nil {
			return false
		}
		return e.ObjectOld.GetDeletionTimestamp().IsZero() && !e.ObjectNew.GetDeletionTimestamp().IsZero()
	},
}

// exceptionStateChangedPredicate passes Update events where a
// ScheduleException's lifecycle state changed (e.g. Pending → Active,
// Active → Expired).
//
// Rationale: schedule evaluation only honours exceptions whose Status.State
// is Active (see filterActiveExceptions), but activation and expiry are
// status-subresource writes — they bump neither Generation nor annotations.
// Without this predicate the plan is never re-reconciled when an exception
// takes effect or lapses. Message-only status writes stay suppressed, so
// this adds at most one reconcile per actual transition. No loop risk:
// plan Reconcile never writes exception objects.
var exceptionStateChangedPredicate = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldExc, ok1 := e.ObjectOld.(*hibernatorv1alpha1.ScheduleException)
		newExc, ok2 := e.ObjectNew.(*hibernatorv1alpha1.ScheduleException)
		if !ok1 || !ok2 || oldExc == nil || newExc == nil {
			return false
		}
		return oldExc.Status.State != newExc.Status.State
	},
}

// isJobTerminalTransition reports whether a Job crossed into a terminal state:
// the first 0→1+ transition of Status.Succeeded or Status.Failed. Pure helper
// so the terminal-detection rule is unit-testable without a reconciler.
func isJobTerminalTransition(oldJob, newJob *batchv1.Job) bool {
	if oldJob == nil || newJob == nil {
		return false
	}
	wasTerminal := oldJob.Status.Succeeded > 0 || oldJob.Status.Failed > 0
	isTerminal := newJob.Status.Succeeded > 0 || newJob.Status.Failed > 0
	return !wasTerminal && isTerminal
}

// onJobTerminalUpdate is the predicate UpdateFunc for owned Jobs. On the first
// 0→1+ transition of Job.Status.Succeeded or Job.Status.Failed it increments
// DependencyNonces for the owning plan so that the subsequent Reconcile embeds a
// changed DeliveryNonce into the PlanContext — preventing watchable.Map from
// suppressing re-delivery to subscribers when no HibernatePlan field has changed.
// This enables processors to react to job completion near real-time, bypassing the
// standard polling interval.
//
// Returns true on the terminal transition to allow the event to proceed to Reconcile,
// so the controller immediately stores an updated PlanContext with the incremented
// DeliveryNonce.
func (r *PlanReconciler) onJobTerminalUpdate(e event.UpdateEvent) bool {
	oldJob, ok1 := e.ObjectOld.(*batchv1.Job)
	newJob, ok2 := e.ObjectNew.(*batchv1.Job)
	if !ok1 || !ok2 {
		return false
	}
	condition := isJobTerminalTransition(oldJob, newJob)
	if condition {
		owner := metav1.GetControllerOf(newJob)
		if owner == nil {
			return condition
		}

		nn := types.NamespacedName{
			Name:      owner.Name,
			Namespace: newJob.Namespace,
		}

		r.DependencyNonces.Inc(nn)

		r.Log.V(1).Info("job transitioned to terminal state, enqueuing plan",
			"plan", nn,
			"job", client.ObjectKeyFromObject(newJob),
			"succeeded", newJob.Status.Succeeded,
			"failed", newJob.Status.Failed,
		)
	}

	return condition
}

// jobTerminalPredicate triggers provider reconciliation only when an owned Job
// first reaches a terminal state. Detection uses the monotonically
// increasing Succeeded/Failed counters rather than the Active counter, because
// Active is non-monotonic (0→N→0) and informer coalescing can squash the
// intermediate updates, turning the sequence into Active 0→0 which would be
// invisible. Succeeded and Failed only ever increase, so the 0→1+ transition
// fires exactly once per Job regardless of coalescing.
func (r *PlanReconciler) jobTerminalPredicate() predicate.Funcs {
	return predicate.Funcs{
		UpdateFunc:  r.onJobTerminalUpdate,
		CreateFunc:  func(_ event.CreateEvent) bool { return false },
		DeleteFunc:  func(_ event.DeleteEvent) bool { return false },
		GenericFunc: func(_ event.GenericEvent) bool { return false },
	}
}
