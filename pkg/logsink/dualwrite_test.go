/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package logsink

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

// mockLogSink is a simple mock implementation of logr.LogSink for testing.
type mockLogSink struct {
	enabledLevel int
	infoCalls    []mockInfoCall
	errorCalls   []mockErrorCall
	mu           sync.Mutex
	name         string
	values       []interface{}
}

type mockInfoCall struct {
	level         int
	msg           string
	keysAndValues []interface{}
}

type mockErrorCall struct {
	err           error
	msg           string
	keysAndValues []interface{}
}

func newMockLogSink() *mockLogSink {
	return &mockLogSink{
		enabledLevel: 1, // Enable level 0 and 1
	}
}

func (m *mockLogSink) Init(info logr.RuntimeInfo) {}

func (m *mockLogSink) Enabled(level int) bool {
	return level <= m.enabledLevel
}

func (m *mockLogSink) Info(level int, msg string, keysAndValues ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.infoCalls = append(m.infoCalls, mockInfoCall{
		level:         level,
		msg:           msg,
		keysAndValues: keysAndValues,
	})
}

func (m *mockLogSink) Error(err error, msg string, keysAndValues ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errorCalls = append(m.errorCalls, mockErrorCall{
		err:           err,
		msg:           msg,
		keysAndValues: keysAndValues,
	})
}

func (m *mockLogSink) WithValues(keysAndValues ...interface{}) logr.LogSink {
	return &mockLogSink{
		enabledLevel: m.enabledLevel,
		name:         m.name,
		values:       append(m.values, keysAndValues...),
	}
}

func (m *mockLogSink) WithName(name string) logr.LogSink {
	newName := name
	if m.name != "" {
		newName = m.name + "/" + name
	}
	return &mockLogSink{
		enabledLevel: m.enabledLevel,
		name:         newName,
		values:       m.values,
	}
}

func (m *mockLogSink) getInfoCalls() []mockInfoCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]mockInfoCall{}, m.infoCalls...)
}

func (m *mockLogSink) getErrorCalls() []mockErrorCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]mockErrorCall{}, m.errorCalls...)
}

// mockSender is a mock implementation of LogSender for testing.
type mockSender struct {
	logs []sentLog
	mu   sync.Mutex
}

type sentLog struct {
	level   string
	message string
	fields  map[string]string
}

func newMockSender() *mockSender {
	return &mockSender{
		logs: make([]sentLog, 0),
	}
}

func (m *mockSender) Log(ctx context.Context, level, message string, fields map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, sentLog{
		level:   level,
		message: message,
		fields:  fields,
	})
	return nil
}

func (m *mockSender) getLogs() []sentLog {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]sentLog{}, m.logs...)
}

// Tests

func TestDualWriteSink_Info_DualWrite(t *testing.T) {
	underlying := newMockLogSink()
	sender := newMockSender()

	sink := NewDualWriteSink(underlying, sender)
	defer sink.Stop()

	log := logr.New(sink)
	log.Info("test message", "key", "value")

	// Wait for async processing
	time.Sleep(50 * time.Millisecond)

	// Check underlying sink received the log
	infoCalls := underlying.getInfoCalls()
	if len(infoCalls) != 1 {
		t.Errorf("expected 1 info call to underlying, got %d", len(infoCalls))
	}
	if infoCalls[0].msg != "test message" {
		t.Errorf("expected message 'test message', got %q", infoCalls[0].msg)
	}

	// Check sender received the log
	sentLogs := sender.getLogs()
	if len(sentLogs) != 1 {
		t.Errorf("expected 1 sent log, got %d", len(sentLogs))
	}
	if sentLogs[0].message != "test message" {
		t.Errorf("expected message 'test message', got %q", sentLogs[0].message)
	}
	if sentLogs[0].level != LevelInfo {
		t.Errorf("expected level 'INFO', got %q", sentLogs[0].level)
	}
	if sentLogs[0].fields["key"] != "value" {
		t.Errorf("expected field key='value', got %q", sentLogs[0].fields["key"])
	}
}

