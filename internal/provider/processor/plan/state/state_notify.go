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
		// Best-effort deduplication of sinks by name within a notification
		// to avoid sending duplicate requests for functionally identical sinks.
		// TODO(ardikabs): also add validation to the API to enforce unique sink names within a notification spec.
		if _, ok := done[sink.Name]; ok {
			continue
		}
		done[sink.Name] = struct{}{}

		req := notification.Request{
			Payload:   payload,
			SinkName:  sink.Name,
			SinkType:  string(sink.Type),
			SecretRef: sink.SecretRef,
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
		ID:           client.ObjectKeyFromObject(plan),
		Labels:       plan.Labels,
		Event:        string(event),
		Timestamp:    clk(),
		Phase:        string(plan.Status.Phase),
		Operation:    plan.Status.CurrentOperation,
		CycleID:      plan.Status.CurrentCycleID,
		ErrorMessage: plan.Status.ErrorMessage,
		RetryCount:   plan.Status.RetryCount,
	}

	targets := make([]notification.TargetInfo, len(plan.Status.Executions))
	for i, exec := range plan.Status.Executions {
		targets[i] = notification.TargetInfo{
			Name:     exec.Target,
			Executor: exec.Executor,
			State:    string(exec.State),
			Message:  exec.Message,
		}
	}
	p.Targets = targets
	return p
}
