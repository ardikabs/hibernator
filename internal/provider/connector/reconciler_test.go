/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package connector

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/connectorprobe"
	statusmanager "github.com/ardikabs/hibernator/internal/provider/status"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

const (
	testConnNS  = "team-a"
	testConnRV  = "42"
	kindCP      = "CloudProvider"
	kindCluster = "K8SCluster"
)

var testNow = time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)

// recorder records updates and applies them to the fake client, like the status manager.
// With drop set, writes are recorded but never persisted, like a failing status write.
type recorder[T client.Object] struct {
	c       client.Client
	drop    bool
	updates []statusmanager.Update[T]
}

func (r *recorder[T]) Send(u statusmanager.Update[T]) {
	r.updates = append(r.updates, u)
	if r.drop {
		return
	}
	fresh := u.Resource.DeepCopyObject().(T)
	if err := r.c.Get(context.Background(), u.NamespacedName, fresh); err != nil {
		return
	}
	u.Mutator.Mutate(fresh)
	_ = r.c.Status().Update(context.Background(), fresh)
}

type fixture struct {
	r   *Reconciler // CloudProvider strategy
	k   *Reconciler // K8SCluster strategy, same configuration
	c   client.Client
	cps *recorder[*hibernatorv1alpha1.CloudProvider]
	kcs *recorder[*hibernatorv1alpha1.K8SCluster]
	clk *clocktesting.FakeClock
}

func newFixture(t *testing.T, objs ...client.Object) *fixture {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, hibernatorv1alpha1.AddToScheme(scheme))

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&hibernatorv1alpha1.CloudProvider{}, &hibernatorv1alpha1.K8SCluster{}).
		WithIndex(&hibernatorv1alpha1.HibernatePlan{}, wellknown.FieldIndexPlanConnectorRef, planConnectorIndexValues).
		Build()

	cps := &recorder[*hibernatorv1alpha1.CloudProvider]{c: c}
	kcs := &recorder[*hibernatorv1alpha1.K8SCluster]{c: c}
	clk := clocktesting.NewFakeClock(testNow)

	r := &Reconciler{
		Strategy:  CloudProviderStrategy{},
		Client:    c,
		APIReader: c,
		Clock:     clk,
		Log:       logr.Discard(),
		Statuses: &statusmanager.ControllerStatuses{
			CloudProviderStatuses: cps,
			K8SClusterStatuses:    kcs,
		},
		Job:                 JobConfig{RunnerImage: "runner:test", RunnerServiceAccount: "hibernator-runner", Timeout: 30 * time.Second},
		Resync:              15 * time.Minute,
		MaxConcurrentProbes: 5,
		// Most tests exercise probing, so connectors need no plan. Gating tests turn this off.
		ProbeUnreferenced: true,
		Jitter:            func(time.Duration) time.Duration { return 0 },
	}
	k := *r
	k.Strategy = K8SClusterStrategy{}
	return &fixture{r: r, k: &k, c: c, cps: cps, kcs: kcs, clk: clk}
}

func cloudProvider(name string) *hibernatorv1alpha1.CloudProvider {
	return &hibernatorv1alpha1.CloudProvider{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testConnNS, Generation: 1, UID: types.UID("uid-" + name)},
		Spec: hibernatorv1alpha1.CloudProviderSpec{
			Type: hibernatorv1alpha1.CloudProviderAWS,
			AWS:  &hibernatorv1alpha1.AWSConfig{AccountId: "123456789012", Region: "us-east-1"},
		},
	}
}

func staticCloudProvider(name, secret, secretNS string) *hibernatorv1alpha1.CloudProvider {
	cp := cloudProvider(name)
	cp.Spec.AWS.Auth.Static = &hibernatorv1alpha1.StaticAuth{
		SecretRef: hibernatorv1alpha1.SecretReference{Name: secret, Namespace: secretNS},
	}
	return cp
}

func k8sCluster(name string, spec hibernatorv1alpha1.K8SClusterSpec) *hibernatorv1alpha1.K8SCluster {
	return &hibernatorv1alpha1.K8SCluster{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testConnNS, Generation: 1, UID: types.UID("uid-" + name)},
		Spec:       spec,
	}
}

func secret(name, ns string) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}
}

func ownerOf(kind, name string) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: hibernatorv1alpha1.GroupVersion.String(),
		Kind:       kind,
		Name:       name,
		UID:        types.UID("uid-" + name),
		Controller: ptr.To(true),
	}
}

func req(name string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Namespace: testConnNS, Name: name}}
}

