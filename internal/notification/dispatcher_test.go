/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	sinktypes "github.com/ardikabs/hibernator/internal/notification/sink"
	slacksink "github.com/ardikabs/hibernator/internal/notification/sink/slack"
)

// stubSink is a test double that records Send calls.
type stubSink struct {
	sinkType  string
	sendFunc  func(ctx context.Context, payload Payload, opts sinktypes.SendOptions) error
	mu        sync.Mutex
	calls     []stubSendCall
	callCount atomic.Int32
	// notifyCh is signaled whenever a call is recorded.
	notifyCh chan struct{}
}

type stubSendCall struct {
	Payload Payload
	Config  []byte
}

func newStubSink(sinkType string) *stubSink {
	return &stubSink{
		sinkType: sinkType,
		notifyCh: make(chan struct{}, 100),
	}
}

func (s *stubSink) Type() string { return s.sinkType }

func (s *stubSink) Send(ctx context.Context, payload Payload, opts sinktypes.SendOptions) (Result, error) {
	s.mu.Lock()
	s.calls = append(s.calls, stubSendCall{Payload: payload, Config: opts.Config})
	s.mu.Unlock()
	s.callCount.Add(1)

	// Signal notification
	select {
	case s.notifyCh <- struct{}{}:
	default:
	}

	if s.sendFunc != nil {
		return Result{}, s.sendFunc(ctx, payload, opts)
	}
	return Result{}, nil
}

func (s *stubSink) getCalls() []stubSendCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]stubSendCall, len(s.calls))
	copy(out, s.calls)
	return out
}

func (s *stubSink) waitCalls(count int, timeout time.Duration) bool {
	start := time.Now()
	for int(s.callCount.Load()) < count {
		remaining := timeout - time.Since(start)
		if remaining <= 0 {
			return false
		}
		select {
		case <-s.notifyCh:
		case <-time.After(remaining):
			return false
		}
	}
	return true
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
		Plan: PlanInfo{
			Name:      "test-plan",
			Namespace: "default",
		},
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

	t.Cleanup(func() {
		t.Log("Performing shutting down")
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("dispatcher did not stop in time")
		}
	})
	return cancel
}

func TestDispatcher_SendsNotification(t *testing.T) {
	stub := newStubSink("slack")
	registry := sinktypes.NewRegistry()
	registry.Register(stub)

	secret := sinkSecret("default", "slack-secret", []byte(`{"webhook_url":"https://hooks.slack.com/test"}`))
	client := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(secret).
		Build()

	logger := logr.FromSlogHandler(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	d := NewDispatcher(logger, client, registry, DispatcherConfig{
		Workers: 1, ChannelSize: 8,
	})

	startDispatcher(t, d)

	d.Submit(Request{
		Payload:   testPayload("Start"),
		SinkName:  "test-slack",
		SinkType:  "slack",
		SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "slack-secret"},
	})

	// Wait for delivery without fixed sleep.
	require.True(t, stub.waitCalls(1, 2*time.Second), "Notification should be delivered")

	calls := stub.getCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "Start", calls[0].Payload.Event)
	assert.Equal(t, "test-plan", calls[0].Payload.Plan.Name)
	assert.Equal(t, []byte(`{"webhook_url":"https://hooks.slack.com/test"}`), calls[0].Config)
}

func TestDispatcher_UnknownSinkType(t *testing.T) {
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

	// Should not block or panic.
}

func TestDispatcher_SecretNotFound(t *testing.T) {
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

	// Wait a bit to ensure it had a chance but didn't deliver.
	time.Sleep(100 * time.Millisecond)
	assert.Empty(t, stub.getCalls(), "sink should not be called when secret is missing")
}

