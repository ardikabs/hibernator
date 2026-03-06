/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package provider

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"time"

	"github.com/go-logr/logr"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// PlanReconciler is the HibernatePlan provider reconciler.
// It watches K8s resources and populates the PlanResources watchable.Map with an enriched
// PlanContext snapshot.
//
// Responsibility boundary: the provider owns DECLARATIVE reads — data that feeds
// phase-transition decisions (schedule evaluation, HasRestoreData, active exceptions).
// Supervisor states own OPERATIONAL reads/writes — Job lifecycle, Pod inspection,
// and restore ConfigMap data that are part of executing the current phase.
type PlanReconciler struct {
	client.Client
	APIReader client.Reader
	Clock     clock.Clock

	Log               logr.Logger
	Scheme            *runtime.Scheme
	Resources         *message.ControllerResources
	ScheduleEvaluator *scheduler.ScheduleEvaluator
	RestoreManager    *restore.Manager
	Planner           *scheduler.Planner
}

// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=hibernateplans,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=hibernateplans/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=hibernateplans/finalizers,verbs=update
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=cloudproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=k8sclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile handles HibernatePlan reconciliation by fetching all related resources
// and storing an enriched PlanContext in the watchable map.
func (r *PlanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	key := req.NamespacedName
	log := r.Log.WithValues("plan", key)

	// Fetch the HibernatePlan
	plan := &hibernatorv1alpha1.HibernatePlan{}
	if err := r.Get(ctx, key, plan); err != nil {
		if apierrors.IsNotFound(err) {
			// Plan deleted -> remove from watchable map (triggers delete event in processors)
			r.Resources.PlanResources.Delete(key)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Enrich the logger with cycle metadata when available.
	if plan.Status.CurrentCycleID != "" && plan.Status.CurrentOperation != "" {
		log = log.WithValues("cycleID", plan.Status.CurrentCycleID, "operation", plan.Status.CurrentOperation)
	}

	// Fetch active ScheduleExceptions for this plan
	var exceptionList hibernatorv1alpha1.ScheduleExceptionList
	if err := r.List(ctx, &exceptionList,
		client.InNamespace(plan.Namespace),
		client.MatchingLabels{wellknown.LabelPlan: plan.Name},
	); err != nil {
		log.Error(err, "failed to list schedule exceptions")
		// Continue without exceptions -- don't fail the whole reconcile
	}

	schedResult, err := r.evaluateSchedule(log, plan, exceptionList.Items)
	if err != nil {
		log.Error(err, "failed to evaluate schedule")
		return ctrl.Result{RequeueAfter: wellknown.RequeueIntervalOnScheduleError}, nil
	}

	// Check restore data availability
	hasRestoreData := false
	if ok, err := r.RestoreManager.HasRestoreData(ctx, plan.Namespace, plan.Name); err != nil {
		log.Error(err, "failed to check restore data")
	} else {
		hasRestoreData = ok
	}

	execProgress := r.computeExecutionProgress(ctx, log, plan)

	// Bundle into PlanContext and store in watchable map
	planCtx := &message.PlanContext{
		Plan:              plan,
		Exceptions:        exceptionList.Items,
		ScheduleResult:    schedResult,
		HasRestoreData:    hasRestoreData,
		ExecutionProgress: execProgress,
	}

	r.Resources.PlanResources.Store(key, planCtx)

	log.V(1).Info("stored plan context",
		"phase", plan.Status.Phase,
		"exceptionsCount", len(exceptionList.Items),
		"hasRestoreData", hasRestoreData,
		"executionProgress", execProgress,
		"nextEvent", schedResult.RequeueAfter.String(),
	)

	return ctrl.Result{RequeueAfter: schedResult.RequeueAfter}, nil
}

// evaluateSchedule checks if we should be in hibernation based on schedule and active exceptions.
func (r *PlanReconciler) evaluateSchedule(log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan, exceptions []hibernatorv1alpha1.ScheduleException) (*message.ScheduleEvaluation, error) {
	if r.ScheduleEvaluator == nil {
		// Fallback: always active if no evaluator
		return &message.ScheduleEvaluation{
			ShouldHibernate: false,
			RequeueAfter:    wellknown.RequeueIntervalOnScheduleError,
		}, nil
	}

	// Convert OffHourWindows to scheduler format
	baseWindows := make([]scheduler.OffHourWindow, len(plan.Spec.Schedule.OffHours))
	for i, w := range plan.Spec.Schedule.OffHours {
		baseWindows[i] = scheduler.OffHourWindow{
			Start:      w.Start,
			End:        w.End,
			DaysOfWeek: w.DaysOfWeek,
		}
	}

	// Query for active exception
	exception, err := r.getActiveException(log, plan, exceptions)
	if err != nil {
		log.Error(err, "failed to get active exception, evaluating base schedule only")

		// Fall through to evaluate base schedule
		exception = nil
	}

	// Evaluate schedule with exception (if any)
	result, err := r.ScheduleEvaluator.Evaluate(baseWindows, plan.Spec.Schedule.Timezone, exception)
	if err != nil {
		return nil, err
	}

	requeueAfter := r.ScheduleEvaluator.NextRequeueTime(result)
	log.Info("schedule evaluation result",
		"nextHibernateTime", result.NextHibernateTime,
		"nextWakeUpTime", result.NextWakeUpTime,
		"shouldHibernate", result.ShouldHibernate,
		"nextRequeue", requeueAfter.String(),
	)

	return &message.ScheduleEvaluation{
		ShouldHibernate: result.ShouldHibernate,
		RequeueAfter:    requeueAfter,
	}, nil
}

// getActiveException finds the newest active ScheduleException from a list.
func (r *PlanReconciler) getActiveException(log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan, exceptions []hibernatorv1alpha1.ScheduleException) (*scheduler.Exception, error) {
	now := r.Clock.Now()

	var activeExceptions []hibernatorv1alpha1.ScheduleException
	for _, exc := range exceptions {
		if exc.Status.State != hibernatorv1alpha1.ExceptionStateActive {
			continue
		}
		if now.Before(exc.Spec.ValidFrom.Time) || now.After(exc.Spec.ValidUntil.Time) {
			continue
		}
		activeExceptions = append(activeExceptions, exc)
	}

	if len(activeExceptions) == 0 {
		return nil, nil
	}

	// If multiple active exceptions, pick the newest one
	if len(activeExceptions) > 1 {
		log.Info("multiple active exceptions found, picking newest",
			"count", len(activeExceptions),
			"plan", plan.Name)

		// Sort by CreationTimestamp descending (newest first)
		// Assuming that newest exception is the most relevant when multiple exceptions are active,
		// as it likely reflects the latest intent of the user.
		sort.Slice(activeExceptions, func(i, j int) bool {
			return activeExceptions[j].CreationTimestamp.Before(&activeExceptions[i].CreationTimestamp)
		})
	}

	exc := activeExceptions[0]
	windows := make([]scheduler.OffHourWindow, len(exc.Spec.Windows))
	for i, w := range exc.Spec.Windows {
		windows[i] = scheduler.OffHourWindow{
			Start:      w.Start,
			End:        w.End,
			DaysOfWeek: w.DaysOfWeek,
		}
	}

	var leadTime time.Duration
	if exc.Spec.LeadTime != "" {
		d, err := time.ParseDuration(exc.Spec.LeadTime)
		if err != nil {
			return nil, fmt.Errorf("invalid lead time format (%s) in exception %s: %w", exc.Spec.LeadTime, exc.Name, err)
		}

		leadTime = d
	}

	return &scheduler.Exception{
		Type:       scheduler.ExceptionType(exc.Spec.Type),
		ValidFrom:  exc.Spec.ValidFrom.Time,
		ValidUntil: exc.Spec.ValidUntil.Time,
		LeadTime:   leadTime,
		Windows:    windows,
	}, nil
}

// computeExecutionProgress counts terminal jobs for the current cycle.
// The result is used purely as a watchable change signal — when a job completes or
// fails the counts change, PlanContext.Equal() returns false, and the worker gets an
// event-driven wake-up instead of waiting for the poll timer.
func (r *PlanReconciler) computeExecutionProgress(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) *message.ExecutionProgress {
	if plan.Status.CurrentCycleID == "" || plan.Status.CurrentOperation == "" {
		return nil
	}

	var jobList batchv1.JobList
	if err := r.List(ctx, &jobList,
		client.InNamespace(plan.Namespace),
		client.MatchingLabels{
			wellknown.LabelPlan:      plan.Name,
			wellknown.LabelCycleID:   plan.Status.CurrentCycleID,
			wellknown.LabelOperation: plan.Status.CurrentOperation,
		},
	); err != nil {
		log.Error(err, "failed to list cycle jobs for execution progress")
		return nil
	}

	ep := message.ExecutionProgress{CycleID: plan.Status.CurrentCycleID}
	for _, job := range jobList.Items {
		if _, stale := job.Labels[wellknown.LabelStaleRunnerJob]; stale {
			continue
		}
		if job.Status.Succeeded > 0 {
			ep.Completed++
		} else if job.Status.Failed > 0 {
			ep.Failed++
		}
	}
	return &ep
}

// findPlansForException returns reconcile requests for HibernatePlans when a ScheduleException changes.
func (r *PlanReconciler) findPlansForException(ctx context.Context, obj client.Object) []reconcile.Request {
	exception, ok := obj.(*hibernatorv1alpha1.ScheduleException)
	if !ok {
		return nil
	}
	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Name:      exception.Spec.PlanRef.Name,
				Namespace: exception.Namespace,
			},
		},
	}
}

