/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// ExecutionDuration tracks the duration of hibernation/wakeup operations
	ExecutionDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "hibernator_execution_duration_seconds",
			Help:    "Duration of hibernation and wakeup operations",
			Buckets: prometheus.ExponentialBuckets(1, 2, 10), // 1s to ~17m
		},
		[]string{"operation", "target_type", "status"},
	)

	// ExecutionTotal counts total executions by operation and status
	ExecutionTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hibernator_execution_total",
			Help: "Total number of hibernation and wakeup operations",
		},
		[]string{"operation", "target_type", "status"},
	)

	// ReconcileTotal counts HibernatePlan reconciliation loops
	ReconcileTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hibernator_reconcile_total",
			Help: "Total number of HibernatePlan reconciliations",
		},
		[]string{"plan", "phase", "result"},
	)

	// ReconcileDuration tracks reconciliation duration
	ReconcileDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "hibernator_reconcile_duration_seconds",
			Help:    "Duration of HibernatePlan reconciliation",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"plan", "phase"},
	)

	// RestoreDataSize tracks the size of restore data
	RestoreDataSize = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "hibernator_restore_data_size_bytes",
			Help:    "Size of restore data in bytes",
			Buckets: prometheus.ExponentialBuckets(100, 2, 10), // 100B to ~100KB
		},
		[]string{"target_type"},
	)

	// ActivePlanGauge tracks the number of active HibernatePlans
	ActivePlanGauge = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "hibernator_active_plans",
			Help: "Number of active HibernatePlans by phase",
		},
		[]string{"phase"},
	)

	// JobsCreatedTotal counts Jobs created by the controller
	JobsCreatedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hibernator_jobs_created_total",
			Help: "Total number of runner Jobs created",
		},
		[]string{"plan", "target"},
	)

	// JobFailuresTotal counts Job failures
	JobFailuresTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hibernator_job_failures_total",
			Help: "Total number of runner Job failures",
		},
		[]string{"plan", "target"},
	)

	// StatusQueueDroppedTotal counts status updates dropped because the queue was full.
	// A non-zero value indicates the queue capacity (statusQueueCapacity) should be raised
	// or the writer worker pool should be enlarged.
	StatusQueueDroppedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hibernator_status_queue_dropped_total",
			Help: "Total number of status updates dropped due to a full status queue",
		},
		[]string{"queue"}, // "plan" or "exception"
	)

	// WatchableSubscribeTotal counts per-handler invocations on the internal watchable message bus.
	WatchableSubscribeTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hibernator_watchable_subscribe_total",
			Help: "Total number of watchable subscription handler invocations",
		},
		[]string{"runner", "message", "status"}, // status: success | error | panic
	)

	// WatchableSubscribeDuration tracks watchable handler processing time.
	WatchableSubscribeDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "hibernator_watchable_subscribe_duration_seconds",
			Help:    "Duration of watchable subscription handler processing",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"runner", "message"},
	)

	// WorkerGoroutinesGauge tracks the number of live Worker goroutines in the Coordinator.
	// Essential for capacity planning and debugging goroutine leaks.
	WorkerGoroutinesGauge = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "hibernator_worker_goroutines",
			Help: "Number of live plan Worker goroutines managed by the Coordinator",
		},
	)
)