func TestDispatcher_SecretMissingConfigKey(t *testing.T) {
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

func TestDispatcher_SinkSendError(t *testing.T) {
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

	require.True(t, stub.waitCalls(1, 2*time.Second), "Sink Send should be called even if it returns error")
	assert.Len(t, stub.getCalls(), 1)
}

func TestDispatcher_MultipleSinks(t *testing.T) {
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

	require.True(t, slackStub.waitCalls(1, 2*time.Second), "Slack should receive notification")
	require.True(t, telegramStub.waitCalls(1, 2*time.Second), "Telegram should receive notification")

	assert.Len(t, slackStub.getCalls(), 1)
	assert.Len(t, telegramStub.getCalls(), 1)
}

func TestDispatcher_OverflowQueuesAllRequests(t *testing.T) {
	// Slow sink to create backpressure.
	stub := newStubSink("slack")
	stub.sendFunc = func(_ context.Context, _ Payload, _ sinktypes.SendOptions) error {
		time.Sleep(20 * time.Millisecond)
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
	for i := 0; i < total; i++ {
		d.Submit(Request{
			Payload:   testPayload("Start"),
			SinkName:  "test",
			SinkType:  "slack",
			SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "slack-secret"},
		})
	}

	require.True(t, stub.waitCalls(total, 5*time.Second), "All requests should eventually be delivered")
	assert.Equal(t, total, int(stub.callCount.Load()))
}

func TestDispatcher_SubmitIsNonBlocking(t *testing.T) {
	// No workers running — channel will fill and overflow queue absorbs.
	registry := sinktypes.NewRegistry()
	registry.Register(newStubSink("slack"))

	client := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	d := NewDispatcher(logr.Discard(), client, registry, DispatcherConfig{
		Workers: 1, ChannelSize: 1,
	})

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
		// Success — all Submits returned immediately.
	case <-time.After(1 * time.Second):
		t.Fatal("Submit blocked the caller — should be non-blocking")
	}
}

func TestDispatcher_ContextCancellation(t *testing.T) {
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
	startDone := make(chan struct{})
	go func() {
		defer close(startDone)
		_ = d.Start(ctx)
	}()

	// Wait for start
	time.Sleep(50 * time.Millisecond)

	cancel()
	select {
	case <-startDone:
		// Success — dispatcher stopped cleanly.
	case <-time.After(3 * time.Second):
		t.Fatal("dispatcher did not stop within timeout")
	}
}

func TestDispatcher_Config(t *testing.T) {
	d := NewDispatcher(logr.Discard(), nil, nil, DispatcherConfig{
		Workers:         8,
		ChannelSize:     512,
		DispatchTimeout: 10 * time.Second,
	})

	assert.Equal(t, 8, d.workers)
	assert.Equal(t, 512, d.channelSize)
	assert.Equal(t, 10*time.Second, d.dispatchTimeout)
}

func TestDispatcher_ConfigZeroValuesUseDefaults(t *testing.T) {
	d := NewDispatcher(logr.Discard(), nil, nil, DispatcherConfig{})

	assert.Equal(t, defaultWorkers, d.workers)
	assert.Equal(t, defaultChannelSize, d.channelSize)
	assert.Equal(t, defaultDispatchTimeout, d.dispatchTimeout)
}

func TestDispatcher_PassesPayloadToSink(t *testing.T) {
	tests := []struct {
		name     string
		sinkType string
		event    string
	}{
		{name: "slack start event", sinkType: "slack", event: "Start"},
		{name: "telegram start event", sinkType: "telegram", event: "Start"},
		{name: "webhook start event", sinkType: "webhook", event: "Start"},
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

			require.True(t, stub.waitCalls(1, 2*time.Second))

			calls := stub.getCalls()
			require.Len(t, calls, 1)
			assert.Equal(t, tt.event, calls[0].Payload.Event)
			assert.Equal(t, "test-plan", calls[0].Payload.Plan.Name)
			assert.Equal(t, "Hibernating", calls[0].Payload.Phase)
			assert.Equal(t, "Hibernate", calls[0].Payload.Operation)
			assert.Equal(t, "test-sink", calls[0].Payload.SinkName)
			assert.Equal(t, tt.sinkType, calls[0].Payload.SinkType)
		})
	}
}

func TestDispatcher_NeedLeaderElection(t *testing.T) {
	d := &Dispatcher{}
	assert.True(t, d.NeedLeaderElection())
}

