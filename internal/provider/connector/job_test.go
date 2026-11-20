/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package connector

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/ardikabs/hibernator/internal/wellknown"
)

var testCfg = JobConfig{
	Namespace:            "hibernator-system",
	RunnerImage:          "runner:v1",
	RunnerServiceAccount: "hibernator-runner",
	Timeout:              90 * time.Second,
}

func TestBuildProbeJob(t *testing.T) {
	job := BuildProbeJob(testCfg, "CloudProvider", "team-a", "aws-prod", 7, "rv1,missing")

	assert.Equal(t, "hibernator-system", job.Namespace)
	assert.Equal(t, "connector-probe-", job.GenerateName)
	require.NotNil(t, job.Spec.BackoffLimit)
	assert.Equal(t, int32(0), *job.Spec.BackoffLimit)
	require.NotNil(t, job.Spec.TTLSecondsAfterFinished)
	assert.Equal(t, int32(300), *job.Spec.TTLSecondsAfterFinished)
	require.NotNil(t, job.Spec.ActiveDeadlineSeconds)
	assert.Equal(t, int64(180), *job.Spec.ActiveDeadlineSeconds)

	pod := job.Spec.Template.Spec
	assert.Equal(t, "Never", string(pod.RestartPolicy))
	assert.Equal(t, "hibernator-runner", pod.ServiceAccountName)
	assert.Empty(t, pod.Volumes)
	require.Len(t, pod.Containers, 1)
	c := pod.Containers[0]
	assert.Equal(t, "runner", c.Name)
	assert.Equal(t, "runner:v1", c.Image)
	assert.Equal(t, []string{"--operation", "probe", "--timeout", "1m30s"}, c.Args)
	assert.Empty(t, c.VolumeMounts)

	env := map[string]string{}
	for _, e := range c.Env {
		env[e.Name] = e.Value
	}
	assert.Equal(t, map[string]string{
		"POD_NAMESPACE":                  "hibernator-system",
		"HIBERNATOR_CONNECTOR_KIND":      "CloudProvider",
		"HIBERNATOR_CONNECTOR_NAME":      "aws-prod",
		"HIBERNATOR_CONNECTOR_NAMESPACE": "team-a",
	}, env)

	assert.Equal(t, "CloudProvider", job.Labels[wellknown.LabelConnectorKind])
	assert.Equal(t, "aws-prod", job.Labels[wellknown.LabelConnectorName])
	assert.Equal(t, "team-a", job.Labels[wellknown.LabelConnectorNamespace])
	assert.Equal(t, "true", job.Labels[wellknown.LabelConnectorProbe])
	assert.NotContains(t, job.Labels, wellknown.LabelPlan)
	assert.Equal(t, job.Labels, job.Spec.Template.Labels)
	assert.Equal(t, "aws-prod", job.Annotations[wellknown.AnnotationConnectorName])
	assert.Equal(t, "team-a", job.Annotations[wellknown.AnnotationConnectorNamespace])
	assert.Equal(t, "7", job.Annotations[wellknown.AnnotationConnectorGeneration])
	assert.Equal(t, "rv1,missing", job.Annotations[wellknown.AnnotationConnectorSecretVersion])
	assert.Equal(t, job.Annotations, job.Spec.Template.Annotations)
	job.Annotations["x"] = "y"
	assert.NotContains(t, job.Spec.Template.Annotations, "x", "Job and pod template must not share one map")
}

func TestProbeJobStamp(t *testing.T) {
	tests := []struct {
		name    string
		ann     map[string]string
		wantGen int64
		wantSV  string
	}{
		{"stamped", map[string]string{wellknown.AnnotationConnectorGeneration: "3", wellknown.AnnotationConnectorSecretVersion: "9"}, 3, "9"},
		{"missing generation forces a re-probe", map[string]string{wellknown.AnnotationConnectorSecretVersion: "9"}, 0, "9"},
		{"invalid generation", map[string]string{wellknown.AnnotationConnectorGeneration: "x"}, 0, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gen, sv := probeJobStamp(&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Annotations: tc.ann}})
			assert.Equal(t, tc.wantGen, gen)
			assert.Equal(t, tc.wantSV, sv)
		})
	}
}

func TestBuildProbeJob_LongNames(t *testing.T) {
	longName := strings.Repeat("a", 253)
	longName2 := strings.Repeat("a", 252) + "b"
	ns := strings.Repeat("n", 63)

	job := BuildProbeJob(testCfg, "K8SCluster", ns, longName, 1, "")
	for k, v := range job.Labels {
		assert.Empty(t, validation.IsValidLabelValue(v), "label %s=%q", k, v)
	}
	assert.Equal(t, longName, job.Annotations[wellknown.AnnotationConnectorName])
	assert.Equal(t, longName, job.Spec.Template.Spec.Containers[0].Env[2].Value)
	assert.Empty(t, validation.IsDNS1123Label(strings.TrimSuffix(job.GenerateName, "-")))

	other := BuildProbeJob(testCfg, "K8SCluster", ns, longName2, 1, "")
	assert.NotEqual(t, job.Labels[wellknown.LabelConnectorName], other.Labels[wellknown.LabelConnectorName])
}

func TestProbeJobSelector_MatchesJobLabels(t *testing.T) {
	cases := []struct{ kind, ns, name string }{
		{"CloudProvider", "team-a", "aws-prod"},
		{"K8SCluster", strings.Repeat("n", 63), strings.Repeat("x", 253)},
	}
	for _, tc := range cases {
		job := BuildProbeJob(testCfg, tc.kind, tc.ns, tc.name, 1, "")
		sel := ProbeJobSelector(tc.kind, tc.ns, tc.name)
		for k, v := range sel {
			assert.Equal(t, v, job.Labels[k], k)
		}
		assert.Len(t, sel, 4)
		assert.Equal(t, "true", sel[wellknown.LabelConnectorProbe])
	}
}

func TestBuildProbeJob_OwnerReference(t *testing.T) {
	t.Run("set from controller pod", func(t *testing.T) {
		cfg := testCfg
		cfg.Owner = &OwnerPod{Name: "hibernator-abc", UID: "uid-1"}
		job := BuildProbeJob(cfg, "CloudProvider", "team-a", "aws-prod", 1, "")

		require.Len(t, job.OwnerReferences, 1)
		ref := job.OwnerReferences[0]
		assert.Equal(t, "v1", ref.APIVersion)
		assert.Equal(t, "Pod", ref.Kind)
		assert.Equal(t, "hibernator-abc", ref.Name)
		assert.Equal(t, "uid-1", string(ref.UID))
		assert.Nil(t, ref.Controller)
	})

	t.Run("omitted without owner", func(t *testing.T) {
		job := BuildProbeJob(testCfg, "CloudProvider", "team-a", "aws-prod", 1, "")
		assert.Empty(t, job.OwnerReferences)
	})
}
