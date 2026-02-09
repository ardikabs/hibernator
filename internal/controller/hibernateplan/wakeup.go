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

// startWakeUp initiates the wake-up (restoration) process.
func (r *Reconciler) startWakeUp(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (ctrl.Result, error) {
	log.V(1).Info("starting wake-up")

	hasRestoreData, err := r.RestoreManager.HasRestoreData(ctx, plan.Namespace, plan.Name)
	if err != nil {
		return r.setError(ctx, plan, fmt.Errorf("check restore data: %w", err))
	}
	if !hasRestoreData {
		return r.setError(ctx, plan, fmt.Errorf("cannot wake up: no restore point found"))
	}

	// Initialize wakeup operation
	execPlan, err := r.initializeOperation(ctx, log, plan, "wakeup")
	if err != nil {
		return r.setError(ctx, plan, fmt.Errorf("initialize wakeup operation: %w", err))
	}

	// Start first stage with latest plan instance
	return r.executeStage(ctx, log, plan, execPlan, 0, "wakeup")
}

// reconcileWakeUp continues the wake-up (restoration) process.
// It monitors job progress and advances through execution stages in reverse order.
func (r *Reconciler) reconcileWakeUp(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (ctrl.Result, error) {
	// a safeguard to ensure we are in the correct operation
	if plan.Status.CurrentOperation != "wakeup" {
		log.Info("starting wakeup (mismatched operation in status)", "currentOperation", plan.Status.CurrentOperation)
		return r.startWakeUp(ctx, log, plan)
	}

	return r.reconcileExecution(ctx, log, plan, "wakeup")
}
