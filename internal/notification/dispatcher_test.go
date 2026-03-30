/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	sinktypes "github.com/ardikabs/hibernator/internal/notification/sink"
)

// stubSink is a test double that records Send calls.
type stubSink struct {
	sinkType  string
	sendFunc  func(ctx context.Context, payload Payload, opts sinktypes.SendOptions) error
	mu        sync.Mutex
	calls     []stubSendCall
	callCount atomic.Int32
}

type stubSendCall struct {
	Payload Payload
	Config  []byte
}

func newStubSink(sinkType string) *stubSink {
	return &stubSink{sinkType: sinkType}
}

func (s *stubSink) Type() string { return s.sinkType }

func (s *stubSink) Send(ctx context.Context, payload Payload, opts sinktypes.SendOptions) error {
	s.mu.Lock()
	s.calls = append(s.calls, stubSendCall{Payload: payload, Config: opts.Config})
	s.mu.Unlock()
	s.callCount.Add(1)
	if s.sendFunc != nil {
		return s.sendFunc(ctx, payload, opts)
	}
	return nil
}

func (s *stubSink) getCalls() []stubSendCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]stubSendCall, len(s.calls))
	copy(out, s.calls)
	return out
}

func newTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	return scheme
}

func sinkSecret(namespace, name string, config []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Data: map[string][]byte{
			"config": config,
		},
	}
}

func testPayload(event string) Payload {
	return Payload{
		ID:        types.NamespacedName{Namespace: "default", Name: "test-plan"},
		Event:     event,
		Timestamp: time.Date(2026, 3, 28, 10, 0, 0, 0, time.UTC),
		Phase:     "Hibernating",
		Operation: "Hibernate",
		CycleID:   "abc123",
	}
}

// startDispatcher starts the dispatcher in a goroutine and returns a cancel func.
func startDispatcher(t *testing.T, d *Dispatcher) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = d.Start(ctx)
	}()
	// Give the goroutines time to start.
	time.Sleep(50 * time.Millisecond)
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("dispatcher did not stop in time")
		}
	})
	return cancel
}

func TestDispatcherSendsNotification(t *testing.T) {
	stub := newStubSink("slack")
	registry := sinktypes.NewRegistry()
	registry.Register(stub)

	secret := sinkSecret("default", "slack-secret", []byte(`{"webhook_url":"https://hooks.slack.com/test"}`))
	client := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(secret).
		Build()

	d := NewDispatcher(logr.Discard(), client, registry, DispatcherConfig{
		Workers: 1, ChannelSize: 8,
	})

	startDispatcher(t, d)

	d.Submit(Request{
		Payload:   testPayload("Start"),
		SinkName:  "test-slack",
		SinkType:  "slack",
		SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "slack-secret"},
	})

	// Wait for delivery.
	assert.Eventually(t, func() bool {
		return stub.callCount.Load() >= 1
	}, 2*time.Second, 10*time.Millisecond)

	calls := stub.getCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "Start", calls[0].Payload.Event)
	assert.Equal(t, "test-plan", calls[0].Payload.ID.Name)
	assert.Equal(t, []byte(`{"webhook_url":"https://hooks.slack.com/test"}`), calls[0].Config)
}

func TestDispatcherUnknownSinkType(t *testing.T) {
	registry := sinktypes.NewRegistry()
	client := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()

	d := NewDispatcher(logr.Discard(), client, registry, DispatcherConfig{
		Workers: 1, ChannelSize: 8,
	})

	startDispatcher(t, d)

	d.Submit(Request{
		Payload:   testPayload("Start"),
		SinkName:  "missing-sink",
		SinkType:  "unknown",
		SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "some-secret"},
	})

	// Give time for the worker to process (and fail).
	time.Sleep(100 * time.Millisecond)
	// No panic, no blocking — test passes if we reach here.
}

func TestDispatcherSecretNotFound(t *testing.T) {
	stub := newStubSink("slack")
	registry := sinktypes.NewRegistry()
	registry.Register(stub)

	// No secret seeded.
	client := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()

	d := NewDispatcher(logr.Discard(), client, registry, DispatcherConfig{
		Workers: 1, ChannelSize: 8,
	})

	startDispatcher(t, d)

	d.Submit(Request{
		Payload:   testPayload("Success"),
		SinkName:  "test-slack",
		SinkType:  "slack",
		SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "nonexistent"},
	})

	time.Sleep(100 * time.Millisecond)
	assert.Empty(t, stub.getCalls(), "sink should not be called when secret is missing")
}

