/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/metrics"
	"github.com/ardikabs/hibernator/internal/notification/sink"
	"github.com/ardikabs/hibernator/pkg/cache"
	"github.com/ardikabs/hibernator/pkg/keyedworker"
	"github.com/ardikabs/hibernator/pkg/ratelimit"
)

const (
	// defaultSecretConfigKey is the well-known key inside the Secret that holds sink config.
	defaultSecretConfigKey = "config"

	// defaultTemplateKey is the well-known key inside the ConfigMap that holds the custom template string.
	defaultTemplateKey = "template.gotpl"

	// defaultDispatchTimeout is the per-notification umbrella timeout.
	// It must be generous enough to accommodate:
	//   - multiple sequential API calls in thread mode (3-4 calls)
	//   - client-side rate limit waits (can be 60-120s per minute-tier)
	//   - go-retryablehttp retry back-off for 429 responses
	//
	// The underlying HTTP client has its own per-call timeout (30s) so a
	// single hung request is still bounded. This umbrella timeout prevents
	// a truly pathological sink from holding a worker goroutine forever.
	defaultDispatchTimeout = 5 * time.Minute

	// defaultDrainTimeout is the maximum time to wait for workers to drain remaining items during shutdown.
	defaultDrainTimeout = 30 * time.Second

	// defaultWorkerIdleTTL is how long an idle per-stream worker stays alive.
	defaultWorkerIdleTTL = 30 * time.Minute

	// defaultChannelSize is the default buffered channel capacity for dispatch requests.
	defaultChannelSize = 256

	// defaultStateCacheTTL is how long sink states remain in the in-memory cache.
	// This must be longer than the typical time between notifications for the same
	// plan/cycle to ensure thread continuity. 10 minutes accommodates most use cases.
	defaultStateCacheTTL = 10 * time.Minute
)

// DispatcherConfig holds tuning knobs for the notification Dispatcher.
// Zero values are replaced with sensible defaults.
type DispatcherConfig struct {
	// ChannelSize is the buffered channel capacity for dispatch requests.
	// Default: 256.
	ChannelSize int

	// DispatchTimeout is the per-sink HTTP call timeout.
	// Default: 5s.
	DispatchTimeout time.Duration

	// WorkerIdleTTL is how long an idle per-stream worker stays alive before exiting.
	// Default: 30m.
	WorkerIdleTTL time.Duration
}

// withDefaults returns a copy with zero fields replaced by defaults.
func (c DispatcherConfig) withDefaults() DispatcherConfig {
	if c.ChannelSize <= 0 {
		c.ChannelSize = defaultChannelSize
	}
	if c.DispatchTimeout <= 0 {
		c.DispatchTimeout = defaultDispatchTimeout
	}
	if c.WorkerIdleTTL <= 0 {
		c.WorkerIdleTTL = defaultWorkerIdleTTL
	}
	return c
}

// DispatcherOption configures an optional dependency of a Dispatcher.
type DispatcherOption func(*Dispatcher)

// withDeliveryCallback registers a callback invoked after each dispatch attempt.
func withDeliveryCallback(cb DeliveryCallback) DispatcherOption {
	return func(d *Dispatcher) {
		d.deliveryCallback = cb
	}
}

// withRateLimitRegistry wires the shared rate limiter registry into the dispatcher.
// The registry is closed when the dispatcher shuts down.
func withRateLimitRegistry(r *ratelimit.Registry) DispatcherOption {
	return func(d *Dispatcher) {
		d.rateLimitRegistry = r
	}
}

