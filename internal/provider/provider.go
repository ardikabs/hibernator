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
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"github.com/samber/lo"
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
	"sigs.k8s.io/controller-runtime/pkg/source"

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
//
// The reconciler is a pure data collector — it never requeues. Time-based
// re-enqueuing is handled by the PlanRequeueProcessor via the EnqueueCh channel.
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

	// EnqueueCh receives GenericEvents from internal processors (e.g., PlanRequeueProcessor)
	// to trigger a fresh reconcile without relying on RequeueAfter.
	EnqueueCh <-chan event.GenericEvent

	deliveryNonce atomic.Int64
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
	plan := new(hibernatorv1alpha1.HibernatePlan)
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

	// Fetch ALL exceptions for this plan (all states) using field index.
	allExceptions, err := r.fetchAllExceptions(ctx, plan)
	if err != nil {
		log.Error(err, "failed to fetch all exceptions")
	}

	schedule, err := r.evaluateSchedule(ctx, plan, allExceptions, log)
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

	// Bundle into PlanContext and store in watchable map.
	// The reconciler is a pure data collector — it does not requeue.
	// Time-based re-enqueuing is handled by the PlanRequeueProcessor.
	planCtx := &message.PlanContext{
		Plan:           plan,
		Schedule:       schedule,
		Exceptions:     allExceptions,
		HasRestoreData: hasRestoreData,
		DeliveryNonce:  r.deliveryNonce.Add(1),
	}

	r.Resources.PlanResources.Store(key, planCtx)

	log.V(1).Info("stored plan context",
		"phase", plan.Status.Phase,
		"hasRestoreData", hasRestoreData,
		"totalExceptions", len(allExceptions),
	)

	return ctrl.Result{}, nil
}

// fetchAllExceptions retrieves ALL ScheduleExceptions for a given plan (any state)
// using the field index on spec.planRef.name.
func (r *PlanReconciler) fetchAllExceptions(ctx context.Context, plan *hibernatorv1alpha1.HibernatePlan) ([]hibernatorv1alpha1.ScheduleException, error) {
	var exceptionList hibernatorv1alpha1.ScheduleExceptionList
	if err := r.List(ctx, &exceptionList,
		client.InNamespace(plan.Namespace),
		client.MatchingFields{wellknown.FieldIndexExceptionPlanRef: plan.Name},
	); err != nil {
		return nil, err
	}

	return exceptionList.Items, nil
}

// evaluateSchedule checks if we should be in hibernation based on schedule and active exceptions.
// It derives the active exceptions from the provided full list to avoid a second List call.
func (r *PlanReconciler) evaluateSchedule(_ context.Context, plan *hibernatorv1alpha1.HibernatePlan, allExceptions []hibernatorv1alpha1.ScheduleException, log logr.Logger) (*message.ScheduleEvaluation, error) {
	if r.ScheduleEvaluator == nil {
		return nil, fmt.Errorf("no schedule evaluator configured")
	}

	// Derive active exceptions from the full list.
	activeExceptions := r.filterActiveExceptions(allExceptions)

	// Select the newest active exception
	exception, err := r.selectActiveException(log, plan, activeExceptions)
	if err != nil {
		log.Error(err, "failed to get active exception, evaluating base schedule only")

		// Fall through to evaluate base schedule
		exception = nil
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

	// Evaluate schedule with exception (if any)
	result, err := r.ScheduleEvaluator.Evaluate(baseWindows, plan.Spec.Schedule.Timezone, exception)
	if err != nil {
		return nil, err
	}

	requeueAfter := r.ScheduleEvaluator.NextRequeueTime(result)
	log.Info("schedule evaluation result",
		"shouldHibernate", result.ShouldHibernate,
		"totalActiveExceptions", len(activeExceptions),
		"nextHibernateTime", result.NextHibernateTime.Format(time.RFC3339),
		"nextWakeUpTime", result.NextWakeUpTime.Format(time.RFC3339),
		"nextRequeue", requeueAfter.String(),
	)

	return &message.ScheduleEvaluation{
		Exceptions:      activeExceptions,
		ShouldHibernate: result.ShouldHibernate,
		RequeueAfter:    requeueAfter,
	}, nil
}

// filterActiveExceptions filters and sorts active exceptions from a full list.
// Returns active exceptions sorted by CreationTimestamp descending (newest first).
func (r *PlanReconciler) filterActiveExceptions(allExceptions []hibernatorv1alpha1.ScheduleException) []hibernatorv1alpha1.ScheduleException {
	now := r.Clock.Now()

	activeExceptions := lo.Filter(allExceptions, func(exc hibernatorv1alpha1.ScheduleException, _ int) bool {
		return now.After(exc.Spec.ValidFrom.Time) && now.Before(exc.Spec.ValidUntil.Time)
	})

	// Sort by CreationTimestamp descending (newest first)
	// Assuming that newest exception is the most relevant when multiple exceptions are active,
	// as it likely reflects the latest intent of the user.
	sort.Slice(activeExceptions, func(i, j int) bool {
		return activeExceptions[j].CreationTimestamp.Before(&activeExceptions[i].CreationTimestamp)
	})

	return activeExceptions
}

// selectActiveException selects the newest active ScheduleException from a list.
// TODO(ardikabs): on handling multiple active exceptions
// - P1: validationwebhook on determining multiple exceptions within the same window, or prioritization logic based on exception type or other criteria.
func (r *PlanReconciler) selectActiveException(log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan, activeExceptions []hibernatorv1alpha1.ScheduleException) (*scheduler.Exception, error) {
	// With multiple active exceptions, pick the newest one
	exc, ok := lo.First(activeExceptions)
	if !ok {
		log.V(1).Info("no active exceptions found")
		return nil, nil
	}

	log.Info("active exception found, evaluating against schedule",
		"count", len(activeExceptions),
		"exception", exc.Name,
		"plan", plan.Name)

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
		// React to Spec changes (generation bump) and annotation changes (retry-now,
		// suspend-until, override-action, restart, etc.). Status writes are excluded —
		// they neither bump Generation nor change Annotations, preventing the
		// status-write → reconcile → re-store loop.
		For(&hibernatorv1alpha1.HibernatePlan{}, builder.WithPredicates(
			predicate.Or(
				predicate.GenerationChangedPredicate{},
				predicate.AnnotationChangedPredicate{},
			),
		)).
		Owns(&batchv1.Job{}, builder.WithPredicates(jobTerminalPredicate)).
		Owns(&corev1.ConfigMap{}, builder.WithPredicates(configMapDataChangedPredicate)).
		Watches(
			&hibernatorv1alpha1.ScheduleException{},
			handler.EnqueueRequestsFromMapFunc(r.findPlansForException),
			// Only Spec changes matter — no state handler reads exc.Status.State;
			// schedule evaluation uses ValidFrom/ValidUntil directly. Suppressing
			// exception status writes eliminates the ExceptionLifecycleProcessor
			// status-write → reconcile loop.
			builder.WithPredicates(predicate.GenerationChangedPredicate{}),
		).
		WatchesRawSource(source.Channel(r.EnqueueCh, &handler.EnqueueRequestForObject{})).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: workers,
		}).
		Named("hibernateplan-provider").
		Complete(r)
}
