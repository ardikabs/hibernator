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

	// defaultChannelSize is the default buffered channel capacity for dispatch requests.
	defaultChannelSize = 256

	// defaultWorkers is the default number of concurrent dispatch goroutines.
	defaultWorkers = 4

	// maxOverflowSize is the maximum number of requests allowed in the overflow queue before new requests are dropped.
	maxOverflowSize = 4096
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

	// ch is the primary buffered channel between Submit and workers.
	ch chan Request
	// done is closed when the dispatcher is shutting down.
	done chan struct{}

	// overflow is a mutex-protected unbounded queue for requests that
	// could not be sent to ch without blocking.
	mu       sync.Mutex
	overflow []Request
	// signal is a 1-buffered channel used to wake the drainer goroutine
	// when items are added to overflow.
	signal chan struct{}
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
		ch:              make(chan Request, cfg.ChannelSize),
		done:            make(chan struct{}),
		signal:          make(chan struct{}, 1),
	}
}

// NeedLeaderElection returns true — notifications should only fire from the leader.
func (d *Dispatcher) NeedLeaderElection() bool { return true }

// Notifier returns the Notifier interface backed by this Dispatcher.
// Consumers (plan processors, state handlers) should depend on this
// interface rather than on *Dispatcher directly.
func (d *Dispatcher) Notifier() Notifier { return d }

// Start implements manager.Runnable. It spawns the overflow drainer, worker
// goroutines, and blocks until ctx is cancelled. On shutdown, the drainer exits
// first, then remaining overflow is flushed into the channel, the channel is
// closed, and workers drain remaining items before returning.
func (d *Dispatcher) Start(ctx context.Context) error {
	d.log.Info("starting notification dispatcher", "workers", d.workers, "channelSize", d.channelSize)

	// Spawn overflow drainer.
	drainerDone := make(chan struct{})
	go func() {
		defer close(drainerDone)
		d.drainOverflow()
	}()

	// Spawn worker goroutines.
	workersDone := make(chan struct{})
	go func() {
		defer close(workersDone)
		d.runWorkers(ctx)
	}()

	// Block until context is cancelled.
	<-ctx.Done()

	// 1. Signal shutdown — stops drainer loop and makes Submit discard new requests.
	close(d.done)

	// 2. Wait for the drainer to fully exit so no goroutine races on ch or overflow.
	<-drainerDone

	// 3. Flush any remaining overflow items into ch.
	d.flushOverflow()

	// 4. Close ch so workers drain remaining items and exit.
	close(d.ch)
	<-workersDone

	d.log.Info("notification dispatcher stopped")
	return nil
}

// Submit enqueues a dispatch request. It never blocks the caller: if the buffered
// channel has room the request goes directly; otherwise it is appended to the
// internal overflow queue and the drainer will move it to the channel asynchronously.
// After shutdown, requests are discarded with a metric.
func (d *Dispatcher) Submit(req Request) {
	// Fast path — check shutdown first.
	select {
	case <-d.done:
		d.log.V(1).Info("notification dispatcher stopped, discarding request",
			"sink", req.SinkName, "sinkType", req.SinkType, "event", req.Payload.Event)
		metrics.NotificationDropTotal.WithLabelValues(req.SinkType, req.Payload.Event).Inc()
		return
	default:
	}

	// Try non-blocking send to the channel.
	select {
	case d.ch <- req:
		return
	default:
	}

	// Channel full — park in overflow queue (non-blocking for the caller).
	d.mu.Lock()
	if len(d.overflow) >= maxOverflowSize {
		d.mu.Unlock()
		d.log.Error(fmt.Errorf("overflow queue full, dropping notification request"),
			"id", req.Payload.ID,
			"sink", req.SinkName,
			"sinkType", req.SinkType,
			"event", req.Payload.Event,
		)
		metrics.NotificationDropTotal.WithLabelValues(req.SinkType, req.Payload.Event).Inc()
		return
	}
	d.overflow = append(d.overflow, req)
	d.mu.Unlock()

	// Wake the drainer (non-blocking signal).
	select {
	case d.signal <- struct{}{}:
	default: // already signaled
	}
}

// drainOverflow continuously moves requests from the overflow queue into ch.
// It exits when done is closed and the overflow is empty.
func (d *Dispatcher) drainOverflow() {
	for {
		select {
		case <-d.signal:
			d.transferOverflow()
		case <-d.done:
			return
		}
	}
}

// transferOverflow drains the current overflow slice into ch, blocking on each
// send so backpressure is absorbed by the drainer goroutine, not the caller.
func (d *Dispatcher) transferOverflow() {
	d.mu.Lock()
	batch := d.overflow
	d.overflow = nil
	d.mu.Unlock()

	for _, req := range batch {
		select {
		case d.ch <- req:
		case <-d.done:
			// Shutdown — put remaining items back for flushOverflow.
			d.mu.Lock()
			d.overflow = append(d.overflow, req)
			d.mu.Unlock()
			return
		}
	}
}

// flushOverflow moves any remaining overflow items into ch. Called during shutdown
// after done is closed and before ch is closed.
func (d *Dispatcher) flushOverflow() {
	d.mu.Lock()
	remaining := d.overflow
	d.overflow = nil
	d.mu.Unlock()

	for _, req := range remaining {
		d.ch <- req
	}
}

// runWorkers spawns d.workers goroutines that drain the dispatch channel.
// All goroutines exit when the channel is closed (after ctx cancellation).
func (d *Dispatcher) runWorkers(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < d.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for {
				select {
				case req, ok := <-d.ch:
					if !ok {
						return
					}

					dCtx := ctx
					var cancel context.CancelFunc
					select {
					case <-ctx.Done():
						dCtx, cancel = context.WithTimeout(context.Background(), d.dispatchTimeout)
						defer cancel()
					default:
					}

					d.dispatch(dCtx, req)

				case <-ctx.Done():
					return
				}
			}
		}()
	}
	wg.Wait()
}

// dispatch processes a single DispatchRequest: resolves credentials, builds
// SendOptions with the Renderer and optional custom template, and delegates
// formatting + delivery to the sink.
func (d *Dispatcher) dispatch(ctx context.Context, req Request) {
	start := time.Now()
	log := d.log.WithValues(
		"sink", req.SinkName,
		"sinkType", req.SinkType,
		"event", req.Payload.Event,
		"plan", req.Payload.ID,
	)

	// Look up the sink implementation.
	provider, ok := d.registry.Get(req.SinkType)
	if !ok {
		log.Error(fmt.Errorf("sink type %q not registered", req.SinkType), "unknown sink type")
		metrics.NotificationErrorsTotal.WithLabelValues(req.SinkType, req.Payload.Event).Inc()
		return
	}

	// Resolve sink credentials from Secret.
	config, err := d.resolveSecret(ctx, req.Payload.ID.Namespace, req.SecretRef)
	if err != nil {
		log.Error(err, "failed to resolve sink secret")
		metrics.NotificationErrorsTotal.WithLabelValues(req.SinkType, req.Payload.Event).Inc()
		return
	}

	// Build the sink payload from the notification request.
	sinkPayload := Payload{
		ID:            req.Payload.ID,
		Labels:        req.Payload.Labels,
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
		tmplStr, tmplErr := d.resolveCustomTemplate(ctx, req.Payload.ID.Namespace, *req.TemplateRef)
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
		return
	}

	log.Info("notification sent successfully")
	metrics.NotificationSentTotal.WithLabelValues(req.SinkType, req.Payload.Event).Inc()
	metrics.NotificationLatency.WithLabelValues(req.SinkType).Observe(time.Since(start).Seconds())
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