// Dispatcher is a standalone controller-runtime Runnable that processes notification
// dispatch requests asynchronously. Hook closures submit Requests via Submit, which
// returns immediately (fire-and-forget). Requests are routed into per-stream FIFO
// slots managed by keyedworker. Each stream is processed by at most one worker at
// a time, guaranteeing deterministic ordering for that stream while preserving
// concurrency across independent streams.
//
// The dispatcher:
//   - resolves sink credentials (Secret lookup via informer cache)
//   - renders the message via TemplateEngine (built-in defaults or custom ConfigMap templates)
//   - sends to the appropriate sink with a per-request timeout
//   - records Prometheus metrics (sent/errors/latency/drops)
//
// Dispatch failures are logged and metered but never propagate errors to the caller.
type Dispatcher struct {
	log             logr.Logger
	client          client.Reader
	registry        *sink.Registry
	channelSize     int // per-stream buffer capacity
	dispatchTimeout time.Duration
	workerIdleTTL   time.Duration

	// deliveryCallback is called after each dispatch attempt to report success/failure.
	// Nil means no delivery tracking.
	deliveryCallback DeliveryCallback

	// done is closed when the dispatcher begins shutting down.
	// Submit checks this via non-blocking select for the fast-path discard.
	done chan struct{}

	// pool manages per-stream workers and their request queues.
	pool *keyedworker.Pool[streamKey, Request]

	// activeWorkerCount tracks the number of currently active workers for graceful shutdown.
	activeWorkerCount atomic.Int64

	// stateCache provides in-memory caching of sink states to prevent race conditions
	// between async status updates and immediate state reads. This ensures thread
	// continuity in Slack thread mode when notifications fire in rapid succession.
	stateCache *cache.Cache[string, map[string]string]

	// rateLimitRegistry is the shared rate limiter registry used by HTTP sink clients.
	// It is closed when the dispatcher shuts down to release background resources.
	rateLimitRegistry *ratelimit.Registry

	// closeOnce ensures d.done is closed at most once, guarding against duplicate
	// closure if Start() is invoked more than once.
	closeOnce sync.Once
}

type streamKey struct {
	Plan            types.NamespacedName
	CycleID         string
	NotificationRef types.NamespacedName
	SinkName        string
	SinkType        string
	Operation       string
}

// String returns a string representation of the stream key for logging.
func (k streamKey) String() string {
	return fmt.Sprintf("plan=%s/%s,cycle=%s,notif=%s,sink=%s,type=%s,op=%s",
		k.Plan.Namespace, k.Plan.Name,
		k.CycleID,
		k.NotificationRef.String(),
		k.SinkName,
		k.SinkType,
		k.Operation)
}

func streamKeyFromRequest(req Request) streamKey {
	return streamKey{
		Plan: types.NamespacedName{
			Namespace: req.Payload.Plan.Namespace,
			Name:      req.Payload.Plan.Name,
		},
		CycleID:         req.Payload.CycleID,
		NotificationRef: req.NotificationRef,
		SinkName:        req.SinkName,
		SinkType:        req.SinkType,
		Operation:       req.Payload.Operation,
	}
}

// NewDispatcher creates a new NotificationDispatcher.
// The client should be the cached reader (informer cache) for Secret lookups.
func NewDispatcher(log logr.Logger, c client.Reader, registry *sink.Registry, cfg DispatcherConfig, opts ...DispatcherOption) *Dispatcher {
	cfg = cfg.withDefaults()

	stateCache, err := cache.New(
		cache.WithTTL[string, map[string]string](defaultStateCacheTTL),
		cache.WithActiveMode[string, map[string]string](),
	)
	if err != nil {
		// Should never happen with positive TTL.
		panic(err)
	}

	d := &Dispatcher{
		log:             log,
		client:          c,
		registry:        registry,
		channelSize:     cfg.ChannelSize,
		dispatchTimeout: cfg.DispatchTimeout,
		workerIdleTTL:   cfg.WorkerIdleTTL,
		done:            make(chan struct{}),
		stateCache:      stateCache,
	}

	for _, opt := range opts {
		opt(d)
	}

	d.pool = keyedworker.New(
		keyedworker.WithSlotFactory[streamKey](func() keyedworker.Slot[Request] {
			return keyedworker.FIFOSlotWithOnDrop(cfg.ChannelSize, d.onRecordDrop)()
		}),
		keyedworker.WithAutoRemoveOnIdle[streamKey, Request](),
		keyedworker.WithOnSpawnCallback[streamKey, Request](func(_ streamKey) {
			d.activeWorkerCount.Add(1)
			metrics.NotificationWorkerGoroutinesGauge.Inc()
		}),
		keyedworker.WithOnRemoveCallback[streamKey, Request](func(_ streamKey) {
			d.activeWorkerCount.Add(-1)
			metrics.NotificationWorkerGoroutinesGauge.Dec()
		}),
		keyedworker.WithLogger[streamKey, Request](log.WithName("pool")),
	)

	return d
}

