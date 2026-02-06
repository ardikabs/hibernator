package status

import (
	"context"

	"github.com/go-logr/logr"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Mutator is an interface to hold mutator functions for status updates.
type Mutator interface {
	Mutate(obj client.Object) client.Object
}

// MutatorFunc is a function adaptor for Mutators.
type MutatorFunc func(client.Object) client.Object

// Mutate adapts the MutatorFunc to fit through the Mutator interface.
func (m MutatorFunc) Mutate(old client.Object) client.Object {
	if m == nil {
		return nil
	}

	return m(old)
}

// Synchronous handler - direct calls, no channels
type SyncStatusUpdater struct {
	log    logr.Logger
	client client.Client
}

func NewSyncStatusUpdater(cl client.Client) *SyncStatusUpdater {
	return &SyncStatusUpdater{
		client: cl,
	}
}

// Apply updates immediately and returns error
func (s *SyncStatusUpdater) Update(ctx context.Context, obj client.Object, mutator Mutator) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		// Fresh Get inside retry
		if err := s.client.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			return client.IgnoreNotFound(err)
		}

		// Apply mutations
		updated := mutator.Mutate(obj)
		updated.SetUID(obj.GetUID())

		// Update with fresh resourceVersion
		return s.client.Status().Update(ctx, updated)
	})
}