// probeJob returns a probe Job for the connector at generation 1, optionally finished.
func probeJob(f *fixture, kind, name, jobName string, cond batchv1.JobConditionType) *batchv1.Job {
	job := BuildProbeJob(f.r.Job, ownerOf(kind, name), kind, testConnNS, name, 1, "")
	job.Name = jobName
	job.GenerateName = ""
	job.CreationTimestamp = metav1.NewTime(testNow.Add(-time.Minute))
	if cond != "" {
		job.Status.Conditions = []batchv1.JobCondition{{Type: cond, Status: corev1.ConditionTrue}}
		done := metav1.NewTime(testNow.Add(-30 * time.Second))
		job.Status.CompletionTime = &done
	}
	return job
}

func probePod(job *batchv1.Job, msg string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      job.Name + "-pod",
			Namespace: job.Namespace,
			Labels:    job.Spec.Template.Labels,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "batch/v1", Kind: "Job", Name: job.Name, UID: job.UID,
			}},
		},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			Name:  "runner",
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Message: msg}},
		}}},
	}
}

func listProbeJobs(t *testing.T, c client.Client) []batchv1.Job {
	t.Helper()
	var jobs batchv1.JobList
	require.NoError(t, c.List(context.Background(), &jobs, client.InNamespace(testConnNS),
		client.MatchingLabels{wellknown.LabelConnectorProbe: "true"}))
	return jobs.Items
}

func getCP(t *testing.T, c client.Client, name string) *hibernatorv1alpha1.CloudProvider {
	t.Helper()
	var cp hibernatorv1alpha1.CloudProvider
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Namespace: testConnNS, Name: name}, &cp))
	return &cp
}

func getKC(t *testing.T, c client.Client, name string) *hibernatorv1alpha1.K8SCluster {
	t.Helper()
	var kc hibernatorv1alpha1.K8SCluster
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Namespace: testConnNS, Name: name}, &kc))
	return &kc
}

func TestReconcile_DueCreatesOneJob(t *testing.T) {
	f := newFixture(t, cloudProvider("aws"))
	ctx := context.Background()

	res, err := f.r.Reconcile(ctx, req("aws"))
	require.NoError(t, err)
	assert.Equal(t, 10*time.Second, res.RequeueAfter)

	jobs := listProbeJobs(t, f.c)
	require.Len(t, jobs, 1)
	assert.Equal(t, "aws", jobs[0].Annotations[wellknown.AnnotationConnectorName])
	assert.Equal(t, testConnNS, jobs[0].Annotations[wellknown.AnnotationConnectorNamespace])
	assert.Equal(t, "1", jobs[0].Annotations[wellknown.AnnotationConnectorGeneration])
	assert.Equal(t, testConnNS, jobs[0].Namespace, "probe Job runs in the connector's namespace")
	require.Len(t, jobs[0].OwnerReferences, 1)
	owner := jobs[0].OwnerReferences[0]
	assert.Equal(t, hibernatorv1alpha1.GroupVersion.String(), owner.APIVersion)
	assert.Equal(t, kindCP, owner.Kind)
	assert.Equal(t, "aws", owner.Name)
	assert.Equal(t, types.UID("uid-aws"), owner.UID)
	assert.Equal(t, ptr.To(true), owner.Controller)
	assert.Nil(t, owner.BlockOwnerDeletion)

	res, err = f.r.Reconcile(ctx, req("aws"))
	require.NoError(t, err)
	assert.Equal(t, 10*time.Second, res.RequeueAfter)
	assert.Len(t, listProbeJobs(t, f.c), 1)
	assert.Empty(t, f.cps.updates)
}