func TestDualWriteSink_Error_DualWrite(t *testing.T) {
	underlying := newMockLogSink()
	sender := newMockSender()

	sink := NewDualWriteSink(underlying, sender)
	defer sink.Stop()

	log := logr.New(sink)
	testErr := errors.New("test error")
	log.Error(testErr, "error occurred", "key", "value")

	// Wait for async processing
	time.Sleep(50 * time.Millisecond)

	// Check underlying sink received the error
	errorCalls := underlying.getErrorCalls()
	if len(errorCalls) != 1 {
		t.Errorf("expected 1 error call to underlying, got %d", len(errorCalls))
	}
	if errorCalls[0].msg != "error occurred" {
		t.Errorf("expected message 'error occurred', got %q", errorCalls[0].msg)
	}

	// Check sender received the error log
	sentLogs := sender.getLogs()
	if len(sentLogs) != 1 {
		t.Errorf("expected 1 sent log, got %d", len(sentLogs))
	}
	if sentLogs[0].level != LevelError {
		t.Errorf("expected level 'ERROR', got %q", sentLogs[0].level)
	}
	if sentLogs[0].fields["error"] != "test error" {
		t.Errorf("expected error field 'test error', got %q", sentLogs[0].fields["error"])
	}
}

func TestDualWriteSink_LevelMapping(t *testing.T) {
	underlying := newMockLogSink()
	sender := newMockSender()

	sink := NewDualWriteSink(underlying, sender)
	defer sink.Stop()

	log := logr.New(sink)

	// V(0) should map to INFO
	log.V(0).Info("info message")

	// V(1) should map to DEBUG
	log.V(1).Info("debug message")

	// Wait for async processing
	time.Sleep(50 * time.Millisecond)

	sentLogs := sender.getLogs()
	if len(sentLogs) != 2 {
		t.Fatalf("expected 2 sent logs, got %d", len(sentLogs))
	}

	if sentLogs[0].level != LevelInfo {
		t.Errorf("expected level 'INFO' for V(0), got %q", sentLogs[0].level)
	}
	if sentLogs[1].level != LevelDebug {
		t.Errorf("expected level 'DEBUG' for V(1), got %q", sentLogs[1].level)
	}
}

func TestDualWriteSink_LevelFiltering(t *testing.T) {
	underlying := &mockLogSink{enabledLevel: 0} // Only enable level 0
	sender := newMockSender()

	sink := NewDualWriteSink(underlying, sender)
	defer sink.Stop()

	log := logr.New(sink)

	// V(0) should be enabled
	log.V(0).Info("enabled message")

	// V(1) should be filtered by Enabled()
	// Note: logr's V() checks Enabled() and won't call Info() if disabled
	if log.V(1).Enabled() {
		t.Error("expected V(1) to be disabled")
	}

	// Wait for async processing
	time.Sleep(50 * time.Millisecond)

	sentLogs := sender.getLogs()
	if len(sentLogs) != 1 {
		t.Errorf("expected 1 sent log (filtered at logr level), got %d", len(sentLogs))
	}
}

func TestDualWriteSink_WithValues(t *testing.T) {
	underlying := newMockLogSink()
	sender := newMockSender()

	sink := NewDualWriteSink(underlying, sender)
	defer sink.Stop()

	log := logr.New(sink).WithValues("component", "test")
	log.Info("with values message")

	// Wait for async processing
	time.Sleep(50 * time.Millisecond)

	sentLogs := sender.getLogs()
	if len(sentLogs) != 1 {
		t.Fatalf("expected 1 sent log, got %d", len(sentLogs))
	}
	if sentLogs[0].fields["component"] != "test" {
		t.Errorf("expected field component='test', got %q", sentLogs[0].fields["component"])
	}
}