func TestDispatcherSecretMissingConfigKey(t *testing.T) {
	stub := newStubSink("slack")
	registry := sinktypes.NewRegistry()
	registry.Register(stub)

	// Secret exists but without "config" key.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "bad-secret"},
		Data:       map[string][]byte{"other_key": []byte("val")},
	}
	client := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(secret).
		Build()

	d := NewDispatcher(logr.Discard(), client, registry, DispatcherConfig{
		Workers: 1, ChannelSize: 8,
	})

	startDispatcher(t, d)

	d.Submit(Request{
		Payload:   testPayload("Failure"),
		SinkName:  "test-slack",
		SinkType:  "slack",
		SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "bad-secret"},
	})

	time.Sleep(100 * time.Millisecond)
	assert.Empty(t, stub.getCalls(), "sink should not be called when config key is missing")
}

func TestDispatcherSinkSendError(t *testing.T) {
	stub := newStubSink("slack")
	stub.sendFunc = func(_ context.Context, _ Payload, _ sinktypes.SendOptions) error {
		return errors.New("connection refused")
	}
	registry := sinktypes.NewRegistry()
	registry.Register(stub)

	secret := sinkSecret("default", "slack-secret", []byte(`{"webhook_url":"https://hooks.slack.com/test"}`))
	client := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(secret).
		Build()

	d := NewDispatcher(logr.Discard(), client, registry, DispatcherConfig{
		Workers: 1, ChannelSize: 8,
	})

	startDispatcher(t, d)

	d.Submit(Request{
		Payload:   testPayload("Failure"),
		SinkName:  "test-slack",
		SinkType:  "slack",
		SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "slack-secret"},
	})

	assert.Eventually(t, func() bool {
		return stub.callCount.Load() >= 1
	}, 2*time.Second, 10*time.Millisecond)

	// Confirm Send was called (even though it returned an error).
	assert.Len(t, stub.getCalls(), 1)
}

func TestDispatcherMultipleSinks(t *testing.T) {
	slackStub := newStubSink("slack")
	telegramStub := newStubSink("telegram")
	registry := sinktypes.NewRegistry()
	registry.Register(slackStub)
	registry.Register(telegramStub)

	slackSecret := sinkSecret("default", "slack-secret", []byte(`{"webhook_url":"url"}`))
	telegramSecret := sinkSecret("default", "telegram-secret", []byte(`{"token":"t","chat_id":"c"}`))
	client := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(slackSecret, telegramSecret).
		Build()

	d := NewDispatcher(logr.Discard(), client, registry, DispatcherConfig{
		Workers: 2, ChannelSize: 16,
	})

	startDispatcher(t, d)

	d.Submit(Request{
		Payload:   testPayload("Start"),
		SinkName:  "slack-sink",
		SinkType:  "slack",
		SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "slack-secret"},
	})
	d.Submit(Request{
		Payload:   testPayload("Start"),
		SinkName:  "telegram-sink",
		SinkType:  "telegram",
		SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "telegram-secret"},
	})

	assert.Eventually(t, func() bool {
		return slackStub.callCount.Load() >= 1 && telegramStub.callCount.Load() >= 1
	}, 2*time.Second, 10*time.Millisecond)

	assert.Len(t, slackStub.getCalls(), 1)
	assert.Len(t, telegramStub.getCalls(), 1)
}

func TestDispatcherOverflowQueuesAllRequests(t *testing.T) {
	// Slow sink to create backpressure — every request takes 50ms.
	stub := newStubSink("slack")
	stub.sendFunc = func(_ context.Context, _ Payload, _ sinktypes.SendOptions) error {
		time.Sleep(50 * time.Millisecond)
		return nil
	}
	registry := sinktypes.NewRegistry()
	registry.Register(stub)

	secret := sinkSecret("default", "slack-secret", []byte(`{"webhook_url":"url"}`))
	client := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(secret).
		Build()

	// Small channel — overflow queue absorbs the excess.
	d := NewDispatcher(logr.Discard(), client, registry, DispatcherConfig{
		Workers: 1, ChannelSize: 2,
	})

	startDispatcher(t, d)

	const total = 10
	// Submit must return immediately for every request — never block the caller.
	for i := 0; i < total; i++ {
		d.Submit(Request{
			Payload:   testPayload("Start"),
			SinkName:  "test",
			SinkType:  "slack",
			SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "slack-secret"},
		})
	}

	// All 10 requests should eventually be delivered — none dropped.
	assert.Eventually(t, func() bool {
		return stub.callCount.Load() >= int32(total)
	}, 5*time.Second, 10*time.Millisecond)

	assert.Equal(t, total, int(stub.callCount.Load()))
}

