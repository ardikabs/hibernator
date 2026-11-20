/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package connector

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/connectorprobe"
)

// probeTarget is the kind-independent view of one connector for a reconcile.
type probeTarget struct {
	kind string
	key  types.NamespacedName
	// owner is the connector itself, set on the probe Job.
	owner          metav1.OwnerReference
	generation     int64
	lastProbe      *metav1.Time
	observedGen    int64
	observedSecret string
	// forceDue probes regardless of lastProbe, e.g. after the provider recovered.
	forceDue   bool
	secretRefs []types.NamespacedName
	// referenced are the executor types that active plans use on this connector.
	// The reconciler fills it from Strategy.Usage after Prepare.
	referenced []string
	// sendResult and sendFailed take the generation and secret version stamped
	// on the probe Job, not the current ones. sendResult scopes the access verdict
	// to the referenced executor types.
	sendResult func(res connectorprobe.Result, now metav1.Time, generation int64, secretVersion string, referenced []string)
	sendFailed func(now metav1.Time, generation int64, secretVersion, msg string)
	// sendReferenced rescopes an already recorded access verdict when the
	// referenced executor types change, without a new probe. Optional.
	sendReferenced func(generation int64, referenced []string)
}

// JobConfig configures the connector probe Job.
type JobConfig struct {
	RunnerImage          string
	RunnerServiceAccount string
	Timeout              time.Duration
}

// statusView points at the status fields shared by CloudProvider and K8SCluster.
type statusView struct {
	ready                 *bool
	message               *string
	lastValidated         **metav1.Time
	lastProbeTime         **metav1.Time
	observedGeneration    *int64
	observedSecretVersion *string
	conditions            *[]metav1.Condition
	allowedExecutors      *[]string
	executorAccess        *[]hibernatorv1alpha1.ExecutorAccess
}

// CloudProviderStrategy probes CloudProvider connectors.
type CloudProviderStrategy struct{}

// K8SClusterStrategy probes K8SCluster connectors.
type K8SClusterStrategy struct{}
