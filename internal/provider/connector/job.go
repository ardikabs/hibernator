/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package connector

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"strconv"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ardikabs/hibernator/internal/wellknown"
)

const (
	probeJobTTLSeconds   int32 = 300
	probeDeadlineGrace         = 90 * time.Second // covers image pull on cold nodes
	maxLabelValue              = 63
	labelHashLen               = 16
	probeJobGenerateName       = "connector-probe-"
)

// probeLabels is the single source of truth for probe Job labels and its selector.
func probeLabels(kind, namespace, name string) map[string]string {
	return map[string]string{
		wellknown.LabelConnectorProbe:     "true",
		wellknown.LabelConnectorKind:      labelValue(kind),
		wellknown.LabelConnectorNamespace: labelValue(namespace),
		wellknown.LabelConnectorName:      labelValue(name),
	}
}

// labelValue returns v when it is a valid label value, otherwise a truncated
// prefix plus a hash of the full value. The full value lives in annotations.
func labelValue(v string) string {
	if len(v) <= maxLabelValue {
		return v
	}
	sum := sha256.Sum256([]byte(v))
	h := hex.EncodeToString(sum[:])[:labelHashLen]
	prefix := v[:maxLabelValue-labelHashLen-1]
	return prefix + "-" + h
}

// ProbeJobSelector selects the probe Jobs of one connector.
func ProbeJobSelector(kind, namespace, name string) client.MatchingLabels {
	return client.MatchingLabels(probeLabels(kind, namespace, name))
}

// BuildProbeJob builds the one-shot Job that probes a connector. generation and
// secretVersion are stamped on the Job so its verdict is recorded for the
// connector state it actually probed.
func BuildProbeJob(cfg JobConfig, kind, namespace, name string, generation int64, secretVersion string) *batchv1.Job {
	deadline := int64((cfg.Timeout + probeDeadlineGrace).Seconds())

	annotations := map[string]string{
		wellknown.AnnotationConnectorName:          name,
		wellknown.AnnotationConnectorNamespace:     namespace,
		wellknown.AnnotationConnectorGeneration:    strconv.FormatInt(generation, 10),
		wellknown.AnnotationConnectorSecretVersion: secretVersion,
	}

	var owners []metav1.OwnerReference
	if cfg.Owner != nil && cfg.Owner.Name != "" && cfg.Owner.UID != "" {
		owners = []metav1.OwnerReference{{
			APIVersion: "v1",
			Kind:       "Pod",
			Name:       cfg.Owner.Name,
			UID:        cfg.Owner.UID,
		}}
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName:    probeJobGenerateName,
			Namespace:       cfg.Namespace,
			Labels:          probeLabels(kind, namespace, name),
			Annotations:     annotations,
			OwnerReferences: owners,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            ptr.To[int32](0),
			TTLSecondsAfterFinished: ptr.To[int32](probeJobTTLSeconds),
			ActiveDeadlineSeconds:   &deadline,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      probeLabels(kind, namespace, name),
					Annotations: maps.Clone(annotations),
				},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: cfg.RunnerServiceAccount,
					Containers: []corev1.Container{{
						Name:  "runner",
						Image: cfg.RunnerImage,
						Args:  []string{"--operation", "probe", "--timeout", cfg.Timeout.String()},
						Env: []corev1.EnvVar{
							{Name: "POD_NAMESPACE", Value: cfg.Namespace},
							{Name: "HIBERNATOR_CONNECTOR_KIND", Value: kind},
							{Name: "HIBERNATOR_CONNECTOR_NAME", Value: name},
							{Name: "HIBERNATOR_CONNECTOR_NAMESPACE", Value: namespace},
						},
					}},
				},
			},
		},
	}
}

// probeJobStamp returns the generation and secret version a probe Job was
// created for. A missing or invalid generation yields 0, which never matches a
// live connector, so the next reconcile probes again.
func probeJobStamp(job *batchv1.Job) (int64, string) {
	gen, err := strconv.ParseInt(job.Annotations[wellknown.AnnotationConnectorGeneration], 10, 64)
	if err != nil {
		gen = 0
	}
	return gen, job.Annotations[wellknown.AnnotationConnectorSecretVersion]
}
