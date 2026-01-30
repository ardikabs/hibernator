/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package client

import (
	"testing"
	"time"

	"github.com/go-logr/logr"
)

func TestClientTypes(t *testing.T) {
	if ClientTypeGRPC != "grpc" {
		t.Errorf("expected 'grpc', got %s", ClientTypeGRPC)
	}
	if ClientTypeWebhook != "webhook" {
		t.Errorf("expected 'webhook', got %s", ClientTypeWebhook)
	}
	if ClientTypeAuto != "auto" {
		t.Errorf("expected 'auto', got %s", ClientTypeAuto)
	}
}

func TestClientConfig(t *testing.T) {
	cfg := ClientConfig{
		Type:        ClientTypeGRPC,
		GRPCAddress: "localhost:9443",
		WebhookURL:  "http://localhost:8080",
		ExecutionID: "exec-123",
		TokenPath:   "/var/run/secrets/token",
		UseTLS:      true,
		Timeout:     30 * time.Second,
		Log:         logr.Discard(),
	}

	if cfg.Type != ClientTypeGRPC {
		t.Error("type mismatch")
	}
	if cfg.GRPCAddress != "localhost:9443" {
		t.Error("gRPC address mismatch")
	}
	if cfg.ExecutionID != "exec-123" {
		t.Error("execution ID mismatch")
	}
	if !cfg.UseTLS {
		t.Error("expected TLS enabled")
	}
}

func TestNewClient_GRPC(t *testing.T) {
	cfg := ClientConfig{
		Type:        ClientTypeGRPC,
		GRPCAddress: "localhost:9443",
		ExecutionID: "exec-123",
		Log:         logr.Discard(),
	}
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if client == nil {
		t.Fatal("nil client")
	}
}

func TestNewClient_Webhook(t *testing.T) {
	cfg := ClientConfig{
		Type:        ClientTypeWebhook,
		WebhookURL:  "http://localhost:8080",
		ExecutionID: "exec-123",
		Timeout:     10 * time.Second,
		Log:         logr.Discard(),
	}
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if client == nil {
		t.Fatal("nil client")
	}
}

func TestNewClient_Auto(t *testing.T) {
	cfg := ClientConfig{
		Type:        ClientTypeAuto,
		GRPCAddress: "localhost:9443",
		WebhookURL:  "http://localhost:8080",
		ExecutionID: "exec-123",
		Log:         logr.Discard(),
	}
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if client == nil {
		t.Fatal("nil client")
	}
}

func TestNewAutoClient(t *testing.T) {
	cfg := ClientConfig{
		GRPCAddress: "localhost:9443",
		WebhookURL:  "http://localhost:8080",
		ExecutionID: "exec-123",
		Log:         logr.Discard(),
	}
	client := NewAutoClient(cfg)
	if client == nil {
		t.Fatal("nil auto client")
	}
	if client.grpcClient == nil {
		t.Error("nil grpc client")
	}
	if client.webhookClient == nil {
		t.Error("nil webhook client")
	}
}

func TestGRPCClientOptions(t *testing.T) {
	opts := GRPCClientOptions{
		Address:     "localhost:9443",
		ExecutionID: "exec-123",
		TokenPath:   "/var/run/secrets/token",
		UseTLS:      true,
		Log:         logr.Discard(),
	}
	if opts.Address != "localhost:9443" {
		t.Error("address mismatch")
	}
	if opts.ExecutionID != "exec-123" {
		t.Error("execution ID mismatch")
	}
}

func TestWebhookClientOptions(t *testing.T) {
	opts := WebhookClientOptions{
		BaseURL:     "http://localhost:8080",
		ExecutionID: "exec-123",
		TokenPath:   "/var/run/secrets/token",
		Timeout:     30 * time.Second,
		Log:         logr.Discard(),
	}
	if opts.BaseURL != "http://localhost:8080" {
		t.Error("base URL mismatch")
	}
	if opts.Timeout != 30*time.Second {
		t.Error("timeout mismatch")
	}
}