// SetupWithManager sets up the provider reconciler with the Manager.
func (r *PlanReconciler) SetupWithManager(mgr ctrl.Manager, workers int) error {
	// configMapDataChangedPredicate fires only when a ConfigMap's Data or BinaryData
	// changes, ignoring annotation/label-only updates.
	//
	// During a wakeup cycle each runner calls MarkTargetRestored(), which patches the
	// restore ConfigMap's annotations (not its Data). Without this predicate every
	// such annotation write would trigger a provider reconcile even though HasRestoreData
	// would return the same answer — producing one spurious reconcile per wakeup stage.
	configMapDataChangedPredicate := predicate.Funcs{
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

	// jobTerminalPredicate triggers provider reconciliation only when an owned Job
	// first reaches a terminal state.  We detect this via the monotonically
	// increasing Succeeded/Failed counters rather than the Active counter, because
	// Active is non-monotonic (0→N→0) and informer coalescing can squash the
	// intermediate updates, turning the sequence into Active 0→0 which would be
	// invisible.  Succeeded and Failed only ever increase, so the 0→1+ transition
	// fires exactly once per Job regardless of coalescing.
	jobTerminalPredicate := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldJob, ok1 := e.ObjectOld.(*batchv1.Job)
			newJob, ok2 := e.ObjectNew.(*batchv1.Job)
			if !ok1 || !ok2 {
				return false
			}
			// Fire on the 0→1+ transition of Succeeded or Failed.
			wasTerminal := oldJob.Status.Succeeded > 0 || oldJob.Status.Failed > 0
			isTerminal := newJob.Status.Succeeded > 0 || newJob.Status.Failed > 0
			return !wasTerminal && isTerminal
		},
		CreateFunc:  func(_ event.CreateEvent) bool { return false },
		DeleteFunc:  func(_ event.DeleteEvent) bool { return false },
		GenericFunc: func(_ event.GenericEvent) bool { return false },
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&hibernatorv1alpha1.HibernatePlan{}).
		Owns(&batchv1.Job{}, builder.WithPredicates(jobTerminalPredicate)).
		Owns(&corev1.ConfigMap{}, builder.WithPredicates(configMapDataChangedPredicate)).
		Watches(
			&hibernatorv1alpha1.ScheduleException{},
			handler.EnqueueRequestsFromMapFunc(r.findPlansForException),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: workers,
		}).
		Named("hibernateplan-provider").
		Complete(r)
}