func TestReconcile_ConsumeFinishedJob(t *testing.T) {
	valid := connectorprobe.Result{Ready: true, Message: "authenticated", AccessVerified: true}.Marshal()

	tests := []struct {
		name       string
		cond       batchv1.JobConditionType
		msg        string
		wantReady  bool
		wantReason string
		wantStatus metav1.ConditionStatus
		wantMsg    string
	}{
		{name: "valid message", cond: batchv1.JobComplete, msg: valid, wantReady: true,
			wantReason: hibernatorv1alpha1.ReasonReady, wantStatus: metav1.ConditionTrue, wantMsg: "authenticated"},
		{name: "empty message", cond: batchv1.JobComplete, msg: "",
			wantReason: hibernatorv1alpha1.ReasonProbeFailed, wantStatus: metav1.ConditionUnknown,
			wantMsg: "probe failed: empty probe result"},
		{name: "invalid message", cond: batchv1.JobComplete, msg: "not json",
			wantReason: hibernatorv1alpha1.ReasonProbeFailed, wantStatus: metav1.ConditionUnknown},
		{name: "failed job", cond: batchv1.JobFailed, msg: "boom",
			wantReason: hibernatorv1alpha1.ReasonProbeFailed, wantStatus: metav1.ConditionUnknown,
			wantMsg: "probe failed: job failed: boom"},
		{name: "failed job with runner prefix", cond: batchv1.JobFailed, msg: "probe failed: context deadline exceeded",
			wantReason: hibernatorv1alpha1.ReasonProbeFailed, wantStatus: metav1.ConditionUnknown,
			wantMsg: "probe failed: job failed: context deadline exceeded"},
		{name: "failed job without message", cond: batchv1.JobFailed,
			wantReason: hibernatorv1alpha1.ReasonProbeFailed, wantStatus: metav1.ConditionUnknown,
			wantMsg: "probe failed: job failed without a termination message"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			cp := staticCloudProvider("aws", "creds", "")
			sec := secret("creds", testConnNS)
			require.NoError(t, f.c.Create(context.Background(), cp))
			require.NoError(t, f.c.Create(context.Background(), sec))
			job := probeJob(f, kindCP, "aws", "connector-probe-1", tc.cond)
			job.Annotations[wellknown.AnnotationConnectorSecretVersion] = sec.ResourceVersion
			require.NoError(t, f.c.Create(context.Background(), job))
			require.NoError(t, f.c.Create(context.Background(), probePod(job, tc.msg)))

			res, err := f.r.Reconcile(context.Background(), req("aws"))
			require.NoError(t, err)
			assert.Equal(t, 5*time.Second, res.RequeueAfter)

			require.Len(t, f.cps.updates, 1)
			assert.Len(t, listProbeJobs(t, f.c), 1, "Job is kept until its result is persisted")

			got := getCP(t, f.c, "aws")
			assert.Equal(t, tc.wantReady, got.Status.Ready)
			require.NotNil(t, got.Status.LastProbeTime)
			assert.True(t, got.Status.LastProbeTime.Time.Equal(testNow))
			assert.Equal(t, int64(1), got.Status.ObservedGeneration)
			assert.Equal(t, sec.ResourceVersion, got.Status.ObservedSecretVersion)
			if tc.wantMsg != "" {
				assert.Equal(t, tc.wantMsg, got.Status.Message)
			}
			c := meta.FindStatusCondition(got.Status.Conditions, hibernatorv1alpha1.ConditionReady)
			require.NotNil(t, c)
			assert.Equal(t, tc.wantStatus, c.Status)
			assert.Equal(t, tc.wantReason, c.Reason)

			// Persisted: the Job is released, nothing re-sent, and nothing is due within resync.
			f.clk.Step(time.Minute)
			res, err = f.r.Reconcile(context.Background(), req("aws"))
			require.NoError(t, err)
			assert.Empty(t, listProbeJobs(t, f.c))
			assert.Len(t, f.cps.updates, 1)
			assert.Equal(t, 14*time.Minute, res.RequeueAfter)
		})
	}
}

func TestReconcile_StaleJobVerdictKeepsProbeDue(t *testing.T) {
	tests := []struct {
		name       string
		generation int64
		secretRV   string // stamped on the Job; the Secret's current RV differs
		wantGen    int64
	}{
		{name: "spec changed while in flight", generation: 2, wantGen: 1},
		{name: "secret changed while in flight", generation: 1, secretRV: "stale", wantGen: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cp := staticCloudProvider("aws", "creds", "")
			cp.Generation = tc.generation
			sec := secret("creds", testConnNS)
			f := newFixture(t, cp, sec)
			require.NoError(t, f.c.Get(context.Background(), client.ObjectKeyFromObject(sec), sec))

			job := probeJob(f, kindCP, "aws", "job-1", batchv1.JobComplete)
			stamped := sec.ResourceVersion
			if tc.secretRV != "" {
				stamped = tc.secretRV
			}
			job.Annotations[wellknown.AnnotationConnectorSecretVersion] = stamped
			require.NoError(t, f.c.Create(context.Background(), job))
			require.NoError(t, f.c.Create(context.Background(), probePod(job, connectorprobe.Result{Ready: true}.Marshal())))

			_, err := f.r.Reconcile(context.Background(), req("aws"))
			require.NoError(t, err)
			got := getCP(t, f.c, "aws")
			assert.Equal(t, tc.wantGen, got.Status.ObservedGeneration)
			assert.Equal(t, stamped, got.Status.ObservedSecretVersion)

			// The old verdict is released and a new probe starts for the current spec and Secret.
			_, err = f.r.Reconcile(context.Background(), req("aws"))
			require.NoError(t, err)
			jobs := listProbeJobs(t, f.c)
			require.Len(t, jobs, 1)
			assert.NotEqual(t, "job-1", jobs[0].Name)
			assert.Equal(t, strconv.FormatInt(tc.generation, 10), jobs[0].Annotations[wellknown.AnnotationConnectorGeneration])
			assert.Equal(t, sec.ResourceVersion, jobs[0].Annotations[wellknown.AnnotationConnectorSecretVersion])
		})
	}
}

