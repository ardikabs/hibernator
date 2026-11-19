/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package connector

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/connectorprobe"
)

const (
	reasonVerified = "Verified"
	msgNoExecutors = "no executors require access verification"
	msgGKE         = "GKE clusters are not probed yet"
	// probeFailedPrefix starts the status message of a probe that produced no verdict.
	probeFailedPrefix = "probe failed: "
)

// ResultFromTerminationMessage decodes a probe termination message. A result
// that is not ready and carries no reason is not a verdict and is an error.
func ResultFromTerminationMessage(msg string) (connectorprobe.Result, error) {
	res, err := connectorprobe.Unmarshal(msg)
	if err != nil {
		return connectorprobe.Result{}, err
	}
	if !res.Ready && res.Reason == "" {
		return connectorprobe.Result{}, errors.New("probe result has no verdict")
	}
	return res, nil
}

func cloudProviderView(s *hibernatorv1alpha1.CloudProviderStatus) statusView {
	return statusView{&s.Ready, &s.Message, &s.LastValidated, &s.LastProbeTime, &s.ObservedGeneration,
		&s.ObservedSecretVersion, &s.Conditions, &s.AllowedExecutors, &s.ExecutorAccess}
}

func k8sClusterView(s *hibernatorv1alpha1.K8SClusterStatus) statusView {
	return statusView{&s.Ready, &s.Message, &s.LastValidated, &s.LastProbeTime, &s.ObservedGeneration,
		&s.ObservedSecretVersion, &s.Conditions, &s.AllowedExecutors, &s.ExecutorAccess}
}

// ApplyResult records a probe verdict on a CloudProvider.
func ApplyResult(res connectorprobe.Result, now metav1.Time, generation int64, secretVersion string) func(*hibernatorv1alpha1.CloudProvider) {
	return func(cp *hibernatorv1alpha1.CloudProvider) {
		applyResult(cloudProviderView(&cp.Status), res, now, generation, secretVersion)
	}
}

// ApplyK8SResult records a probe verdict on a K8SCluster. clusterType is derived
// from spec by the reconciler and is not written here.
func ApplyK8SResult(res connectorprobe.Result, now metav1.Time, generation int64, secretVersion string) func(*hibernatorv1alpha1.K8SCluster) {
	return func(kc *hibernatorv1alpha1.K8SCluster) {
		applyResult(k8sClusterView(&kc.Status), res, now, generation, secretVersion)
	}
}

// ApplyProbeFailed records that the probe produced no verdict on a CloudProvider.
// The ready bool cannot express Unknown, so it is set false; the executor lists
// are cleared and lastValidated keeps the last verdict time.
func ApplyProbeFailed(now metav1.Time, generation int64, secretVersion, msg string) func(*hibernatorv1alpha1.CloudProvider) {
	return func(cp *hibernatorv1alpha1.CloudProvider) {
		applyProbeFailed(cloudProviderView(&cp.Status), now, generation, secretVersion, msg)
	}
}

// ApplyK8SProbeFailed is ApplyProbeFailed for a K8SCluster.
func ApplyK8SProbeFailed(now metav1.Time, generation int64, secretVersion, msg string) func(*hibernatorv1alpha1.K8SCluster) {
	return func(kc *hibernatorv1alpha1.K8SCluster) {
		applyProbeFailed(k8sClusterView(&kc.Status), now, generation, secretVersion, msg)
	}
}

// ApplyK8SProviderNotReady records that the referenced CloudProvider is missing
// or not ready. status is the Ready condition status: False when the provider is
// missing or failed, Unknown when its own state is unknown. No probe ran, so
// lastProbeTime and lastValidated are kept.
func ApplyK8SProviderNotReady(generation int64, status metav1.ConditionStatus, msg string) func(*hibernatorv1alpha1.K8SCluster) {
	return func(kc *hibernatorv1alpha1.K8SCluster) {
		v := k8sClusterView(&kc.Status)
		reason := hibernatorv1alpha1.ReasonProviderNotReady
		clearVerdict(v, generation, msg)
		setCondition(v, hibernatorv1alpha1.ConditionReady, status, reason, msg, generation)
		setCondition(v, hibernatorv1alpha1.ConditionAuthValid, metav1.ConditionUnknown, reason, msg, generation)
		setCondition(v, hibernatorv1alpha1.ConditionExecutorAccessVerified, metav1.ConditionUnknown, reason, msg, generation)
	}
}

// ApplyK8SProbeUnsupported records that the cluster type is not probed. It
// writes no timestamps, so repeated writes are no-ops.
func ApplyK8SProbeUnsupported(generation int64) func(*hibernatorv1alpha1.K8SCluster) {
	return func(kc *hibernatorv1alpha1.K8SCluster) {
		v := k8sClusterView(&kc.Status)
		reason := hibernatorv1alpha1.ReasonProbeUnsupported
		clearVerdict(v, generation, msgGKE)
		for _, typ := range []string{hibernatorv1alpha1.ConditionReady, hibernatorv1alpha1.ConditionAuthValid,
			hibernatorv1alpha1.ConditionExecutorAccessVerified} {
			setCondition(v, typ, metav1.ConditionUnknown, reason, msgGKE, generation)
		}
	}
}

