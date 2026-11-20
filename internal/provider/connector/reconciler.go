/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package connector

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	statusmanager "github.com/ardikabs/hibernator/internal/provider/status"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=cloudproviders/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=hibernator.ardikabs.com,resources=k8sclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

const (
	// DefaultResync is the probe interval used when none is configured.
	DefaultResync = 15 * time.Minute
	// DefaultMaxConcurrentProbes caps in-flight probe Jobs when none is configured.
	DefaultMaxConcurrentProbes = 5
	// DefaultProbeTimeout is the probe timeout used when none is configured.
	DefaultProbeTimeout = 30 * time.Second

	requeueInFlight = 10 * time.Second
	requeueAtCap    = 15 * time.Second
	requeuePending  = 5 * time.Second
	minRequeue      = time.Second
	jitterDivisor   = 10 // jitter is up to 10% of the resync interval

	// secretMissing stands in for a referenced Secret that does not exist, so
	// its appearance or removal changes the observed version.
	secretMissing = "missing"
)

// Reconciler probes connectors with one-shot Jobs and records the verdicts
// through the status writer pipeline. Everything specific to a connector kind
// comes from Strategy, so supporting a new kind means adding a Strategy.
type Reconciler struct {
	// Strategy selects the connector kind this Reconciler handles.
	Strategy Strategy
	Client   client.Client
	// APIReader reads probe Jobs uncached so a Job just created is never missed.
	// Falls back to Client when nil.
	APIReader           client.Reader
	Clock               clock.Clock
	Log                 logr.Logger
	Statuses            *statusmanager.ControllerStatuses
	Job                 JobConfig
	Resync              time.Duration
	MaxConcurrentProbes int
	// ProbeUnreferenced probes connectors that no active HibernatePlan uses.
	// By default they are skipped and report NotInUse.
	ProbeUnreferenced bool
	// Jitter returns the extra delay added to resync. Nil means random, up to 10%.
	Jitter func(resync time.Duration) time.Duration
}

// SetupWithManager registers the controller for the connector kind of Strategy.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager, workers int) error {
	s := r.Strategy
	if s == nil {
		return errors.New("connector reconciler: strategy is required")
	}

	b := ctrl.NewControllerManagedBy(mgr).
		For(s.New(), builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
			return s.SecretRequests(ctx, r, obj)
		}), builder.OnlyMetadata, builder.WithPredicates(predicate.ResourceVersionChangedPredicate{})).
		Watches(&batchv1.Job{}, handler.EnqueueRequestsFromMapFunc(connectorForJob),
			builder.WithPredicates(probeJobFinishedPredicate(s.Kind()))).
		// A plan starts or stops using a connector when it is created, deleted,
		// suspended or retargeted. All of these are spec changes, and the update
		// handler maps both the old and the new object.
		Watches(&hibernatorv1alpha1.HibernatePlan{}, handler.EnqueueRequestsFromMapFunc(
			func(_ context.Context, obj client.Object) []reconcile.Request {
				plan, ok := obj.(*hibernatorv1alpha1.HibernatePlan)
				if !ok {
					return nil
				}
				return connectorRequestsForPlan(s.Kind(), plan)
			}), builder.WithPredicates(predicate.GenerationChangedPredicate{}))

	return s.Watches(b, r).
		WithOptions(controller.Options{MaxConcurrentReconciles: max(workers, 1)}).
		Named(s.Name()).
		Complete(r)
}

// Reconcile reconciles one connector of the kind selected by Strategy.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Strategy == nil {
		return ctrl.Result{}, errors.New("connector reconciler: strategy is required")
	}

	obj := r.Strategy.New()
	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, r.deleteProbeJobs(ctx, r.Strategy.Kind(), req.NamespacedName)
		}
		return ctrl.Result{}, err
	}

	usage, err := r.Strategy.Usage(ctx, r, obj)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !usage.InUse && !r.ProbeUnreferenced {
		// Requeued by the HibernatePlan watch when a plan starts using the connector.
		if err := r.deleteProbeJobs(ctx, r.Strategy.Kind(), req.NamespacedName); err != nil {
			return ctrl.Result{}, err
		}
		r.Strategy.SendNotInUse(r, obj)
		return ctrl.Result{}, nil
	}

	target, done, err := r.Strategy.Prepare(ctx, r, obj)
	if err != nil || done {
		return ctrl.Result{}, err
	}
	target.referenced = usage.ReferencedTypes
	return r.reconcileProbe(ctx, target)
}

