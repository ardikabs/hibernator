/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package message

import (
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/telepresenceio/watchable"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

// NotificationBindingKey is a composite key encoding a (notification, plan) pair.
// Format: "{namespace}/{notification-name}/{plan-name}".
// Each plan reconcile owns its own set of binding keys, preserving single-writer
// discipline on the watchable.Map.
type NotificationBindingKey struct {
	Namespace        string
	NotificationName string
	PlanName         string
}

// NewNotificationBindingKey constructs a NotificationBindingKey from component parts.
func NewNotificationBindingKey(notif, plan client.ObjectKey) (NotificationBindingKey, error) {
	if notif.Namespace != plan.Namespace {
		return NotificationBindingKey{}, fmt.Errorf("notification and plan must be in the same namespace: %s != %s", notif.Namespace, plan.Namespace)
	}

	return NotificationBindingKey{
		Namespace:        notif.Namespace,
		NotificationName: notif.Name,
		PlanName:         plan.Name,
	}, nil
}

// String returns the general purpose string representation
func (k NotificationBindingKey) String() string {
	return k.Namespace + "/" + k.NotificationName + "/" + k.PlanName
}

// GetNotificationKey returns a namespaced name for the HibernateNotification
func (k NotificationBindingKey) GetNotificationKey() types.NamespacedName {
	return types.NamespacedName{
		Namespace: k.Namespace,
		Name:      k.NotificationName,
	}
}

// GetNotificationKey returns a namespaced name for the HibernatePlan
func (k NotificationBindingKey) GetPlanKey() types.NamespacedName {
	return types.NamespacedName{
		Namespace: k.Namespace,
		Name:      k.PlanName,
	}
}

// ControllerResources contains the watchable maps for provider-to-processor communication.
// Providers write to these maps; processors subscribe and react to changes.
type ControllerResources struct {
	// PlanResources holds enriched PlanContext for each HibernatePlan.
	PlanResources watchable.Map[types.NamespacedName, *PlanContext]

	// NotificationResources holds per-(notification, plan) bindings written by the
	// provider during each plan reconcile. The lifecycle processor subscribes to
	// these bindings to maintain watchedPlans status.
	NotificationResources watchable.Map[NotificationBindingKey, *NotificationContext]
}

// PlanContext contains all data needed by processors to make decisions for a single HibernatePlan.
// It is the value stored in PlanResources and represents the provider's enriched view of the plan.
type PlanContext struct {
	// Plan is the HibernatePlan resource fetched from K8s.
	Plan *hibernatorv1alpha1.HibernatePlan

	// Schedule contains the pre-computed schedule evaluation.
	// Nil if schedule evaluation failed or is not applicable.
	Schedule *ScheduleEvaluation

	// Exceptions is the full list of ScheduleExceptions associated with this plan
	// (all states: Pending, Active, Expired). Used by the ExceptionLifecycleProcessor
	// for state transitions and by the PlanRequeueProcessor for time-boundary calculations.
	// This is distinct from Schedule.Exceptions which only contains active exceptions
	// for schedule evaluation.
	Exceptions []hibernatorv1alpha1.ScheduleException

	// Notifications is the list of HibernateNotifications whose selector matches this plan.
	// Used by the NotificationDispatcher to send notifications on lifecycle events.
	Notifications []hibernatorv1alpha1.HibernateNotification

	// HasRestoreData indicates whether restore data exists for this plan.
	HasRestoreData bool

	// DeliveryNonce is a monotonically increasing counter that increments whenever
	// a dependent resource (external to the plan state itself) changes in a way that
	// affects plan execution. Examples include Job terminal state transitions (success/failure),
	// ConfigMap data updates, or ScheduleException lifecycle changes.
	//
	// This ensures that watchable.Map.Store() detects such external state changes and
	// re-delivers the PlanContext to subscribers, even when no HibernatePlan field
	// has changed. This mechanism treats dependent-resource state changes as plan-related
	// changes for the purpose of processor notifications, preventing watchable suppression
	// when only external dependencies have changed.
	//
	// Workers and state handlers never read this field; it exists solely as a signal
	// to watchable that a re-delivery should occur.
	DeliveryNonce int64
}

// DeepCopy creates a deep copy of PlanContext.
func (pc *PlanContext) DeepCopy() *PlanContext {
	if pc == nil {
		return nil
	}
	result := &PlanContext{
		HasRestoreData: pc.HasRestoreData,
		DeliveryNonce:  pc.DeliveryNonce,
	}
	if pc.Plan != nil {
		result.Plan = pc.Plan.DeepCopy()
	}
	if len(pc.Exceptions) > 0 {
		result.Exceptions = make([]hibernatorv1alpha1.ScheduleException, len(pc.Exceptions))
		for i, exc := range pc.Exceptions {
			result.Exceptions[i] = *exc.DeepCopy()
		}
	}
	if len(pc.Notifications) > 0 {
		result.Notifications = make([]hibernatorv1alpha1.HibernateNotification, len(pc.Notifications))
		for i, notif := range pc.Notifications {
			result.Notifications[i] = *notif.DeepCopy()
		}
	}
	if pc.Schedule != nil {
		schedExceptions := make([]hibernatorv1alpha1.ScheduleException, len(pc.Schedule.Exceptions))
		for i, exc := range pc.Schedule.Exceptions {
			schedExceptions[i] = *exc.DeepCopy()
		}

		result.Schedule = &ScheduleEvaluation{
			ShouldHibernate: pc.Schedule.ShouldHibernate,
			NextEvent:       pc.Schedule.NextEvent,
			Exceptions:      schedExceptions,
		}
	}
	return result
}

// Equal checks if two PlanContext objects are equal.
// Uses reflect.DeepEqual for comparison as a pragmatic choice.
func (pc *PlanContext) Equal(other *PlanContext) bool {
	if pc == other {
		return true
	}

	if pc == nil || other == nil {
		return false
	}

	// Compare fields using basic equality and deep comparison where needed
	if pc.HasRestoreData != other.HasRestoreData {
		return false
	}

	if pc.DeliveryNonce != other.DeliveryNonce {
		return false
	}

	if (pc.Plan == nil) != (other.Plan == nil) {
		return false
	}

	if pc.Plan != nil && !planEqual(pc.Plan, other.Plan) {
		return false
	}

	if len(pc.Exceptions) != len(other.Exceptions) {
		return false
	}

	for i := range pc.Exceptions {
		if !exceptionsEqual(&pc.Exceptions[i], &other.Exceptions[i]) {
			return false
		}
	}

	if len(pc.Notifications) != len(other.Notifications) {
		return false
	}

	for i := range pc.Notifications {
		if !notificationsEqual(&pc.Notifications[i], &other.Notifications[i]) {
			return false
		}
	}

	if (pc.Schedule == nil) != (other.Schedule == nil) {
		return false
	}

	if pc.Schedule != nil {
		if len(pc.Schedule.Exceptions) != len(other.Schedule.Exceptions) {
			return false
		}

		for i := range pc.Schedule.Exceptions {
			if !exceptionsEqual(&pc.Schedule.Exceptions[i], &other.Schedule.Exceptions[i]) {
				return false
			}
		}

		if pc.Schedule.ShouldHibernate != other.Schedule.ShouldHibernate {
			return false
		}

		// NextEvent is an absolute timestamp that only changes when the underlying
		// schedule or exception windows change. Safe to include in equality.
		if !pc.Schedule.NextEvent.Equal(other.Schedule.NextEvent) {
			return false
		}
	}
	return true
}

// Helper functions for equality comparisons
func planEqual(a, b *hibernatorv1alpha1.HibernatePlan) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	// Include ResourceVersion so that any K8s update (spec, annotations, or true status writes)
	// is detected by watchable and causes re-delivery to subscribers.
	//
	// Also include Status.Phase so that in-memory optimistic phase updates (which do NOT bump
	// ResourceVersion) are still detected. Without this, optimistic phase changes would be
	// suppressed by watchable, breaking cross-processor phase routing.
	return a.Name == b.Name && a.Namespace == b.Namespace && a.UID == b.UID &&
		a.ResourceVersion == b.ResourceVersion &&
		a.Status.Phase == b.Status.Phase
}