// clearVerdict marks the connector not ready and drops the executor lists.
func clearVerdict(v statusView, gen int64, msg string) {
	*v.ready = false
	*v.message = msg
	*v.observedGeneration = gen
	*v.allowedExecutors = nil
	*v.executorAccess = nil
}

// ClusterTypeOf derives the cluster type from the K8SCluster spec.
func ClusterTypeOf(spec hibernatorv1alpha1.K8SClusterSpec) hibernatorv1alpha1.K8SClusterType {
	switch {
	case spec.EKS != nil:
		return hibernatorv1alpha1.ClusterTypeEKS
	case spec.GKE != nil:
		return hibernatorv1alpha1.ClusterTypeGKE
	case spec.K8S != nil:
		return hibernatorv1alpha1.ClusterTypeK8S
	}
	return ""
}

func applyResult(v statusView, res connectorprobe.Result, now metav1.Time, gen int64, secretVersion string) {
	t := now
	*v.ready = res.Ready
	*v.message = res.Message
	*v.lastValidated = &t
	lp := now
	*v.lastProbeTime = &lp
	*v.observedGeneration = gen
	*v.observedSecretVersion = secretVersion

	readyStatus, readyReason := metav1.ConditionTrue, hibernatorv1alpha1.ReasonReady
	if !res.Ready {
		readyStatus, readyReason = metav1.ConditionFalse, res.Reason
	}
	setCondition(v, hibernatorv1alpha1.ConditionReady, readyStatus, readyReason, res.Message, gen)
	setCondition(v, hibernatorv1alpha1.ConditionAuthValid, readyStatus, readyReason, res.Message, gen)

	*v.allowedExecutors = nil
	*v.executorAccess = nil

	switch {
	case !res.Ready:
		setCondition(v, hibernatorv1alpha1.ConditionExecutorAccessVerified, metav1.ConditionUnknown,
			hibernatorv1alpha1.ReasonAuthFailed, "connector authentication failed", gen)
	case !res.AccessVerified:
		setCondition(v, hibernatorv1alpha1.ConditionExecutorAccessVerified, metav1.ConditionFalse,
			res.AccessReason, res.AccessMessage, gen)
	case res.EligibleExecutors == 0 && len(res.ExecutorAccess) == 0:
		setCondition(v, hibernatorv1alpha1.ConditionExecutorAccessVerified, metav1.ConditionTrue,
			hibernatorv1alpha1.ReasonNoExecutorsToVerify, msgNoExecutors, gen)
	case res.CheckedExecutors == 0 && len(res.ExecutorAccess) == 0:
		setCondition(v, hibernatorv1alpha1.ConditionExecutorAccessVerified, metav1.ConditionFalse,
			hibernatorv1alpha1.ReasonUnverified,
			fmt.Sprintf("access could not be verified for %d executor(s); no entries recorded", res.EligibleExecutors), gen)
	default:
		setCondition(v, hibernatorv1alpha1.ConditionExecutorAccessVerified, metav1.ConditionTrue,
			reasonVerified, verifiedMessage(res), gen)
		*v.executorAccess = slices.Clone(res.ExecutorAccess)
		var allowed []string
		for _, e := range res.ExecutorAccess {
			if e.Allowed {
				allowed = append(allowed, e.Type)
			}
		}
		slices.Sort(allowed)
		*v.allowedExecutors = allowed
	}
}

// verifiedMessage names how many eligible executors were checked when not all were.
func verifiedMessage(res connectorprobe.Result) string {
	checked := res.CheckedExecutors
	if checked == 0 {
		checked = len(res.ExecutorAccess)
	}
	if res.EligibleExecutors == 0 || checked >= res.EligibleExecutors {
		return res.AccessMessage
	}
	msg := fmt.Sprintf("%d of %d executors verified; executors without an entry are unknown", checked, res.EligibleExecutors)
	if res.AccessMessage != "" {
		msg += " (" + res.AccessMessage + ")"
	}
	return msg
}

func applyProbeFailed(v statusView, now metav1.Time, gen int64, secretVersion, msg string) {
	lp := now
	*v.lastProbeTime = &lp
	*v.observedSecretVersion = secretVersion
	clearVerdict(v, gen, probeFailedPrefix+msg)
	for _, typ := range []string{hibernatorv1alpha1.ConditionReady, hibernatorv1alpha1.ConditionAuthValid,
		hibernatorv1alpha1.ConditionExecutorAccessVerified} {
		setCondition(v, typ, metav1.ConditionUnknown, hibernatorv1alpha1.ReasonProbeFailed, msg, gen)
	}
}

func setCondition(v statusView, typ string, status metav1.ConditionStatus, reason, msg string, gen int64) {
	if reason == "" {
		reason = hibernatorv1alpha1.ReasonProbeError
	}
	meta.SetStatusCondition(v.conditions, metav1.Condition{
		Type: typ, Status: status, Reason: reason, Message: msg, ObservedGeneration: gen,
	})
}

// ProbeDue reports whether a connector needs a new probe.
func ProbeDue(lastProbe *metav1.Time, observedGen, gen int64, observedSecret, secret string, resync time.Duration, now time.Time, jitter func() time.Duration) bool {
	if lastProbe == nil || observedGen != gen || observedSecret != secret {
		return true
	}
	return !now.Before(lastProbe.Add(resync + jitter()))
}
