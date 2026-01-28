/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

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
	var payload streamingv1alpha1.WebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		ws.log.Error(err, "failed to decode payload")
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Validate execution access
	if !auth.ValidateExecutionAccess(result, payload.ExecutionID) {
		ws.log.Info("execution access denied",
			"executionId", payload.ExecutionID,
			"namespace", result.Namespace,
			"serviceAccount", result.ServiceAccount,
		)
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var response streamingv1alpha1.WebhookResponse

	switch payload.Type {
	case "log":
		if payload.Log != nil {
			ws.processLog(payload.ExecutionID, payload.Log)
			response.Acknowledged = true
		}
	case "progress":
		if payload.Progress != nil {
			resp, err := ws.executionService.ReportProgress(r.Context(), &streamingv1alpha1.ProgressReport{
				ExecutionID:     payload.ExecutionID,
				Phase:           payload.Progress.Phase,
				ProgressPercent: payload.Progress.ProgressPercent,
				Message:         payload.Progress.Message,
			})
			if err != nil {
				ws.log.Error(err, "failed to process progress")
				http.Error(w, "Internal error", http.StatusInternalServerError)
				return
			}
			response.Acknowledged = resp.Acknowledged
		}
	case "completion":
		if payload.Completion != nil {
			resp, err := ws.executionService.ReportCompletion(r.Context(), &streamingv1alpha1.CompletionReport{
				ExecutionID:  payload.ExecutionID,
				Success:      payload.Completion.Success,
				ErrorMessage: payload.Completion.ErrorMessage,
				DurationMs:   payload.Completion.DurationMs,
				RestoreData:  payload.Completion.RestoreData,
			})
			if err != nil {
				ws.log.Error(err, "failed to process completion")
				http.Error(w, "Internal error", http.StatusInternalServerError)
				return
			}
			response.Acknowledged = resp.Acknowledged
			response.RestoreRef = resp.RestoreRef
		}
	case "heartbeat":
		resp, err := ws.executionService.Heartbeat(r.Context(), &streamingv1alpha1.HeartbeatRequest{
			ExecutionID: payload.ExecutionID,
		})
		if err != nil {
			ws.log.Error(err, "failed to process heartbeat")
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		response.Acknowledged = resp.Acknowledged
		response.ServerTime = resp.ServerTime
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

	result, err := ws.validateRequest(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var entries []streamingv1alpha1.LogEntry
	if err := json.NewDecoder(r.Body).Decode(&entries); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	for _, entry := range entries {
		if !auth.ValidateExecutionAccess(result, entry.ExecutionID) {
			continue
		}
		ws.processLog(entry.ExecutionID, &streamingv1alpha1.LogPayload{
			Level:   entry.Level,
			Message: entry.Message,
			Fields:  entry.Fields,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(streamingv1alpha1.WebhookResponse{
		Acknowledged: true,
	})
}

// handleProgress handles progress-specific endpoint.
func (ws *WebhookServer) handleProgress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	result, err := ws.validateRequest(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var report streamingv1alpha1.ProgressReport
	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if !auth.ValidateExecutionAccess(result, report.ExecutionID) {
		http.Error(w, "Forbidden", http.StatusForbidden)
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

	result, err := ws.validateRequest(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var report streamingv1alpha1.CompletionReport
	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if !auth.ValidateExecutionAccess(result, report.ExecutionID) {
		http.Error(w, "Forbidden", http.StatusForbidden)
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

	result, err := ws.validateRequest(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req streamingv1alpha1.HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if !auth.ValidateExecutionAccess(result, req.ExecutionID) {
		http.Error(w, "Forbidden", http.StatusForbidden)
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

	return ws.validator.ValidateToken(r.Context(), parts[1])
}

// processLog processes a single log entry.
func (ws *WebhookServer) processLog(executionID string, log *streamingv1alpha1.LogPayload) {
	entry := streamingv1alpha1.LogEntry{
		ExecutionID: executionID,
		Timestamp:   time.Now(),
		Level:       log.Level,
		Message:     log.Message,
		Fields:      log.Fields,
	}

	ws.executionService.executionLogsMu.Lock()
	ws.executionService.executionLogs[executionID] = append(
		ws.executionService.executionLogs[executionID],
		entry,
	)
	ws.executionService.executionLogsMu.Unlock()

	switch log.Level {
	case "ERROR":
		ws.log.Error(nil, log.Message, "executionId", executionID, "fields", log.Fields)
	case "WARN":
		ws.log.Info(log.Message, "executionId", executionID, "level", "warn", "fields", log.Fields)
	default:
		ws.log.V(1).Info(log.Message, "executionId", executionID, "level", log.Level, "fields", log.Fields)
	}
}