func TestDispatcherSubmitIsNonBlocking(t *testing.T) {
	// No workers running — channel will fill and overflow queue absorbs.
	registry := sinktypes.NewRegistry()
	registry.Register(newStubSink("slack"))

	client := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	d := NewDispatcher(logr.Discard(), client, registry, DispatcherConfig{
		Workers: 1, ChannelSize: 1,
	})

	// Start the dispatcher so done channel is active.
	startDispatcher(t, d)

	// Submit many more than channel capacity — must return immediately.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			d.Submit(Request{
				Payload:   testPayload("Start"),
				SinkName:  "test",
				SinkType:  "slack",
				SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s"},
			})
		}
	}()

	select {
	case <-done:
		// All Submits returned immediately.
	case <-time.After(1 * time.Second):
		t.Fatal("Submit blocked the caller — should be non-blocking")
	}
}

func TestDispatcherContextCancellation(t *testing.T) {
	stub := newStubSink("slack")
	registry := sinktypes.NewRegistry()
	registry.Register(stub)

	secret := sinkSecret("default", "slack-secret", []byte(`{"webhook_url":"url"}`))
	client := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(secret).
		Build()

	d := NewDispatcher(logr.Discard(), client, registry, DispatcherConfig{
		Workers: 1, ChannelSize: 8,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = d.Start(ctx)
	}()

	// Give it time to start.
	time.Sleep(50 * time.Millisecond)

	// Cancel and verify clean shutdown.
	cancel()
	select {
	case <-done:
		// Success — dispatcher stopped cleanly.
	case <-time.After(3 * time.Second):
		t.Fatal("dispatcher did not stop within timeout")
	}
}

func TestDispatcherConfig(t *testing.T) {
	d := NewDispatcher(logr.Discard(), nil, nil, DispatcherConfig{
		Workers:         8,
		ChannelSize:     512,
		DispatchTimeout: 10 * time.Second,
	})

	assert.Equal(t, 8, d.workers)
	assert.Equal(t, 512, d.channelSize)
	assert.Equal(t, 10*time.Second, d.dispatchTimeout)
}

func TestDispatcherConfigZeroValuesUseDefaults(t *testing.T) {
	d := NewDispatcher(logr.Discard(), nil, nil, DispatcherConfig{})

	// Zero fields should be replaced by defaults.
	assert.Equal(t, defaultWorkers, d.workers)
	assert.Equal(t, defaultChannelSize, d.channelSize)
	assert.Equal(t, defaultDispatchTimeout, d.dispatchTimeout)
}

func TestDispatcherPassesPayloadToSink(t *testing.T) {
	tests := []struct {
		name     string
		sinkType string
		event    string
	}{
		{
			name:     "slack start event",
			sinkType: "slack",
			event:    "Start",
		},
		{
			name:     "telegram start event",
			sinkType: "telegram",
			event:    "Start",
		},
		{
			name:     "webhook start event",
			sinkType: "webhook",
			event:    "Start",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := newStubSink(tt.sinkType)
			registry := sinktypes.NewRegistry()
			registry.Register(stub)

			secret := sinkSecret("default", "test-secret", []byte(`{"url":"http://example.com"}`))
			client := fake.NewClientBuilder().
				WithScheme(newTestScheme()).
				WithObjects(secret).
				Build()

			d := NewDispatcher(logr.Discard(), client, registry, DispatcherConfig{
				Workers: 1, ChannelSize: 8,
			})

			startDispatcher(t, d)

			d.Submit(Request{
				Payload:   testPayload(tt.event),
				SinkName:  "test-sink",
				SinkType:  tt.sinkType,
				SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "test-secret"},
			})

			assert.Eventually(t, func() bool {
				return stub.callCount.Load() >= 1
			}, 2*time.Second, 10*time.Millisecond)

			calls := stub.getCalls()
			require.Len(t, calls, 1)
			assert.Equal(t, tt.event, calls[0].Payload.Event)
			assert.Equal(t, "test-plan", calls[0].Payload.ID.Name)
			assert.Equal(t, "default", calls[0].Payload.ID.Namespace)
			assert.Equal(t, "Hibernating", calls[0].Payload.Phase)
			assert.Equal(t, "Hibernate", calls[0].Payload.Operation)
			assert.Equal(t, "test-sink", calls[0].Payload.SinkName)
			assert.Equal(t, tt.sinkType, calls[0].Payload.SinkType)
		})
	}
}

func TestResolveSecret(t *testing.T) {
	secret := sinkSecret("test-ns", "my-secret", []byte(`{"url":"https://example.com"}`))
	client := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(secret).
		Build()

	d := &Dispatcher{
		log:    logr.Discard(),
		client: client,
	}

	config, err := d.resolveSecret(context.Background(), "test-ns", hibernatorv1alpha1.ObjectKeyReference{Name: "my-secret"})
	require.NoError(t, err)
	assert.Equal(t, []byte(`{"url":"https://example.com"}`), config)
}