func TestReconcile_UnpersistedResultBlocksNewProbe(t *testing.T) {
	f := newFixture(t, cloudProvider("aws"))
	f.cps.drop = true
	job := probeJob(f, kindCP, "aws", "job-1", batchv1.JobComplete)
	require.NoError(t, f.c.Create(context.Background(), job))
	require.NoError(t, f.c.Create(context.Background(), probePod(job, connectorprobe.Result{Ready: true}.Marshal())))

	for i := range 4 {
		res, err := f.r.Reconcile(context.Background(), req("aws"))
		require.NoError(t, err)
		assert.Equal(t, 5*time.Second, res.RequeueAfter)
		jobs := listProbeJobs(t, f.c)
		require.Len(t, jobs, 1, "no second Job and the pending Job is kept")
		assert.Equal(t, "job-1", jobs[0].Name)
		assert.Len(t, f.cps.updates, i+1, "the verdict is re-sent")
		f.clk.Step(20 * time.Minute)
	}

	// Once the write lands, the Job is released and no extra probe starts.
	f.cps.drop = false
	_, err := f.r.Reconcile(context.Background(), req("aws"))
	require.NoError(t, err)
	assert.Len(t, listProbeJobs(t, f.c), 1, "the write lands; the Job is still the pending one")
	res, err := f.r.Reconcile(context.Background(), req("aws"))
	require.NoError(t, err)
	assert.Empty(t, listProbeJobs(t, f.c))
	assert.Equal(t, 15*time.Minute, res.RequeueAfter)
}

func TestReconcile_JobCompletedAfterClockIsReleased(t *testing.T) {
	f := newFixture(t, cloudProvider("aws"))
	job := probeJob(f, kindCP, "aws", "job-1", batchv1.JobComplete)
	future := metav1.NewTime(testNow.Add(10 * time.Second))
	job.Status.CompletionTime = &future
	require.NoError(t, f.c.Create(context.Background(), job))
	require.NoError(t, f.c.Create(context.Background(), probePod(job, connectorprobe.Result{Ready: true}.Marshal())))

	_, err := f.r.Reconcile(context.Background(), req("aws"))
	require.NoError(t, err)
	got := getCP(t, f.c, "aws")
	require.NotNil(t, got.Status.LastProbeTime)
	assert.False(t, got.Status.LastProbeTime.Time.Before(future.Time))

	_, err = f.r.Reconcile(context.Background(), req("aws"))
	require.NoError(t, err)
	assert.Empty(t, listProbeJobs(t, f.c))
	assert.Len(t, f.cps.updates, 1)
}

func TestJobCompletionTime(t *testing.T) {
	created := metav1.NewTime(testNow.Add(-3 * time.Minute))
	started := metav1.NewTime(testNow.Add(-2 * time.Minute))
	transition := metav1.NewTime(testNow.Add(-time.Minute))
	completed := metav1.NewTime(testNow)

	tests := []struct {
		name string
		job  batchv1.Job
		want time.Time
	}{
		{"completion time", batchv1.Job{Status: batchv1.JobStatus{CompletionTime: &completed, StartTime: &started}}, completed.Time},
		{"terminal condition", batchv1.Job{Status: batchv1.JobStatus{StartTime: &started, Conditions: []batchv1.JobCondition{
			{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, LastTransitionTime: transition}}}}, transition.Time},
		{"start time", batchv1.Job{Status: batchv1.JobStatus{StartTime: &started, Conditions: []batchv1.JobCondition{
			{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}}}}, started.Time},
		{"creation time", batchv1.Job{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: created}}, created.Time},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.True(t, tc.want.Equal(jobCompletionTime(&tc.job)))
		})
	}
}

