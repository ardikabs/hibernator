/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

import (
	"context"
	"fmt"
	"sort"

	"github.com/go-logr/logr"
	"github.com/samber/lo"
	"github.com/telepresenceio/watchable"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/internal/notification"
	statusprocessor "github.com/ardikabs/hibernator/internal/provider/processor/status"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// LifecycleProcessor manages HibernateNotification status updates.
//
// It subscribes to NotificationResources and, for each binding update, syncs the
// watchedPlans list and State field in the status of the corresponding
// HibernateNotification. It also receives delivery callbacks from the notification
// Dispatcher to record per-sink delivery history (newest-first, capped at 20 entries).
//
// # State machine
//
// The notification progresses through two states:
//   - Bound:    at least one HibernatePlan matches the selector. A finalizer is present
//     to ensure graceful cleanup on deletion.
//   - Detached: no HibernatePlan matches. The finalizer is removed so the notification
//     can be freely deleted without depending on plan reconciles.
//
// # Concurrency model
//
// A single HibernateNotification can match many HibernatePlans. When those plans
// trigger notifications, the Dispatcher's worker pool (default 4 goroutines) may
// call HandleDeliveryResult concurrently for the same notification key.
//
// History ordering is guaranteed by three layers:
//  1. keyedworker.Pool per-key FIFO serialization — at most one apply() runs per
//     notification key at any time, turning concurrent Sends into a serial queue.
//  2. Fresh apiReader.Get inside RetryOnConflict — each apply reads the true server
//     state before mutating, so prepend always targets the latest slice.
//  3. Independent Resource per Send — HandleDeliveryResult creates a fresh empty
//     HibernateNotification each call, so defaultUpdater.Send's pre-application
//     never mutates shared state across concurrent callers.
type LifecycleProcessor struct {
	client.Client
	Clock clock.Clock
	Log   logr.Logger

	Resources *message.ControllerResources
	Statuses  *statusprocessor.ControllerStatuses
}

// NeedLeaderElection returns true since this processor modifies resource status.
func (p *LifecycleProcessor) NeedLeaderElection() bool {
	return true
}

// Start implements manager.Runnable. It subscribes to NotificationResources and
// reacts to individual (notification, plan) binding changes. Each binding carries
// a Matches flag: true means the plan should be in watchedPlans, false means it
// should be removed. Delete events (from plan deletion or notification disappearance)
// also trigger removal.
func (p *LifecycleProcessor) Start(ctx context.Context) error {
	log := p.Log.WithName("lifecycle")
	log.Info("starting notification lifecycle processor")

	message.HandleSubscription(ctx, log, message.Metadata{
		Runner:  "notification-lifecycle",
		Message: "notification-resources",
	}, p.Resources.NotificationResources.Subscribe(ctx),
		func(update watchable.Update[message.NotificationBindingKey, *message.NotificationContext], errChan chan error) {
			if update.Delete {
				// Binding deleted — plan was deleted or notification disappeared from namespace.
				// Remove the plan from the notification's watchedPlans if the last known binding
				// indicated a match.
				if update.Value != nil {
					p.removePlanRef(ctx, log, update.Key, update.Value.Notification)
				}
				return
			}

			p.handleBinding(ctx, log, update)
		},
	)

	log.Info("notification lifecycle processor stopped")
	return nil
}

// handleBinding processes a single NotificationResources binding update.
func (p *LifecycleProcessor) handleBinding(ctx context.Context, log logr.Logger, update watchable.Update[message.NotificationBindingKey, *message.NotificationContext]) {
	if update.Value == nil || update.Value.Notification == nil {
		return
	}

	nc := update.Value
	notifKey := update.Key.GetNotificationKey()

	// Handle notification being deleted (DeletionTimestamp set, blocked by finalizer).
	if !nc.Notification.DeletionTimestamp.IsZero() {
		p.handleNotificationDeletion(ctx, log, notifKey)
		return
	}

	if nc.Matches {
		p.ensureFinalizer(ctx, log, notifKey)
		p.upsertPlanRef(log, update.Key, nc.Notification)
	} else {
		p.removePlanRef(ctx, log, update.Key, nc.Notification)
	}
}

// ensureFinalizer adds the notification finalizer if not already present.
// It re-fetches the notification from the API server to avoid 409 Conflict on stale ResourceVersion.
func (p *LifecycleProcessor) ensureFinalizer(ctx context.Context, log logr.Logger, notifKey types.NamespacedName) {
	notif := new(hibernatorv1alpha1.HibernateNotification)
	if err := p.Get(ctx, notifKey, notif); err != nil {
		log.Error(err, "failed to fetch notification for finalizer", "notification", notifKey)
		return
	}

	if controllerutil.ContainsFinalizer(notif, wellknown.NotificationFinalizerName) {
		return
	}

	orig := notif.DeepCopy()
	controllerutil.AddFinalizer(notif, wellknown.NotificationFinalizerName)
	if err := p.Patch(ctx, notif, client.MergeFrom(orig)); err != nil {
		log.Error(err, "failed to add notification finalizer", "notification", notifKey)
		return
	}
	log.V(1).Info("added notification finalizer", "notification", notifKey)
}

// removeFinalizer removes the notification finalizer if present.
// It re-fetches the notification from the API server to avoid 409 Conflict on stale ResourceVersion.
func (p *LifecycleProcessor) removeFinalizer(ctx context.Context, log logr.Logger, notifKey types.NamespacedName) {
	notif := new(hibernatorv1alpha1.HibernateNotification)
	if err := p.Get(ctx, notifKey, notif); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.V(1).Info("notification already gone, nothing to clean up", "notification", notifKey)
			return
		}
		log.Error(err, "failed to fetch notification for finalizer removal", "notification", notifKey)
		return
	}

	if !controllerutil.ContainsFinalizer(notif, wellknown.NotificationFinalizerName) {
		return
	}

	orig := notif.DeepCopy()
	controllerutil.RemoveFinalizer(notif, wellknown.NotificationFinalizerName)
	if err := p.Patch(ctx, notif, client.MergeFrom(orig)); err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "failed to remove notification finalizer", "notification", notifKey)
		return
	}
	log.V(1).Info("removed notification finalizer", "notification", notifKey)
}

