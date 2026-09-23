/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package provider

import (
	"testing"
	"time"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// ---------------------------------------------------------------------------
// configMapDataChangedPredicate
// ---------------------------------------------------------------------------

func configMapWithData(data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cm", Namespace: "default"},
		Data:       data,
	}
}

func TestConfigMapPredicate_DataChanged_Passes(t *testing.T) {
	assert.True(t, configMapDataChangedPredicate.Update(event.UpdateEvent{
		ObjectOld: configMapWithData(map[string]string{"k": "v1"}),
		ObjectNew: configMapWithData(map[string]string{"k": "v2"}),
	}))
}

func TestConfigMapPredicate_AnnotationOnly_Blocked(t *testing.T) {
	oldCM := configMapWithData(map[string]string{"k": "v"})
	oldCM.Annotations = map[string]string{"a": "1"}
	newCM := configMapWithData(map[string]string{"k": "v"})
	newCM.Annotations = map[string]string{"a": "2"}
	assert.False(t, configMapDataChangedPredicate.Update(event.UpdateEvent{
		ObjectOld: oldCM,
		ObjectNew: newCM,
	}))
}

func TestConfigMapPredicate_NonConfigMap_PassesThrough(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{}
	assert.True(t, configMapDataChangedPredicate.Update(event.UpdateEvent{
		ObjectOld: plan,
		ObjectNew: plan,
	}))
}

func TestConfigMapPredicate_CreateDelete_Pass_GenericBlocked(t *testing.T) {
	cm := configMapWithData(nil)
	assert.True(t, configMapDataChangedPredicate.Create(event.CreateEvent{Object: cm}))
	assert.True(t, configMapDataChangedPredicate.Delete(event.DeleteEvent{Object: cm}))
	assert.False(t, configMapDataChangedPredicate.Generic(event.GenericEvent{Object: cm}))
}

// ---------------------------------------------------------------------------
// notificationDeletionPredicate
// ---------------------------------------------------------------------------

func notifWithDeletion(deletion *metav1.Time) *hibernatorv1alpha1.HibernateNotification {
	n := &hibernatorv1alpha1.HibernateNotification{
		ObjectMeta: metav1.ObjectMeta{Name: "n", Namespace: "default"},
	}
	if deletion != nil {
		n.DeletionTimestamp = deletion
	}
	return n
}

func TestNotificationDeletionPredicate_DeletionSet_Passes(t *testing.T) {
	now := metav1.NewTime(time.Now())
	assert.True(t, notificationDeletionPredicate.Update(event.UpdateEvent{
		ObjectOld: notifWithDeletion(nil),
		ObjectNew: notifWithDeletion(&now),
	}))
}

func TestNotificationDeletionPredicate_NoChange_Blocked(t *testing.T) {
	now := metav1.NewTime(time.Now())
	assert.False(t, notificationDeletionPredicate.Update(event.UpdateEvent{
		ObjectOld: notifWithDeletion(nil),
		ObjectNew: notifWithDeletion(nil),
	}))
	assert.False(t, notificationDeletionPredicate.Update(event.UpdateEvent{
		ObjectOld: notifWithDeletion(&now),
		ObjectNew: notifWithDeletion(&now),
	}))
}

func TestNotificationDeletionPredicate_NilObjects_Blocked(t *testing.T) {
	assert.False(t, notificationDeletionPredicate.Update(event.UpdateEvent{
		ObjectOld: nil,
		ObjectNew: notifWithDeletion(nil),
	}))
}

// ---------------------------------------------------------------------------
// exceptionStateChangedPredicate
// ---------------------------------------------------------------------------

func exceptionWithState(state hibernatorv1alpha1.ExceptionState) *hibernatorv1alpha1.ScheduleException {
	return &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "exc", Namespace: "default"},
		Status:     hibernatorv1alpha1.ScheduleExceptionStatus{State: state},
	}
}

func TestExceptionStateChangedPredicate_PendingToActive_Passes(t *testing.T) {
	assert.True(t, exceptionStateChangedPredicate.Update(event.UpdateEvent{
		ObjectOld: exceptionWithState(hibernatorv1alpha1.ExceptionStatePending),
		ObjectNew: exceptionWithState(hibernatorv1alpha1.ExceptionStateActive),
	}))
}

func TestExceptionStateChangedPredicate_ActiveToExpired_Passes(t *testing.T) {
	assert.True(t, exceptionStateChangedPredicate.Update(event.UpdateEvent{
		ObjectOld: exceptionWithState(hibernatorv1alpha1.ExceptionStateActive),
		ObjectNew: exceptionWithState(hibernatorv1alpha1.ExceptionStateExpired),
	}))
}

func TestExceptionStateChangedPredicate_UnsetToActive_Passes(t *testing.T) {
	assert.True(t, exceptionStateChangedPredicate.Update(event.UpdateEvent{
		ObjectOld: exceptionWithState(""),
		ObjectNew: exceptionWithState(hibernatorv1alpha1.ExceptionStateActive),
	}))
}

