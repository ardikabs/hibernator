/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package provider

import (
	"context"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
)

// ExceptionReconciler is the ScheduleException provider reconciler.
// It watches ScheduleException resources and populates the ExceptionResources watchable.Map.
type ExceptionReconciler struct {
	client.Client
	APIReader client.Reader
	Clock     clock.Clock

	Log       logr.Logger
	Scheme    *runtime.Scheme
	Resources *message.ControllerResources
}

// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=scheduleexceptions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=scheduleexceptions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=scheduleexceptions/finalizers,verbs=update

// Reconcile handles ScheduleException reconciliation by storing/deleting from watchable map.
func (r *ExceptionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	key := req.NamespacedName
	log := r.Log.WithValues("exception", key)

	// Fetch the ScheduleException
	exception := &hibernatorv1alpha1.ScheduleException{}
	if err := r.Get(ctx, req.NamespacedName, exception); err != nil {
		if apierrors.IsNotFound(err) {
			// Exception deleted → remove from watchable map
			r.Resources.ExceptionResources.Delete(key)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Expired exceptions require no further processing — remove from watchable map
	// so the lifecycle processor never re-evaluates them, and return without requeue.
	if exception.Status.State == hibernatorv1alpha1.ExceptionStateExpired {
		r.Resources.ExceptionResources.Delete(key)
		return ctrl.Result{}, nil
	}

	// Store in watchable map
	r.Resources.ExceptionResources.Store(key, exception)

	log.V(1).Info("stored exception in watchable map",
		"state", exception.Status.State,
		"type", exception.Spec.Type,
		"plan", exception.Spec.PlanRef.Name,
	)

	return r.handleRequeue(exception)
}

// handleRequeue schedules the next reconcile at the precise boundary when the
// exception's state should change:
//   - Pending  → requeue at ValidFrom (activation time)
//   - Active   → requeue at ValidUntil (expiry time), if set
//
// Expired exceptions never reach this function — they are filtered out in Reconcile.
func (r *ExceptionReconciler) handleRequeue(exception *hibernatorv1alpha1.ScheduleException) (ctrl.Result, error) {
	now := r.Clock.Now()

	switch exception.Status.State {
	case hibernatorv1alpha1.ExceptionStatePending:
		return ctrl.Result{RequeueAfter: max(0, exception.Spec.ValidFrom.Sub(now))}, nil

	case hibernatorv1alpha1.ExceptionStateActive:
		if !exception.Spec.ValidUntil.IsZero() {
			return ctrl.Result{RequeueAfter: max(0, exception.Spec.ValidUntil.Sub(now))}, nil
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the exception provider reconciler with the Manager.
func (r *ExceptionReconciler) SetupWithManager(mgr ctrl.Manager, workers int) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hibernatorv1alpha1.ScheduleException{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: workers,
		}).
		Named("scheduleexception-provider").
		Complete(r)
}
