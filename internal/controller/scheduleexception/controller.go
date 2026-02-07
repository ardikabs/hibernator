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
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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

	// Initialize status if needed
	if exception.Status.State == "" {
		// Determine initial state based on validFrom
		if now.Before(exception.Spec.ValidFrom.Time) {
			exception.Status.State = hibernatorv1alpha1.ExceptionStatePending
		} else if now.After(exception.Spec.ValidUntil.Time) {
			exception.Status.State = hibernatorv1alpha1.ExceptionStateExpired
			exception.Status.ExpiredAt = &metav1.Time{Time: now}
		} else {
			exception.Status.State = hibernatorv1alpha1.ExceptionStateActive
			exception.Status.AppliedAt = &metav1.Time{Time: now}
		}

		if err := r.Status().Update(ctx, exception); err != nil {
			return ctrl.Result{}, err
		}

		log.Info("initialized exception status", "state", exception.Status.State)
	}

	// Add plan label if missing (for efficient querying)
	if err := r.ensurePlanLabel(ctx, log, exception); err != nil {
		log.Error(err, "failed to add plan label")
		return ctrl.Result{RequeueAfter: wellknown.RequeueIntervalForScheduleException}, nil
	}

	// Check if pending exception should activate
	if exception.Status.State == hibernatorv1alpha1.ExceptionStatePending &&
		!now.Before(exception.Spec.ValidFrom.Time) {

		orig := exception.DeepCopy()
		exception.Status.State = hibernatorv1alpha1.ExceptionStateActive
		exception.Status.AppliedAt = &metav1.Time{Time: now}
		exception.Status.Message = "Exception activated"

		if err := r.Status().Patch(ctx, exception, client.MergeFrom(orig)); err != nil {
			return ctrl.Result{}, fmt.Errorf("activate exception: %w", err)
		}

		log.Info("activated exception", "validFrom", exception.Spec.ValidFrom)

		// Trigger plan reconciliation
		if err := r.triggerPlanReconciliation(ctx, log, exception); err != nil {
			log.Error(err, "failed to trigger plan reconciliation after activation")
		}
	}

	// Check if exception should expire
	if exception.Status.State == hibernatorv1alpha1.ExceptionStateActive &&
		!exception.Spec.ValidUntil.IsZero() &&
		now.After(exception.Spec.ValidUntil.Time) {
		return r.expireException(ctx, log, exception)
	}

	// Update message with expiry info
	if err := r.updateExceptionMessage(ctx, exception); err != nil {
		log.Error(err, "failed to update exception message")
		return ctrl.Result{RequeueAfter: wellknown.RequeueIntervalForScheduleException}, nil
	}

	// Trigger HibernatePlan reconciliation
	if err := r.triggerPlanReconciliation(ctx, log, exception); err != nil {
		log.Error(err, "failed to trigger plan reconciliation")
		// Don't fail reconciliation if trigger fails - plan will eventually reconcile
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

// expireException transitions the exception from Active to Expired.
func (r *Reconciler) expireException(ctx context.Context, log logr.Logger, exception *hibernatorv1alpha1.ScheduleException) (ctrl.Result, error) {
	log.Info("expiring exception", "validUntil", exception.Spec.ValidUntil)

	orig := exception.DeepCopy()

	// Update state to Expired
	exception.Status.State = hibernatorv1alpha1.ExceptionStateExpired
	exception.Status.ExpiredAt = &metav1.Time{Time: time.Now()}
	exception.Status.Message = "Exception expired"

	if err := r.Status().Patch(ctx, exception, client.MergeFrom(orig)); err != nil {
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

// triggerPlanReconciliation enqueues the referenced HibernatePlan for reconciliation
// by updating an annotation on the plan. This causes the controller-runtime to
// detect a change and reconcile the plan.
func (r *Reconciler) triggerPlanReconciliation(ctx context.Context, log logr.Logger, exception *hibernatorv1alpha1.ScheduleException) error {
	plan := &hibernatorv1alpha1.HibernatePlan{}
	planKey := types.NamespacedName{
		Name:      exception.Spec.PlanRef.Name,
		Namespace: exception.Namespace, // Same namespace constraint enforced by webhook
	}

	if err := r.Get(ctx, planKey, plan); err != nil {
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
	if plan.Annotations[wellknown.AnnotationExceptionTrigger] == triggerValue {
		// Already triggered with same value, skip to avoid infinite loop
		return nil
	}

	orig := plan.DeepCopy()
	plan.Annotations[wellknown.AnnotationExceptionTrigger] = triggerValue
	if err := r.Patch(ctx, plan, client.MergeFrom(orig)); err != nil {
		return fmt.Errorf("update plan annotation to trigger reconciliation: %w", err)
	}

	log.Info("triggered plan reconciliation via annotation update",
		"plan", planKey.Name,
		"trigger", triggerValue)

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
		Watches(
			&hibernatorv1alpha1.HibernatePlan{},
			handler.EnqueueRequestsFromMapFunc(r.findExceptionsForPlan),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: workers,
		}).
		Complete(r)
}

// findExceptionsForPlan returns reconcile requests for ScheduleExceptions when a HibernatePlan changes.
func (r *Reconciler) findExceptionsForPlan(ctx context.Context, obj client.Object) []reconcile.Request {
	plan, ok := obj.(*hibernatorv1alpha1.HibernatePlan)
	if !ok {
		return nil
	}

	// List all exceptions in the same namespace with matching plan label
	var exceptions hibernatorv1alpha1.ScheduleExceptionList
	if err := r.List(ctx, &exceptions,
		client.InNamespace(plan.Namespace),
		client.MatchingLabels{wellknown.LabelPlan: plan.Name},
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