func exceptionsEqual(a, b *hibernatorv1alpha1.ScheduleException) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Name == b.Name && a.Namespace == b.Namespace && a.UID == b.UID && a.ResourceVersion == b.ResourceVersion
}

func notificationsEqual(a, b *hibernatorv1alpha1.HibernateNotification) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Name == b.Name && a.Namespace == b.Namespace && a.UID == b.UID && a.ResourceVersion == b.ResourceVersion
}

// ScheduleEvaluation contains the result of schedule evaluation by the provider.
type ScheduleEvaluation struct {
	// Exceptions is the list of active ScheduleExceptions associated with this plan.
	Exceptions []hibernatorv1alpha1.ScheduleException

	// ShouldHibernate indicates if the plan should currently be hibernating.
	ShouldHibernate bool

	// NextEvent is the absolute timestamp of the next schedule-driven event
	// (hibernate or wake-up transition), including schedule buffer and safety buffer.
	// Unlike a relative duration, this value is stable across reconcile cycles —
	// it only changes when the underlying schedule or exception windows change.
	// Consumers compute time-until-event locally: time.Until(NextEvent).
	NextEvent time.Time
}

// NotificationContext represents a single (notification, plan) binding stored in
// NotificationResources. Each plan reconcile writes one binding per notification
// it discovers in the namespace. The Matches field indicates whether the plan's
// current labels satisfy the notification's selector.
//
// Equality is based on the notification's selector, the plan's labels, and
// the Matches flag — so a label or selector change produces a new watchable
// event even when the notification's ResourceVersion hasn't changed.
type NotificationContext struct {
	// Notification is the full HibernateNotification resource.
	Notification *hibernatorv1alpha1.HibernateNotification

	// PlanKey identifies the HibernatePlan this binding relates to.
	PlanKey types.NamespacedName

	// PlanLabels is the plan's current label set at the time of binding.
	PlanLabels map[string]string

	// Matches is true when the plan's labels satisfy the notification's selector.
	Matches bool
}

