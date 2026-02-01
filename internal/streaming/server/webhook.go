/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package server provides streaming servers for controller-side runner communication.
// It implements both gRPC and HTTP webhook endpoints for receiving logs, progress reports,
// and completion notifications from runner pods.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/client-go/kubernetes"

	streamingv1alpha1 "github.com/ardikabs/hibernator/api/streaming/v1alpha1"
	"github.com/ardikabs/hibernator/internal/streaming/auth"
	"github.com/ardikabs/hibernator/internal/streaming/types"
)

// WebhookServer handles HTTP webhook callbacks from runners.
type WebhookServer struct {
	server           *http.Server
	executionService *ExecutionServiceServer
	validator        *auth.TokenValidator
	log              logr.Logger
}

// NewWebhookServer creates a new webhook server.
func NewWebhookServer(
	address string,
	clientset *kubernetes.Clientset,
	executionService *ExecutionServiceServer,
	log logr.Logger,
) *WebhookServer {
	validator := auth.NewTokenValidator(clientset, log)

	ws := &WebhookServer{
		executionService: executionService,
		validator:        validator,
		log:              log.WithName("webhook-server"),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1alpha1/callback", ws.handleCallback)
	mux.HandleFunc("/v1alpha1/logs", ws.handleLogs)
	mux.HandleFunc("/v1alpha1/progress", ws.handleProgress)
	mux.HandleFunc("/v1alpha1/completion", ws.handleCompletion)
	mux.HandleFunc("/v1alpha1/heartbeat", ws.handleHeartbeat)
	mux.HandleFunc("/healthz", ws.handleHealthz)

	ws.server = &http.Server{
		Addr:         address,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return ws
}

// Start starts the webhook server.
func (ws *WebhookServer) Start(ctx context.Context) error {
	ws.log.Info("starting webhook server", "address", ws.server.Addr)

	// Handle graceful shutdown
	go func() {
		<-ctx.Done()
		ws.log.Info("shutting down webhook server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := ws.server.Shutdown(shutdownCtx); err != nil {
			ws.log.Error(err, "error shutting down webhook server")
		}
	}()

	if err := ws.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("webhook server error: %w", err)
	}

	return nil
}

// handleCallback handles unified webhook callbacks.
func (ws *WebhookServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Validate token
	result, err := ws.validateRequest(r)
	if err != nil {
		ws.log.Error(err, "authentication failed")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Parse payload
	var payload types.WebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		ws.log.Error(err, "failed to decode payload")
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Extract execution ID from payload based on type
	var executionID string
	switch payload.Type {
	case "log":
		if payload.Log != nil {
			executionID = payload.Log.ExecutionID
		}
	case "progress":
		if payload.Progress != nil {
			executionID = payload.Progress.ExecutionID
		}
	case "completion":
		if payload.Completion != nil {
			executionID = payload.Completion.ExecutionID
		}
	case "heartbeat":
		if payload.Heartbeat != nil {
			executionID = payload.Heartbeat.ExecutionID
		}
	}

	if executionID == "" {
		ws.log.Info("missing execution ID in payload", "type", payload.Type)
		http.Error(w, "Bad request: missing execution ID", http.StatusBadRequest)
		return
	}

	// Validate execution access
	// TODO: Implement proper execution access validation based on plan name/namespace
	// For now, just check if the service account is valid
	if !result.Valid {
		ws.log.Info("execution access denied",
			"executionId", executionID,
			"namespace", result.Namespace,
			"serviceAccount", result.ServiceAccount,
		)
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var response types.WebhookResponse

	switch payload.Type {
	case "log":
		if payload.Log != nil {
			ws.processLog(payload.Log.ToProto())
			response.Acknowledged = true
		}
	case "progress":
		if payload.Progress != nil {
			resp, err := ws.executionService.ReportProgress(r.Context(), payload.Progress.ToProto())
			if err != nil {
				ws.log.Error(err, "failed to process progress")
				http.Error(w, "Internal error", http.StatusInternalServerError)
				return
			}
			response.Acknowledged = resp.Acknowledged
		}
	case "completion":
		if payload.Completion != nil {
			resp, err := ws.executionService.ReportCompletion(r.Context(), payload.Completion.ToProto())
			if err != nil {
				ws.log.Error(err, "failed to process completion")
				http.Error(w, "Internal error", http.StatusInternalServerError)
				return
			}
			response.Acknowledged = resp.Acknowledged
			response.RestoreRef = resp.RestoreRef
		}
	case "heartbeat":
		if payload.Heartbeat != nil {
			resp, err := ws.executionService.Heartbeat(r.Context(), payload.Heartbeat.ToProto())
			if err != nil {
				ws.log.Error(err, "failed to process heartbeat")
				http.Error(w, "Internal error", http.StatusInternalServerError)
				return
			}
			response.Acknowledged = resp.Acknowledged
		}
	default:
		ws.log.Info("unknown payload type", "type", payload.Type)
		http.Error(w, "Unknown payload type", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleLogs handles log-specific endpoint.
func (ws *WebhookServer) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	_, err := ws.validateRequest(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var entries []*streamingv1alpha1.LogEntry
	if err := json.NewDecoder(r.Body).Decode(&entries); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	for _, entry := range entries {
		// Process each log entry
		ws.processLog(entry)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(types.WebhookResponse{
		Acknowledged: true,
	})
}

// handleProgress handles progress-specific endpoint.
func (ws *WebhookServer) handleProgress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	_, err := ws.validateRequest(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var report streamingv1alpha1.ProgressReport
	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	resp, err := ws.executionService.ReportProgress(r.Context(), &report)
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleCompletion handles completion-specific endpoint.
func (ws *WebhookServer) handleCompletion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	_, err := ws.validateRequest(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var report streamingv1alpha1.CompletionReport
	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	resp, err := ws.executionService.ReportCompletion(r.Context(), &report)
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleHeartbeat handles heartbeat-specific endpoint.
func (ws *WebhookServer) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	_, err := ws.validateRequest(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req streamingv1alpha1.HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	resp, err := ws.executionService.Heartbeat(r.Context(), &req)
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleHealthz handles health check requests.
func (ws *WebhookServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// validateRequest validates the bearer token in the Authorization header.
func (ws *WebhookServer) validateRequest(r *http.Request) (*auth.ValidationResult, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return nil, fmt.Errorf("missing authorization header")
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return nil, fmt.Errorf("invalid authorization header format")
	}

	result := ws.validator.ValidateToken(r.Context(), parts[1])
	if result.Error != nil {
		return result, result.Error
	}
	return result, nil
}

// processLog processes a single log entry.
func (ws *WebhookServer) processLog(log *streamingv1alpha1.LogEntry) {
	// Store or forward log entry
	ws.log.V(2).Info("received log",
		"executionId", log.ExecutionId,
		"level", log.Level,
		"message", log.Message,
	)

	// Delegate to business logic layer
	if err := ws.executionService.StoreLog(log); err != nil {
		ws.log.Error(err, "failed to store log entry")
		return
	}

	switch log.Level {
	case "ERROR":
		ws.log.Error(nil, log.Message, "executionId", log.ExecutionId, "fields", log.Fields)
	case "WARN":
		ws.log.Info(log.Message, "executionId", log.ExecutionId, "level", "warn", "fields", log.Fields)
	default:
		ws.log.V(1).Info(log.Message, "executionId", log.ExecutionId, "level", log.Level, "fields", log.Fields)
	}
}
