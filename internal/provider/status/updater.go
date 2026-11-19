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

// Send applies the update's Mutator to the resource (optimistic in-memory mutation)
// and submits the update to the async pool for eventual writeback to Kubernetes by the status writer.
func (u *defaultUpdater[T]) Send(update Update[T]) {
	// TODO: Send invokes the Mutator eagerly (in-memory mutation)
	// AND delivers to the async pool (which applies the same Mutator again during
	// writeback). Today this works because all Mutators are idempotent set-field
	// operations. If a non-idempotent Mutator is added in the future, the double
	// invocation could cause subtle bugs. Consider splitting "optimistic local
	// update" from "queued server write" into two distinct paths.
	if update.Mutator != nil {
		update.Mutator.Mutate(update.Resource)
	}

	u.pool.Deliver(update.NamespacedName, update)
}