// Equal checks whether two NotificationContext values are semantically equal.
// It compares the notification's selector, the plan's labels, and the Matches flag.
func (nc *NotificationContext) Equal(other *NotificationContext) bool {
	if nc == other {
		return true
	}
	if nc == nil || other == nil {
		return false
	}
	if nc.Matches != other.Matches {
		return false
	}
	if nc.PlanKey != other.PlanKey {
		return false
	}
	// Compare plan labels.
	if len(nc.PlanLabels) != len(other.PlanLabels) {
		return false
	}
	for k, v := range nc.PlanLabels {
		if other.PlanLabels[k] != v {
			return false
		}
	}
	// Compare notification selector (the field that determines matching).
	if nc.Notification == nil || other.Notification == nil {
		return (nc.Notification == nil) == (other.Notification == nil)
	}

	if !selectorEqual(&nc.Notification.Spec.Selector, &other.Notification.Spec.Selector) {
		return false
	}

	return notificationsEqual(nc.Notification, other.Notification)
}

// DeepCopy creates a deep copy of NotificationContext.
func (nc *NotificationContext) DeepCopy() *NotificationContext {
	if nc == nil {
		return nil
	}
	result := &NotificationContext{
		PlanKey: nc.PlanKey,
		Matches: nc.Matches,
	}
	if nc.Notification != nil {
		result.Notification = nc.Notification.DeepCopy()
	}
	if nc.PlanLabels != nil {
		result.PlanLabels = make(map[string]string, len(nc.PlanLabels))
		for k, v := range nc.PlanLabels {
			result.PlanLabels[k] = v
		}
	}
	return result
}

// selectorEqual compares two LabelSelectors for structural equality.
func selectorEqual(a, b *metav1.LabelSelector) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if len(a.MatchLabels) != len(b.MatchLabels) {
		return false
	}
	for k, v := range a.MatchLabels {
		if b.MatchLabels[k] != v {
			return false
		}
	}
	if len(a.MatchExpressions) != len(b.MatchExpressions) {
		return false
	}
	for i := range a.MatchExpressions {
		ae := a.MatchExpressions[i]
		be := b.MatchExpressions[i]
		if ae.Key != be.Key || ae.Operator != be.Operator {
			return false
		}
		if len(ae.Values) != len(be.Values) {
			return false
		}
		for j := range ae.Values {
			if ae.Values[j] != be.Values[j] {
				return false
			}
		}
	}
	return true
}
