/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package scheduleexception

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// Reconciler reconciles a ScheduleException object.
type Reconciler struct {
	client.Client
	APIReader client.Reader

	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=scheduleexceptions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=scheduleexceptions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=scheduleexceptions/finalizers,verbs=update
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=hibernateplans,verbs=get;list;watch
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=hibernateplans/status,verbs=get;update;patch

// Reconcile handles ScheduleException reconciliation.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("scheduleexception", req.NamespacedName)

	// Fetch the ScheduleException
	exception := &hibernatorv1alpha1.ScheduleException{}
	if err := r.Get(ctx, req.NamespacedName, exception); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !exception.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, log, exception)
	}

	// Ensure finalizer
	if !controllerutil.ContainsFinalizer(exception, wellknown.ExceptionFinalizerName) {
		orig := exception.DeepCopy()
		controllerutil.AddFinalizer(exception, wellknown.ExceptionFinalizerName)
		if err := r.Patch(ctx, exception, client.MergeFrom(orig)); err != nil {
			return ctrl.Result{}, fmt.Errorf("add finalizer to scheduleexception: %w", err)
		}
		return ctrl.Result{Requeue: true}, nil
	}

	now := time.Now()

	// Determine desired state
	desiredState := hibernatorv1alpha1.ExceptionStatePending
	if !now.Before(exception.Spec.ValidFrom.Time) {
		if !exception.Spec.ValidUntil.IsZero() && now.After(exception.Spec.ValidUntil.Time) {
			desiredState = hibernatorv1alpha1.ExceptionStateExpired
		} else {
			desiredState = hibernatorv1alpha1.ExceptionStateActive
		}
	}

	// Handle state transitions
	if exception.Status.State != desiredState {
		orig := exception.DeepCopy()
		oldState := exception.Status.State
		if oldState == "" {
			oldState = "<unset>"
		}

		exception.Status.State = desiredState

		switch desiredState {
		case hibernatorv1alpha1.ExceptionStatePending:
			exception.Status.AppliedAt = nil
			exception.Status.ExpiredAt = nil
			exception.Status.Message = "Exception pending"
		case hibernatorv1alpha1.ExceptionStateActive:
			exception.Status.AppliedAt = &metav1.Time{Time: now}
			exception.Status.ExpiredAt = nil
			exception.Status.Message = "Exception activated"
		case hibernatorv1alpha1.ExceptionStateExpired:
			exception.Status.ExpiredAt = &metav1.Time{Time: now}
			exception.Status.Message = "Exception expired"
		}

		log.Info("transitioned exception state", "from", oldState, "to", desiredState)
		if err := r.Status().Patch(ctx, exception, client.MergeFrom(orig)); err != nil {
			return ctrl.Result{}, fmt.Errorf("transition exception state from %s to %s: %w", oldState, desiredState, err)
		}

		return ctrl.Result{Requeue: true}, nil
	}

	// Add plan label if missing (for efficient querying)
	if err := r.ensurePlanLabel(ctx, log, exception); err != nil {
		log.Error(err, "failed to add plan label")
		return ctrl.Result{RequeueAfter: wellknown.RequeueIntervalForScheduleException}, nil
	}

	// Update message with expiry info
	if err := r.updateExceptionMessage(ctx, exception); err != nil {
		log.Error(err, "failed to update exception message")
		return ctrl.Result{RequeueAfter: wellknown.RequeueIntervalForScheduleException}, nil
	}

	// Calculate next requeue time based on state
	var requeueAfter time.Duration
	switch exception.Status.State {
	case hibernatorv1alpha1.ExceptionStatePending:
		// Requeue at validFrom time to activate
		requeueAfter = time.Until(exception.Spec.ValidFrom.Time)
		if requeueAfter < 0 {
			requeueAfter = 0 // Activate immediately
		}
		requeueAfter += 5 * time.Second

	case hibernatorv1alpha1.ExceptionStateActive:
		// Requeue at validUntil time to expire
		if !exception.Spec.ValidUntil.IsZero() {
			requeueAfter = time.Until(exception.Spec.ValidUntil.Time)
			if requeueAfter < 0 {
				requeueAfter = 0 // Expire immediately
			}
			requeueAfter += 5 * time.Second
		} else {
			requeueAfter = wellknown.RequeueIntervalForScheduleException
		}

	default:
		// Expired or unknown state
		requeueAfter = wellknown.RequeueIntervalForScheduleException
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// ensurePlanLabel adds the plan label if it's missing.
func (r *Reconciler) ensurePlanLabel(ctx context.Context, log logr.Logger, exception *hibernatorv1alpha1.ScheduleException) error {
	planName := exception.Spec.PlanRef.Name
	labelValue := exception.Labels[wellknown.LabelPlan]

	if labelValue == planName {
		// Label already correct
		return nil
	}

	// Add or update label
	if exception.Labels == nil {
		exception.Labels = make(map[string]string)
	}
	orig := exception.DeepCopy()
	exception.Labels[wellknown.LabelPlan] = planName
	if err := r.Patch(ctx, exception, client.MergeFrom(orig)); err != nil {
		return fmt.Errorf("update exception labels: %w", err)
	}

	log.Info("added plan label to exception", "label", wellknown.LabelPlan, "value", planName)
	return nil
}

// updateExceptionMessage updates the status message with expiry information.
func (r *Reconciler) updateExceptionMessage(ctx context.Context, exception *hibernatorv1alpha1.ScheduleException) error {
	var newMessage string
	orig := exception.DeepCopy()

	switch exception.Status.State {
	case hibernatorv1alpha1.ExceptionStatePending:
		if !exception.Spec.ValidFrom.IsZero() {
			daysUntilActive := int(time.Until(exception.Spec.ValidFrom.Time).Hours() / 24)
			if daysUntilActive > 0 {
				newMessage = fmt.Sprintf("Exception pending, activates in %d days", daysUntilActive)
			} else {
				hoursUntilActive := int(time.Until(exception.Spec.ValidFrom.Time).Hours())
				if hoursUntilActive > 0 {
					newMessage = fmt.Sprintf("Exception pending, activates in %d hours", hoursUntilActive)
				} else {
					newMessage = "Exception pending, activates soon"
				}
			}
		} else {
			newMessage = "Exception pending"
		}
	case hibernatorv1alpha1.ExceptionStateActive:
		if !exception.Spec.ValidUntil.IsZero() {
			daysUntilExpiry := int(time.Until(exception.Spec.ValidUntil.Time).Hours() / 24)
			if daysUntilExpiry > 0 {
				newMessage = fmt.Sprintf("Exception active, expires in %d days", daysUntilExpiry)
			} else {
				hoursUntilExpiry := int(time.Until(exception.Spec.ValidUntil.Time).Hours())
				if hoursUntilExpiry > 0 {
					newMessage = fmt.Sprintf("Exception active, expires in %d hours", hoursUntilExpiry)
				} else {
					newMessage = "Exception active, expires soon"
				}
			}
		} else {
			newMessage = "Exception active"
		}
	case hibernatorv1alpha1.ExceptionStateExpired:
		newMessage = "Exception expired"
	default:
		newMessage = fmt.Sprintf("Exception state: %s", exception.Status.State)
	}

	if exception.Status.Message != newMessage {
		exception.Status.Message = newMessage
		return r.Status().Patch(ctx, exception, client.MergeFrom(orig))
	}

	return nil
}

// reconcileDelete handles ScheduleException deletion.
func (r *Reconciler) reconcileDelete(ctx context.Context, log logr.Logger, exception *hibernatorv1alpha1.ScheduleException) (ctrl.Result, error) {
	log.V(1).Info("reconciling exception deletion")
	orig := exception.DeepCopy()

	// Update HibernatePlan status to remove this exception from active exceptions list
	if err := r.removeFromPlanStatus(ctx, log, exception); err != nil {
		log.Error(err, "failed to remove exception from plan status")
		// Don't block deletion on this error
	}

	// Remove finalizer to allow deletion
	if controllerutil.ContainsFinalizer(exception, wellknown.ExceptionFinalizerName) {
		controllerutil.RemoveFinalizer(exception, wellknown.ExceptionFinalizerName)
		if err := r.Patch(ctx, exception, client.MergeFrom(orig)); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
	}

	log.Info("exception deleted successfully")
	return ctrl.Result{}, nil
}

// removeFromPlanStatus removes the exception from the HibernatePlan's active exceptions list.
func (r *Reconciler) removeFromPlanStatus(ctx context.Context, log logr.Logger, exception *hibernatorv1alpha1.ScheduleException) error {
	plan := &hibernatorv1alpha1.HibernatePlan{}
	planKey := types.NamespacedName{
		Name:      exception.Spec.PlanRef.Name,
		Namespace: exception.Namespace,
	}

	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := r.Get(ctx, planKey, plan); err != nil {
			return client.IgnoreNotFound(err)
		}

		var updatedExceptions []hibernatorv1alpha1.ExceptionReference
		for _, ref := range plan.Status.ActiveExceptions {
			if ref.Name != exception.Name {
				updatedExceptions = append(updatedExceptions, ref)
			}
		}

		plan.Status.ActiveExceptions = updatedExceptions
		log.V(1).Info("removed exception from plan status", "plan", planKey.Name)
		return r.Status().Update(ctx, plan)

	}); err != nil {
		return err
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager, workers int) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hibernatorv1alpha1.ScheduleException{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: workers,
		}).
		Complete(r)
}
