/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/notification"
	statusprocessor "github.com/ardikabs/hibernator/internal/provider/processor/status"
	"github.com/ardikabs/hibernator/internal/wellknown"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// revertState handles plans in PhaseError with revert annotation.
// It orchestrates a selective wakeup by transitioning Error -> WakingUp,
// leaving the revert annotation for idleState to consume after wakeup completes.
type revertState struct {
	*state
}

func (s *revertState) Handle(ctx context.Context) (StateResult, error) {
	plan := s.plan()
	log := s.Log.WithName("revert").WithValues("plan", s.Key.String())

	if plan.Annotations[wellknown.AnnotationRevert] != "true" {
		log.V(1).Info("revert annotation not present, skipping")
		return StateResult{}, nil
	}

	log.Info("reverting plan from error state")

	// Use PlanSnapshot targets when available for this cycle, matching transitionToWakingUp.
	// Runner handles skipping targets without live restore data as no-op.
	var targetList []hibernatorv1alpha1.Target
	if snap := plan.Status.PlanSnapshot; snap != nil && snap.CycleID == plan.Status.CurrentCycleID {
		targetList = snap.Targets
		log.V(1).Info("reusing plan snapshot targets for revert", "cycleID", snap.CycleID, "exception", snap.ExceptionName)
	} else if snap != nil {
		targetList = plan.Spec.Targets
		log.V(1).Info("plan snapshot cycleID mismatch for revert, using live plan targets",
			"snapshotCycleID", snap.CycleID, "currentCycleID", plan.Status.CurrentCycleID)
	} else {
		targetList = plan.Spec.Targets
		log.V(1).Info("no plan snapshot for current cycle, using live plan targets for revert")
	}

	// Guard against no-op revert: if none of the targets have live restore data,
	// remove the annotation and stay in Error so the user sees a clear failure.
	hasLiveRestoreData := false
	for _, target := range targetList {
		data, err := s.RestoreManager.Load(ctx, plan.Namespace, plan.Name, target.Name)
		if err != nil {
			log.Error(err, "failed to check restore data for target", "target", target.Name)
			continue
		}
		if data != nil && data.IsLive {
			hasLiveRestoreData = true
			break
		}
	}
	if !hasLiveRestoreData {
		log.Info("no targets have live restore data, removing revert annotation and keeping plan in Error")
		orig := plan.DeepCopy()
		delete(plan.Annotations, wellknown.AnnotationRevert)
		if err := s.patchAndPreserveStatus(ctx, plan, client.MergeFrom(orig)); err != nil {
			return StateResult{}, err
		}
		s.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
			NamespacedName: s.Key,
			Resource:       plan,
			Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
				p.Status.ErrorMessage = "revert skipped: no targets have live restore data"
				p.Status.LastTransitionTime = ptr.To(metav1.NewTime(s.Clock.Now()))
			}),
		})
		return StateResult{Requeue: true}, nil
	}

	executions := make([]hibernatorv1alpha1.ExecutionStatus, len(targetList))
	for i, t := range targetList {
		executions[i] = hibernatorv1alpha1.ExecutionStatus{
			Target:   t.Name,
			Executor: t.Type,
			State:    hibernatorv1alpha1.StatePending,
			Message:  "Target pending revert wakeup",
		}
	}

	previousPhase := plan.Status.Phase
	now := s.Clock.Now()

	s.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: s.Key,
		Resource:       plan,
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.Phase = hibernatorv1alpha1.PhaseWakingUp
			p.Status.CurrentStageIndex = 0
			p.Status.CurrentOperation = hibernatorv1alpha1.OperationWakeUp
			p.Status.Executions = executions
			p.Status.LastTransitionTime = ptr.To(metav1.NewTime(now))
			// Revert annotation is preserved - idleState will consume it after wakeup
		}),
		PostHook: chainHooks(
			s.notifyHook(hibernatorv1alpha1.EventStart, func(p *hibernatorv1alpha1.HibernatePlan) notification.Payload {
				return buildPayload(p, hibernatorv1alpha1.EventStart, s.Clock.Now)
			}),
			s.phaseChangePostHook(previousPhase),
		),
	})

	log.V(1).Info("queued transition to WakingUp for revert", "cycleID", plan.Status.CurrentCycleID)
	return StateResult{Requeue: true}, nil
}
