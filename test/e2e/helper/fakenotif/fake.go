//go:build e2e

/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package fakenotif

import (
	"context"
	"sync"

	"github.com/ardikabs/hibernator/internal/notification/sink"
)

const (
	// SinkType is the identifier for the fake sink.
	// "webhook" is used because it is a valid NotificationSinkType enum value in the CRD,
	// which allows HibernateNotification resources to reference this sink in E2E tests.
	// No built-in webhook sink implementation is registered by default, so the fake sink
	// acts as the sole "webhook" provider when DisableDefaultSinks is set.
	SinkType = "webhook"
)

// Record holds a single captured notification call.
type Record struct {
	// Payload is the full notification payload received by the sink.
	Payload sink.Payload
}

// Sink is a no-op sink that stores every Send call in memory.
// It is safe for concurrent use.
type Sink struct {
	mu      sync.Mutex
	records []Record
}

// New creates a new dummy Sink with an empty record store.
func New() *Sink {
	return &Sink{}
}

// Type returns the sink type identifier.
func (s *Sink) Type() string {
	return SinkType
}

// Send records the notification payload. It never returns an error and does not
// require any valid config in opts — the config bytes are intentionally ignored.
func (s *Sink) Send(_ context.Context, payload sink.Payload, _ sink.SendOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, Record{
		Payload: payload,
	})
	return nil
}

// Records returns a copy of all notifications received so far.
func (s *Sink) Records() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, len(s.records))
	copy(out, s.records)
	return out
}

// Reset clears all recorded notifications.
func (s *Sink) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = s.records[:0]
}

// Len returns the number of notifications received so far.
func (s *Sink) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}