func TestExceptionStateChangedPredicate_SameState_Blocked(t *testing.T) {
	assert.False(t, exceptionStateChangedPredicate.Update(event.UpdateEvent{
		ObjectOld: exceptionWithState(hibernatorv1alpha1.ExceptionStateActive),
		ObjectNew: exceptionWithState(hibernatorv1alpha1.ExceptionStateActive),
	}))
}

func TestExceptionStateChangedPredicate_NonException_Blocked(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{}
	assert.False(t, exceptionStateChangedPredicate.Update(event.UpdateEvent{
		ObjectOld: plan,
		ObjectNew: plan,
	}))
}

func TestExceptionStateChangedPredicate_NilObjects_Blocked(t *testing.T) {
	assert.False(t, exceptionStateChangedPredicate.Update(event.UpdateEvent{
		ObjectOld: nil,
		ObjectNew: exceptionWithState(hibernatorv1alpha1.ExceptionStateActive),
	}))
}

// ---------------------------------------------------------------------------
// jobTerminalPredicate / onJobTerminalUpdate
// ---------------------------------------------------------------------------

func jobWithStatus(succeeded, failed int32, owned bool) *batchv1.Job {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "runner-abc", Namespace: "default"},
		Status:     batchv1.JobStatus{Succeeded: succeeded, Failed: failed},
	}
	if owned {
		job.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: "hibernator.ardikabs.com/v1alpha1",
			Kind:       "HibernatePlan",
			Name:       "p",
			UID:        "uid-1",
			Controller: ptr.To(true),
		}}
	}
	return job
}

func TestJobTerminalPredicate_SucceededTransition_PassesAndIncrementsNonce(t *testing.T) {
	r := &PlanReconciler{Log: logr.Discard()}
	key := types.NamespacedName{Name: "p", Namespace: "default"}

	passed := r.onJobTerminalUpdate(event.UpdateEvent{
		ObjectOld: jobWithStatus(0, 0, true),
		ObjectNew: jobWithStatus(1, 0, true),
	})

	assert.True(t, passed)
	assert.Equal(t, int64(1), r.DependencyNonces.Get(key))
}

func TestJobTerminalPredicate_FailedTransition_Passes(t *testing.T) {
	r := &PlanReconciler{Log: logr.Discard()}

	passed := r.onJobTerminalUpdate(event.UpdateEvent{
		ObjectOld: jobWithStatus(0, 0, true),
		ObjectNew: jobWithStatus(0, 3, true),
	})

	assert.True(t, passed)
	assert.Equal(t, int64(1), r.DependencyNonces.Get(types.NamespacedName{Name: "p", Namespace: "default"}))
}

func TestJobTerminalPredicate_AlreadyTerminal_Blocked(t *testing.T) {
	r := &PlanReconciler{Log: logr.Discard()}

	passed := r.onJobTerminalUpdate(event.UpdateEvent{
		ObjectOld: jobWithStatus(1, 0, true),
		ObjectNew: jobWithStatus(1, 1, true),
	})

	assert.False(t, passed)
	assert.Equal(t, int64(0), r.DependencyNonces.Get(types.NamespacedName{Name: "p", Namespace: "default"}))
}

func TestJobTerminalPredicate_RunningUnchanged_Blocked(t *testing.T) {
	r := &PlanReconciler{Log: logr.Discard()}

	passed := r.onJobTerminalUpdate(event.UpdateEvent{
		ObjectOld: jobWithStatus(0, 0, true),
		ObjectNew: jobWithStatus(0, 0, true),
	})

	assert.False(t, passed)
}

func TestJobTerminalPredicate_NoOwner_PassesWithoutNonce(t *testing.T) {
	r := &PlanReconciler{Log: logr.Discard()}

	passed := r.onJobTerminalUpdate(event.UpdateEvent{
		ObjectOld: jobWithStatus(0, 0, false),
		ObjectNew: jobWithStatus(1, 0, false),
	})

	// Event still proceeds to Reconcile; only the nonce bump is skipped.
	assert.True(t, passed)
}

func TestJobTerminalPredicate_NonJob_Blocked(t *testing.T) {
	r := &PlanReconciler{Log: logr.Discard()}
	plan := &hibernatorv1alpha1.HibernatePlan{}
	assert.False(t, r.onJobTerminalUpdate(event.UpdateEvent{
		ObjectOld: plan,
		ObjectNew: plan,
	}))
}

func TestJobTerminalPredicate_CreateDeleteGeneric_Blocked(t *testing.T) {
	r := &PlanReconciler{Log: logr.Discard()}
	pred := r.jobTerminalPredicate()
	job := jobWithStatus(0, 0, true)
	require.NotNil(t, pred.UpdateFunc)
	assert.False(t, pred.Create(event.CreateEvent{Object: job}))
	assert.False(t, pred.Delete(event.DeleteEvent{Object: job}))
	assert.False(t, pred.Generic(event.GenericEvent{Object: job}))
}
