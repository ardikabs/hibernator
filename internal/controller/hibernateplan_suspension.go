package controller

import (
	"context"
	"fmt"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// reconcileSuspension handles suspension state transitions (both suspend and resume).
// Returns (handled, result, error) where handled indicates if a state transition occurred.
func (r *HibernatePlanReconciler) reconcileSuspension(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (bool, ctrl.Result, error) {
	// Handle suspend transition
	if plan.Spec.Suspend && plan.Status.Phase != hibernatorv1alpha1.PhaseSuspended {
		result, err := r.transitionToSuspended(ctx, log, plan)
		return true, result, err
	}

	// Handle resume transition
	if !plan.Spec.Suspend && plan.Status.Phase == hibernatorv1alpha1.PhaseSuspended {
		result, err := r.transitionFromSuspended(ctx, log, plan)
		return true, result, err
	}

	// No state transition needed
	return false, ctrl.Result{}, nil
}

// transitionToSuspended handles transitioning a plan to suspended state.
func (r *HibernatePlanReconciler) transitionToSuspended(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (ctrl.Result, error) {
	orig := plan.DeepCopy()

	log.Info("suspending plan", "currentPhase", plan.Status.Phase)

	// Record the phase at suspension time for resume decision
	if plan.Annotations == nil {
		plan.Annotations = make(map[string]string)
	}
	plan.Annotations[AnnotationSuspendedAtPhase] = string(plan.Status.Phase)

	if err := r.Patch(ctx, plan, client.MergeFrom(orig)); err != nil {
		return ctrl.Result{}, fmt.Errorf("update suspension annotation: %w", err)
	}

	// Transition to Suspended phase
	if err := r.statusUpdater.Update(ctx, plan, MutatorFunc(func(obj client.Object) client.Object {
		p := obj.(*hibernatorv1alpha1.HibernatePlan)
		p.Status.Phase = hibernatorv1alpha1.PhaseSuspended
		p.Status.ErrorMessage = "" // Clear error message (clean slate for resume)
		now := metav1.Now()
		p.Status.LastTransitionTime = &now
		return p
	})); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{Requeue: true}, nil
}

// transitionFromSuspended handles transitioning a plan from suspended state back to active operation.
func (r *HibernatePlanReconciler) transitionFromSuspended(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (ctrl.Result, error) {
	log.Info("resuming plan from suspended state")

	// Check if we need to force wake-up
	if shouldWakeUp, err := r.shouldForceWakeUpOnResume(ctx, log, plan); err != nil {
		return ctrl.Result{}, err
	} else if shouldWakeUp {
		return r.forceWakeUpOnResume(ctx, log, plan)
	}

	// Normal resume: transition to Active phase
	if err := r.statusUpdater.Update(ctx, plan, MutatorFunc(func(obj client.Object) client.Object {
		p := obj.(*hibernatorv1alpha1.HibernatePlan)
		p.Status.Phase = hibernatorv1alpha1.PhaseActive
		now := metav1.Now()
		p.Status.LastTransitionTime = &now
		return p
	})); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{Requeue: true}, nil
}

// shouldForceWakeUpOnResume determines if we should force wake-up when resuming from suspended state.
// Returns true if:
// - Plan was suspended during hibernation (not Active)
// - Restore data exists
// - Current schedule says we should be awake
func (r *HibernatePlanReconciler) shouldForceWakeUpOnResume(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (bool, error) {
	suspendedAtPhase := plan.Annotations[AnnotationSuspendedAtPhase]

	// Only consider forced wake-up if suspended during hibernation
	if suspendedAtPhase == "" || suspendedAtPhase == string(hibernatorv1alpha1.PhaseActive) {
		return false, nil
	}

	// Check if restore data exists
	hasRestoreData, err := r.RestoreManager.HasRestoreData(ctx, plan.Namespace, plan.Name)
	if err != nil {
		return false, err
	}
	if !hasRestoreData {
		return false, nil
	}

	// Check current schedule
	shouldHibernate, _, err := r.evaluateSchedule(ctx, log, plan)
	if err != nil {
		return false, err
	}

	// Force wake-up if schedule says we should be awake
	return !shouldHibernate, nil
}

// forceWakeUpOnResume transitions plan to WakingUp phase and starts wake-up immediately.
func (r *HibernatePlanReconciler) forceWakeUpOnResume(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (ctrl.Result, error) {
	suspendedAtPhase := plan.Annotations[AnnotationSuspendedAtPhase]
	log.Info("forcing wake-up after unsuspend",
		"suspendedAtPhase", suspendedAtPhase,
		"reason", "restore data exists and schedule indicates active period")

	// Transition to WakingUp phase
	if err := r.statusUpdater.Update(ctx, plan, MutatorFunc(func(obj client.Object) client.Object {
		p := obj.(*hibernatorv1alpha1.HibernatePlan)
		p.Status.Phase = hibernatorv1alpha1.PhaseWakingUp
		now := metav1.Now()
		p.Status.LastTransitionTime = &now
		return p
	})); err != nil {
		return ctrl.Result{}, err
	}

	// Immediately start wake-up
	return r.startWakeUp(ctx, log, plan)
}
