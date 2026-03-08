/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package message

import (
	"time"

	"k8s.io/apimachinery/pkg/types"

	"github.com/telepresenceio/watchable"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

// ControllerResources contains the watchable maps for provider-to-processor communication.
// Providers write to these maps; processors subscribe and react to changes.
type ControllerResources struct {
	// PlanResources holds enriched PlanContext for each HibernatePlan.
	PlanResources watchable.Map[types.NamespacedName, *PlanContext]

	// ExceptionResources holds raw ScheduleException resources.
	ExceptionResources watchable.Map[types.NamespacedName, *hibernatorv1alpha1.ScheduleException]
}

// PlanContext contains all data needed by processors to make decisions for a single HibernatePlan.
// It is the value stored in PlanResources and represents the provider's enriched view of the plan.
type PlanContext struct {
	// Plan is the HibernatePlan resource fetched from K8s.
	Plan *hibernatorv1alpha1.HibernatePlan

	// Exceptions is the list of ScheduleExceptions associated with this plan.
	Exceptions []hibernatorv1alpha1.ScheduleException

	// ScheduleResult contains the pre-computed schedule evaluation.
	// Nil if schedule evaluation failed or is not applicable.
	ScheduleResult *ScheduleEvaluation

	// HasRestoreData indicates whether restore data exists for this plan.
	HasRestoreData bool

	// DeliveryNonce is a monotonically increasing counter set by the provider on every
	// reconcile. It guarantees that watchable.Map.Store() always detects a change and
	// re-delivers the context to subscribers, even when no other field has changed.
	// Workers never read this field; it exists solely as a change-detection signal.
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
	if pc.Exceptions != nil {
		result.Exceptions = make([]hibernatorv1alpha1.ScheduleException, len(pc.Exceptions))
		for i, exc := range pc.Exceptions {
			result.Exceptions[i] = *exc.DeepCopy()
		}
	}
	if pc.ScheduleResult != nil {
		result.ScheduleResult = &ScheduleEvaluation{
			ShouldHibernate: pc.ScheduleResult.ShouldHibernate,
			RequeueAfter:    pc.ScheduleResult.RequeueAfter,
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
	if (pc.ScheduleResult == nil) != (other.ScheduleResult == nil) {
		return false
	}
	if pc.ScheduleResult != nil {
		if pc.ScheduleResult.ShouldHibernate != other.ScheduleResult.ShouldHibernate {
			return false
		}
		// RequeueAfter is intentionally excluded — it is a time-varying scheduling hint
		// computed from time.Until(), which changes on every reconcile cycle even when
		// nothing meaningful has changed. Including it would cause spurious watchable
		// re-delivery on every provider reconcile.
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

// ScheduleEvaluation contains the result of schedule evaluation by the provider.
type ScheduleEvaluation struct {
	// ShouldHibernate indicates if the plan should currently be hibernating.
	ShouldHibernate bool

	// RequeueAfter is the suggested requeue duration based on schedule evaluation.
	RequeueAfter time.Duration
}
