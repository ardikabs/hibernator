/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"

	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/go-logr/logr"
)

// lifecycleState handles plan initialization (phase == "") and finalizer-based deletion.
type lifecycleState struct {
	*state

	delete bool
}

func (state *lifecycleState) Handle(ctx context.Context) (StateResult, error) {
	plan := state.plan()
	log := state.Log.
		WithName("lifecycle").
		WithValues("plan", state.Key.String())

	if state.delete {
		return state.handleDelete(ctx, log, plan)
	}

	return state.handle(ctx, log, plan)
}

func (state *lifecycleState) handleDelete(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (StateResult, error) {
	log.V(1).Info("plan has deletion timestamp, handling deletion")

	var jobList batchv1.JobList
	if err := state.List(ctx, &jobList, client.InNamespace(plan.Namespace), client.MatchingLabels{
		wellknown.LabelPlan: plan.Name,
	}); err != nil {
		log.Error(err, "failed to list jobs for cleanup")
		return StateResult{}, err
	}

	propagation := metav1.DeletePropagationBackground
	for i := range jobList.Items {
		job := &jobList.Items[i]
		if err := state.Delete(ctx, job, &client.DeleteOptions{PropagationPolicy: &propagation}); err != nil && !apierrors.IsNotFound(err) {
			log.Error(err, "failed to delete job during finalizer cleanup", "job", job.Name)
			return StateResult{}, err
		}
	}

	if controllerutil.ContainsFinalizer(plan, wellknown.PlanFinalizerName) {
		orig := plan.DeepCopy()
		controllerutil.RemoveFinalizer(plan, wellknown.PlanFinalizerName)
		if err := state.Patch(ctx, plan, client.MergeFrom(orig)); err != nil && !apierrors.IsNotFound(err) {
			log.Error(err, "failed to remove finalizer")
			return StateResult{}, err
		}

		log.V(1).Info("removed finalizer")
	}

	return StateResult{}, nil
}

func (state *lifecycleState) handle(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) (StateResult, error) {
	// Ensure finalizer exists before doing anything else.
	// The provider informer will fire again with the updated plan; no local cascade needed.
	if !controllerutil.ContainsFinalizer(plan, wellknown.PlanFinalizerName) {
		orig := plan.DeepCopy()
		controllerutil.AddFinalizer(plan, wellknown.PlanFinalizerName)
		if err := state.Patch(ctx, plan, client.MergeFrom(orig)); err != nil {
			return StateResult{}, err
		}

		log.V(1).Info("added finalizer")
	}

	// Set Phase = Active if still unset.
	if plan.Status.Phase == "" {
		log.Info("initializing plan status")

		mutate := func(st *hibernatorv1alpha1.HibernatePlanStatus) {
			st.Phase = hibernatorv1alpha1.PhaseActive
			st.ObservedGeneration = plan.Generation
		}

		mutate(&plan.Status)
		state.Statuses.PlanStatuses.Send(&message.PlanStatusUpdate{
			NamespacedName: state.Key,
			Mutate:         mutate,
		})

		if err := state.RestoreManager.PrepareRestorePoint(ctx, plan.Namespace, plan.Name); err != nil {
			log.Error(err, "failed to prepare restore point (non-fatal)")
		}

		log.V(1).Info("plan status initialized (Active)")
		return StateResult{Requeue: true}, nil
	}

	return StateResult{}, nil
}
