/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package hibernateplan

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

// startHibernation initiates the hibernation (shutdown) process.
func (r *Reconciler) startHibernation(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (ctrl.Result, error) {
	log.V(1).Info("starting hibernation")

	// Initialize shutdown operation
	execPlan, err := r.initializeOperation(ctx, log, plan, "shutdown")
	if err != nil {
		return r.setError(ctx, plan, fmt.Errorf("initialize shutdown operation: %w", err))
	}

	// Start first stage with latest plan instance
	return r.executeStage(ctx, log, plan, execPlan, 0, "shutdown")
}

// reconcileHibernation continues the hibernation (shutdown) process.
// It monitors job progress and advances through execution stages.
func (r *Reconciler) reconcileHibernation(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (ctrl.Result, error) {
	// a safeguard to ensure we are in the correct operation
	if plan.Status.CurrentOperation != "shutdown" {
		log.Info("starting hibernation (mismatched operation in status)", "currentOperation", plan.Status.CurrentOperation)
		return r.startHibernation(ctx, log, plan)
	}

	// Check job statuses and progress through stages
	return r.reconcileExecution(ctx, log, plan, "shutdown")
}
