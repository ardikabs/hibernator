/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package controller

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

// startWakeUp initiates the wake-up (restoration) process.
func (r *HibernatePlanReconciler) startWakeUp(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (ctrl.Result, error) {
	log.V(1).Info("starting wake-up")

	// Initialize wakeup operation
	execPlan, err := r.initializeOperation(ctx, log, plan, "wakeup")
	if err != nil {
		return r.setError(ctx, plan, fmt.Errorf("initialize wakeup operation: %w", err))
	}

	if err := r.APIReader.Get(ctx, types.NamespacedName{Name: plan.Name, Namespace: plan.Namespace}, plan); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, err
	}

	// Start first stage with latest plan instance
	return r.executeStage(ctx, log, plan, execPlan, 0, "wakeup")
}

// reconcileWakeUp continues the wake-up (restoration) process.
// It monitors job progress and advances through execution stages in reverse order.
func (r *HibernatePlanReconciler) reconcileWakeUp(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (ctrl.Result, error) {
	return r.reconcileExecution(ctx, log, plan, "wakeup")
}