func TestDualWriteSink_WithName(t *testing.T) {
	underlying := newMockLogSink()
	sender := newMockSender()

	sink := NewDualWriteSink(underlying, sender)
	defer sink.Stop()

	log := logr.New(sink).WithName("mylogger")
	log.Info("named logger message")

	// Wait for async processing
	time.Sleep(50 * time.Millisecond)

	sentLogs := sender.getLogs()
	if len(sentLogs) != 1 {
		t.Fatalf("expected 1 sent log, got %d", len(sentLogs))
	}
	if sentLogs[0].fields["logger"] != "mylogger" {
		t.Errorf("expected field logger='mylogger', got %q", sentLogs[0].fields["logger"])
	}
}

func TestDualWriteSink_ChannelOverflow(t *testing.T) {
	underlying := newMockLogSink()
	sender := newMockSender()

	// Create sink with very small buffer
	sink := NewDualWriteSink(underlying, sender, WithBufferSize(2))
	defer sink.Stop()

	log := logr.New(sink)

	// Send more logs than buffer can hold
	for i := 0; i < 10; i++ {
		log.Info("overflow test", "index", i)
	}

	// Wait for async processing
	time.Sleep(100 * time.Millisecond)

	// All logs should reach underlying sink
	infoCalls := underlying.getInfoCalls()
	if len(infoCalls) != 10 {
		t.Errorf("expected 10 info calls to underlying, got %d", len(infoCalls))
	}

	// Some logs may be dropped in sender due to overflow (best-effort)
	sentLogs := sender.getLogs()
	if len(sentLogs) > 10 {
		t.Errorf("unexpected: got more sent logs than logged: %d", len(sentLogs))
	}
	t.Logf("sent %d of 10 logs (some may be dropped due to overflow)", len(sentLogs))
}

func TestDualWriteSink_GracefulShutdown(t *testing.T) {
	underlying := newMockLogSink()
	sender := newMockSender()

	sink := NewDualWriteSink(underlying, sender)

	log := logr.New(sink)
	log.Info("before shutdown")

	// Stop should drain the channel
	sink.Stop()

	// Wait a bit to ensure processing is complete
	time.Sleep(50 * time.Millisecond)

	sentLogs := sender.getLogs()
	if len(sentLogs) != 1 {
		t.Errorf("expected 1 sent log after graceful shutdown, got %d", len(sentLogs))
	}
}

func TestDualWriteSink_NilSender(t *testing.T) {
	underlying := newMockLogSink()

	// Create sink with nil sender - should not panic
	sink := NewDualWriteSink(underlying, nil)
	defer sink.Stop()

	log := logr.New(sink)

	// Should not panic
	log.Info("test with nil sender")
	log.Error(errors.New("test"), "error with nil sender")

	// Wait for async processing
	time.Sleep(50 * time.Millisecond)

	// Underlying should still receive logs
	infoCalls := underlying.getInfoCalls()
	if len(infoCalls) != 1 {
		t.Errorf("expected 1 info call to underlying, got %d", len(infoCalls))
	}
	errorCalls := underlying.getErrorCalls()
	if len(errorCalls) != 1 {
		t.Errorf("expected 1 error call to underlying, got %d", len(errorCalls))
	}
}

func TestDualWriteSink_StopIdempotent(t *testing.T) {
	underlying := newMockLogSink()
	sender := newMockSender()

	sink := NewDualWriteSink(underlying, sender)

	// Multiple stops should not panic
	sink.Stop()
	sink.Stop()
	sink.Stop()
}

func TestDualWriteSink_LogAfterStop(t *testing.T) {
	underlying := newMockLogSink()
	sender := newMockSender()

	sink := NewDualWriteSink(underlying, sender)
	sink.Stop()

	log := logr.New(sink)

	// Logging after stop should not panic
	log.Info("after stop")

	// Underlying should still receive logs
	infoCalls := underlying.getInfoCalls()
	if len(infoCalls) != 1 {
		t.Errorf("expected 1 info call to underlying even after stop, got %d", len(infoCalls))
	}

	// Sender should not receive (channel stopped)
	sentLogs := sender.getLogs()
	if len(sentLogs) != 0 {
		t.Errorf("expected 0 sent logs after stop, got %d", len(sentLogs))
	}
}
