/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package conflate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type dummyStruct struct {
	value string
}

func TestPipeline_SendRecv_Basic(t *testing.T) {
	slot := New[*dummyStruct]()
	obj := &dummyStruct{value: "test"}

	slot.Send(obj)

	select {
	case <-slot.C():
	default:
		t.Fatal("expected ready signal after send")
	}

	got := slot.Recv()
	assert.Same(t, obj, got)
}

func TestPipeline_RapidSends_KeepsLatest(t *testing.T) {
	slot := New[*dummyStruct]()

	first := &dummyStruct{value: "first"}
	second := &dummyStruct{value: "second"}
	third := &dummyStruct{value: "third"}

	slot.Send(first)
	slot.Send(second)
	slot.Send(third)

	// Capacity-1 channel: at most one signal.
	signalCount := 0
	for {
		select {
		case <-slot.C():
			signalCount++
		default:
			goto done
		}
	}

done:
	assert.Equal(t, 1, signalCount, "at most one signal should be buffered")
	assert.Same(t, third, slot.Recv(), "recv should return the last sent value")
}

func TestPipeline_SendNil_SignalsReady(t *testing.T) {
	slot := New[*dummyStruct]()
	slot.Send(nil)

	select {
	case <-slot.C():
	default:
		t.Fatal("expected ready signal even for nil send")
	}

	assert.Nil(t, slot.Recv())
}

func TestPipeline_RecvWithoutSend_ReturnsNil(t *testing.T) {
	slot := New[*dummyStruct]()
	assert.Nil(t, slot.Recv())
}
