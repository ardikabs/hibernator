/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package plan

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	statusmanager "github.com/ardikabs/hibernator/internal/provider/status"
)

// captureUpdater is a test helper that buffers statusmanager.Update[T] values
// sent via Send so that tests can inspect Len() or drain the channel.
type captureUpdater[T client.Object] struct {
	ch chan statusmanager.Update[T]
}

func (u *captureUpdater[T]) Send(upd statusmanager.Update[T]) {
	if upd.Mutator != nil {
		upd.Mutator.Mutate(upd.Resource)
	}
	u.ch <- upd
}
func (u *captureUpdater[T]) Len() int                          { return len(u.ch) }
func (u *captureUpdater[T]) C() <-chan statusmanager.Update[T] { return u.ch }

func newTestStatuses() *statusmanager.ControllerStatuses {
	return &statusmanager.ControllerStatuses{
		PlanStatuses:      &captureUpdater[*hibernatorv1alpha1.HibernatePlan]{ch: make(chan statusmanager.Update[*hibernatorv1alpha1.HibernatePlan], 64)},
		ExceptionStatuses: &captureUpdater[*hibernatorv1alpha1.ScheduleException]{ch: make(chan statusmanager.Update[*hibernatorv1alpha1.ScheduleException], 16)},
	}
}
