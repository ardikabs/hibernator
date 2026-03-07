/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package server

// constructors_test.go tests server constructor functions and simple helpers
// that are not covered by the existing high-level tests.

import (
	"testing"

	"github.com/go-logr/logr"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// ---- GRPCServer ----

func TestNewServer_NotNil(t *testing.T) {
	fakeClient := k8sfake.NewSimpleClientset()
	execService := NewExecutionServiceServer(nil, nil, clk)

	srv := NewServer(":0", fakeClient, execService, logr.Discard())
	if srv == nil {
		t.Fatal("NewServer returned nil")
	}
}

func TestGRPCServer_NeedLeaderElection(t *testing.T) {
	fakeClient := k8sfake.NewSimpleClientset()
	execService := NewExecutionServiceServer(nil, nil, clk)

	srv := NewServer(":0", fakeClient, execService, logr.Discard())
	if srv.NeedLeaderElection() {
		t.Error("GRPCServer should not require leader election")
	}
}

// ---- WebhookServer ----

func TestNewWebhookServer_NotNil(t *testing.T) {
	fakeClient := k8sfake.NewSimpleClientset()
	execService := NewExecutionServiceServer(nil, nil, clk)

	ws := NewWebhookServer(":0", fakeClient, execService, logr.Discard())
	if ws == nil {
		t.Fatal("NewWebhookServer returned nil")
	}
}

func TestWebhookServer_NeedLeaderElection(t *testing.T) {
	fakeClient := k8sfake.NewSimpleClientset()
	execService := NewExecutionServiceServer(nil, nil, clk)

	ws := NewWebhookServer(":0", fakeClient, execService, logr.Discard())
	if ws.NeedLeaderElection() {
		t.Error("WebhookServer should not require leader election")
	}
}

// ---- WebSocketServer ----

func TestNewWebSocketServer_NotNil(t *testing.T) {
	fakeClient := k8sfake.NewSimpleClientset()
	execService := NewExecutionServiceServer(nil, nil, clk)

	srv := NewWebSocketServer(WebSocketServerOptions{
		Addr:         ":0",
		ExecService:  execService,
		K8sClientset: fakeClient,
		Log:          logr.Discard(),
	})
	if srv == nil {
		t.Fatal("NewWebSocketServer returned nil")
	}
}

func TestNewWebSocketServer_DefaultsApplied(t *testing.T) {
	fakeClient := k8sfake.NewSimpleClientset()
	execService := NewExecutionServiceServer(nil, nil, clk)

	srv := NewWebSocketServer(WebSocketServerOptions{
		Addr:         ":0",
		ExecService:  execService,
		K8sClientset: fakeClient,
		Log:          logr.Discard(),
		// Leave all durations zero → should be set to defaults
	})

	if srv.pingInterval != DefaultWebSocketPingInterval {
		t.Errorf("pingInterval = %v, want %v", srv.pingInterval, DefaultWebSocketPingInterval)
	}
	if srv.writeTimeout != DefaultWebSocketWriteTimeout {
		t.Errorf("writeTimeout = %v, want %v", srv.writeTimeout, DefaultWebSocketWriteTimeout)
	}
	if srv.readTimeout != DefaultWebSocketReadTimeout {
		t.Errorf("readTimeout = %v, want %v", srv.readTimeout, DefaultWebSocketReadTimeout)
	}
	if srv.maxMessageSize != DefaultWebSocketMaxMessageSize {
		t.Errorf("maxMessageSize = %v, want %v", srv.maxMessageSize, DefaultWebSocketMaxMessageSize)
	}
}

func TestWebSocketServer_NeedLeaderElection(t *testing.T) {
	fakeClient := k8sfake.NewSimpleClientset()
	execService := NewExecutionServiceServer(nil, nil, clk)

	srv := NewWebSocketServer(WebSocketServerOptions{
		Addr:         ":0",
		ExecService:  execService,
		K8sClientset: fakeClient,
		Log:          logr.Discard(),
	})

	if srv.NeedLeaderElection() {
		t.Error("WebSocketServer should not require leader election")
	}
}

// ---- contains helper ----

func TestContains_Found(t *testing.T) {
	sl := []string{"a", "b", "c"}
	if !contains(sl, "b") {
		t.Error("contains should return true for existing element")
	}
}

func TestContains_NotFound(t *testing.T) {
	sl := []string{"a", "b", "c"}
	if contains(sl, "z") {
		t.Error("contains should return false for missing element")
	}
}

func TestContains_EmptySlice(t *testing.T) {
	if contains([]string{}, "a") {
		t.Error("contains should return false for empty slice")
	}
}

func TestContains_NilSlice(t *testing.T) {
	if contains(nil, "a") {
		t.Error("contains should return false for nil slice")
	}
}
