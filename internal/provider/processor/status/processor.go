package status

import (
	"context"
	"time"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/metrics"
	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ardikabs/hibernator/pkg/keyedworker"
)

// processorIdleTTL is how long a per-key worker goroutine idles with no work
// before it exits. The next Send for that key restarts it on demand.
const processorIdleTTL = 30 * time.Minute

// HookFunc is called with the K8s object before (PreHook) or after (PostHook)
// the status mutation is written. A non-nil error from PreHook aborts the write.
type HookFunc[T client.Object] func(ctx context.Context, obj T) error

// Update contains all information needed to apply a single status mutation to a
// K8s object. Instances are sent via Updater.Send and processed by UpdateProcessor.
type Update[T client.Object] struct {
	// NamespacedName identifies the target object.
	NamespacedName types.NamespacedName

	// Resource is the object template used for Get calls inside RetryOnConflict.
	// The processor always fetches a fresh copy from the API server before mutating.
	Resource T

	// PreHook is called once with the current (pre-mutation) object before Mutate
	// is applied. A non-nil error aborts the transition entirely; nothing is written.
	// +Optional
	PreHook HookFunc[T]

	// Mutator applies the desired status change to a freshly-fetched copy of the object.
	Mutator Mutator[T]

	// PostHook is called once with the object as written to K8s. It is only
	// invoked when the status write actually occurred; no-op updates (where
	// the status was already equal) do not trigger it. Errors are logged but
	// do not roll back the write.
	// +Optional
	PostHook HookFunc[T]
}

// Mutator mutates a K8s object in-place (typically only its Status sub-resource).
// The object is a pointer, so changes are reflected in the caller's copy immediately.
type Mutator[T client.Object] interface {
	Mutate(obj T)
}

// MutatorFunc is a function adaptor for Mutator.
type MutatorFunc[T client.Object] func(T)

// Mutate implements Mutator.
func (m MutatorFunc[T]) Mutate(obj T) {
	if m != nil {
		m(obj)
	}
}

// UpdateProcessor is a per-key status writer backed by a keyedworker.Pool.
//
// Properties:
//   - Per-key serial ordering: updates for the same key are processed strictly
//     in FIFO order; different keys are processed in parallel.
//   - Non-blocking Send: Deliver to the pool's FIFOSlot never blocks the caller.
//     If the per-key buffer (cap 1000) is full, the oldest update is dropped —
//     safe because RetryOnConflict always fetches a fresh object from the API server.
//   - Context-aware apply: all K8s calls inside apply() propagate ctx so they
//     respect the manager's shutdown deadline.
//   - No startup gate: Send may be called before Start; buffered items are
//     processed once Start wires the run factory (Pool guarantee).
//   - Auto-remove on idle: entries are removed from the pool map after the idle
//     TTL grace period expires with an empty slot, reclaiming memory for inactive
//     resources without requiring an explicit Remove call.
//
// Register with the controller-runtime manager via mgr.Add(processor).
type UpdateProcessor[T client.Object] struct {
	log       logr.Logger
	kind      string // Kubernetes object kind, used as a metric label
	client    client.Client
	apiReader client.Reader
	pool      *keyedworker.Pool[types.NamespacedName, Update[T]]
}

// NewUpdateProcessor creates a new UpdateProcessor. It must be registered as a
// manager.Runnable before it processes any updates.
//
// The kind label is derived automatically from T via KindOf, so callers do not
// need to pass it explicitly.
//
// apiReader must be the uncached reader (mgr.GetAPIReader()) so that Get calls
// inside RetryOnConflict always see the true server state rather than a potentially
// stale informer-cache snapshot.
func NewUpdateProcessor[T client.Object](log logr.Logger, c client.Client, apiReader client.Reader) *UpdateProcessor[T] {
	var zero T
	kind := hibernatorv1alpha1.KindOf(zero)
	u := &UpdateProcessor[T]{
		log:       log,
		kind:      kind,
		client:    c,
		apiReader: apiReader,
	}
	u.pool = keyedworker.New(
		keyedworker.WithSlotFactory[types.NamespacedName](keyedworker.FIFOSlot[Update[T]](1000)),
		keyedworker.WithLogger[types.NamespacedName, Update[T]](log.WithName("pool")),
		keyedworker.WithAutoRemoveOnIdle[types.NamespacedName, Update[T]](),
		keyedworker.WithOnSpawnCallback[types.NamespacedName, Update[T]](func(key types.NamespacedName) {
			metrics.StatusWriterActiveGauge.WithLabelValues(kind, key.String()).Inc()
		}),
		keyedworker.WithOnRemoveCallback[types.NamespacedName, Update[T]](func(key types.NamespacedName) {
			metrics.StatusWriterActiveGauge.WithLabelValues(kind, key.String()).Dec()
		}),
	)

	return u
}

// NeedLeaderElection returns true — status writes require leader election.
func (u *UpdateProcessor[T]) NeedLeaderElection() bool { return true }