func TestDispatcher_MalformedCustomTemplate(t *testing.T) {
	stub := newStubSink("slack")
	registry := sinktypes.NewRegistry()
	registry.Register(stub)

	secret := sinkSecret("default", "slack-secret", []byte(`{"webhook_url":"https://hooks.slack.com/test"}`))
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

	require.True(t, stub.waitCalls(1, 2*time.Second))
	assert.Equal(t, "Start", stub.getCalls()[0].Payload.Event)
}

func TestDispatcher_SlackJSONTemplate_EndToEnd(t *testing.T) {
	receivedCh := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var payload map[string]any
		require.NoError(t, json.Unmarshal(body, &payload))
		select {
		case receivedCh <- payload:
		default:
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	}))
	defer server.Close()

	secret := sinkSecret("default", "slack-secret", []byte(fmt.Sprintf(`{"webhook_url":%q,"format":"json"}`, server.URL)))
	tmplKey := defaultTemplateKey
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "slack-json-template"},
		Data: map[string]string{
			tmplKey: `{
	"text": {{ (printf "custom %s %s/%s" .Event .Plan.Namespace .Plan.Name) | toJson }},
	"blocks": [
		{
			"type": "section",
			"text": {"type": "mrkdwn", "text": {{ (printf "*Plan:* %s/%s" .Plan.Namespace .Plan.Name) | toJson }}}
		}
	]
}`,
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(secret, cm).
		Build()

	registry := sinktypes.NewRegistry()
	registry.Register(slacksink.New(NewTemplateEngine(logr.Discard()), slacksink.WithHTTPClient(&http.Client{Timeout: 5 * time.Second})))

	d := NewDispatcher(logr.Discard(), client, registry, DispatcherConfig{Workers: 1, ChannelSize: 8})
	startDispatcher(t, d)

	d.Submit(Request{
		Payload:   testPayload("Start"),
		SinkName:  "slack-json",
		SinkType:  "slack",
		SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "slack-secret"},
		TemplateRef: &hibernatorv1alpha1.ObjectKeyReference{
			Name: "slack-json-template",
			Key:  &tmplKey,
		},
	})

	select {
	case got := <-receivedCh:
		text, _ := got["text"].(string)
		assert.Contains(t, text, "custom Start default/test-plan")

		blocks, ok := got["blocks"].([]any)
		require.True(t, ok)
		require.NotEmpty(t, blocks)

		first, ok := blocks[0].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "section", first["type"])

	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Slack webhook payload")
	}
}

func TestDispatcher_SlackJSONTemplate_InvalidFallsBackToPreset(t *testing.T) {
	receivedCh := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var payload map[string]any
		require.NoError(t, json.Unmarshal(body, &payload))
		select {
		case receivedCh <- payload:
		default:
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	}))
	defer server.Close()

	secret := sinkSecret("default", "slack-secret", []byte(fmt.Sprintf(`{"webhook_url":%q,"format":"json","block_layout":"compact"}`, server.URL)))
	tmplKey := defaultTemplateKey
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "slack-bad-json-template"},
		Data: map[string]string{
			tmplKey: `this is not json`,
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(secret, cm).
		Build()

	registry := sinktypes.NewRegistry()
	registry.Register(slacksink.New(NewTemplateEngine(logr.Discard()), slacksink.WithHTTPClient(&http.Client{Timeout: 5 * time.Second})))

	d := NewDispatcher(logr.Discard(), client, registry, DispatcherConfig{Workers: 1, ChannelSize: 8})
	startDispatcher(t, d)

	d.Submit(Request{
		Payload:   testPayload("Start"),
		SinkName:  "slack-json",
		SinkType:  "slack",
		SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "slack-secret"},
		TemplateRef: &hibernatorv1alpha1.ObjectKeyReference{
			Name: "slack-bad-json-template",
			Key:  &tmplKey,
		},
	})

	select {
	case got := <-receivedCh:
		text, _ := got["text"].(string)
		assert.Contains(t, text, "[Start]")
		assert.Contains(t, text, "default/test-plan")

		blocks, ok := got["blocks"].([]any)
		require.True(t, ok)
		require.NotEmpty(t, blocks)

	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Slack webhook payload")
	}
}

