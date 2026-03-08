/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package status

import (
	"github.com/ardikabs/hibernator/pkg/keyedworker"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Updater is the write-facing interface exposed to producers (Workers, state handlers).
// It intentionally hides all pool and processor internals.
type Updater[T client.Object] interface {
	Send(u Update[T])
}

type defaultUpdater[T client.Object] struct {
	pool *keyedworker.Pool[types.NamespacedName, Update[T]]
}

func (u *defaultUpdater[T]) Send(update Update[T]) {
	if update.Mutator != nil {
		update.Mutator.Mutate(update.Resource)
	}

	u.pool.Deliver(update.NamespacedName, update)
}