func (r *Reconciler) reconcileProbe(ctx context.Context, t probeTarget) (ctrl.Result, error) {
	log := r.Log.WithValues("kind", t.kind, "connector", t.key.String())

	// The plans that use the connector can change without a new probe, so rescope
	// the recorded access verdict on every reconcile.
	if t.sendReferenced != nil {
		t.sendReferenced(t.generation, t.referenced)
	}

	jobs, err := r.probeJobs(ctx, t.kind, t.key)
	if err != nil {
		return ctrl.Result{}, err
	}

	var finished []*batchv1.Job
	for i := range jobs {
		job := &jobs[i]
		if !jobFinished(job) {
			return ctrl.Result{RequeueAfter: requeueInFlight}, nil
		}
		finished = append(finished, job)
	}

	sv, err := r.secretVersion(ctx, t.secretRefs)
	if err != nil {
		return ctrl.Result{}, err
	}

	resync := r.resync()
	jitter := r.jitter(resync)
	now := r.Clock.Now()

	// A finished Job is deleted only once its result is visible in the cached
	// status. Until then it blocks new probes and its verdict is re-sent, so a
	// failed or not yet cached status write cannot start another probe.
	var pending *batchv1.Job
	for _, job := range finished {
		if resultPersisted(t.lastProbe, job) {
			if err := r.deleteJob(ctx, job); err != nil {
				return ctrl.Result{}, err
			}
			continue
		}
		if pending == nil || jobCompletionTime(pending).Before(jobCompletionTime(job)) {
			pending = job
		}
	}
	if pending != nil {
		// lastProbeTime must reach the completion time, or the Job is never released.
		probeTime := now
		if c := jobCompletionTime(pending); c.After(probeTime) {
			probeTime = c
		}
		r.consume(ctx, t, pending, metav1.NewTime(probeTime))
		log.V(1).Info("sent probe result", "job", pending.Name)
		return ctrl.Result{RequeueAfter: requeuePending}, nil
	}

	fixedJitter := func() time.Duration { return jitter }
	if !t.forceDue && !ProbeDue(t.lastProbe, t.observedGen, t.generation, t.observedSecret, sv, resync, now, fixedJitter) {
		return ctrl.Result{RequeueAfter: max(t.lastProbe.Add(resync+jitter).Sub(now), minRequeue)}, nil
	}

	inFlight, err := r.inFlightProbes(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	if inFlight >= r.maxConcurrent() {
		log.V(1).Info("probe concurrency cap reached", "inFlight", inFlight)
		return ctrl.Result{RequeueAfter: requeueAtCap}, nil
	}

	job := BuildProbeJob(r.Job, t.kind, t.key.Namespace, t.key.Name, t.generation, sv)
	if err := r.Client.Create(ctx, job); err != nil {
		return ctrl.Result{}, fmt.Errorf("create probe job: %w", err)
	}
	log.Info("created probe job", "job", job.Name)
	return ctrl.Result{RequeueAfter: requeueInFlight}, nil
}

// consume sends the verdict of a finished probe Job, recorded for the
// generation and secret version the Job was created for.
func (r *Reconciler) consume(ctx context.Context, t probeTarget, job *batchv1.Job, now metav1.Time) {
	gen, sv := probeJobStamp(job)
	msg := r.terminationMessage(ctx, job)
	if jobConditionTrue(job, batchv1.JobFailed) {
		if msg == "" {
			t.sendFailed(now, gen, sv, "job failed without a termination message")
		} else {
			// The runner writes its own prefix; the status adds one.
			t.sendFailed(now, gen, sv, "job failed: "+strings.TrimPrefix(msg, probeFailedPrefix))
		}
		return
	}
	res, err := ResultFromTerminationMessage(msg)
	if err != nil {
		t.sendFailed(now, gen, sv, err.Error())
		return
	}
	t.sendResult(res, now, gen, sv, t.referenced)
}

// probeJobs lists the live probe Jobs of one connector. Label values may be
// hashed, so the annotations are checked for the exact name and namespace.
func (r *Reconciler) probeJobs(ctx context.Context, kind string, key types.NamespacedName) ([]batchv1.Job, error) {
	var list batchv1.JobList
	if err := r.reader().List(ctx, &list, client.InNamespace(r.Job.Namespace),
		ProbeJobSelector(kind, key.Namespace, key.Name)); err != nil {
		return nil, fmt.Errorf("list probe jobs: %w", err)
	}
	jobs := list.Items[:0]
	for _, job := range list.Items {
		if job.DeletionTimestamp != nil ||
			job.Annotations[wellknown.AnnotationConnectorName] != key.Name ||
			job.Annotations[wellknown.AnnotationConnectorNamespace] != key.Namespace {
			continue
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (r *Reconciler) deleteProbeJobs(ctx context.Context, kind string, key types.NamespacedName) error {
	jobs, err := r.probeJobs(ctx, kind, key)
	if err != nil {
		return err
	}
	for i := range jobs {
		if err := r.deleteJob(ctx, &jobs[i]); err != nil {
			return err
		}
	}
	return nil
}

func (r *Reconciler) deleteJob(ctx context.Context, job *batchv1.Job) error {
	err := r.Client.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground))
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete probe job %s: %w", job.Name, err)
	}
	return nil
}

func (r *Reconciler) inFlightProbes(ctx context.Context) (int, error) {
	var list batchv1.JobList
	if err := r.reader().List(ctx, &list, client.InNamespace(r.Job.Namespace),
		client.MatchingLabels{wellknown.LabelConnectorProbe: "true"}); err != nil {
		return 0, fmt.Errorf("list probe jobs: %w", err)
	}
	n := 0
	for i := range list.Items {
		if list.Items[i].DeletionTimestamp == nil && !jobFinished(&list.Items[i]) {
			n++
		}
	}
	return n, nil
}

// terminationMessage returns the runner's termination message for a Job.
// Mirrors getTerminationMessageFromPod in plan/state/state_execution.go, but
// filters pods by owner: probe pod labels are shared by all Jobs of a connector.
func (r *Reconciler) terminationMessage(ctx context.Context, job *batchv1.Job) string {
	var pods corev1.PodList
	if err := r.Client.List(ctx, &pods, client.InNamespace(job.Namespace),
		client.MatchingLabels(job.Spec.Template.Labels)); err != nil {
		return ""
	}

	owned := make([]corev1.Pod, 0, len(pods.Items))
	for _, pod := range pods.Items {
		for _, ref := range pod.OwnerReferences {
			if ref.Kind == "Job" && ref.Name == job.Name {
				owned = append(owned, pod)
				break
			}
		}
	}
	sort.Slice(owned, func(i, j int) bool {
		return owned[j].CreationTimestamp.Before(&owned[i].CreationTimestamp)
	})

	for _, pod := range owned {
		for _, st := range pod.Status.ContainerStatuses {
			if st.State.Terminated != nil && st.State.Terminated.Message != "" {
				return st.State.Terminated.Message
			}
			if st.LastTerminationState.Terminated != nil && st.LastTerminationState.Terminated.Message != "" {
				return st.LastTerminationState.Terminated.Message
			}
		}
	}
	return ""
}

// secretVersion combines the resourceVersions of the referenced Secrets.
// Only metadata is read, matching the metadata-only Secret watch.
func (r *Reconciler) secretVersion(ctx context.Context, refs []types.NamespacedName) (string, error) {
	if len(refs) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		obj := &metav1.PartialObjectMetadata{}
		obj.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Secret"))
		if err := r.Client.Get(ctx, ref, obj); err != nil {
			if apierrors.IsNotFound(err) {
				parts = append(parts, secretMissing)
				continue
			}
			return "", fmt.Errorf("get secret %s: %w", ref, err)
		}
		parts = append(parts, obj.ResourceVersion)
	}
	return strings.Join(parts, ","), nil
}