// handleNotificationDeletion cleans up watchedPlans and removes the finalizer
// when a HibernateNotification is being deleted (DeletionTimestamp set).
// It clears all watchedPlans entries, transitions to Detached, and removes
// the finalizer to allow the notification to be garbage-collected.
func (p *LifecycleProcessor) handleNotificationDeletion(ctx context.Context, log logr.Logger, notifKey types.NamespacedName) {
	// Re-fetch the notification from the API server to get the latest state.
	notif := new(hibernatorv1alpha1.HibernateNotification)
	if err := p.Get(ctx, notifKey, notif); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.V(1).Info("notification already gone, nothing to clean up", "notification", notifKey)
			return
		}
		log.Error(err, "failed to re-fetch notification for deletion", "notification", notifKey)
		return
	}

	// Clear all watchedPlans and transition to Detached via status update.
	if len(notif.Status.WatchedPlans) > 0 || notif.Status.State != hibernatorv1alpha1.NotificationStateDetached {
		p.Statuses.NotificationStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernateNotification]{
			NamespacedName: notifKey,
			Resource:       notif.DeepCopy(),
			Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernateNotification](func(n *hibernatorv1alpha1.HibernateNotification) {
				n.Status.WatchedPlans = nil
				n.Status.State = hibernatorv1alpha1.NotificationStateDetached
			}),
		})
		log.V(1).Info("queued watchedPlans clear for deleted notification", "notification", notifKey)
	}

	// Remove finalizer to allow deletion to proceed.
	p.removeFinalizer(ctx, log, notifKey)

	log.Info("notification deletion handled", "notification", notifKey)
}

// upsertPlanRef ensures the plan is present in the notification's watchedPlans list,
// updates observedGeneration, and transitions the state to Bound.
func (p *LifecycleProcessor) upsertPlanRef(log logr.Logger, bindingKey message.NotificationBindingKey, notif *hibernatorv1alpha1.HibernateNotification) {
	planName := bindingKey.PlanName

	// Check if already present and generation unchanged — skip redundant writes.
	_, found := lo.Find(notif.Status.WatchedPlans, func(ref hibernatorv1alpha1.PlanReference) bool {
		return ref.Name == planName
	})
	if found && notif.Status.ObservedGeneration == notif.Generation && notif.Status.State == hibernatorv1alpha1.NotificationStateBound {
		log.V(1).Info("skipping upsertPlanRef: plan already tracked and generation unchanged", "notification", bindingKey.GetNotificationKey(), "plan", bindingKey.GetNotificationKey())
		return
	}

	p.Statuses.NotificationStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernateNotification]{
		NamespacedName: bindingKey.GetNotificationKey(),
		Resource:       notif,
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernateNotification](func(n *hibernatorv1alpha1.HibernateNotification) {
			_, exists := lo.Find(n.Status.WatchedPlans, func(ref hibernatorv1alpha1.PlanReference) bool {
				return ref.Name == planName
			})
			if !exists {
				n.Status.WatchedPlans = append(n.Status.WatchedPlans, hibernatorv1alpha1.PlanReference{Name: planName})
				sort.Slice(n.Status.WatchedPlans, func(i, j int) bool {
					return n.Status.WatchedPlans[i].Name < n.Status.WatchedPlans[j].Name
				})
			}

			n.Status.State = hibernatorv1alpha1.NotificationStateBound
			n.Status.ObservedGeneration = n.Generation
		}),
	})

	log.V(1).Info("queued watchedPlans upsert", "notification", bindingKey.GetNotificationKey(), "plan", bindingKey.GetPlanKey())
}