func TestReconcile_AtCapCreatesNoJob(t *testing.T) {
	f := newFixture(t, cloudProvider("aws"))
	f.r.MaxConcurrentProbes = 2
	for _, n := range []string{"other-1", "other-2"} {
		require.NoError(t, f.c.Create(context.Background(), probeJob(f, kindCP, n, "job-"+n, "")))
	}
	// A finished Job does not count towards the cap.
	require.NoError(t, f.c.Create(context.Background(), probeJob(f, kindCP, "other-3", "job-other-3", batchv1.JobComplete)))

	res, err := f.r.Reconcile(context.Background(), req("aws"))
	require.NoError(t, err)
	assert.Equal(t, 15*time.Second, res.RequeueAfter)
	assert.Len(t, listProbeJobs(t, f.c), 3)
}

func TestReconcile_NotFoundDeletesProbeJobs(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.c.Create(context.Background(), probeJob(f, kindCP, "gone", "job-a", "")))
	require.NoError(t, f.c.Create(context.Background(), probeJob(f, kindCP, "gone", "job-b", batchv1.JobComplete)))
	require.NoError(t, f.c.Create(context.Background(), probeJob(f, kindCP, "kept", "job-c", "")))
	require.NoError(t, f.c.Create(context.Background(), probeJob(f, kindCluster, "gone", "job-d", "")))

	res, err := f.r.Reconcile(context.Background(), req("gone"))
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, res)

	var names []string
	for _, j := range listProbeJobs(t, f.c) {
		names = append(names, j.Name)
	}
	assert.ElementsMatch(t, []string{"job-c", "job-d"}, names)
}

func TestReconcile_NotFoundIgnoresCollidingJob(t *testing.T) {
	f := newFixture(t)
	other := probeJob(f, kindCP, "gone", "job-other", "")
	other.Annotations[wellknown.AnnotationConnectorName] = "different"
	require.NoError(t, f.c.Create(context.Background(), other))

	_, err := f.r.Reconcile(context.Background(), req("gone"))
	require.NoError(t, err)
	assert.Len(t, listProbeJobs(t, f.c), 1)
}

func TestReconcile_CollidingLabelsIgnored(t *testing.T) {
	f := newFixture(t, cloudProvider("aws"))
	running := probeJob(f, kindCP, "aws", "job-running", "")
	running.Annotations[wellknown.AnnotationConnectorName] = "aws-other"
	finished := probeJob(f, kindCP, "aws", "job-finished", batchv1.JobComplete)
	finished.Annotations[wellknown.AnnotationConnectorNamespace] = "team-b"
	require.NoError(t, f.c.Create(context.Background(), running))
	require.NoError(t, f.c.Create(context.Background(), finished))

	res, err := f.r.Reconcile(context.Background(), req("aws"))
	require.NoError(t, err)
	assert.Equal(t, 10*time.Second, res.RequeueAfter)
	assert.Empty(t, f.cps.updates, "a colliding finished Job must not be consumed")
	assert.Len(t, listProbeJobs(t, f.c), 3, "a colliding running Job must not block a new probe")
}

func TestReconcile_ProviderNotReady(t *testing.T) {
	withReady := func(status metav1.ConditionStatus, reason string) *hibernatorv1alpha1.CloudProvider {
		cp := cloudProvider("aws")
		cp.Status.Conditions = []metav1.Condition{{Type: hibernatorv1alpha1.ConditionReady, Status: status, Reason: reason}}
		return cp
	}

	tests := []struct {
		name       string
		provider   *hibernatorv1alpha1.CloudProvider
		wantStatus metav1.ConditionStatus
	}{
		{name: "missing provider", wantStatus: metav1.ConditionFalse},
		{name: "provider not ready", provider: withReady(metav1.ConditionFalse, hibernatorv1alpha1.ReasonInvalidCredentials),
			wantStatus: metav1.ConditionFalse},
		{name: "provider probe failed", provider: withReady(metav1.ConditionUnknown, hibernatorv1alpha1.ReasonProbeFailed),
			wantStatus: metav1.ConditionUnknown},
		{name: "provider never probed", provider: cloudProvider("aws"), wantStatus: metav1.ConditionUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			validated := metav1.NewTime(testNow.Add(-time.Hour))
			kc := k8sCluster("eks", hibernatorv1alpha1.K8SClusterSpec{
				ProviderRef: &hibernatorv1alpha1.ProviderRef{Name: "aws"},
				EKS:         &hibernatorv1alpha1.EKSConfig{Name: "prod", Region: "us-east-1"},
			})
			kc.Status.LastValidated = &validated
			objs := []client.Object{kc}
			if tc.provider != nil {
				objs = append(objs, tc.provider)
			}
			f := newFixture(t, objs...)

			res, err := f.k.Reconcile(context.Background(), req("eks"))
			require.NoError(t, err)
			assert.Equal(t, ctrl.Result{}, res)
			assert.Empty(t, listProbeJobs(t, f.c))

			require.Len(t, f.kcs.updates, 1)
			got := getKC(t, f.c, "eks")
			assert.False(t, got.Status.Ready)
			assert.Equal(t, hibernatorv1alpha1.ClusterTypeEKS, got.Status.ClusterType)
			require.NotNil(t, got.Status.LastValidated)
			assert.True(t, got.Status.LastValidated.Time.Equal(validated.Time))
			c := meta.FindStatusCondition(got.Status.Conditions, hibernatorv1alpha1.ConditionReady)
			require.NotNil(t, c)
			assert.Equal(t, tc.wantStatus, c.Status)
			assert.Equal(t, hibernatorv1alpha1.ReasonProviderNotReady, c.Reason)
		})
	}
}