func (r *Reconciler) reader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

func (r *Reconciler) resync() time.Duration {
	if r.Resync <= 0 {
		return DefaultResync
	}
	return r.Resync
}

func (r *Reconciler) maxConcurrent() int {
	if r.MaxConcurrentProbes <= 0 {
		return DefaultMaxConcurrentProbes
	}
	return r.MaxConcurrentProbes
}

func (r *Reconciler) jitter(resync time.Duration) time.Duration {
	if r.Jitter != nil {
		return max(r.Jitter(resync), 0)
	}
	return time.Duration(rand.Int64N(int64(resync)/jitterDivisor + 1))
}

// jobCompletionTime is when a finished Job completed: status.completionTime,
// else the terminal condition's transition time, else start or creation time.
func jobCompletionTime(job *batchv1.Job) time.Time {
	if job.Status.CompletionTime != nil {
		return job.Status.CompletionTime.Time
	}
	for _, c := range job.Status.Conditions {
		if (c.Type == batchv1.JobComplete || c.Type == batchv1.JobFailed) &&
			c.Status == corev1.ConditionTrue && !c.LastTransitionTime.IsZero() {
			return c.LastTransitionTime.Time
		}
	}
	if job.Status.StartTime != nil {
		return job.Status.StartTime.Time
	}
	return job.CreationTimestamp.Time
}

// resultPersisted reports whether lastProbe already records the Job's result.
// Both sides are compared at second precision, as metav1.Time is serialized.
func resultPersisted(lastProbe *metav1.Time, job *batchv1.Job) bool {
	if lastProbe == nil {
		return false
	}
	completed := jobCompletionTime(job).Truncate(time.Second)
	return !lastProbe.Time.Truncate(time.Second).Before(completed)
}

func jobFinished(job *batchv1.Job) bool {
	return jobConditionTrue(job, batchv1.JobComplete) || jobConditionTrue(job, batchv1.JobFailed)
}

func jobConditionTrue(job *batchv1.Job, typ batchv1.JobConditionType) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == typ && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// connectorForJob maps a probe Job to its connector through its annotations.
func connectorForJob(_ context.Context, obj client.Object) []reconcile.Request {
	ann := obj.GetAnnotations()
	name, ns := ann[wellknown.AnnotationConnectorName], ann[wellknown.AnnotationConnectorNamespace]
	if name == "" || ns == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: ns, Name: name}}}
}

// probeJobFinishedPredicate passes a probe Job of the given kind when it becomes finished.
func probeJobFinishedPredicate(kind string) predicate.Funcs {
	isProbe := func(obj client.Object) bool {
		l := obj.GetLabels()
		return l[wellknown.LabelConnectorProbe] == "true" && l[wellknown.LabelConnectorKind] == kind
	}
	return predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return false },
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldJob, okOld := e.ObjectOld.(*batchv1.Job)
			newJob, okNew := e.ObjectNew.(*batchv1.Job)
			return okOld && okNew && isProbe(newJob) && !jobFinished(oldJob) && jobFinished(newJob)
		},
	}
}
