/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"slices"
	"time"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/notification"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// notifyHook returns a HookFunc that dispatches notifications for the given event.
// The returned function is placement-agnostic — callers attach it as either a
// PreHook or PostHook depending on the event semantics:
//
//   - PreHook (Start, Recovery): notification fires before the status write.
//     The payloadFn receives the pre-mutation object, so callers must override
//     Phase/Operation/CycleID with the target values the mutation will set.
//   - PostHook (Success, Failure, PhaseChange): notification fires after a
//     successful write. The payloadFn receives the written object with final state.
//
// Returns nil when the dispatcher is not configured or no notifications exist,
// so callers can unconditionally assign it.
func (s *state) notifyHook(event hibernatorv1alpha1.NotificationEvent, payloadFn func(*hibernatorv1alpha1.HibernatePlan) notification.Payload) func(context.Context, *hibernatorv1alpha1.HibernatePlan) error {
	if s.Notifier == nil {
		return nil
	}
	notifications := s.PlanCtx.Notifications
	if len(notifications) == 0 {
		return nil
	}

	return func(ctx context.Context, plan *hibernatorv1alpha1.HibernatePlan) error {
		payload := payloadFn(plan)
		enrichConnectorInfo(ctx, s.APIReader, plan.Namespace, payload.Targets)
		for i := range notifications {
			submitForNotification(ctx, s.Notifier, &notifications[i], event, payload)
		}
		return nil
	}
}

// phaseChangePostHook returns a PostHook that dispatches PhaseChange notifications.
// It captures previousPhase before the status write and reads the new phase from the
// written object. Returns nil if no dispatcher or notifications.
func (s *state) phaseChangePostHook(previousPhase hibernatorv1alpha1.PlanPhase) func(context.Context, *hibernatorv1alpha1.HibernatePlan) error {
	if s.Notifier == nil {
		return nil
	}
	notifications := s.PlanCtx.Notifications
	if len(notifications) == 0 {
		return nil
	}

	return func(ctx context.Context, plan *hibernatorv1alpha1.HibernatePlan) error {
		payload := buildPayload(plan, hibernatorv1alpha1.EventPhaseChange, s.Clock.Now)
		payload.PreviousPhase = string(previousPhase)
		enrichConnectorInfo(ctx, s.APIReader, plan.Namespace, payload.Targets)
		for i := range notifications {
			submitForNotification(ctx, s.Notifier, &notifications[i], hibernatorv1alpha1.EventPhaseChange, payload)
		}
		return nil
	}
}

// chainHooks combines multiple HookFunc functions into one. Any nil hooks in the
// variadic list are skipped. Returns nil if all inputs are nil.
func chainHooks[T client.Object](hooks ...func(context.Context, T) error) func(context.Context, T) error {
	var nonNil []func(context.Context, T) error
	for _, h := range hooks {
		if h != nil {
			nonNil = append(nonNil, h)
		}
	}
	if len(nonNil) == 0 {
		return nil
	}
	if len(nonNil) == 1 {
		return nonNil[0]
	}
	return func(ctx context.Context, obj T) error {
		for _, h := range nonNil {
			if err := h(ctx, obj); err != nil {
				return err
			}
		}
		return nil
	}
}

// submitForNotification checks whether the notification subscribes to the given
// event and, for each matching sink, submits a DispatchRequest to the dispatcher.
func submitForNotification(_ context.Context, n notification.Notifier, notif *hibernatorv1alpha1.HibernateNotification, event hibernatorv1alpha1.NotificationEvent, payload notification.Payload) {
	if !subscribesToEvent(notif, event) {
		return
	}

	done := make(map[string]struct{})
	for _, sink := range notif.Spec.Sinks {
		// Ensure with deduplication of sinks by name within a notification
		// to avoid sending duplicate requests for functionally identical sinks.
		if _, ok := done[sink.Name]; ok {
			continue
		}
		done[sink.Name] = struct{}{}

		req := notification.Request{
			Payload:         payload,
			SinkName:        sink.Name,
			SinkType:        string(sink.Type),
			SecretRef:       sink.SecretRef,
			NotificationRef: client.ObjectKeyFromObject(notif),
		}
		if sink.TemplateRef != nil {
			req.TemplateRef = sink.TemplateRef
		}
		n.Submit(req)
	}
}

