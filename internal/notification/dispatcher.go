/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/metrics"
	"github.com/ardikabs/hibernator/internal/notification/sink"
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

	// defaultChannelSize is the default buffered channel capacity for dispatch requests.
	defaultChannelSize = 256

	// defaultWorkers is the default number of concurrent dispatch goroutines.
	defaultWorkers = 4

	// defaultMaxOverflowSize is the default maximum number of requests allowed in the overflow queue before new requests are dropped.
	defaultMaxOverflowSize = 4096
)

// DispatcherConfig holds tuning knobs for the notification Dispatcher.
// Zero values are replaced with sensible defaults.
type DispatcherConfig struct {
	// ChannelSize is the buffered channel capacity for dispatch requests.
	// Default: 256.
	ChannelSize int

	// Workers is the number of concurrent dispatch goroutines.
	// Default: 4.
	Workers int

	// DispatchTimeout is the per-sink HTTP call timeout.
	// Default: 5s.
	DispatchTimeout time.Duration

	// MaxOverflowSize is the maximum number of requests allowed in the overflow queue before new requests are dropped.
	// Default: 4096.
	MaxOverflowSize int
}

// withDefaults returns a copy with zero fields replaced by defaults.
func (c DispatcherConfig) withDefaults() DispatcherConfig {
	if c.ChannelSize <= 0 {
		c.ChannelSize = defaultChannelSize
	}
	if c.Workers <= 0 {
		c.Workers = defaultWorkers
	}
	if c.DispatchTimeout <= 0 {
		c.DispatchTimeout = defaultDispatchTimeout
	}
	if c.MaxOverflowSize <= 0 {
		c.MaxOverflowSize = defaultMaxOverflowSize
	}
	return c
}

// Dispatcher is a standalone controller-runtime Runnable that processes notification
// dispatch requests asynchronously. Hook closures submit Requests via Submit, which
// returns immediately (fire-and-forget). Requests flow through a buffered channel to
// a pool of worker goroutines; when the channel is full, an internal overflow queue
// absorbs the excess and a dedicated drainer goroutine feeds items back into the
// channel as space becomes available. The caller never blocks and no request is dropped
// during normal operation.
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
	channelSize     int
	workers         int
	dispatchTimeout time.Duration
	maxOverflowSize int

	// deliveryCallback is called after each dispatch attempt to report success/failure.
	// Nil means no delivery tracking.
	deliveryCallback DeliveryCallback

	// requestCh is the primary buffered channel between Submit and workers.
	// It is NEVER closed — workers exit via the allFlushed signal instead,
	// which avoids send-on-closed-channel panics from concurrent Submit calls.
	requestCh chan Request

	// done is closed when the dispatcher begins shutting down.
	// Submit checks this via non-blocking select for the fast-path discard.
	done chan struct{}

	// drained is closed after the drainer goroutine has fully stopped and returned
	// any undrained items back to the overflow queue. This signals Start() that
	// it's safe to flush the overflow queue into the channel without racing with the drainer.
	drained chan struct{}

	// flushed is closed after overflow has been fully flushed into requestCh.
	// Workers switch from their main loop to a drain loop once this fires.
	flushed chan struct{}

	// overflow holds requests that couldn't fit in the channel when submitted.
	//
	// The overflow is concurrency-safe internally, but the dispatcher ensures that only the drainer goroutine mutates it,
	// so no external locking is needed when the drainer appends or moves items back to the channel.
	overflow *Overflow[Request]

	// drainSignal is a 1-buffered channel used to wake the drainer goroutine
	// when items are added to overflow.
	drainSignal chan struct{}
}

// NewDispatcher creates a new NotificationDispatcher.
// The client should be the cached reader (informer cache) for Secret lookups.
func NewDispatcher(log logr.Logger, c client.Reader, registry *sink.Registry, cfg DispatcherConfig) *Dispatcher {
	cfg = cfg.withDefaults()
	return &Dispatcher{
		log:             log,
		client:          c,
		registry:        registry,
		channelSize:     cfg.ChannelSize,
		workers:         cfg.Workers,
		dispatchTimeout: cfg.DispatchTimeout,
		maxOverflowSize: cfg.MaxOverflowSize,
		overflow:        new(Overflow[Request]),
		requestCh:       make(chan Request, cfg.ChannelSize),
		done:            make(chan struct{}),
		drained:         make(chan struct{}),
		flushed:         make(chan struct{}),
		drainSignal:     make(chan struct{}, 1),
	}
}

// NeedLeaderElection returns true — notifications should only fire from the leader.
func (d *Dispatcher) NeedLeaderElection() bool { return true }