func (d *Dispatcher) onRecordDrop(req Request) {
	log := d.log.WithValues(
		"plan", req.Payload.Plan.String(),
		"sink", req.SinkName,
		"sinkType", req.SinkType,
		"event", req.Payload.Event,
	)
	log.Info("notification stream buffer full, dropping request")
	metrics.NotificationDropTotal.WithLabelValues(req.SinkType, req.Payload.Event).Inc()
}

// NeedLeaderElection returns true — notifications should only fire from the leader.
func (d *Dispatcher) NeedLeaderElection() bool { return true }

// Notifier returns the Notifier interface backed by this Dispatcher.
// Consumers (plan processors, state handlers) should depend on this
// interface rather than on *Dispatcher directly.
func (d *Dispatcher) Notifier() Notifier { return d }

// Start implements manager.Runnable. It wires keyed per-stream workers and
// blocks until ctx is cancelled. During shutdown, new submissions are rejected
// and active stream workers are given a bounded time window to drain pending
// per-stream items before Start returns.
func (d *Dispatcher) Start(ctx context.Context) error {
	d.log.Info("starting notification dispatcher",
		"mode", "keyed-stream",
		"perStreamBuffer", d.channelSize,
		"workerIdleTTL", d.workerIdleTTL,
		"stateCacheTTL", defaultStateCacheTTL,
	)

	d.pool.Register(ctx, d.workerFactory)

	d.log.V(1).Info("notification dispatcher running", "mode", "keyed-stream")
	<-ctx.Done()
	d.log.V(1).Info("notification dispatcher initiating shutdown")
	d.closeOnce.Do(func() { close(d.done) })
	d.pool.Stop()

	deadline := time.NewTimer(defaultDrainTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

waitLoop:
	for {
		if d.activeWorkerCount.Load() <= 0 {
			d.log.V(1).Info("notification dispatcher workers drained")
			break
		}

		select {
		case <-deadline.C:
			d.log.Info("notification dispatcher drain timeout reached; stopping with active workers", "activeWorkers", d.activeWorkerCount.Load())
			break waitLoop
		case <-ticker.C:
		}
	}

	d.stateCache.Close()
	if d.rateLimitRegistry != nil {
		d.rateLimitRegistry.Close()
	}
	d.log.Info("notification dispatcher stopped")
	return nil
}

// Submit enqueues a dispatch request. It never blocks the caller. Requests are
// routed into per-stream FIFO slots; if a per-stream buffer is full, the request
// is dropped by the slot and counted in NotificationDropTotal.
// After shutdown begins (d.done closed), requests are discarded with a metric.
func (d *Dispatcher) Submit(req Request) {
	log := d.log.WithValues(
		"plan", req.Payload.Plan.String(),
		"sink", req.SinkName,
		"sinkType", req.SinkType,
		"event", req.Payload.Event,
	)

	// Fast-path — non-blocking shutdown check via channel.
	select {
	case <-d.done:
		log.V(1).Info("notification dispatcher stopped, discarding request")
		metrics.NotificationDropTotal.WithLabelValues(req.SinkType, req.Payload.Event).Inc()
		return
	default:
	}

	key := streamKeyFromRequest(req)
	log.V(1).Info("enqueueing notification request", "stream", key)
	d.pool.Deliver(key, req)
}

func (d *Dispatcher) workerFactory(key streamKey, slot keyedworker.Slot[Request]) func(context.Context) {
	return func(ctx context.Context) {
		log := d.log.WithValues("stream", key)
		log.V(1).Info("notification stream worker started")

		idleTimer := time.NewTimer(d.workerIdleTTL)
		defer idleTimer.Stop()

		for {
			select {
			case <-ctx.Done():
				d.drainSlot(slot)
				log.V(1).Info("notification stream worker stopped")
				return

			case <-slot.C():
				if !idleTimer.Stop() {
					select {
					case <-idleTimer.C:
					default:
					}
				}
				idleTimer.Reset(d.workerIdleTTL)

				d.dispatch(ctx, slot.Recv())

			case <-idleTimer.C:
				for {
					select {
					case <-slot.C():
						d.dispatch(ctx, slot.Recv())
					default:
						log.V(1).Info("notification stream worker reaped due to idle ttl")
						return
					}
				}
			}
		}
	}
}

func (d *Dispatcher) drainSlot(slot keyedworker.Slot[Request]) {
	drainCtx, cancel := context.WithTimeout(context.Background(), defaultDrainTimeout)
	defer cancel()

	for {
		if drainCtx.Err() != nil {
			return
		}

		select {
		case <-slot.C():
			d.dispatch(drainCtx, slot.Recv())
		default:
			return
		}
	}
}

// dispatch processes a single DispatchRequest: resolves credentials, builds
// SendOptions with the optional custom template, and delegates formatting +
// delivery to the sink.
func (d *Dispatcher) dispatch(ctx context.Context, req Request) {
	start := time.Now()
	log := d.log.WithValues(
		"plan", req.Payload.Plan.String(),
		"sink", req.SinkName,
		"sinkType", req.SinkType,
		"event", req.Payload.Event,
	)

	// Look up the sink implementation.
	provider, ok := d.registry.Get(req.SinkType)
	if !ok {
		log.Error(fmt.Errorf("sink type %q not registered", req.SinkType), "unknown sink type")
		metrics.NotificationErrorsTotal.WithLabelValues(req.SinkType, req.Payload.Event).Inc()
		return
	}

	// Resolve sink credentials from Secret.
	config, resolveErr := d.resolveSecret(ctx, req.Payload.Plan.Namespace, req.SecretRef)
	if resolveErr != nil {
		log.Error(resolveErr, "failed to resolve sink secret")
		metrics.NotificationErrorsTotal.WithLabelValues(req.SinkType, req.Payload.Event).Inc()
		return
	}

	// Build the sink payload from the notification request.
	sinkPayload := Payload{
		Plan:            req.Payload.Plan,
		Event:           req.Payload.Event,
		Timestamp:       req.Payload.Timestamp,
		Phase:           req.Payload.Phase,
		PreviousPhase:   req.Payload.PreviousPhase,
		Operation:       req.Payload.Operation,
		CycleID:         req.Payload.CycleID,
		ErrorMessage:    req.Payload.ErrorMessage,
		RetryCount:      req.Payload.RetryCount,
		SinkName:        req.SinkName,
		SinkType:        req.SinkType,
		Targets:         req.Payload.Targets,
		TargetExecution: req.Payload.TargetExecution,
	}
	if len(req.Payload.Targets) > 0 {
		sinkPayload.Targets = make([]TargetInfo, len(req.Payload.Targets))
		for i, t := range req.Payload.Targets {
			sinkPayload.Targets[i] = TargetInfo(t)
		}
	}

	// Resolve optional custom template from ConfigMap.
	var customTmpl *sink.CustomTemplate
	if req.TemplateRef != nil {
		tmplStr, tmplErr := d.resolveCustomTemplate(ctx, req.Payload.Plan.Namespace, *req.TemplateRef)
		if tmplErr != nil {
			log.Error(tmplErr, "failed to resolve custom template, falling back to sink default",
				"configMap", req.TemplateRef.Name,
				"key", req.TemplateRef.Key)
		} else {
			customTmpl = &sink.CustomTemplate{
				Content: tmplStr,
				Key: types.NamespacedName{
					Namespace: req.Payload.Plan.Namespace,
					Name:      req.TemplateRef.Name,
				},
			}
		}
	}

	sendOpts := sink.SendOptions{
		Config:         config,
		CustomTemplate: customTmpl,
		SinkState:      d.resolveSinkState(ctx, req),
		Log:            log,
	}

	// Send with buffer timeout. This is a safety net, not a budget:
	// the underlying HTTP client has its own per-call timeout ranging from 1s-30s.
	// The buffer must be generous enough for thread mode's multiple
	// API calls + rate limit waits, which can total 1-3 minutes under load.
	sendCtx, cancel := context.WithTimeout(ctx, d.dispatchTimeout)
	defer cancel()

	sendStart := time.Now()
	sendResult, sendErr := provider.Send(sendCtx, sinkPayload, sendOpts)
	sendDuration := time.Since(sendStart)
	states := mergeStates(sendOpts.SinkState, sendResult.States)

	totalDuration := time.Since(start)
	if sendErr != nil {
		log.Error(sendErr, "failed to send notification",
			"resolve_ms", totalDuration.Milliseconds()-sendDuration.Milliseconds(),
			"send_ms", sendDuration.Milliseconds(),
			"total_ms", totalDuration.Milliseconds())
		metrics.NotificationErrorsTotal.WithLabelValues(req.SinkType, req.Payload.Event).Inc()
		metrics.NotificationLatency.WithLabelValues(req.SinkType).Observe(totalDuration.Seconds())
		d.reportDelivery(req, false, sendErr, states)
		return
	}

	log.Info("notification sent successfully",
		"resolve_ms", totalDuration.Milliseconds()-sendDuration.Milliseconds(),
		"send_ms", sendDuration.Milliseconds(),
		"total_ms", totalDuration.Milliseconds())
	metrics.NotificationSentTotal.WithLabelValues(req.SinkType, req.Payload.Event).Inc()
	metrics.NotificationLatency.WithLabelValues(req.SinkType).Observe(totalDuration.Seconds())
	d.reportDelivery(req, true, nil, states)
}

// reportDelivery updates the in-memory cache synchronously and invokes the delivery callback
// for async persistence. The synchronous cache update ensures that immediately subsequent
// notifications in the same stream will see the updated state, preventing duplicate root
// messages in Slack thread mode.
func (d *Dispatcher) reportDelivery(req Request, success bool, err error, states map[string]string) {
	// Update cache synchronously so the next notification in the stream sees the state
	// immediately, before the async status update is persisted.
	if len(states) > 0 {
		statesCopy := make(map[string]string, len(states))
		maps.Copy(statesCopy, states)
		d.stateCache.Add(req.String(), statesCopy)
	}

	if d.deliveryCallback == nil {
		return
	}

	d.deliveryCallback(FromRequest(req).
		At(time.Now()).
		WithOutcome(success, err).
		WithStates(states))
}

func (d *Dispatcher) resolveSinkState(ctx context.Context, req Request) map[string]string {
	if req.NotificationRef.Name == "" {
		return nil
	}

	// Use atomic GetOrFetch to ensure that even when multiple notifications for the
	// same plan/cycle arrive simultaneously, only one fetches from the API and all
	// get the same result. This prevents race conditions where multiple goroutines
	// could read stale/inconsistent states from the API.
	states, err := d.stateCache.GetOrFetch(ctx, req.String(), func(fCtx context.Context) (map[string]string, error) {
		notif := new(hibernatorv1alpha1.HibernateNotification)
		if err := d.client.Get(fCtx, req.NotificationRef, notif); err != nil {
			return nil, err
		}

		if notif.Status.SinkStatuses != nil {
			ssKey := req.ShortName()
			if ss, ok := notif.Status.SinkStatuses[ssKey]; ok {
				if len(ss.States) == 0 {
					return nil, cache.ErrDontCache
				}
				state := make(map[string]string, len(ss.States))
				maps.Copy(state, ss.States)
				return state, nil
			}
		}
		return nil, cache.ErrDontCache
	})

	if err != nil {
		d.log.Error(err, "failed to resolve sink state from API",
			"notification", req.NotificationRef.String(),
			"sink", req.SinkName)
		return nil
	}

	return states
}

// mergeStates combines two state maps. Values in override take precedence over base.
// An empty string value in override deletes the corresponding key from the result.
// Returns nil when the merged result is empty.
func mergeStates(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		if v == "" {
			delete(out, k)
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// resolveCustomTemplate loads a Go template string from a ConfigMap.
func (d *Dispatcher) resolveCustomTemplate(ctx context.Context, namespace string, ref hibernatorv1alpha1.ObjectKeyReference) (string, error) {
	key := ptr.Deref(ref.Key, defaultTemplateKey)

	cm := new(corev1.ConfigMap)
	if err := d.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, cm); err != nil {
		return "", err
	}

	value, ok := cm.Data[key]
	if !ok {
		return "", fmt.Errorf("configmap %s/%s missing key %q", namespace, ref.Name, key)
	}

	return value, nil
}

// resolveSecret fetches the Secret from the informer cache and returns the "config" key value.
func (d *Dispatcher) resolveSecret(ctx context.Context, namespace string, ref hibernatorv1alpha1.ObjectKeyReference) ([]byte, error) {
	key := ptr.Deref(ref.Key, defaultSecretConfigKey)

	secret := new(corev1.Secret)
	if err := d.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, secret); err != nil {
		return nil, err
	}

	value, ok := secret.Data[key]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s missing key %q", namespace, ref.Name, key)
	}

	return value, nil
}