func TestDispatcher_SubmitAfterShutdown(t *testing.T) {
	stub := newStubSink("slack")
	registry := sinktypes.NewRegistry()
	registry.Register(stub)
	secret := sinkSecret("default", "slack-secret", []byte(`{"webhook_url":"url"}`))
	client := fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(secret).Build()
	d := NewDispatcher(logr.Discard(), client, registry, DispatcherConfig{Workers: 1, ChannelSize: 2})

	ctx, cancel := context.WithCancel(context.Background())
	startDone := make(chan struct{})
	go func() {
		defer close(startDone)
		_ = d.Start(ctx)
	}()

	// Wait for start
	time.Sleep(50 * time.Millisecond)

	cancel()
	<-startDone

	// Submit after shutdown should discard request
	d.Submit(Request{
		Payload:   testPayload("Late"),
		SinkName:  "test",
		SinkType:  "slack",
		SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "slack-secret"},
	})

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(0), stub.callCount.Load(), "No delivery after shutdown")
}

func TestDispatcher_SubmitWithCancelledContext(t *testing.T) {
	stub := newStubSink("slack")
	registry := sinktypes.NewRegistry()
	registry.Register(stub)

	client := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	d := NewDispatcher(logr.Discard(), client, registry, DispatcherConfig{
		Workers: 1, ChannelSize: 2,
	})

	ctx, cancel := context.WithCancel(context.Background())
	// Set the context to dispatcher
	cancel() // Cancel it immediately

	// We need to satisfy startWg.Wait() in Submit.
	// In a real scenario, d.Start(ctx) would have been called.
	// For this unit test, we can manually trigger the WaitGroup if we don't call Start.
	// But Submit calls d.startWg.Wait(). If we don't start it, it will block.

	// Let's use the startDispatcher helper from dispatcher_test.go instead,
	// but we'll need to copy it or just use the same pattern.

	done := make(chan struct{})
	go func() {
		_ = d.Start(ctx)
		close(done)
	}()

	// Wait for dispatcher to stop
	<-done

	// Now Submit with cancelled context
	d.Submit(Request{
		Payload:  testPayload("Late"),
		SinkType: "slack",
	})

	assert.Equal(t, int32(0), stub.callCount.Load(), "Should not deliver when context is cancelled")
}

func TestDispatcher_SubmitWithAvailableChannelSpace(t *testing.T) {
	stub := newStubSink("slack")
	registry := sinktypes.NewRegistry()
	registry.Register(stub)

	secret := sinkSecret("default", "slack-secret", []byte(`{"url":"http://test"}`))
	client := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(secret).
		Build()

	d := NewDispatcher(logr.Discard(), client, registry, DispatcherConfig{
		Workers: 1, ChannelSize: 10,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Start(ctx)

	// Wait for start
	time.Sleep(50 * time.Millisecond)

	d.Submit(Request{
		Payload:   testPayload("Normal"),
		SinkType:  "slack",
		SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "slack-secret"},
	})

	assert.Eventually(t, func() bool {
		return stub.callCount.Load() == 1
	}, 1*time.Second, 10*time.Millisecond)
}

func TestDispatcher_SubmitWithFullChannelAndDrain(t *testing.T) {
	stub := newStubSink("slack")
	// Make sink slow to fill the channel
	stub.sendFunc = func(ctx context.Context, payload Payload, opts sinktypes.SendOptions) error {
		time.Sleep(100 * time.Millisecond)
		return nil
	}
	registry := sinktypes.NewRegistry()
	registry.Register(stub)

	secret := sinkSecret("default", "slack-secret", []byte(`{"url":"http://test"}`))
	client := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(secret).
		Build()

	// 1 worker, channel size 1.
	d := NewDispatcher(logr.Discard(), client, registry, DispatcherConfig{
		Workers: 1, ChannelSize: 1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Start(ctx)

	// Wait for start
	time.Sleep(50 * time.Millisecond)

	// 1st request: goes to worker (worker is now busy for 100ms)
	d.Submit(Request{Payload: testPayload("req1"), SinkType: "slack", SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "slack-secret"}})

	// 2nd request: fills the channel (size 1)
	d.Submit(Request{Payload: testPayload("req2"), SinkType: "slack", SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "slack-secret"}})

	// 3rd request: should go to overflow
	d.Submit(Request{Payload: testPayload("req3"), SinkType: "slack", SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "slack-secret"}})

	// Eventually all should be delivered
	assert.Eventually(t, func() bool {
		return stub.callCount.Load() == 3
	}, 2*time.Second, 10*time.Millisecond)
}