// Notifier returns the Notifier interface backed by this Dispatcher.
// Consumers (plan processors, state handlers) should depend on this
// interface rather than on *Dispatcher directly.
func (d *Dispatcher) Notifier() Notifier { return d }

// Start implements manager.Runnable. It spawns the overflow drainer, worker
// goroutines, and blocks until ctx is cancelled. On shutdown it ensures all
// in-flight and overflow items are delivered before returning.
//
// Shutdown sequence:
//  1. close(d.done)         — Submit fast-path discards new requests
//  2. d.shuttingDown = true — Submit overflow-path discards too
//  3. Stop drainer, wait    — drainer returns unsent items to overflow
//  4. Flush overflow → ch   — workers are still consuming from ch
//  5. close(d.allFlushed)   — workers switch to drain-and-exit mode
//  6. d.wg.Wait()           — all workers finish
//
// requestCh is never closed, so Submit can never panic with a
// send-on-closed-channel even in a tiny race window.
func (d *Dispatcher) Start(ctx context.Context) error {
	d.log.Info("starting notification dispatcher", "workers", d.workers, "channelSize", d.channelSize)

	// Start workers and overflow drainer.
	go d.run(ctx)

	d.log.V(1).Info("notification dispatcher running", "workers", d.workers, "channelSize", d.channelSize)
	// Block until context is cancelled.
	<-ctx.Done()

	// --- Shutdown sequence ---
	d.log.V(1).Info("notification dispatcher initiating shutdown")

	// Signal shutdown to drainer and prevent Submit from accepting new requests.
	close(d.done)

	d.log.V(1).Info("notification dispatcher shutting down, waiting for flush",
		"workers", d.workers,
		"channelSize", len(d.requestCh),
		"remainingOverflow", d.overflow.Len(),
	)
	// At this point, workers goroutine might still performing flush from the remaining
	// requests in the channel, once it dones, it will close the d.flushed channel
	// to signal that all items have been flushed and processed regardless of the status.
	<-d.flushed

	d.log.Info("notification dispatcher stopped")
	return nil
}

// Submit enqueues a dispatch request. It never blocks the caller: if the buffered
// channel has room the request goes directly; otherwise it is appended to the
// internal overflow queue and the drainer will move it to the channel asynchronously.
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

	// Try non-blocking send to the channel. No lock needed here because
	// requestCh is never closed — the worst case is sending one extra item
	// after shutdown, which workers will drain.
	select {
	case d.requestCh <- req:
		return
	default:
	}

	if d.overflow.Len() >= d.maxOverflowSize {
		log.Info("overflow queue full, dropping notification request")
		metrics.NotificationDropTotal.WithLabelValues(req.SinkType, req.Payload.Event).Inc()
		return
	}

	log.V(1).Info("request channel full, adding notification request to overflow")
	d.overflow.Append(req)

	// Wake the drainer (non-blocking signal).
	select {
	case d.drainSignal <- struct{}{}:
	default: // already signaled
	}
}

// overflowDrainerLoop runs in a goroutine, waiting for signals to move items
// from the overflow queue to the main channel.
// Exits when drainStop is closed (shutdown).
func (d *Dispatcher) overflowDrainerLoop() {
	d.log.Info("notification overflow drainer started")

	for {
		select {
		case <-d.drainSignal:
			if interrupted := d.drainOverflow(); interrupted {
				d.log.V(1).Info("notification overflow drainer interrupted during shutdown, returning remaining items to overflow",
					"remainingOverflow", d.overflow.Len())
			}
		case <-d.done:
			d.log.Info("notification overflow drainer stopped")
			close(d.drained)
			return
		}
	}
}

// drainOverflow moves all items currently in the overflow queue into the main channel.
// It sends items to the channel in a select with d.drainStop so it can be
// interrupted during shutdown — any unsent items are returned to the overflow
// queue for the final flush in Start().
func (d *Dispatcher) drainOverflow() bool {
	var interrupted bool

	d.overflow.Consume(func(ctx context.Context, req Request) bool {
		select {
		case d.requestCh <- req:
			return true

			// Context canceled after 5 seconds
		case <-ctx.Done():
			return false

			// Dispatcher shutdown is signaled via d.done,
			// but we also need to listen to ctx.Done() here to avoid blocking on send if the dispatcher is trying to shut down while we're draining overflow. This allows the drainer to exit promptly without getting stuck trying to send on a full channel during shutdown.
		case <-d.done:
			interrupted = true
			return false
		}
	})

	return interrupted
}