func TestReconcile_ProviderRecoveredProbesImmediately(t *testing.T) {
	ready := cloudProvider("aws")
	ready.Status.Ready = true
	lp := metav1.NewTime(testNow.Add(-time.Minute))
	kc := k8sCluster("eks", hibernatorv1alpha1.K8SClusterSpec{
		ProviderRef: &hibernatorv1alpha1.ProviderRef{Name: "aws", Namespace: testConnNS},
		EKS:         &hibernatorv1alpha1.EKSConfig{Name: "prod", Region: "us-east-1"},
	})
	kc.Status.LastProbeTime = &lp
	kc.Status.ObservedGeneration = 1
	kc.Status.Conditions = []metav1.Condition{{
		Type: hibernatorv1alpha1.ConditionReady, Status: metav1.ConditionFalse,
		Reason: hibernatorv1alpha1.ReasonProviderNotReady, LastTransitionTime: lp,
	}}
	f := newFixture(t, kc, ready)

	_, err := f.k.Reconcile(context.Background(), req("eks"))
	require.NoError(t, err)
	assert.Len(t, listProbeJobs(t, f.c), 1)
}

func TestClusterTypeOf(t *testing.T) {
	tests := []struct {
		name string
		spec hibernatorv1alpha1.K8SClusterSpec
		want hibernatorv1alpha1.K8SClusterType
	}{
		{"eks", hibernatorv1alpha1.K8SClusterSpec{EKS: &hibernatorv1alpha1.EKSConfig{}}, hibernatorv1alpha1.ClusterTypeEKS},
		{"gke", hibernatorv1alpha1.K8SClusterSpec{GKE: &hibernatorv1alpha1.GKEConfig{}}, hibernatorv1alpha1.ClusterTypeGKE},
		{"k8s", hibernatorv1alpha1.K8SClusterSpec{K8S: &hibernatorv1alpha1.K8SAccessConfig{InCluster: true}}, hibernatorv1alpha1.ClusterTypeK8S},
		{"none", hibernatorv1alpha1.K8SClusterSpec{}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ClusterTypeOf(tc.spec))
		})
	}
}

func TestReconcile_K8SClusterResultSetsClusterType(t *testing.T) {
	tests := []struct {
		name string
		spec hibernatorv1alpha1.K8SClusterSpec
		msg  string
		want hibernatorv1alpha1.K8SClusterType
	}{
		{"k8s result", hibernatorv1alpha1.K8SClusterSpec{K8S: &hibernatorv1alpha1.K8SAccessConfig{InCluster: true}},
			connectorprobe.Result{Ready: true, AccessVerified: true}.Marshal(), hibernatorv1alpha1.ClusterTypeK8S},
		{"k8s probe failed", hibernatorv1alpha1.K8SClusterSpec{K8S: &hibernatorv1alpha1.K8SAccessConfig{
			KubeconfigRef: &hibernatorv1alpha1.KubeconfigRef{Name: "kc"}}}, "", hibernatorv1alpha1.ClusterTypeK8S},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, k8sCluster("c1", tc.spec))
			job := probeJob(f, kindCluster, "c1", "job-c1", batchv1.JobComplete)
			require.NoError(t, f.c.Create(context.Background(), job))
			require.NoError(t, f.c.Create(context.Background(), probePod(job, tc.msg)))

			_, err := f.k.Reconcile(context.Background(), req("c1"))
			require.NoError(t, err)
			require.Len(t, f.kcs.updates, 1)
			assert.Equal(t, tc.want, getKC(t, f.c, "c1").Status.ClusterType)
		})
	}
}

