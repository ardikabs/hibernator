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
	"github.com/samber/lo"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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

	// DependencyNonces tracks per-plan monotonic counters incremented whenever a
	// dependent resource — external to the HibernatePlan state itself — undergoes a
	// significant state transition that the plan execution must react to (e.g., a Job
	// reaching a terminal state). The current counter value is embedded into
	// PlanContext.DeliveryNonce on each Reconcile call, ensuring watchable.Map.Store()
	// detects a meaningful change and re-delivers the context to subscribers even when
	// no HibernatePlan field has changed.
	DependencyNonces dependencyNonceMap
}

// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=hibernateplans,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=hibernateplans/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=hibernateplans/finalizers,verbs=update
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=cloudproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=k8sclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=hibernatenotifications,verbs=get;list;watch
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
			// Plan deleted — remove from watchable map and clean up the dependency nonce.
			r.Resources.PlanResources.Delete(key)
			r.DependencyNonces.Delete(key)
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

	// Fetch matching HibernateNotifications via label selector.
	notifications, err := r.fetchAllNotifications(ctx, plan)
	if err != nil {
		log.Error(err, "failed to fetch notifications")
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
		Notifications:  notifications,
		HasRestoreData: hasRestoreData,
		DeliveryNonce:  r.DependencyNonces.Get(key),
	}

	r.Resources.PlanResources.Store(key, planCtx)

	log.V(1).Info("stored plan context",
		"phase", plan.Status.Phase,
		"hasRestoreData", hasRestoreData,
		"totalExceptions", len(allExceptions),
		"totalNotifications", len(notifications),
		"deliveryNonce", planCtx.DeliveryNonce,
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

// fetchAllNotifications retrieves all HibernateNotifications in the plan's namespace
// whose label selector matches the plan's labels.
func (r *PlanReconciler) fetchAllNotifications(ctx context.Context, plan *hibernatorv1alpha1.HibernatePlan) ([]hibernatorv1alpha1.HibernateNotification, error) {
	var notifList hibernatorv1alpha1.HibernateNotificationList
	if err := r.List(ctx, &notifList, client.InNamespace(plan.Namespace)); err != nil {
		return nil, err
	}

	planLabels := labels.Set(plan.Labels)
	var matched []hibernatorv1alpha1.HibernateNotification
	for _, notif := range notifList.Items {
		selector, err := metav1.LabelSelectorAsSelector(&notif.Spec.Selector)
		if err != nil {
			// Skip notifications with invalid selectors — validation webhook
			// should prevent this, but be defensive.
			continue
		}
		if selector.Matches(planLabels) {
			matched = append(matched, notif)
		}
	}

	return matched, nil
}

// evaluateSchedule checks if we should be in hibernation based on schedule and active exceptions.
// It derives the active exceptions from the provided full list to avoid a second List call.
func (r *PlanReconciler) evaluateSchedule(_ context.Context, plan *hibernatorv1alpha1.HibernatePlan, allExceptions []hibernatorv1alpha1.ScheduleException, log logr.Logger) (*message.ScheduleEvaluation, error) {
	if r.ScheduleEvaluator == nil {
		return nil, fmt.Errorf("no schedule evaluator configured")
	}

	// Derive active exceptions from the full list.
	activeExceptions := r.filterActiveExceptions(allExceptions)

	// Convert each active exception to the scheduler type.
	// Same-type merging is handled internally by Evaluate.
	exceptions := lo.Map(activeExceptions, func(exc hibernatorv1alpha1.ScheduleException, _ int) *scheduler.Exception {
		return convertException(exc)
	})

	if len(exceptions) > 0 {
		log.Info("active exceptions for evaluation",
			"count", len(exceptions),
			"names", lo.Map(activeExceptions, func(exc hibernatorv1alpha1.ScheduleException, _ int) string { return exc.Name }),
		)
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

	// Evaluate schedule with exceptions (if any)
	result, err := r.ScheduleEvaluator.Evaluate(baseWindows, plan.Spec.Schedule.Timezone, exceptions)
	if err != nil {
		return nil, err
	}

	// Compute the next schedule event as an absolute timestamp.
	// This is the moment the system should transition (hibernate or wake-up),
	// including the schedule buffer and a safety buffer to ensure the controller
	// observes the transition after the cron boundary has passed.
	nextEvent := r.computeNextEvent(result)

	log.Info("schedule evaluation result",
		"shouldHibernate", result.ShouldHibernate,
		"totalActiveExceptions", len(activeExceptions),
		"nextHibernateTime", result.NextHibernateTime.Format(time.RFC3339),
		"nextWakeUpTime", result.NextWakeUpTime.Format(time.RFC3339),
		"nextEvent", nextEvent.Format(time.RFC3339),
	)

	return &message.ScheduleEvaluation{
		Exceptions:      activeExceptions,
		ShouldHibernate: result.ShouldHibernate,
		NextEvent:       nextEvent,
	}, nil
}

// computeNextEvent derives the next schedule-driven event as an absolute timestamp
// from the evaluation result. It mirrors the selection logic of
// ScheduleEvaluator.NextRequeueTime but returns a stable time.Time instead of a
// drifting duration. The schedule buffer and safety buffer are added so that the
// requeue processor fires slightly after the cron boundary, giving the system time
// to observe the transition.
func (r *PlanReconciler) computeNextEvent(result *scheduler.EvaluationResult) time.Time {
	const safetyBuffer = 10 * time.Second

	if result.InGracePeriod {
		// Grace period end is already an absolute time; add only safety buffer.
		return result.GracePeriodEnd.Add(safetyBuffer)
	}

	var nextEvent time.Time
	if result.ShouldHibernate {
		// Currently hibernated → next event is wake-up.
		nextEvent = result.NextWakeUpTime
	} else {
		// Currently active → next event is hibernate.
		nextEvent = result.NextHibernateTime
	}

	// Add schedule buffer (configurable, typically 1m) + safety buffer so the
	// requeue fires after the cron boundary has passed.
	return nextEvent.Add(r.ScheduleEvaluator.GetScheduleBuffer() + safetyBuffer)
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

// convertException converts a single ScheduleException API resource into a
// scheduler.Exception.
func convertException(exc hibernatorv1alpha1.ScheduleException) *scheduler.Exception {
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
		if err == nil {
			leadTime = d
		}
	}

	return &scheduler.Exception{
		Type:       scheduler.ExceptionType(exc.Spec.Type),
		ValidFrom:  exc.Spec.ValidFrom.Time,
		ValidUntil: exc.Spec.ValidUntil.Time,
		LeadTime:   leadTime,
		Windows:    windows,
	}
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

// findPlansForNotification returns reconcile requests for all HibernatePlans in the same namespace
// whose labels match the notification's selector.
func (r *PlanReconciler) findPlansForNotification(ctx context.Context, obj client.Object) []reconcile.Request {
	notif, ok := obj.(*hibernatorv1alpha1.HibernateNotification)
	if !ok {
		return nil
	}

	selector, err := metav1.LabelSelectorAsSelector(&notif.Spec.Selector)
	if err != nil {
		return nil
	}

	var planList hibernatorv1alpha1.HibernatePlanList
	if err := r.List(ctx, &planList,
		client.InNamespace(notif.Namespace),
		client.MatchingLabelsSelector{Selector: selector},
	); err != nil {
		r.Log.Error(err, "failed to list plans for notification", "notification", client.ObjectKeyFromObject(notif))
		return nil
	}

	requests := make([]reconcile.Request, len(planList.Items))
	for i, plan := range planList.Items {
		requests[i] = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      plan.Name,
				Namespace: plan.Namespace,
			},
		}
	}
	return requests
}

// onJobTerminalUpdate is the predicate UpdateFunc for owned Jobs. It detects the
// first 0→1+ transition of Job.Status.Succeeded or Job.Status.Failed, signalling
// that the Job has reached a terminal state. On detection, it increments
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
	// Detect the first 0→1+ transition of Succeeded or Failed.
	wasTerminal := oldJob.Status.Succeeded > 0 || oldJob.Status.Failed > 0
	isTerminal := newJob.Status.Succeeded > 0 || newJob.Status.Failed > 0
	condition := !wasTerminal && isTerminal
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
		UpdateFunc:  r.onJobTerminalUpdate,
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
		Watches(
			&hibernatorv1alpha1.HibernateNotification{},
			handler.EnqueueRequestsFromMapFunc(r.findPlansForNotification),
			// Only Spec changes matter — selector or sink changes affect which plans
			// receive notifications. Status writes are excluded.
			builder.WithPredicates(predicate.GenerationChangedPredicate{}),
		).
		WatchesRawSource(source.Channel(r.EnqueueCh, &handler.EnqueueRequestForObject{})).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: workers,
		}).
		Named("hibernateplan-provider").
		Complete(r)
}