// run starts the worker goroutines responsible for processing dispatch requests
// from the channel, including the overflow drainer.
//
// On shutdown, workers exit their loop when ctx is cancelled. After all workers
// have stopped, run() waits for the drainer to finish and then drains both the
// request channel and the overflow queue in a single goroutine. This avoids
// duplicate dispatches that would occur if multiple workers each independently
// drained the overflow, and ensures channel items are not lost.
func (d *Dispatcher) run(ctx context.Context) {
	go d.overflowDrainerLoop()

	var activeWorkers sync.WaitGroup
	for i := 0; i < d.workers; i++ {
		activeWorkers.Add(1)
		go func(workerID int) {
			defer activeWorkers.Done()
			d.log.Info("notification worker started", "workerID", workerID)

			for {
				select {
				case req := <-d.requestCh:
					d.dispatch(ctx, req)

				case <-ctx.Done():
					d.log.Info("notification worker stopped", "workerID", workerID)
					return
				}
			}
		}(i + 1)
	}

	activeWorkers.Wait()

	// Wait for the drainer to finish and return any undrained items back to overflow.
	<-d.drained

	d.log.V(1).Info("draining remaining requests after shutdown",
		"channelLen", len(d.requestCh),
		"remainingOverflow", d.overflow.Len(),
	)

	dCtx, dCancel := context.WithTimeout(context.Background(), defaultDrainTimeout)
	defer dCancel()

	// Drain any items left in the request channel.
drainChannel:
	for {
		select {
		case req := <-d.requestCh:
			d.dispatch(dCtx, req)
		default:
			break drainChannel
		}
	}

	// Drain remaining overflow items.
	d.overflow.Range(func(r Request) {
		d.dispatch(dCtx, r)
	})

	close(d.flushed)
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
	config, err := d.resolveSecret(ctx, req.Payload.Plan.Namespace, req.SecretRef)
	if err != nil {
		log.Error(err, "failed to resolve sink secret")
		metrics.NotificationErrorsTotal.WithLabelValues(req.SinkType, req.Payload.Event).Inc()
		return
	}

	// Build the sink payload from the notification request.
	sinkPayload := Payload{
		Plan:          req.Payload.Plan,
		Event:         req.Payload.Event,
		Timestamp:     req.Payload.Timestamp,
		Phase:         req.Payload.Phase,
		PreviousPhase: req.Payload.PreviousPhase,
		Operation:     req.Payload.Operation,
		CycleID:       req.Payload.CycleID,
		ErrorMessage:  req.Payload.ErrorMessage,
		RetryCount:    req.Payload.RetryCount,
		SinkName:      req.SinkName,
		SinkType:      req.SinkType,
		Targets:       req.Payload.Targets,
	}
	if len(req.Payload.Targets) > 0 {
		sinkPayload.Targets = make([]TargetInfo, len(req.Payload.Targets))
		for i, t := range req.Payload.Targets {
			sinkPayload.Targets[i] = TargetInfo(t)
		}
	}

	// Resolve optional custom template from ConfigMap.
	var customTmpl *string
	if req.TemplateRef != nil {
		tmplStr, tmplErr := d.resolveCustomTemplate(ctx, req.Payload.Plan.Namespace, *req.TemplateRef)
		if tmplErr != nil {
			log.Error(tmplErr, "failed to resolve custom template, falling back to sink default",
				"configMap", req.TemplateRef.Name,
				"key", req.TemplateRef.Key)
		} else {
			customTmpl = &tmplStr
		}
	}

	sendOpts := sink.SendOptions{
		Config:         config,
		CustomTemplate: customTmpl,
	}

	// Send with timeout.
	sendCtx, cancel := context.WithTimeout(ctx, d.dispatchTimeout)
	defer cancel()

	if err := provider.Send(sendCtx, sinkPayload, sendOpts); err != nil {
		log.Error(err, "failed to send notification")
		metrics.NotificationErrorsTotal.WithLabelValues(req.SinkType, req.Payload.Event).Inc()
		metrics.NotificationLatency.WithLabelValues(req.SinkType).Observe(time.Since(start).Seconds())
		d.reportDelivery(req, false, err)
		return
	}

	log.Info("notification sent successfully")
	metrics.NotificationSentTotal.WithLabelValues(req.SinkType, req.Payload.Event).Inc()
	metrics.NotificationLatency.WithLabelValues(req.SinkType).Observe(time.Since(start).Seconds())
	d.reportDelivery(req, true, nil)
}

// reportDelivery invokes the delivery callback if configured.
func (d *Dispatcher) reportDelivery(req Request, success bool, err error) {
	if d.deliveryCallback == nil {
		return
	}
	d.deliveryCallback(DeliveryResult{
		NotificationRef: req.NotificationRef,
		SinkName:        req.SinkName,
		Timestamp:       time.Now(),
		Success:         success,
		Error:           err,
	})
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
