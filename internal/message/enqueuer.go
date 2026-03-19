/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package message

import (
	"k8s.io/apimachinery/pkg/types"
)

// PlanEnqueuer triggers a fresh reconciliation for a HibernatePlan.
// Implementations enqueue the plan's NamespacedName back into the PlanReconciler's
// work queue, causing it to re-fetch all related resources and produce a fresh
// PlanContext in the watchable map.
type PlanEnqueuer interface {
	Enqueue(key types.NamespacedName)
}