func TestReconcile_GKENotProbed(t *testing.T) {
	kc := k8sCluster("gke", hibernatorv1alpha1.K8SClusterSpec{GKE: &hibernatorv1alpha1.GKEConfig{Name: "g", Location: "us-central1"}})
	kc.Status.AllowedExecutors = []string{"gke"}
	f := newFixture(t, kc)
	leftover := probeJob(f, kindCluster, "gke", "job-gke", batchv1.JobComplete)
	require.NoError(t, f.c.Create(context.Background(), leftover))

	for range 2 {
		res, err := f.k.Reconcile(context.Background(), req("gke"))
		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, res, "no requeue polling")
		assert.Empty(t, listProbeJobs(t, f.c))
	}

	got := getKC(t, f.c, "gke")
	assert.False(t, got.Status.Ready)
	assert.Equal(t, hibernatorv1alpha1.ClusterTypeGKE, got.Status.ClusterType)
	assert.Nil(t, got.Status.LastProbeTime)
	assert.Empty(t, got.Status.AllowedExecutors)
	c := meta.FindStatusCondition(got.Status.Conditions, hibernatorv1alpha1.ConditionReady)
	require.NotNil(t, c)
	assert.Equal(t, metav1.ConditionUnknown, c.Status)
	assert.Equal(t, hibernatorv1alpha1.ReasonProbeUnsupported, c.Reason)
	assert.Equal(t, "GKE clusters are not probed yet", c.Message)
}

func TestReconcile_ResyncFallback(t *testing.T) {
	tests := []struct {
		name        string
		resync      time.Duration
		since       time.Duration
		wantJobs    int
		wantRequeue time.Duration
	}{
		{name: "zero resync, not due", resync: 0, since: 5 * time.Minute, wantJobs: 0, wantRequeue: 10 * time.Minute},
		{name: "negative resync, due", resync: -time.Second, since: 16 * time.Minute, wantJobs: 1, wantRequeue: 10 * time.Second},
		{name: "remaining under a second", resync: 15*time.Minute + 500*time.Millisecond, since: 15 * time.Minute, wantJobs: 0, wantRequeue: time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cp := cloudProvider("aws")
			lp := metav1.NewTime(testNow.Add(-tc.since))
			cp.Status.LastProbeTime = &lp
			cp.Status.ObservedGeneration = 1
			f := newFixture(t, cp)
			f.r.Resync = tc.resync

			res, err := f.r.Reconcile(context.Background(), req("aws"))
			require.NoError(t, err)
			assert.Equal(t, tc.wantRequeue, res.RequeueAfter)
			assert.Len(t, listProbeJobs(t, f.c), tc.wantJobs)
		})
	}
}

func TestReconcile_NilJitterIsSafe(t *testing.T) {
	cp := cloudProvider("aws")
	lp := metav1.NewTime(testNow.Add(-time.Minute))
	cp.Status.LastProbeTime = &lp
	cp.Status.ObservedGeneration = 1
	f := newFixture(t, cp)
	f.r.Jitter = nil

	res, err := f.r.Reconcile(context.Background(), req("aws"))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, res.RequeueAfter, 14*time.Minute)
	assert.LessOrEqual(t, res.RequeueAfter, 14*time.Minute+90*time.Second)
}