// Send applies the mutation to update.Resource immediately (so the caller's
// in-memory object reflects the new status without a separate step), then
// enqueues the update for async persistence. Non-blocking.
// Safe to call before Start; the Pool buffers the value and drains it once
// the run factory is registered.
func (u *UpdateProcessor[T]) Writer() Updater[T] {
	return &defaultUpdater[T]{
		pool: u.pool,
	}
}

// Start wires the pool's run factory and blocks until ctx is cancelled.
// Implements manager.Runnable.
func (u *UpdateProcessor[T]) Start(ctx context.Context) error {
	u.log.Info("started status update processor")
	defer u.log.Info("stopped status update processor")

	u.pool.Register(ctx, keyedworker.RunnerFactory[types.NamespacedName](
		processorIdleTTL,
		func(ctx context.Context, update Update[T]) error {
			return u.apply(ctx, update)
		},
		func(err error) { u.log.Error(err, "status update apply error") },
	))

	// pool.Start is non-blocking — block here until the manager signals shutdown.
	<-ctx.Done()

	// Stop signals any still-running per-key goroutines to exit.
	u.pool.Stop()
	return nil
}

// apply performs the actual K8s status write for a single update.
// It fetches a fresh copy of the object via the uncached APIReader to guard
// against stale cache reads, applies the mutation, guards against no-op writes
// via isStatusEqual, then calls Status().Update with RetryOnConflict.
func (u *UpdateProcessor[T]) apply(ctx context.Context, update Update[T]) error {
	startTime := time.Now()
	key := update.NamespacedName.String()
	obj := update.Resource
	objKind := hibernatorv1alpha1.KindOf(obj)

	log := u.log.WithValues("key", key, "kind", objKind)
	log.V(1).Info("processing status update")
	defer func() {
		log.Info("finished processing status update", "duration", time.Since(startTime).String())
	}()

	if update.PreHook != nil {
		// Fetch a fresh copy for the PreHook so it sees current server state.
		current := obj.DeepCopyObject().(T)
		if err := u.apiReader.Get(ctx, update.NamespacedName, current); err != nil {
			if apierrors.IsNotFound(err) {
				log.V(1).Info("object not found, skipping pre-hook")
				return nil
			}
			return err
		}

		if err := update.PreHook(ctx, current); err != nil {
			log.Error(err, "pre-hook failed, aborting update")
			metrics.StatusWriterErrorsTotal.WithLabelValues(objKind, key, "pre_hook").Inc()
			return err
		}
	}

	if update.Mutator == nil {
		log.V(1).Info("no mutator provided, skipping status update")
		return nil
	}

	var (
		written  T
		didWrite bool
	)
	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		// Always fetch fresh from the uncached reader to avoid stale baseline.
		fresh := obj.DeepCopyObject().(T)
		if err := u.apiReader.Get(ctx, update.NamespacedName, fresh); err != nil {
			if apierrors.IsNotFound(err) {
				log.V(1).Info("object not found, skipping status update")
				return nil
			}
			return err
		}

		// Snapshot before mutation so we can detect no-op writes.
		before := fresh.DeepCopyObject().(T)
		update.Mutator.Mutate(fresh)
		if isStatusEqual(before, fresh) {
			log.V(1).Info("status unchanged, bypassing update")
			metrics.StatusWriterNoopTotal.WithLabelValues(objKind, key).Inc()
			return nil
		}

		if err := u.client.Status().Update(ctx, fresh); err != nil {
			return err
		}

		metrics.StatusWriterUpdatesTotal.WithLabelValues(objKind, key).Inc()
		written = fresh
		didWrite = true
		return nil
	}); err != nil {
		log.Error(err, "unable to update status")
		metrics.StatusWriterErrorsTotal.WithLabelValues(objKind, key, "apply").Inc()
		return err
	}

	if update.PostHook != nil && didWrite {
		if err := update.PostHook(ctx, written); err != nil {
			log.Error(err, "post-transition hook failed (non-fatal)")
			metrics.StatusWriterErrorsTotal.WithLabelValues(objKind, key, "post_hook").Inc()
		}
	}

	return nil
}

func isStatusEqual(objA, objB any) bool {
	defaultOpts := cmp.Options{
		cmpopts.IgnoreMapEntries(func(k string, _ any) bool {
			return k == "lastTransitionTime" || k == "lastRetryTime"
		}),
	}

	switch a := objA.(type) {
	case *hibernatorv1alpha1.HibernatePlan:
		if b, ok := objB.(*hibernatorv1alpha1.HibernatePlan); ok {
			opts := append(defaultOpts,
				cmpopts.IgnoreFields(hibernatorv1alpha1.ExecutionStatus{}, "StartedAt", "FinishedAt"),
				cmpopts.IgnoreFields(hibernatorv1alpha1.ExceptionReference{}, "AppliedAt"),
			)
			return cmp.Equal(a.Status, b.Status, opts)
		}
	case *hibernatorv1alpha1.ScheduleException:
		if b, ok := objB.(*hibernatorv1alpha1.ScheduleException); ok {
			return cmp.Equal(a.Status, b.Status,
				cmpopts.IgnoreFields(hibernatorv1alpha1.ScheduleExceptionStatus{}, "AppliedAt", "ExpiredAt"),
			)
		}
	}

	return false
}
