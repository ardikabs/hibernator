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
	"github.com/ardikabs/hibernator/pkg/keyedworker"
)

const (
	// defaultSecretConfigKey is the well-known key inside the Secret that holds sink config.
	defaultSecretConfigKey = "config"

	// defaultTemplateKey is the well-known key inside the ConfigMap that holds the custom template string.
	defaultTemplateKey = "template.gotpl"

	// defaultDispatchTimeout is the per-sink HTTP call timeout.
	defaultDispatchTimeout = 5 * time.Second

	// defaultDrainTimeout is the maximum time to wait for workers to drain remaining items during shutdown.
	defaultDrainTimeout = 30 * time.Second

	// defaultWorkerIdleTTL is how long an idle per-stream worker stays alive.
	defaultWorkerIdleTTL = 30 * time.Minute

	// defaultChannelSize is the default buffered channel capacity for dispatch requests.
	defaultChannelSize = 256
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

	// readiness represents the completion of Start.
	// Submit waits on this to ensure Start has completed before accepting requests.
	readiness *sync.WaitGroup
}

type streamKey struct {
	Plan            types.NamespacedName
	CycleID         string
	NotificationRef types.NamespacedName
	SinkName        string
	SinkType        string
	Operation       string
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
func NewDispatcher(log logr.Logger, c client.Reader, registry *sink.Registry, cfg DispatcherConfig) *Dispatcher {
	cfg = cfg.withDefaults()
	d := &Dispatcher{
		log:             log,
		client:          c,
		registry:        registry,
		channelSize:     cfg.ChannelSize,
		dispatchTimeout: cfg.DispatchTimeout,
		workerIdleTTL:   cfg.WorkerIdleTTL,
		done:            make(chan struct{}),
		readiness:       new(sync.WaitGroup),
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

	d.readiness.Add(1)
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
	)

	d.pool.Register(ctx, d.workerFactory)
	d.log.V(1).Info("notification dispatcher running", "mode", "keyed-stream")
	d.readiness.Done()

	<-ctx.Done()
	d.log.V(1).Info("notification dispatcher initiating shutdown")
	close(d.done)
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

	d.log.Info("notification dispatcher stopped")
	return nil
}

// Submit enqueues a dispatch request. It never blocks the caller. Requests are
// routed into per-stream FIFO slots; if a per-stream buffer is full, the request
// is dropped by the slot and counted in NotificationDropTotal.
// After shutdown begins (d.done closed), requests are discarded with a metric.
func (d *Dispatcher) Submit(req Request) {
	d.readiness.Wait() // ensure Start has completed before accepting requests

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
// SendOptions with the Renderer and optional custom template, and delegates
// formatting + delivery to the sink.
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
		SinkState:      d.resolveSinkState(ctx, req.NotificationRef, req.SinkName, req.Payload.Plan.Namespace, req.Payload.Plan.Name, req.Payload.CycleID, req.Payload.Operation),
		Log:            log,
	}

	// Send with timeout.
	sendCtx, cancel := context.WithTimeout(ctx, d.dispatchTimeout)
	defer cancel()

	sendResult, sendErr := provider.Send(sendCtx, sinkPayload, sendOpts)
	states := mergeStates(sendOpts.SinkState, sendResult.States)
	if sendErr != nil {
		log.Error(sendErr, "failed to send notification")
		metrics.NotificationErrorsTotal.WithLabelValues(req.SinkType, req.Payload.Event).Inc()
		metrics.NotificationLatency.WithLabelValues(req.SinkType).Observe(time.Since(start).Seconds())
		d.reportDelivery(req, false, sendErr, states)
		return
	}

	log.Info("notification sent successfully")
	metrics.NotificationSentTotal.WithLabelValues(req.SinkType, req.Payload.Event).Inc()
	metrics.NotificationLatency.WithLabelValues(req.SinkType).Observe(time.Since(start).Seconds())
	d.reportDelivery(req, true, nil, states)
}

// reportDelivery invokes the delivery callback if configured.
func (d *Dispatcher) reportDelivery(req Request, success bool, err error, states map[string]string) {
	if d.deliveryCallback == nil {
		return
	}
	d.deliveryCallback(DeliveryResult{
		NotificationRef: req.NotificationRef,
		SinkName:        req.SinkName,
		PlanNamespace:   req.Payload.Plan.Namespace,
		PlanName:        req.Payload.Plan.Name,
		CycleID:         req.Payload.CycleID,
		Operation:       req.Payload.Operation,
		Timestamp:       time.Now(),
		Success:         success,
		Error:           err,
		States:          states,
	})
}

func (d *Dispatcher) resolveSinkState(ctx context.Context, notifRef types.NamespacedName, sinkName, planNamespace, planName, cycleID, operation string) map[string]string {
	if notifRef.Name == "" {
		return nil
	}

	notif := new(hibernatorv1alpha1.HibernateNotification)
	if err := d.client.Get(ctx, notifRef, notif); err != nil {
		return nil
	}

	if notif.Status.SinkStatuses != nil {
		key := SinkStatusKey(sinkName, planNamespace, planName, cycleID, operation)
		if ss, ok := notif.Status.SinkStatuses[key]; ok {
			if len(ss.States) == 0 {
				return nil
			}
			state := make(map[string]string, len(ss.States))
			maps.Copy(state, ss.States)
			return state
		}
	}

	return nil
}

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
