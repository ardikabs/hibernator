/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package sink

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSink implements Sink for testing the registry.
type fakeSink struct {
	sinkType string
}

func (f *fakeSink) Type() string { return f.sinkType }

func (f *fakeSink) Send(_ context.Context, _ Payload, _ SendOptions) (SendResult, error) {
	return SendResult{}, nil
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()

	s := &fakeSink{sinkType: "test"}
	r.Register(s)

	got, ok := r.Get("test")
	require.True(t, ok)
	assert.Equal(t, "test", got.Type())
}

func TestRegistryGetUnknown(t *testing.T) {
	r := NewRegistry()

	_, ok := r.Get("nonexistent")
	assert.False(t, ok)
}

func TestRegistryList(t *testing.T) {
	r := NewRegistry()

	r.Register(&fakeSink{sinkType: "slack"})
	r.Register(&fakeSink{sinkType: "telegram"})

	types := r.List()
	assert.Len(t, types, 2)
	assert.Contains(t, types, "slack")
	assert.Contains(t, types, "telegram")
}

func TestRegistryValidate(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeSink{sinkType: "slack"})
	r.Register(&fakeSink{sinkType: "telegram"})

	err := r.Validate([]string{"slack", "telegram"})
	assert.NoError(t, err)

	err = r.Validate([]string{"slack", "discord"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "discord")
}

func TestRegistryOverwrite(t *testing.T) {
	r := NewRegistry()

	r.Register(&fakeSink{sinkType: "slack"})
	r.Register(&fakeSink{sinkType: "slack"})

	types := r.List()
	assert.Len(t, types, 1)
}