// subscribesToEvent returns true if the notification's OnEvents list contains the
// given event.
func subscribesToEvent(notif *hibernatorv1alpha1.HibernateNotification, event hibernatorv1alpha1.NotificationEvent) bool {
	return slices.Contains(notif.Spec.OnEvents, event)
}

// buildPayload constructs a notification.Payload from the plan's current status.
func buildPayload(plan *hibernatorv1alpha1.HibernatePlan, event hibernatorv1alpha1.NotificationEvent, clk func() time.Time) notification.Payload {
	p := notification.Payload{
		Plan: notification.PlanInfo{
			Name:        plan.Name,
			Namespace:   plan.Namespace,
			Labels:      plan.Labels,
			Annotations: plan.Annotations,
		},
		Event:        string(event),
		Timestamp:    clk(),
		Phase:        string(plan.Status.Phase),
		Operation:    string(plan.Status.CurrentOperation),
		CycleID:      plan.Status.CurrentCycleID,
		ErrorMessage: plan.Status.ErrorMessage,
		RetryCount:   plan.Status.RetryCount,
	}

	// Index spec targets by name for connector ref lookup.
	specTargets := make(map[string]*hibernatorv1alpha1.Target, len(plan.Spec.Targets))
	for i := range plan.Spec.Targets {
		specTargets[plan.Spec.Targets[i].Name] = &plan.Spec.Targets[i]
	}

	targets := make([]notification.TargetInfo, len(plan.Status.Executions))
	for i, exec := range plan.Status.Executions {
		targets[i] = notification.TargetInfo{
			Name:     exec.Target,
			Executor: exec.Executor,
			State:    string(exec.State),
			Message:  exec.Message,
		}

		if spec, ok := specTargets[exec.Target]; ok {
			targets[i].Connector = notification.ConnectorInfo{
				Kind: spec.ConnectorRef.Kind,
				Name: spec.ConnectorRef.Name,
			}
		}
	}
	p.Targets = targets
	return p
}

// enrichConnectorInfo populates cloud-specific fields on each target's
// ConnectorInfo by reading the referenced CloudProvider or K8SCluster
// resources. Errors are silently ignored — connector metadata is best-effort
// for notification rendering.
func enrichConnectorInfo(ctx context.Context, reader client.Reader, namespace string, targets []notification.TargetInfo) {
	for i := range targets {
		ci := &targets[i].Connector
		if ci.Kind == "" || ci.Name == "" {
			continue
		}

		ns := namespace
		switch ci.Kind {
		case "CloudProvider":
			var cp hibernatorv1alpha1.CloudProvider
			if err := reader.Get(ctx, client.ObjectKey{Namespace: ns, Name: ci.Name}, &cp); err != nil {
				continue
			}
			ci.Provider = string(cp.Spec.Type)
			if cp.Spec.AWS != nil {
				ci.AccountID = cp.Spec.AWS.AccountId
				ci.Region = cp.Spec.AWS.Region
			}

		case "K8SCluster":
			var kc hibernatorv1alpha1.K8SCluster
			if err := reader.Get(ctx, client.ObjectKey{Namespace: ns, Name: ci.Name}, &kc); err != nil {
				continue
			}
			if kc.Spec.EKS != nil {
				ci.ClusterName = kc.Spec.EKS.Name
				ci.Region = kc.Spec.EKS.Region
			} else if kc.Spec.GKE != nil {
				ci.ClusterName = kc.Spec.GKE.Name
				ci.Region = kc.Spec.GKE.Location
				ci.ProjectID = kc.Spec.GKE.Project
			} else if kc.Spec.K8S != nil {
				ci.ClusterName = lo.Ternary(kc.Spec.K8S.InCluster, "in-cluster", "remote")
			}

			// Resolve cloud provider fields via ProviderRef if present.
			if kc.Spec.ProviderRef != nil {
				provNS := kc.Spec.ProviderRef.Namespace
				if provNS == "" {
					provNS = ns
				}
				var cp hibernatorv1alpha1.CloudProvider
				if err := reader.Get(ctx, client.ObjectKey{Namespace: provNS, Name: kc.Spec.ProviderRef.Name}, &cp); err == nil {
					ci.Provider = string(cp.Spec.Type)
					if cp.Spec.AWS != nil {
						ci.AccountID = cp.Spec.AWS.AccountId
						ci.Region = lo.Ternary(ci.Region == "", cp.Spec.AWS.Region, ci.Region)
					}

					// TODO: handle GCP provider fields if needed
				}
			}
		}
	}
}