func TestDispatcher_FullProcessOverflowAndFlush(t *testing.T) {
	stub := newStubSink("slack")

	// Barrier: signals when the worker has picked up the first request and is blocked.
	workerBusy := make(chan struct{})
	block := make(chan struct{})
	var once sync.Once
	stub.sendFunc = func(ctx context.Context, payload Payload, opts sinktypes.SendOptions) error {
		once.Do(func() { close(workerBusy) })
		<-block
		return nil
	}
	registry := sinktypes.NewRegistry()
	registry.Register(stub)

	secret := sinkSecret("default", "slack-secret", []byte(`{"url":"http://test"}`))
	client := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(secret).
		Build()

	logger := logr.FromSlogHandler(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	d := NewDispatcher(logger, client, registry, DispatcherConfig{
		Workers:     1,
		ChannelSize: 1,
	})

	ctx, cancel := context.WithCancel(context.Background())

	startDone := make(chan struct{})
	go func() {
		_ = d.Start(ctx)
		close(startDone)
	}()

	// Submit first request and wait for the worker to pick it up.
	// Once workerBusy fires, the worker holds req1 and the channel is empty.
	d.Submit(Request{Payload: testPayload("req1"), SinkType: "slack", SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "slack-secret"}})
	select {
	case <-workerBusy:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for worker to pick up first request")
	}

	// Worker is blocked on <-block. Channel is empty.
	// req2 fills the channel (size 1), req3 goes to overflow.
	d.Submit(Request{Payload: testPayload("req2"), SinkType: "slack", SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "slack-secret"}})
	d.Submit(Request{Payload: testPayload("req3"), SinkType: "slack", SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "slack-secret"}})

	// Worker is still blocked — overflow must hold req3 before we release.
	assert.Equal(t, 1, d.overflow.Len(), "req3 should be in overflow while worker is blocked")

	// Unblock the worker so it can process all requests.
	close(block)

	// Wait for all 3 to be delivered deterministically.
	if !stub.waitCalls(3, 2*time.Second) {
		t.Fatal("timed out waiting for all 3 requests to be delivered")
	}

	// Cancel context to trigger shutdown.
	cancel()
	<-startDone

	assert.Equal(t, int32(3), stub.callCount.Load())
}

