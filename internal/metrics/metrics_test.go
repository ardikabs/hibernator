/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package metrics

import (
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

func TestExecutionDuration_Defined(t *testing.T) {
	if ExecutionDuration == nil {
		t.Error("ExecutionDuration should not be nil")
	}
}

func TestExecutionTotal_Defined(t *testing.T) {
	if ExecutionTotal == nil {
		t.Error("ExecutionTotal should not be nil")
	}
}

func TestReconcileTotal_Defined(t *testing.T) {
	if ReconcileTotal == nil {
		t.Error("ReconcileTotal should not be nil")
	}
}

func TestReconcileDuration_Defined(t *testing.T) {
	if ReconcileDuration == nil {
		t.Error("ReconcileDuration should not be nil")
	}
}

func TestActivePlanGauge_Defined(t *testing.T) {
	if ActivePlanGauge == nil {
		t.Error("ActivePlanGauge should not be nil")
	}
}

func TestJobsCreatedTotal_Defined(t *testing.T) {
	if JobsCreatedTotal == nil {
		t.Error("JobsCreatedTotal should not be nil")
	}
}

func TestJobFailuresTotal_Defined(t *testing.T) {
	if JobFailuresTotal == nil {
		t.Error("JobFailuresTotal should not be nil")
	}
}

func TestExecutionDuration_Labels(t *testing.T) {
	// Test that we can observe with the correct labels
	observer, err := ExecutionDuration.GetMetricWithLabelValues("my-plan", "shutdown", "eks", "success")
	if err != nil {
		t.Fatalf("Failed to get metric with labels: %v", err)
	}
	if observer == nil {
		t.Error("Observer should not be nil")
	}

	// Observe a value
	observer.Observe(30.5)
}

func TestExecutionTotal_Labels(t *testing.T) {
	counter, err := ExecutionTotal.GetMetricWithLabelValues("my-plan", "wakeup", "rds", "failed")
	if err != nil {
		t.Fatalf("Failed to get metric with labels: %v", err)
	}
	if counter == nil {
		t.Error("Counter should not be nil")
	}

	// Increment the counter
	counter.Inc()
}

func TestReconcileTotal_Labels(t *testing.T) {
	counter, err := ReconcileTotal.GetMetricWithLabelValues("my-plan", "Hibernating", "success")
	if err != nil {
		t.Fatalf("Failed to get metric with labels: %v", err)
	}
	if counter == nil {
		t.Error("Counter should not be nil")
	}
}

func TestReconcileDuration_Labels(t *testing.T) {
	observer, err := ReconcileDuration.GetMetricWithLabelValues("test-plan", "Active")
	if err != nil {
		t.Fatalf("Failed to get metric with labels: %v", err)
	}
	if observer == nil {
		t.Error("Observer should not be nil")
	}
}

func TestActivePlanGauge_Labels(t *testing.T) {
	gauge, err := ActivePlanGauge.GetMetricWithLabelValues("Hibernated")
	if err != nil {
		t.Fatalf("Failed to get metric with labels: %v", err)
	}
	if gauge == nil {
		t.Error("Gauge should not be nil")
	}

	// Set a value
	gauge.Set(5)
}

func TestJobsCreatedTotal_Labels(t *testing.T) {
	counter, err := JobsCreatedTotal.GetMetricWithLabelValues("prod-plan", "eks-cluster")
	if err != nil {
		t.Fatalf("Failed to get metric with labels: %v", err)
	}
	if counter == nil {
		t.Error("Counter should not be nil")
	}
}

func TestJobFailuresTotal_Labels(t *testing.T) {
	counter, err := JobFailuresTotal.GetMetricWithLabelValues("staging-plan", "rds-db")
	if err != nil {
		t.Fatalf("Failed to get metric with labels: %v", err)
	}
	if counter == nil {
		t.Error("Counter should not be nil")
	}
}

func TestMetricsRegistration(t *testing.T) {
	// Verify every metric is registered with controller-runtime's registry.
	//
	// Gather() only returns Vec metrics that have had at least one observation,
	// so we probe via Registry.Register(): if it returns AlreadyRegisteredError
	// the metric was pre-registered by promauto.With(ctrlmetrics.Registry) at
	// init time. A nil error means the metric was absent (we unregister the
	// accidental registration and fail the test).
	collectors := []prometheus.Collector{
		ExecutionDuration,
		ExecutionTotal,
		ReconcileTotal,
		ReconcileDuration,
		ActivePlanGauge,
		JobsCreatedTotal,
		JobFailuresTotal,
		WatchableSubscribeTotal,
		WatchableSubscribeDuration,
		WorkerGoroutinesGauge,
		StatusWriterActiveGauge,
		StatusWriterUpdatesTotal,
		StatusWriterNoopTotal,
		StatusWriterErrorsTotal,
	}

	for _, c := range collectors {
		err := ctrlmetrics.Registry.Register(c)
		if err == nil {
			t.Errorf("collector %T was not pre-registered in ctrlmetrics.Registry", c)
			ctrlmetrics.Registry.Unregister(c)
			continue
		}
		var are prometheus.AlreadyRegisteredError
		if !errors.As(err, &are) {
			t.Errorf("unexpected error registering %T: %v", c, err)
		}
	}
}

func TestExecutionDuration_Buckets(t *testing.T) {
	// Verify the histogram works with labels
	// ExponentialBuckets(1, 2, 10) = 1, 2, 4, 8, 16, 32, 64, 128, 256, 512
	observer, err := ExecutionDuration.GetMetricWithLabelValues("my-plan", "test", "test", "test")
	if err != nil {
		t.Errorf("Should be able to get metric with labels: %v", err)
	}
	if observer == nil {
		t.Error("Observer should not be nil")
	}
}
