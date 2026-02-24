package hibernateplan

import (
	"context"
	"fmt"
	"time"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/controller/status"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// reconcileSuspension handles suspension state transitions (both suspend and resume).
// Annotation handler adjusts spec.suspend and returns deadline requeue timing.
// Transition handlers always run and take priority if phase needs updating.
func (r *Reconciler) reconcileSuspension(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (bool, ctrl.Result, error) {
	// Annotation handler adjusts spec.suspend based on deadline and provides requeue timing
	suspendDeadlineResult, err := r.handleSuspendUntilAnnotation(ctx, log, plan)
	if err != nil {
		return false, ctrl.Result{}, err
	}

	// Handle suspend transition - takes priority
	if plan.Spec.Suspend && plan.Status.Phase != hibernatorv1alpha1.PhaseSuspended {
		result, err := r.transitionToSuspended(ctx, log, plan)
		return true, result, err
	}

	// Handle resume transition - takes priority
	if !plan.Spec.Suspend && plan.Status.Phase == hibernatorv1alpha1.PhaseSuspended {
		result, err := r.transitionFromSuspended(ctx, log, plan)
		return true, result, err
	}

	// No phase transition needed - use annotation deadline requeue if present
	if suspendDeadlineResult.RequeueAfter > 0 || suspendDeadlineResult.Requeue {
		return true, suspendDeadlineResult, nil
	}

	return false, ctrl.Result{}, nil
}

// transitionToSuspended handles transitioning a plan to suspended state.
func (r *Reconciler) transitionToSuspended(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (ctrl.Result, error) {
	orig := plan.DeepCopy()

	log.Info("suspending plan", "currentPhase", plan.Status.Phase)

	// Record the phase at suspension time for resume decision
	if plan.Annotations == nil {
		plan.Annotations = make(map[string]string)
	}
	plan.Annotations[wellknown.AnnotationSuspendedAtPhase] = string(plan.Status.Phase)

	if err := r.Patch(ctx, plan, client.MergeFrom(orig)); err != nil {
		return ctrl.Result{}, fmt.Errorf("update suspension annotation: %w", err)
	}

	// Transition to Suspended phase
	if err := r.statusUpdater.Update(ctx, plan, status.MutatorFunc(func(obj client.Object) client.Object {
		p := obj.(*hibernatorv1alpha1.HibernatePlan)
		p.Status.Phase = hibernatorv1alpha1.PhaseSuspended
		p.Status.ErrorMessage = "" // Clear error message (clean slate for resume)
		now := metav1.NewTime(r.Clock.Now())
		p.Status.LastTransitionTime = &now
		return p
	})); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{Requeue: true}, nil
}

// transitionFromSuspended handles transitioning a plan from suspended state back to active operation.
func (r *Reconciler) transitionFromSuspended(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (ctrl.Result, error) {
	log.Info("resuming plan from suspended state")

	// Check if we need to force wake-up
	if shouldWakeUp, err := r.shouldForceWakeUpOnResume(ctx, log, plan); err != nil {
		return ctrl.Result{}, err
	} else if shouldWakeUp {
		return r.forceWakeUpOnResume(ctx, log, plan)
	}

	// Normal resume: transition to Active phase
	if err := r.statusUpdater.Update(ctx, plan, status.MutatorFunc(func(obj client.Object) client.Object {
		p := obj.(*hibernatorv1alpha1.HibernatePlan)
		p.Status.Phase = hibernatorv1alpha1.PhaseActive
		now := metav1.NewTime(r.Clock.Now())
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
func (r *Reconciler) shouldForceWakeUpOnResume(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (bool, error) {
	suspendedAtPhase := plan.Annotations[wellknown.AnnotationSuspendedAtPhase]

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
func (r *Reconciler) forceWakeUpOnResume(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (ctrl.Result, error) {
	suspendedAtPhase := plan.Annotations[wellknown.AnnotationSuspendedAtPhase]
	log.Info("forcing wake-up after unsuspend",
		"suspendedAtPhase", suspendedAtPhase,
		"reason", "restore data exists and schedule indicates active period")

	// Transition to WakingUp phase
	if err := r.statusUpdater.Update(ctx, plan, status.MutatorFunc(func(obj client.Object) client.Object {
		p := obj.(*hibernatorv1alpha1.HibernatePlan)
		p.Status.Phase = hibernatorv1alpha1.PhaseWakingUp
		now := metav1.NewTime(r.Clock.Now())
		p.Status.LastTransitionTime = &now
		return p
	})); err != nil {
		return ctrl.Result{}, err
	}

	// Immediately start wake-up
	return r.startWakeUp(ctx, log, plan)
}

// handleSuspendUntilAnnotation adjusts spec.suspend based on the suspend-until deadline
// and returns deadline-based requeue timing. It always lets transition handlers run.
func (r *Reconciler) handleSuspendUntilAnnotation(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (ctrl.Result, error) {
	suspendUntilStr, ok := plan.Annotations[wellknown.AnnotationSuspendUntil]
	// If annotation is missing but plan is suspended, revert suspension
	if !ok && plan.Spec.Suspend && plan.Status.Phase == hibernatorv1alpha1.PhaseSuspended {
		log.Info(fmt.Sprintf("%s annotation removed, reverting suspension", wellknown.AnnotationSuspendUntil))
		orig := plan.DeepCopy()
		plan.Spec.Suspend = false
		if err := r.Patch(ctx, plan, client.MergeFrom(orig)); err != nil {
			return ctrl.Result{}, fmt.Errorf("revert suspend when annotation removed: %w", err)
		}
		// Let transition handler manage phase change
		return ctrl.Result{Requeue: true}, nil
	}

	if !ok {
		return ctrl.Result{}, nil // Guard clause: no annotation, continue
	}

	deadline, err := time.Parse(time.RFC3339, suspendUntilStr)
	if err != nil {
		log.Error(err, "invalid annotation format, expected RFC3339, ignoring ...", "got", suspendUntilStr)
		return ctrl.Result{}, nil // Guard clause: parse error, skip processing
	}

	now := r.Clock.Now()

	// If deadline passed, revert suspension and remove annotation
	if now.After(deadline) {
		if plan.Spec.Suspend {
			log.Info("suspension deadline reached, reverting suspension", "deadline", deadline)
			orig := plan.DeepCopy()
			plan.Spec.Suspend = false
			delete(plan.Annotations, wellknown.AnnotationSuspendUntil)
			if err := r.Patch(ctx, plan, client.MergeFrom(orig)); err != nil {
				return ctrl.Result{}, fmt.Errorf("auto-resume from suspension: %w", err)
			}
		}
		// Let transition handler manage phase change
		return ctrl.Result{Requeue: true}, nil
	}

	// Deadline not reached: enforce suspension if not already set
	if !plan.Spec.Suspend {
		log.Info("suspend-until annotation active, enforcing suspension", "deadline", deadline)
		orig := plan.DeepCopy()
		plan.Spec.Suspend = true
		if err := r.Patch(ctx, plan, client.MergeFrom(orig)); err != nil {
			return ctrl.Result{}, fmt.Errorf("enforce suspension until deadline: %w", err)
		}
	}

	// Return deadline-based requeue timing for when suspension expires
	requeueAfter := deadline.Sub(now) + time.Second
	log.V(1).Info("suspend-until deadline pending, will requeue at deadline", "deadline", deadline, "requeueAfter", requeueAfter)
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}