func TestDispatcher_ShutdownFlushesOverflowItems(t *testing.T) {
	// Verify that items stuck in overflow and the channel at shutdown time are
	// still delivered during the graceful-shutdown drain.
	stub := newStubSink("slack")

	// Barrier: signals when the worker has picked up the first request and is blocked.
	workerBusy := make(chan struct{})
	block := make(chan struct{})
	var once sync.Once
	stub.sendFunc = func(ctx context.Context, payload Payload, opts sinktypes.SendOptions) error {
		once.Do(func() { close(workerBusy) })
		<-block
		return nil
	}
	registry := sinktypes.NewRegistry()
	registry.Register(stub)

	secret := sinkSecret("default", "slack-secret", []byte(`{"url":"http://test"}`))
	client := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(secret).
		Build()

	logger := logr.FromSlogHandler(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	d := NewDispatcher(logger, client, registry, DispatcherConfig{
		Workers: 1, ChannelSize: 1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	startDone := make(chan struct{})
	go func() {
		_ = d.Start(ctx)
		close(startDone)
	}()

	const total = 5

	// Submit first request and wait for the worker to pick it up.
	d.Submit(Request{Payload: testPayload("req"), SinkType: "slack", SinkName: "slack-0", SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "slack-secret"}})
	select {
	case <-workerBusy:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for worker to pick up first request")
	}

	// Worker is blocked. Submit remaining: 1 fills channel, rest go to overflow.
	for i := 1; i < total; i++ {
		d.Submit(Request{Payload: testPayload("req"), SinkType: "slack", SinkName: fmt.Sprintf("slack-%d", i), SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "slack-secret"}})
	}

	// Worker is still blocked — overflow must hold the excess before we release.
	assert.GreaterOrEqual(t, d.overflow.Len(), 1, "some items should be in overflow while worker is blocked")

	// Unblock the worker and cancel immediately — forces shutdown with pending items.
	close(block)
	cancel()
	<-startDone

	// All items must be delivered during graceful shutdown.
	assert.Equal(t, int32(total), stub.callCount.Load(), "All items should be delivered during graceful shutdown")
}

func TestDispatcher_ConcurrentSubmitDuringShutdown(t *testing.T) {
	// Hammer Submit from multiple goroutines while simultaneously shutting down.
	// Must not panic (send on closed channel) or race.
	stub := newStubSink("slack")
	registry := sinktypes.NewRegistry()
	registry.Register(stub)

	secret := sinkSecret("default", "slack-secret", []byte(`{"url":"http://test"}`))
	client := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(secret).
		Build()

	d := NewDispatcher(logr.Discard(), client, registry, DispatcherConfig{
		Workers: 2, ChannelSize: 4,
	})

	ctx, cancel := context.WithCancel(context.Background())
	startDone := make(chan struct{})
	go func() {
		_ = d.Start(ctx)
		close(startDone)
	}()
	time.Sleep(50 * time.Millisecond)

	// Spawn many concurrent submitters.
	const goroutines = 10
	const perGoroutine = 20
	submitDone := make(chan struct{})
	go func() {
		defer close(submitDone)
		var wg sync.WaitGroup
		for g := 0; g < goroutines; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < perGoroutine; i++ {
					d.Submit(Request{
						Payload:   testPayload("concurrent"),
						SinkType:  "slack",
						SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "slack-secret"},
					})
				}
			}()
		}
		wg.Wait()
	}()

	// Cancel context while submitters are still running.
	time.Sleep(5 * time.Millisecond)
	cancel()

	<-submitDone
	<-startDone

	// No panic and no race is the success criteria.
	// Some requests are delivered, some may be dropped after shuttingDown is set.
	assert.GreaterOrEqual(t, stub.callCount.Load(), int32(0))
}

func TestDispatcher_OverflowMaxSizeDropsExcess(t *testing.T) {
	// When overflow queue reaches maxOverflowSize, excess requests are dropped.
	stub := newStubSink("slack")
	// Block all sends so nothing gets consumed.
	stub.sendFunc = func(ctx context.Context, payload Payload, opts sinktypes.SendOptions) error {
		<-ctx.Done()
		return ctx.Err()
	}
	registry := sinktypes.NewRegistry()
	registry.Register(stub)

	client := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()

	const maxOverflow = 3
	d := NewDispatcher(logr.Discard(), client, registry, DispatcherConfig{
		Workers:         1,
		ChannelSize:     1,
		MaxOverflowSize: maxOverflow,
	})

	ctx, cancel := context.WithCancel(context.Background())
	startDone := make(chan struct{})
	go func() {
		_ = d.Start(ctx)
		close(startDone)
	}()
	time.Sleep(50 * time.Millisecond)

	// Fill: 1 in worker (blocked), 1 in channel, maxOverflow in overflow = maxOverflow+2.
	// Any additional should be dropped.
	for i := 0; i < maxOverflow+10; i++ {
		d.Submit(Request{Payload: testPayload("flood"), SinkType: "slack", SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "slack-secret"}})
	}

	overflowLen := d.overflow.Len()
	assert.LessOrEqual(t, overflowLen, maxOverflow, "Overflow should not exceed max size")

	cancel()
	<-startDone
}
