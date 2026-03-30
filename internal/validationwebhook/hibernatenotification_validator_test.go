/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package validationwebhook

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

func newTestNotification(sinks ...hibernatorv1alpha1.NotificationSink) *hibernatorv1alpha1.HibernateNotification {
	return &hibernatorv1alpha1.HibernateNotification{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-notif",
			Namespace: "default",
		},
		Spec: hibernatorv1alpha1.HibernateNotificationSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "test"},
			},
			OnEvents: []hibernatorv1alpha1.NotificationEvent{hibernatorv1alpha1.EventStart},
			Sinks:    sinks,
		},
	}
}

func TestHibernateNotificationValidator_UniqueSinkNames(t *testing.T) {
	v := NewHibernateNotificationValidator(logr.Discard())

	notif := newTestNotification(
		hibernatorv1alpha1.NotificationSink{
			Name: "slack-prod",
			Type: hibernatorv1alpha1.SinkSlack,
			SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "slack-secret"},
		},
		hibernatorv1alpha1.NotificationSink{
			Name: "telegram-prod",
			Type: hibernatorv1alpha1.SinkTelegram,
			SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "tg-secret"},
		},
	)

	_, err := v.ValidateCreate(context.Background(), notif)
	require.NoError(t, err)
}

func TestHibernateNotificationValidator_DuplicateSinkNames(t *testing.T) {
	v := NewHibernateNotificationValidator(logr.Discard())

	notif := newTestNotification(
		hibernatorv1alpha1.NotificationSink{
			Name: "my-sink",
			Type: hibernatorv1alpha1.SinkSlack,
			SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s1"},
		},
		hibernatorv1alpha1.NotificationSink{
			Name: "my-sink",
			Type: hibernatorv1alpha1.SinkTelegram,
			SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s2"},
		},
	)

	_, err := v.ValidateCreate(context.Background(), notif)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate sink name")
	assert.Contains(t, err.Error(), "sinks[1]")
}

func TestHibernateNotificationValidator_MultipleDuplicates(t *testing.T) {
	v := NewHibernateNotificationValidator(logr.Discard())

	notif := newTestNotification(
		hibernatorv1alpha1.NotificationSink{
			Name: "dup",
			Type: hibernatorv1alpha1.SinkSlack,
			SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s1"},
		},
		hibernatorv1alpha1.NotificationSink{
			Name: "unique",
			Type: hibernatorv1alpha1.SinkTelegram,
			SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s2"},
		},
		hibernatorv1alpha1.NotificationSink{
			Name: "dup",
			Type: hibernatorv1alpha1.SinkWebhook,
			SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s3"},
		},
		hibernatorv1alpha1.NotificationSink{
			Name: "dup",
			Type: hibernatorv1alpha1.SinkSlack,
			SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s4"},
		},
	)

	_, err := v.ValidateCreate(context.Background(), notif)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate sink name")
}

func TestHibernateNotificationValidator_Update(t *testing.T) {
	v := NewHibernateNotificationValidator(logr.Discard())

	old := newTestNotification(
		hibernatorv1alpha1.NotificationSink{
			Name: "a",
			Type: hibernatorv1alpha1.SinkSlack,
			SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s1"},
		},
	)
	new := newTestNotification(
		hibernatorv1alpha1.NotificationSink{
			Name: "a",
			Type: hibernatorv1alpha1.SinkSlack,
			SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s1"},
		},
		hibernatorv1alpha1.NotificationSink{
			Name: "a",
			Type: hibernatorv1alpha1.SinkTelegram,
			SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s2"},
		},
	)

	_, err := v.ValidateUpdate(context.Background(), old, new)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate sink name")
}

func TestHibernateNotificationValidator_DeleteAlwaysAllowed(t *testing.T) {
	v := NewHibernateNotificationValidator(logr.Discard())

	_, err := v.ValidateDelete(context.Background(), newTestNotification())
	require.NoError(t, err)
}

func TestHibernateNotificationValidator_SingleSink(t *testing.T) {
	v := NewHibernateNotificationValidator(logr.Discard())

	notif := newTestNotification(
		hibernatorv1alpha1.NotificationSink{
			Name: "only-one",
			Type: hibernatorv1alpha1.SinkSlack,
			SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "secret"},
		},
	)

	_, err := v.ValidateCreate(context.Background(), notif)
	require.NoError(t, err)
}