func TestResolveSecretNotFound(t *testing.T) {
	client := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		Build()

	d := &Dispatcher{
		log:    logr.Discard(),
		client: client,
	}

	_, err := d.resolveSecret(context.Background(), "ns", hibernatorv1alpha1.ObjectKeyReference{Name: "nonexistent"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "secrets \"nonexistent\" not found")
}

func TestResolveSecretMissingConfigKey(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "bad"},
		Data:       map[string][]byte{"wrong": []byte("val")},
	}
	client := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(secret).
		Build()

	d := &Dispatcher{
		log:    logr.Discard(),
		client: client,
	}

	_, err := d.resolveSecret(context.Background(), "ns", hibernatorv1alpha1.ObjectKeyReference{Name: "bad"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), `missing key "config"`)
}

func TestResolveCustomTemplate(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "test-ns", Name: "my-template"},
		Data:       map[string]string{defaultTemplateKey: `{{ .Plan.Name }} - {{ .Event }}`},
	}
	client := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(cm).
		Build()

	d := &Dispatcher{
		log:    logr.Discard(),
		client: client,
	}

	tmpl, err := d.resolveCustomTemplate(context.Background(), "test-ns", hibernatorv1alpha1.ObjectKeyReference{Name: "my-template"})
	require.NoError(t, err)
	assert.Equal(t, `{{ .Plan.Name }} - {{ .Event }}`, tmpl)
}

func TestResolveCustomTemplateWithCustomKey(t *testing.T) {
	customKey := "custom.gotpl"
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "tmpl-cm"},
		Data:       map[string]string{customKey: `custom template`},
	}
	client := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(cm).
		Build()

	d := &Dispatcher{
		log:    logr.Discard(),
		client: client,
	}

	tmpl, err := d.resolveCustomTemplate(context.Background(), "ns", hibernatorv1alpha1.ObjectKeyReference{
		Name: "tmpl-cm",
		Key:  &customKey,
	})
	require.NoError(t, err)
	assert.Equal(t, "custom template", tmpl)
}

func TestResolveCustomTemplateNotFound(t *testing.T) {
	client := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		Build()

	d := &Dispatcher{
		log:    logr.Discard(),
		client: client,
	}

	_, err := d.resolveCustomTemplate(context.Background(), "ns", hibernatorv1alpha1.ObjectKeyReference{Name: "nonexistent"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), `configmaps "nonexistent" not found`)
}

func TestResolveCustomTemplateMissingKey(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "bad-cm"},
		Data:       map[string]string{"wrong_key": "val"},
	}
	client := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(cm).
		Build()

	d := &Dispatcher{
		log:    logr.Discard(),
		client: client,
	}

	_, err := d.resolveCustomTemplate(context.Background(), "ns", hibernatorv1alpha1.ObjectKeyReference{Name: "bad-cm"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("missing key %q", defaultTemplateKey))
}

func TestDispatcherNeedLeaderElection(t *testing.T) {
	d := &Dispatcher{}
	assert.True(t, d.NeedLeaderElection())
}

func TestDispatcherMalformedCustomTemplate(t *testing.T) {
	stub := newStubSink("slack")
	registry := sinktypes.NewRegistry()
	registry.Register(stub)

	secret := sinkSecret("default", "slack-secret", []byte(`{"webhook_url":"https://hooks.slack.com/test"}`))
	// ConfigMap with invalid Go template syntax.
	malformedCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "bad-template"},
		Data:       map[string]string{defaultTemplateKey: `{{ .Plan.Name | nonexistentFunc }}`},
	}
	client := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(secret, malformedCM).
		Build()

	d := NewDispatcher(logr.Discard(), client, registry, DispatcherConfig{
		Workers: 1, ChannelSize: 8,
	})

	startDispatcher(t, d)

	tmplKey := defaultTemplateKey
	d.Submit(Request{
		Payload:   testPayload("Start"),
		SinkName:  "test-slack",
		SinkType:  "slack",
		SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "slack-secret"},
		TemplateRef: &hibernatorv1alpha1.ObjectKeyReference{
			Name: "bad-template",
			Key:  &tmplKey,
		},
	})

	// The sink should still receive the call — the template engine falls back
	// to plain text when execution fails (unrecognized function).
	assert.Eventually(t, func() bool {
		return stub.callCount.Load() >= 1
	}, 2*time.Second, 10*time.Millisecond)

	calls := stub.getCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "Start", calls[0].Payload.Event)
}
