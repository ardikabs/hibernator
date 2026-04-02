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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/internal/notification"
	statusprocessor "github.com/ardikabs/hibernator/internal/provider/processor/status"
)

// LifecycleProcessor manages HibernateNotification status updates.
//
// It subscribes to PlanResources and, for each plan update, syncs the
// watchedPlans list in the status of every matched HibernateNotification.
// It also receives delivery callbacks from the notification Dispatcher
// to record per-sink delivery history (newest-first, capped at 20 entries).
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
	Clock clock.Clock
	Log   logr.Logger

	Resources *message.ControllerResources
	Statuses  *statusprocessor.ControllerStatuses
}

// NeedLeaderElection returns true since this processor modifies resource status.
func (p *LifecycleProcessor) NeedLeaderElection() bool {
	return true
}

// Start implements manager.Runnable. It subscribes to PlanResources and processes
// notification status updates from PlanContext.Notifications.
func (p *LifecycleProcessor) Start(ctx context.Context) error {
	log := p.Log.WithName("lifecycle")
	log.Info("starting notification lifecycle processor")

	message.HandleSubscription(ctx, log, message.Metadata{
		Runner:  "notification-lifecycle",
		Message: "plan-resources",
	}, p.Resources.PlanResources.Subscribe(ctx),
		func(update watchable.Update[types.NamespacedName, *message.PlanContext], errChan chan error) {
			if update.Delete {
				return
			}
			p.syncWatchedPlans(ctx, log, update.Key, update.Value, errChan)
		},
	)

	log.Info("notification lifecycle processor stopped")
	return nil
}

// syncWatchedPlans syncs watchedPlans and observedGeneration for each
// HibernateNotification in the PlanContext.
func (p *LifecycleProcessor) syncWatchedPlans(_ context.Context, log logr.Logger, planKey types.NamespacedName, planCtx *message.PlanContext, _ chan error) {
	if planCtx == nil {
		return
	}

	for i := range planCtx.Notifications {
		notif := &planCtx.Notifications[i]
		notifKey := types.NamespacedName{Name: notif.Name, Namespace: notif.Namespace}
		p.upsertPlanRef(log, notifKey, notif, planKey)
	}
}

// upsertPlanRef ensures the plan is present in the notification's watchedPlans list
// and updates observedGeneration.
func (p *LifecycleProcessor) upsertPlanRef(log logr.Logger, notifKey types.NamespacedName, notif *hibernatorv1alpha1.HibernateNotification, planKey types.NamespacedName) {
	planName := planKey.Name

	// Check if already present and generation unchanged — skip redundant writes.
	_, found := lo.Find(notif.Status.WatchedPlans, func(ref hibernatorv1alpha1.PlanReference) bool {
		return ref.Name == planName
	})
	if found && notif.Status.ObservedGeneration == notif.Generation {
		return
	}

	p.Statuses.NotificationStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernateNotification]{
		NamespacedName: notifKey,
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

			n.Status.ObservedGeneration = n.Generation
		}),
	})

	log.V(1).Info("queued watchedPlans update", "notification", notifKey, "plan", planName)
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
