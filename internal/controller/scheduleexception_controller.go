/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

const (
	// ExceptionFinalizerName is the finalizer for ScheduleException resources.
	ExceptionFinalizerName = "hibernator.ardikabs.com/exception-finalizer"

	// LabelException is the label key for the exception name.
	LabelException = "hibernator.ardikabs.com/exception"

	// ExceptionRequeueInterval is the default requeue interval for exception reconciliation.
	ExceptionRequeueInterval = 1 * time.Minute
)

// ScheduleExceptionReconciler reconciles a ScheduleException object.
type ScheduleExceptionReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=scheduleexceptions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=scheduleexceptions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=scheduleexceptions/finalizers,verbs=update
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=hibernateplans,verbs=get;list;watch
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=hibernateplans/status,verbs=get;update;patch

// Reconcile handles ScheduleException reconciliation.
func (r *ScheduleExceptionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("scheduleexception", req.NamespacedName)

	// Fetch the ScheduleException
	var exception hibernatorv1alpha1.ScheduleException
	if err := r.Get(ctx, req.NamespacedName, &exception); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !exception.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, log, &exception)
	}

	// Ensure finalizer
	if !controllerutil.ContainsFinalizer(&exception, ExceptionFinalizerName) {
		controllerutil.AddFinalizer(&exception, ExceptionFinalizerName)
		if err := r.Update(ctx, &exception); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Initialize status if needed
	if exception.Status.State == "" {
		exception.Status.State = hibernatorv1alpha1.ExceptionStateActive
		exception.Status.AppliedAt = &metav1.Time{Time: time.Now()}
		if err := r.Status().Update(ctx, &exception); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("initialized exception status", "state", exception.Status.State)
	}

	// Add plan label if missing (for efficient querying)
	if err := r.ensurePlanLabel(ctx, log, &exception); err != nil {
		log.Error(err, "failed to add plan label")
		return ctrl.Result{RequeueAfter: ExceptionRequeueInterval}, nil
	}

	// Check if exception should expire
	now := time.Now()
	if exception.Status.State == hibernatorv1alpha1.ExceptionStateActive &&
		!exception.Spec.ValidUntil.IsZero() &&
		now.After(exception.Spec.ValidUntil.Time) {
		return r.expireException(ctx, log, &exception)
	}

	// Update message with expiry info
	if err := r.updateExceptionMessage(ctx, &exception); err != nil {
		log.Error(err, "failed to update exception message")
		return ctrl.Result{RequeueAfter: ExceptionRequeueInterval}, nil
	}

	// Trigger HibernatePlan reconciliation
	if err := r.triggerPlanReconciliation(ctx, log, &exception); err != nil {
		log.Error(err, "failed to trigger plan reconciliation")
		// Don't fail reconciliation if trigger fails - plan will eventually reconcile
	}

	// Calculate next requeue time (at ValidUntil for automatic expiration)
	var requeueAfter time.Duration
	if exception.Status.State == hibernatorv1alpha1.ExceptionStateActive && !exception.Spec.ValidUntil.IsZero() {
		requeueAfter = time.Until(exception.Spec.ValidUntil.Time)
		if requeueAfter < 0 {
			// Already expired, requeue immediately
			requeueAfter = 0
		}
		// Add small buffer to ensure we're past the expiry time
		requeueAfter += 5 * time.Second
	} else {
		requeueAfter = ExceptionRequeueInterval
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// ensurePlanLabel adds the plan label if it's missing.
func (r *ScheduleExceptionReconciler) ensurePlanLabel(ctx context.Context, log logr.Logger, exception *hibernatorv1alpha1.ScheduleException) error {
	planName := exception.Spec.PlanRef.Name
	labelValue := exception.Labels[LabelPlan]

	if labelValue == planName {
		// Label already correct
		return nil
	}

	// Add or update label
	if exception.Labels == nil {
		exception.Labels = make(map[string]string)
	}
	exception.Labels[LabelPlan] = planName

	if err := r.Update(ctx, exception); err != nil {
		return fmt.Errorf("update exception labels: %w", err)
	}

	log.Info("added plan label to exception", "label", LabelPlan, "value", planName)
	return nil
}

// expireException transitions the exception from Active to Expired.
func (r *ScheduleExceptionReconciler) expireException(ctx context.Context, log logr.Logger, exception *hibernatorv1alpha1.ScheduleException) (ctrl.Result, error) {
	log.Info("expiring exception", "validUntil", exception.Spec.ValidUntil)

	// Update state to Expired
	exception.Status.State = hibernatorv1alpha1.ExceptionStateExpired
	exception.Status.ExpiredAt = &metav1.Time{Time: time.Now()}
	exception.Status.Message = "Exception expired"

	if err := r.Status().Update(ctx, exception); err != nil {
		return ctrl.Result{}, fmt.Errorf("update exception status to expired: %w", err)
	}

	// Trigger plan reconciliation to remove from active exceptions
	if err := r.triggerPlanReconciliation(ctx, log, exception); err != nil {
		log.Error(err, "failed to trigger plan reconciliation after expiration")
	}

	log.Info("exception expired successfully")
	return ctrl.Result{}, nil
}

// updateExceptionMessage updates the status message with expiry information.
func (r *ScheduleExceptionReconciler) updateExceptionMessage(ctx context.Context, exception *hibernatorv1alpha1.ScheduleException) error {
	var newMessage string

	switch exception.Status.State {
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
		return r.Status().Update(ctx, exception)
	}

	return nil
}

// AnnotationExceptionTrigger is the annotation key used to trigger plan reconciliation
// when an exception changes. The value is the timestamp of the last exception change.
const AnnotationExceptionTrigger = "hibernator.ardikabs.com/exception-trigger"

// triggerPlanReconciliation enqueues the referenced HibernatePlan for reconciliation
// by updating an annotation on the plan. This causes the controller-runtime to
// detect a change and reconcile the plan.
func (r *ScheduleExceptionReconciler) triggerPlanReconciliation(ctx context.Context, log logr.Logger, exception *hibernatorv1alpha1.ScheduleException) error {
	var plan hibernatorv1alpha1.HibernatePlan
	planKey := types.NamespacedName{
		Name:      exception.Spec.PlanRef.Name,
		Namespace: exception.Namespace, // Same namespace constraint enforced by webhook
	}

	if err := r.Get(ctx, planKey, &plan); err != nil {
		if errors.IsNotFound(err) {
			log.Info("referenced plan not found, skipping reconciliation trigger",
				"plan", planKey.Name)
			return nil
		}
		return fmt.Errorf("get referenced plan: %w", err)
	}

	// Update annotation to trigger reconciliation
	// This is a common pattern to force controller-runtime to detect a change
	if plan.Annotations == nil {
		plan.Annotations = make(map[string]string)
	}

	triggerValue := fmt.Sprintf("%s/%s/%d", exception.Name, exception.Status.State, time.Now().Unix())
	if plan.Annotations[AnnotationExceptionTrigger] == triggerValue {
		// Already triggered with same value, skip to avoid infinite loop
		return nil
	}

	plan.Annotations[AnnotationExceptionTrigger] = triggerValue
	if err := r.Update(ctx, &plan); err != nil {
		return fmt.Errorf("update plan annotation to trigger reconciliation: %w", err)
	}

	log.Info("triggered plan reconciliation via annotation update",
		"plan", planKey.Name,
		"trigger", triggerValue)

	return nil
}

// reconcileDelete handles ScheduleException deletion.
func (r *ScheduleExceptionReconciler) reconcileDelete(ctx context.Context, log logr.Logger, exception *hibernatorv1alpha1.ScheduleException) (ctrl.Result, error) {
	log.Info("reconciling exception deletion")

	// Update HibernatePlan status to remove this exception from active exceptions list
	if err := r.removeFromPlanStatus(ctx, log, exception); err != nil {
		log.Error(err, "failed to remove exception from plan status")
		// Don't block deletion on this error
	}

	// Remove finalizer to allow deletion
	if controllerutil.ContainsFinalizer(exception, ExceptionFinalizerName) {
		controllerutil.RemoveFinalizer(exception, ExceptionFinalizerName)
		if err := r.Update(ctx, exception); err != nil {
			return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
		}
	}

	log.Info("exception deleted successfully")
	return ctrl.Result{}, nil
}

// removeFromPlanStatus removes the exception from the HibernatePlan's active exceptions list.
func (r *ScheduleExceptionReconciler) removeFromPlanStatus(ctx context.Context, log logr.Logger, exception *hibernatorv1alpha1.ScheduleException) error {
	var plan hibernatorv1alpha1.HibernatePlan
	planKey := types.NamespacedName{
		Name:      exception.Spec.PlanRef.Name,
		Namespace: exception.Namespace,
	}

	if err := r.Get(ctx, planKey, &plan); err != nil {
		if errors.IsNotFound(err) {
			// Plan doesn't exist, nothing to update
			return nil
		}
		return fmt.Errorf("get plan: %w", err)
	}

	// Filter out this exception from active exceptions
	var updatedExceptions []hibernatorv1alpha1.ExceptionReference
	for _, ref := range plan.Status.ActiveExceptions {
		if ref.Name != exception.Name {
			updatedExceptions = append(updatedExceptions, ref)
		}
	}

	if len(updatedExceptions) != len(plan.Status.ActiveExceptions) {
		plan.Status.ActiveExceptions = updatedExceptions
		if err := r.Status().Update(ctx, &plan); err != nil {
			return fmt.Errorf("update plan status: %w", err)
		}
		log.Info("removed exception from plan status", "plan", planKey.Name)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ScheduleExceptionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hibernatorv1alpha1.ScheduleException{}).
		Watches(
			&hibernatorv1alpha1.HibernatePlan{},
			handler.EnqueueRequestsFromMapFunc(r.findExceptionsForPlan),
		).
		Complete(r)
}

// findExceptionsForPlan returns reconcile requests for ScheduleExceptions when a HibernatePlan changes.
func (r *ScheduleExceptionReconciler) findExceptionsForPlan(ctx context.Context, obj client.Object) []reconcile.Request {
	plan, ok := obj.(*hibernatorv1alpha1.HibernatePlan)
	if !ok {
		return nil
	}

	// List all exceptions in the same namespace with matching plan label
	var exceptions hibernatorv1alpha1.ScheduleExceptionList
	if err := r.List(ctx, &exceptions,
		client.InNamespace(plan.Namespace),
		client.MatchingLabels{LabelPlan: plan.Name},
	); err != nil {
		r.Log.Error(err, "failed to list exceptions for plan", "plan", plan.Name)
		return nil
	}

	// Enqueue reconcile requests for each exception
	requests := make([]reconcile.Request, len(exceptions.Items))
	for i, exception := range exceptions.Items {
		requests[i] = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      exception.Name,
				Namespace: exception.Namespace,
			},
		}
	}

	return requests
}