// removePlanRef removes a plan from a notification's watchedPlans list.
// When the last plan is removed, it transitions the notification to Detached state
// and removes the finalizer so the notification can be freely deleted.
func (p *LifecycleProcessor) removePlanRef(ctx context.Context, log logr.Logger, bindingKey message.NotificationBindingKey, notif *hibernatorv1alpha1.HibernateNotification) {
	planName := bindingKey.PlanName

	// Check if the plan is even tracked — skip if not present.
	_, found := lo.Find(notif.Status.WatchedPlans, func(ref hibernatorv1alpha1.PlanReference) bool {
		return ref.Name == planName
	})
	if !found {
		return
	}

	// Optimistically compute the remaining plans from the snapshot.
	// The actual mutation uses the fresh server state inside the mutator.
	remaining := lo.Filter(notif.Status.WatchedPlans, func(ref hibernatorv1alpha1.PlanReference, _ int) bool {
		return ref.Name != planName
	})
	becomesDetached := len(remaining) == 0

	p.Statuses.NotificationStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernateNotification]{
		NamespacedName: bindingKey.GetNotificationKey(),
		Resource:       notif,
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernateNotification](func(n *hibernatorv1alpha1.HibernateNotification) {
			n.Status.WatchedPlans = lo.Filter(n.Status.WatchedPlans, func(ref hibernatorv1alpha1.PlanReference, _ int) bool {
				return ref.Name != planName
			})
			if len(n.Status.WatchedPlans) > 0 {
				return
			}

			n.Status.State = hibernatorv1alpha1.NotificationStateDetached
		}),
	})

	log.V(1).Info("queued watchedPlans removal", "notification", bindingKey.GetNotificationKey(), "plan", bindingKey.GetPlanKey())

	// If the snapshot indicates no plans remain, optimistically remove the finalizer.
	// If another plan matches between now and the patch, ensureFinalizer will re-add it
	// on the next matched binding event.
	if becomesDetached {
		p.removeFinalizer(ctx, log, bindingKey.GetNotificationKey())
	}
}

// HandleDeliveryResult processes a delivery callback from the notification
// Dispatcher and records it as a history entry in SinkStatuses.
//
// Safe to call from any goroutine — each call creates an independent Update
// with its own empty Resource, so concurrent calls never share mutable state.
// The underlying keyedworker.Pool serializes apply() per notification key,
// ensuring history entries are prepended in strict FIFO order to a
// freshly-fetched server copy.
func (p *LifecycleProcessor) HandleDeliveryResult(result notification.DeliveryResult) {
	if result.NotificationRef.Name == "" {
		return
	}

	p.Statuses.NotificationStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernateNotification]{
		NamespacedName: result.NotificationRef,
		Resource:       &hibernatorv1alpha1.HibernateNotification{},
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernateNotification](func(n *hibernatorv1alpha1.HibernateNotification) {
			ts := metav1.NewTime(result.Timestamp)

			// Build the history entry.
			entry := hibernatorv1alpha1.NotificationSinkStatus{
				Name:                result.SinkName,
				Success:             result.Success,
				TransitionTimestamp: ts,
				Metadata:            result.Metadata,
			}
			if result.Success {
				// TODO: add relevant success message from the sink provider.
				entry.Message = fmt.Sprintf("Successfully sent notification for %s", result.SinkName)
				n.Status.LastDeliveryTime = &ts
			} else {
				entry.Message = fmt.Sprintf("Failed to send notification for %s", result.SinkName)
				if result.Error != nil {
					entry.Message = result.Error.Error()
				}
				n.Status.LastFailureTime = &ts
			}

			// Prepend (newest-first) and cap at maxSinkStatusHistory.
			n.Status.SinkStatuses = lo.Slice(
				append([]hibernatorv1alpha1.NotificationSinkStatus{entry}, n.Status.SinkStatuses...),
				0, hibernatorv1alpha1.MaxSinkStatusHistory,
			)
		}),
	})
}
