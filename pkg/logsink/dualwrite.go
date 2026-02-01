/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package logsink provides log sink implementations for dual-write logging.
package logsink

import (
	"context"
	"fmt"
	"sync"

	"github.com/go-logr/logr"
)

const (
	// DefaultBufferSize is the default channel buffer size for log entries.
	DefaultBufferSize = 100

	// LevelInfo maps to logr level 0.
	LevelInfo = "INFO"

	// LevelDebug maps to logr level 1+.
	LevelDebug = "DEBUG"

	// LevelError is used for Error() calls.
	LevelError = "ERROR"
)

// LogSender defines the interface for sending logs to a remote destination.
type LogSender interface {
	// Log sends a single log entry to the remote destination.
	Log(ctx context.Context, level, message string, fields map[string]string) error
}

// logEntry represents a log entry to be sent to the remote destination.
type logEntry struct {
	level   string
	message string
	fields  map[string]string
}

// sharedState holds state that is shared across all child sinks.
type sharedState struct {
	logCh   chan *logEntry
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	stopped bool
	mu      sync.RWMutex
}

// DualWriteSink is a logr.LogSink that writes to both an underlying sink (stdout)
// and streams logs asynchronously via a channel to a remote destination.
type DualWriteSink struct {
	underlying logr.LogSink
	sender     LogSender
	name       string
	values     []interface{}
	shared     *sharedState
}

// DualWriteSinkOption configures a DualWriteSink.
type DualWriteSinkOption func(*sharedState)

// WithBufferSize sets the channel buffer size.
func WithBufferSize(size int) DualWriteSinkOption {
	return func(s *sharedState) {
		s.logCh = make(chan *logEntry, size)
	}
}

// NewDualWriteSink creates a new DualWriteSink that wraps the underlying sink
// and streams logs to the provided sender.
func NewDualWriteSink(underlying logr.LogSink, sender LogSender, opts ...DualWriteSinkOption) *DualWriteSink {
	ctx, cancel := context.WithCancel(context.Background())

	shared := &sharedState{
		logCh:  make(chan *logEntry, DefaultBufferSize),
		ctx:    ctx,
		cancel: cancel,
	}

	for _, opt := range opts {
		opt(shared)
	}

	sink := &DualWriteSink{
		underlying: underlying,
		sender:     sender,
		shared:     shared,
	}

	// Start the streaming goroutine
	shared.wg.Add(1)
	go sink.streamLoop()

	return sink
}

// streamLoop continuously reads from the channel and sends logs to the remote destination.
func (s *DualWriteSink) streamLoop() {
	defer s.shared.wg.Done()

	for {
		select {
		case <-s.shared.ctx.Done():
			// Drain remaining entries before exiting
			s.drainChannel()
			return
		case entry, ok := <-s.shared.logCh:
			if !ok {
				return
			}
			s.sendEntry(entry)
		}
	}
}

// drainChannel sends all remaining entries in the channel.
func (s *DualWriteSink) drainChannel() {
	for {
		select {
		case entry, ok := <-s.shared.logCh:
			if !ok {
				return
			}
			s.sendEntry(entry)
		default:
			return
		}
	}
}

// sendEntry sends a single entry to the sender (best-effort).
func (s *DualWriteSink) sendEntry(entry *logEntry) {
	if s.sender == nil {
		return
	}
	// Use background context for sending - ctx is already cancelled during shutdown
	_ = s.sender.Log(context.Background(), entry.level, entry.message, entry.fields)
}

// Stop gracefully shuts down the sink, draining remaining log entries.
func (s *DualWriteSink) Stop() {
	s.shared.mu.Lock()
	if s.shared.stopped {
		s.shared.mu.Unlock()
		return
	}
	s.shared.stopped = true
	s.shared.mu.Unlock()

	s.shared.cancel()
	s.shared.wg.Wait()
}

// Init implements logr.LogSink.
func (s *DualWriteSink) Init(info logr.RuntimeInfo) {
	s.underlying.Init(info)
}

// Enabled implements logr.LogSink - delegates to underlying sink.
func (s *DualWriteSink) Enabled(level int) bool {
	return s.underlying.Enabled(level)
}

// Info implements logr.LogSink.
func (s *DualWriteSink) Info(level int, msg string, keysAndValues ...interface{}) {
	// Always write to underlying sink (stdout)
	s.underlying.Info(level, msg, keysAndValues...)

	// Queue for async streaming (non-blocking, drop on overflow)
	s.queueLog(level, msg, keysAndValues...)
}

// Error implements logr.LogSink.
func (s *DualWriteSink) Error(err error, msg string, keysAndValues ...interface{}) {
	// Always write to underlying sink (stdout)
	s.underlying.Error(err, msg, keysAndValues...)

	// Queue for async streaming with error info
	fields := s.kvToFields(keysAndValues...)
	if err != nil {
		if fields == nil {
			fields = make(map[string]string)
		}
		fields["error"] = err.Error()
	}

	s.queueEntry(LevelError, msg, fields)
}

// WithValues implements logr.LogSink.
func (s *DualWriteSink) WithValues(keysAndValues ...interface{}) logr.LogSink {
	return &DualWriteSink{
		underlying: s.underlying.WithValues(keysAndValues...),
		sender:     s.sender,
		name:       s.name,
		values:     append(s.values, keysAndValues...),
		shared:     s.shared, // Share the same shared state
	}
}

// WithName implements logr.LogSink.
func (s *DualWriteSink) WithName(name string) logr.LogSink {
	newName := name
	if s.name != "" {
		newName = s.name + "/" + name
	}
	return &DualWriteSink{
		underlying: s.underlying.WithName(name),
		sender:     s.sender,
		name:       newName,
		values:     s.values,
		shared:     s.shared, // Share the same shared state
	}
}

// queueLog converts log info and queues it for async streaming.
func (s *DualWriteSink) queueLog(level int, msg string, keysAndValues ...interface{}) {
	levelStr := LevelInfo
	if level > 0 {
		levelStr = LevelDebug
	}

	fields := s.kvToFields(keysAndValues...)
	s.queueEntry(levelStr, msg, fields)
}

// queueEntry adds an entry to the channel (non-blocking, drop on overflow).
func (s *DualWriteSink) queueEntry(level, msg string, fields map[string]string) {
	s.shared.mu.RLock()
	stopped := s.shared.stopped
	s.shared.mu.RUnlock()

	if stopped {
		return
	}

	entry := &logEntry{
		level:   level,
		message: msg,
		fields:  fields,
	}

	// Add logger name to fields if present
	if s.name != "" {
		if entry.fields == nil {
			entry.fields = make(map[string]string)
		}
		entry.fields["logger"] = s.name
	}

	// Add stored values to fields
	if len(s.values) > 0 {
		storedFields := s.kvToFields(s.values...)
		if entry.fields == nil {
			entry.fields = make(map[string]string)
		}
		for k, v := range storedFields {
			entry.fields[k] = v
		}
	}

	// Non-blocking send - drop on overflow (best-effort)
	select {
	case s.shared.logCh <- entry:
	default:
		// Channel full, drop the log (best-effort streaming)
	}
}

// kvToFields converts key-value pairs to a string map.
func (s *DualWriteSink) kvToFields(keysAndValues ...interface{}) map[string]string {
	if len(keysAndValues) == 0 {
		return nil
	}

	fields := make(map[string]string)
	for i := 0; i+1 < len(keysAndValues); i += 2 {
		key := fmt.Sprintf("%v", keysAndValues[i])
		value := fmt.Sprintf("%v", keysAndValues[i+1])
		fields[key] = value
	}
	return fields
}