func TestSecretVersion(t *testing.T) {
	sec := secret("creds", testConnNS)
	f := newFixture(t, sec)
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKeyFromObject(sec), sec))

	tests := []struct {
		name string
		refs []types.NamespacedName
		want string
	}{
		{name: "no refs", want: ""},
		{name: "present", refs: []types.NamespacedName{{Namespace: testConnNS, Name: "creds"}}, want: sec.ResourceVersion},
		{name: "missing", refs: []types.NamespacedName{{Namespace: testConnNS, Name: "nope"}}, want: secretMissing},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.r.secretVersion(context.Background(), tc.refs)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestReconcile_SecretChangeIsDue(t *testing.T) {
	cp := staticCloudProvider("aws", "creds", "")
	lp := metav1.NewTime(testNow.Add(-time.Minute))
	cp.Status.LastProbeTime = &lp
	cp.Status.ObservedGeneration = 1
	cp.Status.ObservedSecretVersion = "stale"
	f := newFixture(t, cp, secret("creds", testConnNS))

	_, err := f.r.Reconcile(context.Background(), req("aws"))
	require.NoError(t, err)
	assert.Len(t, listProbeJobs(t, f.c), 1)
}

func TestSecretMapping(t *testing.T) {
	f := newFixture(t,
		staticCloudProvider("same-ns", "creds", ""),
		staticCloudProvider("explicit-ns", "creds", testConnNS),
		staticCloudProvider("other-secret", "other", ""),
		staticCloudProvider("other-ns", "creds", "team-b"),
		cloudProvider("irsa"),
		k8sCluster("kubeconfig", hibernatorv1alpha1.K8SClusterSpec{K8S: &hibernatorv1alpha1.K8SAccessConfig{
			KubeconfigRef: &hibernatorv1alpha1.KubeconfigRef{Name: "creds"}}}),
		k8sCluster("incluster", hibernatorv1alpha1.K8SClusterSpec{K8S: &hibernatorv1alpha1.K8SAccessConfig{InCluster: true}}),
		k8sCluster("eks-a", hibernatorv1alpha1.K8SClusterSpec{ProviderRef: &hibernatorv1alpha1.ProviderRef{Name: "same-ns"},
			EKS: &hibernatorv1alpha1.EKSConfig{Name: "a"}}),
		k8sCluster("eks-b", hibernatorv1alpha1.K8SClusterSpec{ProviderRef: &hibernatorv1alpha1.ProviderRef{Name: "same-ns", Namespace: "team-b"},
			EKS: &hibernatorv1alpha1.EKSConfig{Name: "b"}}),
	)
	ctx := context.Background()

	names := func(reqs []ctrl.Request) []string {
		var out []string
		for _, r := range reqs {
			out = append(out, r.Name)
		}
		return out
	}

	assert.ElementsMatch(t, []string{"same-ns", "explicit-ns"}, names(CloudProviderStrategy{}.SecretRequests(ctx, f.r, secret("creds", testConnNS))))
	assert.ElementsMatch(t, []string{"kubeconfig"}, names(K8SClusterStrategy{}.SecretRequests(ctx, f.k, secret("creds", testConnNS))))
	assert.Empty(t, CloudProviderStrategy{}.SecretRequests(ctx, f.r, secret("unused", testConnNS)))
	assert.ElementsMatch(t, []string{"eks-a"}, names(K8SClusterStrategy{}.requestsForCloudProvider(ctx, f.k, cloudProvider("same-ns"))))
}

func TestProbeJobFinishedPredicate(t *testing.T) {
	f := newFixture(t)
	running := probeJob(f, kindCP, "aws", "j", "")
	done := probeJob(f, kindCP, "aws", "j", batchv1.JobComplete)
	failed := probeJob(f, kindCP, "aws", "j", batchv1.JobFailed)
	otherKind := probeJob(f, kindCluster, "aws", "j", batchv1.JobComplete)
	plain := &batchv1.Job{Status: done.Status}

	tests := []struct {
		name     string
		old, new *batchv1.Job
		want     bool
	}{
		{"running to complete", running, done, true},
		{"running to failed", running, failed, true},
		{"still running", running, running, false},
		{"already finished", done, done, false},
		{"other kind", probeJob(f, kindCluster, "aws", "j", ""), otherKind, false},
		{"not a probe job", &batchv1.Job{}, plain, false},
	}
	p := probeJobFinishedPredicate(kindCP)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, p.Update(event.UpdateEvent{ObjectOld: tc.old, ObjectNew: tc.new}))
		})
	}
	assert.False(t, p.Create(event.CreateEvent{Object: running}))
	assert.False(t, p.Delete(event.DeleteEvent{Object: done}))
}

func TestProviderReadyChangedPredicate(t *testing.T) {
	notReady, ready := cloudProvider("aws"), cloudProvider("aws")
	ready.Status.Ready = true
	unknown, failed := cloudProvider("aws"), cloudProvider("aws")
	unknown.Status.Conditions = []metav1.Condition{{Type: hibernatorv1alpha1.ConditionReady, Status: metav1.ConditionUnknown}}
	failed.Status.Conditions = []metav1.Condition{{Type: hibernatorv1alpha1.ConditionReady, Status: metav1.ConditionFalse}}

	assert.True(t, providerReadyChangedPredicate.Update(event.UpdateEvent{ObjectOld: notReady, ObjectNew: ready}))
	assert.False(t, providerReadyChangedPredicate.Update(event.UpdateEvent{ObjectOld: ready, ObjectNew: ready}))
	assert.True(t, providerReadyChangedPredicate.Update(event.UpdateEvent{ObjectOld: unknown, ObjectNew: failed}))
	assert.False(t, providerReadyChangedPredicate.Update(event.UpdateEvent{ObjectOld: failed, ObjectNew: failed}))
}
